/*
Copyright 2024 Huawei Technologies

SPDX-License-Identifier: Apache-2.0
*/

package helpers

import (
	"fmt"
	"time"

	opencasv1alpha1 "opencas-csi/pkg/csi-operator/api/v1alpha1"
	"opencas-csi/pkg/k8sutils"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	storagev1 "k8s.io/api/storage/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	ServiceAccountName   = "opencas-csi-driver-sa"
	ConfigMapName        = "opencas-csi-driver-config-map"
	ControllerPluginName = "opencas-csi-controller-plugin"
	NodePluginName       = "opencas-csi-node-plugin"

	DefaultRequequeAfter = 10 * time.Second
	LongRequeueAfter     = 1 * time.Minute
	Finalizer            = "csi.open-cas.com/operator"
)

func GetNewObject(obj client.Object) client.Object {
	switch obj.(type) {
	case *corev1.Namespace:
		return &corev1.Namespace{}
	case *corev1.ServiceAccount:
		return &corev1.ServiceAccount{}
	case *rbacv1.Role:
		return &rbacv1.Role{}
	case *rbacv1.RoleBinding:
		return &rbacv1.RoleBinding{}
	case *rbacv1.ClusterRole:
		return &rbacv1.ClusterRole{}
	case *rbacv1.ClusterRoleBinding:
		return &rbacv1.ClusterRoleBinding{}
	case *storagev1.CSIDriver:
		return &storagev1.CSIDriver{}
	case *corev1.ConfigMap:
		return &corev1.ConfigMap{}
	case *appsv1.Deployment:
		return &appsv1.Deployment{}
	case *appsv1.DaemonSet:
		return &appsv1.DaemonSet{}
	}
	return nil
}

func GetNamespaceConfiguration(di *opencasv1alpha1.DriverInstance) *corev1.Namespace {
	namespace := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: di.Spec.DriverConfig.DriverNamespace,
		},
		TypeMeta: metav1.TypeMeta{Kind: "Namespace", APIVersion: corev1.SchemeGroupVersion.String()},
	}
	labels := di.GetDefaultLabels()
	labels["app.kubernetes.io/component"] = "namespace"
	for k, v := range di.Spec.Labels {
		labels[k] = v
	}
	namespace.SetLabels(labels)
	return namespace
}

func GetServiceAccountConfiguration(di *opencasv1alpha1.DriverInstance) *corev1.ServiceAccount {
	serviceAccount := &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      ServiceAccountName,
			Namespace: di.Spec.DriverConfig.DriverNamespace,
		},
		TypeMeta: metav1.TypeMeta{Kind: "ServiceAccount", APIVersion: corev1.SchemeGroupVersion.String()},
	}
	labels := di.GetDefaultLabels()
	labels["app.kubernetes.io/component"] = "service-account"
	for k, v := range di.Spec.Labels {
		labels[k] = v
	}
	serviceAccount.SetLabels(labels)
	return serviceAccount
}

func GetRolesConfiguration(di *opencasv1alpha1.DriverInstance) []*rbacv1.Role {
	roles := []*rbacv1.Role{
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "pod-executioner",
				Namespace: di.Spec.DriverConfig.DriverNamespace,
			},
			Rules: []rbacv1.PolicyRule{
				{
					APIGroups: []string{""},
					Resources: []string{"pods"},
					Verbs:     []string{"get", "list", "watch", "create", "delete", "update"},
				},
				{
					APIGroups: []string{""},
					Resources: []string{"pods/exec"},
					Verbs:     []string{"create"},
				},
			},
		},
	}

	labels := di.GetDefaultLabels()
	labels["app.kubernetes.io/component"] = "role"
	for k, v := range di.Spec.Labels {
		labels[k] = v
	}

	for _, role := range roles {
		role.SetLabels(labels)
		role.TypeMeta = metav1.TypeMeta{Kind: "Role", APIVersion: rbacv1.SchemeGroupVersion.String()}
	}

	return roles
}

