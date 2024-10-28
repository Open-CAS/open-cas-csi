/*
Copyright 2024 Huawei Technologies

SPDX-License-Identifier: Apache-2.0
*/
package k8sutils

import (
	"context"
	"fmt"
	"io"
	"opencas-csi/pkg/utils"
	"opencas-csi/pkg/webhook"
	"os"
	"strings"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog/v2"
	"k8s.io/utils/ptr"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

const (
	ProtectedNamespaceEnv = "PROTECTED_NAMESPACE"
	OperatorNamespaceEnv  = "OPERATOR_NAMESPACE"
	PodNameEnv            = "DRIVER_POD_NAME"
	KubeletDirEnv         = "KUBELET_DIR"
	CasadmImageEnv        = "CASADM_IMAGE"
	WebhookImageEnv       = "WEBHOOK_IMAGE"

	defaultKubeletDir         = "/var/lib/kubelet"
	defaultProtectedNamespace = "opencas-csi-protected"
	OperatingCacheLabelKey    = "csi.open-cas.com/operating-cache-id"
)

var kubeletDir = ""
var protectedNamespace = ""

type K8sUtils struct {
	client *kubernetes.Clientset
	config *rest.Config
}

type PvcConfig struct {
	StorageClassName string
	Params           map[string]string
	Name             string
	RequiredBytes    int64
	LimitBytes       int64
}

type PodConfig struct {
	Name        string
	Pvc         *corev1.PersistentVolumeClaim
	CasadmImage string
	NodeId      string
	Namespace   string
}

func NewK8sUtils() (*K8sUtils, error) {
	var config *rest.Config
	var err error

	if kubeconfig := os.Getenv("KUBECONFIG"); kubeconfig != "" {
		config, err = clientcmd.BuildConfigFromFlags("", kubeconfig)
	} else {
		config, err = rest.InClusterConfig()
	}
	if err != nil {
		return nil, fmt.Errorf("failed to create K8S REST config: %w", err)
	}
	ConfiguredKubeletDir()
	ConfiguredProtectedNamespace()

	client, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create K8S client: %w", err)
	}
	return &K8sUtils{
		client: client,
		config: config,
	}, nil
}

func (k *K8sUtils) GetClient() *kubernetes.Clientset {
	return k.client
}

func ConfiguredProtectedNamespace() string {
	if protectedNamespace == "" {
		if protectedNamespace = os.Getenv(ProtectedNamespaceEnv); protectedNamespace == "" {
			protectedNamespace = defaultProtectedNamespace
		}
	}
	return protectedNamespace
}

func ConfiguredKubeletDir() string {
	if kubeletDir == "" {
		if kubeletDir = os.Getenv(KubeletDirEnv); kubeletDir == "" {
			kubeletDir = defaultKubeletDir
		}
	}
	return kubeletDir
}

func (k *K8sUtils) GetPodByLabel(ctx context.Context, node string, label string) (*corev1.Pod, error) {
	var pods *corev1.PodList
	var err error
	for i := 0; i < 5; i++ {
		pods, err = k.client.CoreV1().Pods("").List(ctx, metav1.ListOptions{
			FieldSelector: "spec.nodeName=" + node,
			LabelSelector: label,
		})
		if err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err != nil {
		return nil, err
	}
	if len(pods.Items) == 0 {
		return nil, fmt.Errorf("pod labeled %q not found in node %q", label, node)
	}

	return &pods.Items[0], nil
}

func (k *K8sUtils) ExecCommandJob(ctx context.Context, job *batchv1.Job) (*utils.Output, error) {
	commandJob, err := k.client.BatchV1().Jobs(job.Namespace).Create(ctx, job, metav1.CreateOptions{})
	if err != nil {
		return nil, err
	}
	defer k.client.BatchV1().Jobs(job.Namespace).Delete(ctx, commandJob.Name, metav1.DeleteOptions{
		GracePeriodSeconds: ptr.To(int64(0)),
		PropagationPolicy:  ptr.To(metav1.DeletePropagationBackground),
	})

	watch, err := k.client.BatchV1().Jobs(job.Namespace).Watch(ctx, metav1.SingleObject(commandJob.ObjectMeta))
	if err != nil {
		return nil, err
	}
	func() {
		for {
			select {
			case events, ok := <-watch.ResultChan():
				if !ok {
					return
				}
				// sometime watch returns Status instead of Job, so check is needed
				if jobWatch, ok := events.Object.(*batchv1.Job); ok {
					if jobWatch.Status.Succeeded+jobWatch.Status.Failed > 0 {
						klog.V(5).Infof("Command job complete, success = %v", jobWatch.Status.Succeeded > 0)
						watch.Stop()
					}
				}
			case <-time.After(5 * time.Minute):
				err = fmt.Errorf("timeout waiting for command job completion: '%s/%s'", job.Namespace, job.Name)
				watch.Stop()
			}
		}
	}()

	pods, err := k.client.CoreV1().Pods(job.Namespace).List(ctx, metav1.ListOptions{
		LabelSelector: fmt.Sprintf("batch.kubernetes.io/controller-uid=%s", commandJob.UID),
	})
	if err != nil {
		return nil, err
	}
	if len(pods.Items) == 0 {
		return nil, fmt.Errorf("pod for command job '%s/%s' not found", job.Namespace, job.Name)
	}
	pod := pods.Items[0]
	output := &utils.Output{
		ExitCode: -1,
	}
	if pod.Status.ContainerStatuses == nil || len(pod.Status.ContainerStatuses) == 0 {
		return nil, fmt.Errorf("cannot read pod status for command job '%s/%s'", job.Namespace, job.Name)
	}
	if pod.Status.ContainerStatuses[0].State.Terminated != nil {
		output.ExitCode = int(pod.Status.ContainerStatuses[0].State.Terminated.ExitCode)
	}
	logRequest := k.client.CoreV1().Pods(pod.Namespace).GetLogs(pod.Name, &corev1.PodLogOptions{})
	logs, err := logRequest.Stream(ctx)
	if err != nil {
		return output, err
	}
	defer logs.Close()
	out, err := io.ReadAll(logs)
	output.StdOut = string(out)
	klog.V(5).Infof("Command job result: ExitCode: %d\n%q", output.ExitCode, output.StdOut)
	return output, err
}

func (k *K8sUtils) DeleteSubproviderPvc(name string) error {
	pvc, err := k.client.CoreV1().PersistentVolumeClaims(protectedNamespace).Get(context.TODO(), name, metav1.GetOptions{})
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			return nil
		}
		return err
	}
	if _, ok := pvc.GetLabels()[webhook.Label]; ok {
		labels := pvc.GetLabels()
		delete(labels, webhook.Label)
		pvc.SetLabels(labels)
		_, err := k.client.CoreV1().PersistentVolumeClaims(protectedNamespace).Update(context.TODO(), pvc, metav1.UpdateOptions{})
		if err != nil {
			return err
		}
	}
	return k.client.CoreV1().PersistentVolumeClaims(protectedNamespace).Delete(context.TODO(), name, metav1.DeleteOptions{})
}

func (k *K8sUtils) CreateSubproviderPvc(config PvcConfig) (*corev1.PersistentVolumeClaim, error) {
	spec := getSubProviderPvcSpec(config)

	return k.client.CoreV1().PersistentVolumeClaims(protectedNamespace).Create(
		context.TODO(),
		&corev1.PersistentVolumeClaim{
			ObjectMeta: metav1.ObjectMeta{
				Name:      config.Name,
				Namespace: protectedNamespace,
				Labels:    map[string]string{webhook.Label: ""},
			},
			Spec: spec,
		},
		metav1.CreateOptions{},
	)
}

func (k *K8sUtils) GetSubproviderPvcByName(name string) (pvc *corev1.PersistentVolumeClaim, err error) {
	pvc, err = k.client.CoreV1().PersistentVolumeClaims(protectedNamespace).Get(context.TODO(), name, metav1.GetOptions{})
	if pvc.Name == "" {
		pvc = nil
	}
	return
}

func (k *K8sUtils) GetPvByName(name string) (pv *corev1.PersistentVolume, err error) {
	pv, err = k.client.CoreV1().PersistentVolumes().Get(context.TODO(), name, metav1.GetOptions{})
	if pv.Name == "" {
		pv = nil
	}
	return
}

func (k *K8sUtils) GetScByName(name string) (sc *storagev1.StorageClass, err error) {
	sc, err = k.client.StorageV1().StorageClasses().Get(context.TODO(), name, metav1.GetOptions{})
	if sc.Name == "" {
		sc = nil
	}
	return
}