func GetRoleBindingsConfiguration(di *opencasv1alpha1.DriverInstance) []*rbacv1.RoleBinding {
	sa := GetServiceAccountConfiguration(di)
	roles := GetRolesConfiguration(di)

	roleBindings := []*rbacv1.RoleBinding{}

	labels := di.GetDefaultLabels()
	labels["app.kubernetes.io/component"] = "role-binding"
	for k, v := range di.Spec.Labels {
		labels[k] = v
	}

	for _, role := range roles {
		roleBinding := &rbacv1.RoleBinding{
			ObjectMeta: metav1.ObjectMeta{
				Name:      fmt.Sprintf("%s-%s", ServiceAccountName, role.Name),
				Namespace: di.Spec.DriverConfig.DriverNamespace,
			},
			RoleRef: rbacv1.RoleRef{
				APIGroup: role.GroupVersionKind().Group,
				Kind:     role.GroupVersionKind().Kind,
				Name:     role.Name,
			},
			Subjects: []rbacv1.Subject{
				{
					Kind: sa.GroupVersionKind().Kind,
					Name: sa.Name,
				},
			},
		}
		roleBinding.SetLabels(labels)
		roleBinding.TypeMeta = metav1.TypeMeta{Kind: "RoleBinding", APIVersion: rbacv1.SchemeGroupVersion.String()}

		roleBindings = append(roleBindings, roleBinding)
	}

	return roleBindings
}

func GetClusterRolesConfiguration(di *opencasv1alpha1.DriverInstance) []*rbacv1.ClusterRole {
	clusterRoles := []*rbacv1.ClusterRole{
		{
			ObjectMeta: metav1.ObjectMeta{
				Name: "external-provisioner-runner",
			},
			Rules: []rbacv1.PolicyRule{
				{
					APIGroups: []string{""},
					Resources: []string{"persistentvolumes"},
					Verbs:     []string{"get", "list", "watch", "create", "delete", "update"},
				},
				{
					APIGroups: []string{""},
					Resources: []string{"persistentvolumeclaims"},
					Verbs:     []string{"get", "list", "watch", "create", "delete", "update"},
				},
				{
					APIGroups: []string{""},
					Resources: []string{"nodes"},
					Verbs:     []string{"get", "list", "watch"},
				},
				{
					APIGroups: []string{""},
					Resources: []string{"configmaps"},
					Verbs:     []string{"get", "list", "watch"},
				},
				{
					APIGroups: []string{"storage.k8s.io"},
					Resources: []string{"storageclasses"},
					Verbs:     []string{"get", "list", "watch"},
				},
				{
					APIGroups: []string{"storage.k8s.io"},
					Resources: []string{"volumeattachments"},
					Verbs:     []string{"get", "list", "watch"},
				},
				{
					APIGroups: []string{"storage.k8s.io"},
					Resources: []string{"csinodes"},
					Verbs:     []string{"get", "list", "watch"},
				},
				{
					APIGroups: []string{""},
					Resources: []string{"events"},
					Verbs:     []string{"list", "watch", "create", "update", "patch"},
				},
				{
					APIGroups: []string{"snapshot.storage.k8s.io"},
					Resources: []string{"volumesnapshotcontents"},
					Verbs:     []string{"get", "list"},
				},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				Name: "external-attacher-runner",
			},
			Rules: []rbacv1.PolicyRule{
				{
					APIGroups: []string{""},
					Resources: []string{"persistentvolumes"},
					Verbs:     []string{"get", "list", "watch", "patch"},
				},
				{
					APIGroups: []string{"storage.k8s.io"},
					Resources: []string{"volumeattachments"},
					Verbs:     []string{"get", "list", "watch", "patch"},
				},
				{
					APIGroups: []string{"storage.k8s.io"},
					Resources: []string{"volumeattachments/status"},
					Verbs:     []string{"patch"},
				},
				{
					APIGroups: []string{"storage.k8s.io"},
					Resources: []string{"csinodes"},
					Verbs:     []string{"get", "list", "watch"},
				},
			},
		},
	}

	labels := di.GetDefaultLabels()
	labels["app.kubernetes.io/component"] = "cluster-role"
	for k, v := range di.Spec.Labels {
		labels[k] = v
	}

	for _, clusterRole := range clusterRoles {
		clusterRole.SetLabels(labels)
		clusterRole.TypeMeta = metav1.TypeMeta{Kind: "ClusterRole", APIVersion: rbacv1.SchemeGroupVersion.String()}
	}

	return clusterRoles
}

func GetClusterRoleBindingsConfiguration(di *opencasv1alpha1.DriverInstance) []*rbacv1.ClusterRoleBinding {
	sa := GetServiceAccountConfiguration(di)
	clusterRoles := GetClusterRolesConfiguration(di)

	clusterRoleBindings := []*rbacv1.ClusterRoleBinding{}

	labels := di.GetDefaultLabels()
	labels["app.kubernetes.io/component"] = "cluster-role-binding"
	for k, v := range di.Spec.Labels {
		labels[k] = v
	}

	for _, clusterRole := range clusterRoles {
		clusterRoleBinding := &rbacv1.ClusterRoleBinding{
			ObjectMeta: metav1.ObjectMeta{
				Name: fmt.Sprintf("%s-%s", ServiceAccountName, clusterRole.Name),
			},
			RoleRef: rbacv1.RoleRef{
				APIGroup: clusterRole.GroupVersionKind().Group,
				Kind:     clusterRole.GroupVersionKind().Kind,
				Name:     clusterRole.Name,
			},
			Subjects: []rbacv1.Subject{
				{
					Kind:      sa.GroupVersionKind().Kind,
					Name:      sa.Name,
					Namespace: sa.Namespace,
				},
			},
		}
		clusterRoleBinding.SetLabels(labels)
		clusterRoleBinding.TypeMeta = metav1.TypeMeta{Kind: "ClusterRoleBinding", APIVersion: rbacv1.SchemeGroupVersion.String()}

		clusterRoleBindings = append(clusterRoleBindings, clusterRoleBinding)
	}

	return clusterRoleBindings
}

func GetCSIDriverConfiguration(di *opencasv1alpha1.DriverInstance) *storagev1.CSIDriver {
	csiDriver := &storagev1.CSIDriver{
		ObjectMeta: metav1.ObjectMeta{
			Name: di.Name,
		},
		TypeMeta: metav1.TypeMeta{Kind: "CSIDriver", APIVersion: storagev1.SchemeGroupVersion.String()},
		Spec: storagev1.CSIDriverSpec{
			AttachRequired: ptr.To(true),
			FSGroupPolicy:  ptr.To(storagev1.FileFSGroupPolicy),
			PodInfoOnMount: ptr.To(true),
			VolumeLifecycleModes: []storagev1.VolumeLifecycleMode{
				storagev1.VolumeLifecyclePersistent,
			},
		},
	}
	labels := di.GetDefaultLabels()
	labels["app.kubernetes.io/component"] = "csi-driver"
	for k, v := range di.Spec.Labels {
		labels[k] = v
	}
	csiDriver.SetLabels(labels)
	return csiDriver
}

func GetConfigMapConfiguration(di *opencasv1alpha1.DriverInstance) *corev1.ConfigMap {
	configMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      ConfigMapName,
			Namespace: di.Spec.DriverConfig.DriverNamespace,
		},
		TypeMeta: metav1.TypeMeta{Kind: "ConfigMap", APIVersion: corev1.SchemeGroupVersion.String()},
		Data: map[string]string{
			k8sutils.CasadmImageEnv:        di.Spec.DriverConfig.CasadmImage,
			k8sutils.KubeletDirEnv:         k8sutils.ConfiguredKubeletDir(),
			k8sutils.ProtectedNamespaceEnv: di.Spec.DriverConfig.ProtectedNamespace,
			k8sutils.WebhookImageEnv:       di.Spec.DriverConfig.WebhookImage,
		},
	}
	labels := di.GetDefaultLabels()
	labels["app.kubernetes.io/component"] = "config-map"
	for k, v := range di.Spec.Labels {
		labels[k] = v
	}
	configMap.SetLabels(labels)
	return configMap
}