func (k *K8sUtils) CreateIntermediatePod(config PodConfig) (*corev1.Pod, error) {
	spec := getIntermediatePodSpec(config)

	pod, err := k.client.CoreV1().Pods(config.Namespace).Create(
		context.TODO(),
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      config.Name,
				Namespace: config.Namespace,
				Labels:    map[string]string{webhook.Label: ""},
			},
			Spec: spec,
		},
		metav1.CreateOptions{},
	)
	if err != nil {
		return nil, err
	}
	status := pod.Status
	watch, err := k.client.CoreV1().Pods(config.Namespace).Watch(context.TODO(), metav1.SingleObject(pod.ObjectMeta))
	if err != nil {
		return pod, err
	}
	func() {
		for {
			select {
			case events, ok := <-watch.ResultChan():
				if !ok {
					return
				}
				// sometime watch returns Status instead of Pod, so check is needed
				if pod, ok := events.Object.(*corev1.Pod); ok {
					status = pod.Status
					if pod.Status.Phase == corev1.PodRunning {
						watch.Stop()
					}
				}
			case <-time.After(2 * time.Minute):
				err = fmt.Errorf("timeout waiting for intermediate Pod: '%s/%s'", config.Namespace, config.Name)
				watch.Stop()
			}
		}
	}()
	if status.Phase != corev1.PodRunning {
		k.client.CoreV1().Pods(config.Namespace).Delete(context.TODO(), config.Name, metav1.DeleteOptions{})
		return pod, err
	}
	return pod, nil
}

func (k *K8sUtils) DeleteIntermediatePod(name, namespace string) error {
	pod, err := k.client.CoreV1().Pods(namespace).Get(context.TODO(), name, metav1.GetOptions{})
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			return nil
		}
		return err
	}
	if _, ok := pod.GetLabels()[webhook.Label]; ok {
		labels := pod.GetLabels()
		delete(labels, webhook.Label)
		pod.SetLabels(labels)
		_, err := k.client.CoreV1().Pods(namespace).Update(context.TODO(), pod, metav1.UpdateOptions{})
		if err != nil {
			return err
		}
	}

	return k.client.CoreV1().Pods(namespace).Delete(context.TODO(), name, metav1.DeleteOptions{})
}

func (k *K8sUtils) GetIntermediatePodByName(name, namespace string) (pod *corev1.Pod, err error) {
	pod, err = k.client.CoreV1().Pods(namespace).Get(context.TODO(), name, metav1.GetOptions{})
	if pod.Name == "" {
		pod = nil
	}
	return
}

func getSubProviderPvcSpec(config PvcConfig) corev1.PersistentVolumeClaimSpec {
	spec := corev1.PersistentVolumeClaimSpec{
		AccessModes:      []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
		StorageClassName: &config.StorageClassName,
		VolumeMode:       ptr.To(corev1.PersistentVolumeBlock),
	}
	if config.RequiredBytes > 0 || config.LimitBytes > 0 {
		storageRequest, storageLimit := resource.Quantity{}, resource.Quantity{}
		spec.Resources = corev1.VolumeResourceRequirements{}
		if config.RequiredBytes > 0 {
			storageRequest.Set(config.RequiredBytes)
			spec.Resources.Requests = corev1.ResourceList{"storage": storageRequest}
		}
		if config.LimitBytes > 0 {
			storageLimit.Set(config.LimitBytes)
			spec.Resources.Limits = corev1.ResourceList{"storage": storageLimit}
		}
	}
	return spec
}

func getIntermediatePodSpec(config PodConfig) corev1.PodSpec {
	podSpec := corev1.PodSpec{
		Containers: []corev1.Container{
			{
				Name:            "casadm",
				Image:           "alpine",
				ImagePullPolicy: corev1.PullIfNotPresent,
				Command:         []string{"sleep", "infinity"},
				SecurityContext: &corev1.SecurityContext{
					Privileged: ptr.To(true),
				},
			},
		},
		// don't change to NodeName (PVs are not provided properly then)
		NodeSelector: map[string]string{
			"kubernetes.io/hostname": config.NodeId,
		},
	}

	if config.Pvc != nil {
		podSpec.Containers[0].VolumeDevices = []corev1.VolumeDevice{
			{
				Name:       "backend",
				DevicePath: "/dev/xvda",
			},
		}
		podSpec.Volumes = append(podSpec.Volumes,
			corev1.Volume{
				Name: "backend",
				VolumeSource: corev1.VolumeSource{
					PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
						ClaimName: config.Pvc.Name,
					},
				},
			},
		)
	}

	return podSpec
}