func GetControllerDeploymentConfiguration(di *opencasv1alpha1.DriverInstance) *appsv1.Deployment {
	labels := di.GetDefaultLabels()
	partOfKey := "app.kubernetes.io/part-of"
	componentKey := "app.kubernetes.io/component"
	labels[componentKey] = "controller-plugin"
	for k, v := range di.Spec.Labels {
		labels[k] = v
	}

	socketDirMount := corev1.VolumeMount{
		Name:      "socket-dir",
		MountPath: "/csi",
	}

	replicas := di.Spec.DriverConfig.ControllerReplicas
	if replicas == 0 {
		replicas = 1
	}

	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      ControllerPluginName,
			Namespace: di.Spec.DriverConfig.DriverNamespace,
		},
		TypeMeta: metav1.TypeMeta{Kind: "Deployment", APIVersion: appsv1.SchemeGroupVersion.String()},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: labels,
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: labels,
				},
				Spec: corev1.PodSpec{
					NodeSelector: map[string]string{
						"node-role.kubernetes.io/control-plane": "",
					},
					Affinity: &corev1.Affinity{
						PodAntiAffinity: &corev1.PodAntiAffinity{
							RequiredDuringSchedulingIgnoredDuringExecution: []corev1.PodAffinityTerm{
								{
									LabelSelector: &metav1.LabelSelector{
										MatchExpressions: []metav1.LabelSelectorRequirement{
											{
												Key:      partOfKey,
												Operator: metav1.LabelSelectorOpIn,
												Values:   []string{labels[partOfKey]},
											},
											{
												Key:      componentKey,
												Operator: metav1.LabelSelectorOpIn,
												Values:   []string{labels[componentKey]},
											},
										},
									},
									TopologyKey: "kubernetes.io/hostname",
								},
							},
						},
					},
					ServiceAccountName: ServiceAccountName,
					Containers: []corev1.Container{
						{
							Name:            ControllerPluginName,
							Image:           di.Spec.DriverConfig.DriverImage,
							ImagePullPolicy: corev1.PullAlways,
							Args: []string{
								fmt.Sprintf("--drivername=%s", di.Name),
								fmt.Sprintf("--v=%v", di.Spec.LogLevel),
								"--nodeid=$(KUBE_NODE_NAME)",
								"--endpoint=unix:///csi/csi.sock",
								"--controllerserver",
							},
							Env: []corev1.EnvVar{
								{
									Name: "KUBE_NODE_NAME",
									ValueFrom: &corev1.EnvVarSource{
										FieldRef: &corev1.ObjectFieldSelector{
											APIVersion: "v1",
											FieldPath:  "spec.nodeName",
										},
									},
								},
								{
									Name: "DRIVER_NAMESPACE",
									ValueFrom: &corev1.EnvVarSource{
										FieldRef: &corev1.ObjectFieldSelector{
											APIVersion: "v1",
											FieldPath:  "metadata.namespace",
										},
									},
								},
							},
							EnvFrom: []corev1.EnvFromSource{
								{
									ConfigMapRef: &corev1.ConfigMapEnvSource{
										LocalObjectReference: corev1.LocalObjectReference{Name: ConfigMapName},
									},
								},
							},
							SecurityContext: &corev1.SecurityContext{
								Privileged: ptr.To(true),
							},
							Ports: []corev1.ContainerPort{
								{
									Name:          "healthz",
									ContainerPort: 9898,
									Protocol:      corev1.ProtocolTCP,
								},
							},
							LivenessProbe: &corev1.Probe{
								FailureThreshold: 5,
								ProbeHandler: corev1.ProbeHandler{
									HTTPGet: &corev1.HTTPGetAction{
										Path: "/healthz",
										Port: intstr.FromString("healthz"),
									},
								},
								InitialDelaySeconds: 10,
								TimeoutSeconds:      3,
								PeriodSeconds:       2,
							},
							VolumeMounts: []corev1.VolumeMount{
								socketDirMount,
							},
						},
						{
							Name:  "liveness-probe",
							Image: "registry.k8s.io/sig-storage/livenessprobe:v2.11.0",
							VolumeMounts: []corev1.VolumeMount{
								socketDirMount,
							},
							Args: []string{
								"--csi-address=/csi/csi.sock",
								"--health-port=9898",
							},
						},
						{
							Name:  "csi-provisioner",
							Image: "registry.k8s.io/sig-storage/csi-provisioner:v3.6.0",
							Args: []string{
								fmt.Sprintf("--v=%v", di.Spec.LogLevel),
								"--csi-address=/csi/csi.sock",
								"--leader-election=true",
							},
							SecurityContext: &corev1.SecurityContext{
								Privileged: ptr.To(true),
							},
							VolumeMounts: []corev1.VolumeMount{
								socketDirMount,
							},
						},
						{
							Name:  "csi-attacher",
							Image: "registry.k8s.io/sig-storage/csi-attacher:v4.4.0",
							Args: []string{
								fmt.Sprintf("--v=%v", di.Spec.LogLevel),
								"--csi-address=/csi/csi.sock",
								"--leader-election=true",
								"--http-endpoint=:8080",
							},
							VolumeMounts: []corev1.VolumeMount{
								socketDirMount,
							},
						},
					},
					PriorityClassName: "system-cluster-critical",
					Volumes: []corev1.Volume{
						{
							Name: socketDirMount.Name,
							VolumeSource: corev1.VolumeSource{
								EmptyDir: &corev1.EmptyDirVolumeSource{},
							},
						},
					},
				},
			},
		},
	}
	deployment.SetLabels(labels)
	return deployment
}

func GetNodeDaemonSetConfiguration(di *opencasv1alpha1.DriverInstance) *appsv1.DaemonSet {
	labels := di.GetDefaultLabels()
	labels["app.kubernetes.io/component"] = "node-plugin"
	for k, v := range di.Spec.Labels {
		labels[k] = v
	}

	socketDirMount := corev1.VolumeMount{
		Name:      "socket-dir",
		MountPath: "/csi",
	}
	mountpointDirMount := corev1.VolumeMount{
		Name:             "mountpoint-dir",
		MountPath:        fmt.Sprintf("%s/pods", k8sutils.ConfiguredKubeletDir()),
		MountPropagation: ptr.To(corev1.MountPropagationBidirectional),
	}
	pluginsDirMount := corev1.VolumeMount{
		Name:             "plugins-dir",
		MountPath:        fmt.Sprintf("%s/plugins", k8sutils.ConfiguredKubeletDir()),
		MountPropagation: ptr.To(corev1.MountPropagationBidirectional),
	}
	devDirMount := corev1.VolumeMount{
		Name:      "dev-dir",
		MountPath: "/dev",
	}
	registrationDirMount := corev1.VolumeMount{
		Name:      "registration-dir",
		MountPath: "/registration",
	}

	daemonSet := &appsv1.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      NodePluginName,
			Namespace: di.Spec.DriverConfig.DriverNamespace,
		},
		TypeMeta: metav1.TypeMeta{Kind: "DaemonSet", APIVersion: appsv1.SchemeGroupVersion.String()},
		Spec: appsv1.DaemonSetSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: labels,
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: labels,
				},
				Spec: corev1.PodSpec{
					NodeSelector:       di.Spec.NodeSelector,
					ServiceAccountName: ServiceAccountName,
					Containers: []corev1.Container{
						{
							Name:            NodePluginName,
							Image:           di.Spec.DriverConfig.DriverImage,
							ImagePullPolicy: corev1.PullAlways,
							Args: []string{
								fmt.Sprintf("--drivername=%s", di.Name),
								fmt.Sprintf("--v=%v", di.Spec.LogLevel),
								"--nodeid=$(KUBE_NODE_NAME)",
								"--endpoint=unix:///csi/csi.sock",
								"--nodeserver",
							},
							Env: []corev1.EnvVar{
								{
									Name: "KUBE_NODE_NAME",
									ValueFrom: &corev1.EnvVarSource{
										FieldRef: &corev1.ObjectFieldSelector{
											APIVersion: "v1",
											FieldPath:  "spec.nodeName",
										},
									},
								},
								{
									Name: "DRIVER_NAMESPACE",
									ValueFrom: &corev1.EnvVarSource{
										FieldRef: &corev1.ObjectFieldSelector{
											APIVersion: "v1",
											FieldPath:  "metadata.namespace",
										},
									},
								},
							},
							EnvFrom: []corev1.EnvFromSource{
								{
									ConfigMapRef: &corev1.ConfigMapEnvSource{
										LocalObjectReference: corev1.LocalObjectReference{Name: ConfigMapName},
									},
								},
							},
							SecurityContext: &corev1.SecurityContext{
								Privileged: ptr.To(true),
							},
							Ports: []corev1.ContainerPort{
								{
									Name:          "healthz",
									ContainerPort: 9898,
									Protocol:      corev1.ProtocolTCP,
								},
							},
							LivenessProbe: &corev1.Probe{
								FailureThreshold: 5,
								ProbeHandler: corev1.ProbeHandler{
									HTTPGet: &corev1.HTTPGetAction{
										Path: "/healthz",
										Port: intstr.FromString("healthz"),
									},
								},
								InitialDelaySeconds: 10,
								TimeoutSeconds:      3,
								PeriodSeconds:       2,
							},
							VolumeMounts: []corev1.VolumeMount{
								socketDirMount,
								mountpointDirMount,
								pluginsDirMount,
								devDirMount,
							},
						},
						{
							Name:  "liveness-probe",
							Image: "registry.k8s.io/sig-storage/livenessprobe:v2.11.0",
							VolumeMounts: []corev1.VolumeMount{
								socketDirMount,
							},
							Args: []string{
								"--csi-address=/csi/csi.sock",
								"--health-port=9898",
							},
						},
						{
							Name:  "node-driver-registrar",
							Image: "registry.k8s.io/sig-storage/csi-node-driver-registrar:v2.9.0",
							Args: []string{
								fmt.Sprintf("--v=%v", di.Spec.LogLevel),
								"--csi-address=/csi/csi.sock",
								fmt.Sprintf("--kubelet-registration-path=%s/plugins/%s/csi.sock", k8sutils.ConfiguredKubeletDir(), di.Name),
							},
							SecurityContext: &corev1.SecurityContext{
								Privileged: ptr.To(true),
							},
							Env: []corev1.EnvVar{
								{
									Name: "KUBE_NODE_NAME",
									ValueFrom: &corev1.EnvVarSource{
										FieldRef: &corev1.ObjectFieldSelector{
											APIVersion: "v1",
											FieldPath:  "spec.nodeName",
										},
									},
								},
							},
							VolumeMounts: []corev1.VolumeMount{
								socketDirMount,
								registrationDirMount,
							},
						},
					},
					PriorityClassName: "system-node-critical",
					Volumes: []corev1.Volume{
						{
							Name: socketDirMount.Name,
							VolumeSource: corev1.VolumeSource{
								HostPath: &corev1.HostPathVolumeSource{
									Path: fmt.Sprintf("%s/plugins/%s", k8sutils.ConfiguredKubeletDir(), di.Name),
									Type: ptr.To(corev1.HostPathDirectoryOrCreate),
								},
							},
						},
						{
							Name: pluginsDirMount.Name,
							VolumeSource: corev1.VolumeSource{
								HostPath: &corev1.HostPathVolumeSource{
									Path: fmt.Sprintf("%s/plugins", k8sutils.ConfiguredKubeletDir()),
									Type: ptr.To(corev1.HostPathDirectoryOrCreate),
								},
							},
						},
						{
							Name: registrationDirMount.Name,
							VolumeSource: corev1.VolumeSource{
								HostPath: &corev1.HostPathVolumeSource{
									Path: fmt.Sprintf("%s/plugins_registry", k8sutils.ConfiguredKubeletDir()),
									Type: ptr.To(corev1.HostPathDirectory),
								},
							},
						},
						{
							Name: mountpointDirMount.Name,
							VolumeSource: corev1.VolumeSource{
								HostPath: &corev1.HostPathVolumeSource{
									Path: fmt.Sprintf("%s/pods", k8sutils.ConfiguredKubeletDir()),
									Type: ptr.To(corev1.HostPathDirectoryOrCreate),
								},
							},
						},
						{
							Name: devDirMount.Name,
							VolumeSource: corev1.VolumeSource{
								HostPath: &corev1.HostPathVolumeSource{
									Path: "/dev",
									Type: ptr.To(corev1.HostPathDirectory),
								},
							},
						},
					},
				},
			},
		},
	}
	daemonSet.SetLabels(labels)
	return daemonSet
}
