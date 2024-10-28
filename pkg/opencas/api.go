/*
Copyright 2024 Huawei Technologies

SPDX-License-Identifier: Apache-2.0
*/

package opencas

import (
	"context"
	"encoding/csv"
	"fmt"
	"io"
	"opencas-csi/pkg/k8sutils"
	"opencas-csi/pkg/utils"
	"os"
	"regexp"
	"strconv"
	"strings"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog/v2"
	"k8s.io/utils/ptr"
)

type OpenCAS struct {
	k8sUtils  *k8sutils.K8sUtils
	ctx       context.Context
	nodeId    string
	namespace string
}

type Cache struct {
	CacheId    int                      `json:"cache_id"`
	DevicePath string                   `json:"device_path"`
	Status     string                   `json:"status"`
	CacheMode  CacheMode                `json:"cache_mode"`
	Volumes    map[string]*CachedVolume `json:"-"`
}

type CacheInstance struct {
	Cache *Cache `json:"cache"`
}

type CachedVolume struct {
	CoreId      int    `json:"core_id"`
	BackendPath string `json:"device_path"`
	Status      string `json:"status"`
	Path        string `json:"path"`
	Cache       *Cache `json:"cache"`
}

type CacheLineSize uint8

const (
	CLS_4k  CacheLineSize = 4
	CLS_8k  CacheLineSize = 8
	CLS_16k CacheLineSize = 16
	CLS_64k CacheLineSize = 64
)

type CacheMode string

const (
	WriteThrough CacheMode = "wt"
	WriteBack    CacheMode = "wb"
	WriteAround  CacheMode = "wa"
	PassThrough  CacheMode = "pt"
	WriteOnly    CacheMode = "wo"
)

const (
	casadmPodLabel   = "run=casadm"
	CsiDriverByIdDir = "/dev/disk/by-id/opencas-csi"
)

func NewOpenCasManager(ctx context.Context, nodeId, namespace string) (*OpenCAS, error) {
	cas := &OpenCAS{
		ctx:       ctx,
		nodeId:    nodeId,
		namespace: namespace,
	}
	logger := klog.FromContext(ctx).WithName("Init Open-CAS manager")

	if k8sUtils, err := k8sutils.NewK8sUtils(); err != nil {
		return nil, err
	} else {
		cas.k8sUtils = k8sUtils
	}
	logger.V(5).Info("K8s connectivity initialized")

	return cas, nil
}

func NewCasadmJob() *batchv1.Job {
	return &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{},
		Spec: batchv1.JobSpec{
			Completions: ptr.To(int32(1)),
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					RestartPolicy: corev1.RestartPolicyNever,
					Containers: []corev1.Container{
						{
							Image:           os.Getenv(k8sutils.CasadmImageEnv),
							ImagePullPolicy: corev1.PullAlways,
							Name:            "casadm",
							SecurityContext: &corev1.SecurityContext{
								Privileged: ptr.To(true),
							},
							VolumeMounts: []corev1.VolumeMount{
								{
									Name:      "dev-dir",
									MountPath: "/dev",
								},
								{
									Name:      "plugins-dir",
									MountPath: k8sutils.ConfiguredKubeletDir() + "/plugins",
								},
							},
						},
					},
					Volumes: []corev1.Volume{
						{
							Name: "dev-dir",
							VolumeSource: corev1.VolumeSource{
								HostPath: &corev1.HostPathVolumeSource{
									Path: "/dev",
									Type: ptr.To(corev1.HostPathDirectory),
								},
							},
						},
						{
							Name: "plugins-dir",
							VolumeSource: corev1.VolumeSource{
								HostPath: &corev1.HostPathVolumeSource{
									Path: k8sutils.ConfiguredKubeletDir() + "/plugins",
									Type: ptr.To(corev1.HostPathDirectory),
								},
							},
						},
					},
				},
			},
		},
	}
}

func (cas *OpenCAS) ExecCasadmCommandAsJob(ctx context.Context, cmd, nodeId, jobName, namespace string) (output *utils.Output, err error) {
	job := NewCasadmJob()
	job.Name = jobName
	job.Namespace = namespace
	job.Spec.Template.Spec.Containers[0].Command = strings.Split(cmd, " ")
	job.Spec.TTLSecondsAfterFinished = ptr.To(int32(60))
	if nodeId != "" {
		job.Spec.Template.Spec.NodeName = nodeId
	}
	return cas.k8sUtils.ExecCommandJob(ctx, job)
}

// Operator handles starting cache
func (cas *OpenCAS) StartCache(path string, mode CacheMode, cls CacheLineSize) (*Cache, error) {
	cmd := startCacheCmd(path, mode, cls, -1 /*cacheId autoassign*/)
	jobName := cas.getJobName(cmd)
	output, err := cas.ExecCasadmCommandAsJob(cas.ctx, cmd, cas.nodeId, jobName, cas.namespace)
	if err != nil {
		return nil, err
	}
	if output.ExitCode != 0 {
		return nil, fmt.Errorf(
			"unexpected error in command %q (job %v/%v): exit_code=%d %s",
			cmd, cas.namespace, jobName, output.ExitCode, output.StdOut,
		)
	}
	if caches, err := cas.ListCaches(); err != nil {
		return nil, err
	} else {
		for _, cache := range caches {
			if cache.DevicePath == path {
				return cache, nil
			}
		}
	}

	return nil, fmt.Errorf("command %q completed (job %v/%v), but cache was not found in cache list", cmd, cas.namespace, jobName)
}

func (cas *OpenCAS) StopCache(c *Cache) error {
	cmd := stopCacheCmd(c.CacheId)
	jobName := cas.getJobName(cmd)
	output, err := cas.ExecCasadmCommandAsJob(cas.ctx, cmd, cas.nodeId, jobName, cas.namespace)
	if err != nil {
		return err
	}
	if output.ExitCode != 0 && !strings.Contains(output.String(), "Cache ID does not exist") {
		return fmt.Errorf(
			"unexpected error in command %q (job %v/%v): exit_code=%d %s",
			cmd, cas.namespace, jobName, output.ExitCode, output.StdOut,
		)
	}

	if caches, err := cas.ListCaches(); err != nil {
		return err
	} else {
		for _, cache := range caches {
			if cache.DevicePath == c.DevicePath {
				return fmt.Errorf("command %q completed (job %v/%v), but cache still exists in system", cmd, cas.namespace, jobName)
			}
		}
	}

	return nil
}

func (cas *OpenCAS) AddCore(c *Cache, path string) (*CachedVolume, error) {
	cmd := addCoreCmd(c.CacheId, path, -1)
	jobName := cas.getJobName(cmd)
	output, err := cas.ExecCasadmCommandAsJob(cas.ctx, cmd, cas.nodeId, jobName, cas.namespace)
	if err != nil {
		return nil, err
	}
	if output.ExitCode != 0 {
		return nil, fmt.Errorf(
			"unexpected error in command %q (job %v/%v): exit_code=%d %s",
			cmd, cas.namespace, jobName, output.ExitCode, output.StdOut,
		)
	}

	core, err := cas.GetVolumeFromCacheList(path)
	if err != nil {
		return nil, err
	}
	if core == nil {
		return nil, fmt.Errorf("command %q completed (job %v/%v), but cached volume was not found in cache list", cmd, cas.namespace, jobName)
	}
	if exists, err := cas.isDeviceInSystem(core.Path); err != nil {
		return nil, err
	} else if !exists {
		return nil, fmt.Errorf("command %q completed (job %v/%v), but cached volume was not found in system", cmd, cas.namespace, jobName)
	}

	return core, nil
}

func (cas *OpenCAS) RemoveCore(v *CachedVolume) error {
	cmd := removeCoreCmd(v.Cache.CacheId, v.CoreId)
	jobName := cas.getJobName(cmd)
	output, err := cas.ExecCasadmCommandAsJob(cas.ctx, cmd, cas.nodeId, jobName, cas.namespace)
	if err != nil {
		return err
	}
	if output.ExitCode != 0 &&
		!(strings.Contains(output.String(), "Cache ID does not exist") ||
			strings.Contains(output.String(), "Core ID does not exist")) {
		return fmt.Errorf(
			"unexpected error in command %q (job %v/%v): exit_code=%d %s",
			cmd, cas.namespace, jobName, output.ExitCode, output.StdOut,
		)
	}

	if core, err := cas.GetVolumeFromCacheList(v.BackendPath); err != nil {
		return err
	} else if core != nil {
		return fmt.Errorf("command %q completed (job %v/%v), but cached volume still exists in cache list", cmd, cas.namespace, jobName)
	}
	if exists, err := cas.isDeviceInSystem(v.Path); err != nil {
		return err
	} else if exists {
		return fmt.Errorf("command %q completed (job %v/%v), but cached volume still exists in system", cmd, cas.namespace, jobName)
	}

	return nil
}

func (cas *OpenCAS) ListCaches() ([]*Cache, error) {
	cmd := listCachesCmd()
	output, err := cas.ExecCasadmCommandAsJob(cas.ctx, cmd, cas.nodeId, cas.getJobName(cmd), cas.namespace)
	klog.V(5).Info(output)
	if err != nil {
		return nil, err
	}

	return parseCacheList(output.StdOut), nil
}

func (cas *OpenCAS) GetStatistics(cacheId, coreId int) (map[string]string, error) {
	cmd := statsCmd(cacheId, coreId)
	output, err := cas.ExecCasadmCommandAsJob(cas.ctx, cmd, cas.nodeId, cas.getJobName(cmd), cas.namespace)
	if err != nil {
		return nil, err
	}

	return parseStats(output.StdOut)
}

func (cas *OpenCAS) SetCacheMode(c *Cache, mode CacheMode) error {
	cmd := setCacheModeCmd(c.CacheId, mode)
	jobName := cas.getJobName(cmd)
	output, err := cas.ExecCasadmCommandAsJob(cas.ctx, cmd, cas.nodeId, jobName, cas.namespace)
	if err != nil {
		return err
	}
	if output.ExitCode != 0 {
		return fmt.Errorf(
			"unexpected error in command %q (job %v/%v): exit_code=%d %s",
			cmd, cas.namespace, jobName, output.ExitCode, output.StdOut,
		)
	}
	if caches, err := cas.ListCaches(); err != nil {
		return err
	} else {
		for _, cache := range caches {
			if cache.CacheId == c.CacheId {
				if cache.CacheMode == mode {
					return nil
				} else {
					return fmt.Errorf(
						"command %q completed (job %v/%v), but cache mode was not changed from %s to %s",
						cmd, cas.namespace, jobName, cache.CacheMode, mode,
					)
				}
			}
		}
	}

	return fmt.Errorf("command %q completed (job %v/%v), but cache was not found in cache list", cmd, cas.namespace, jobName)
}

func (cas *OpenCAS) GetCacheLineSize(c *Cache) (CacheLineSize, error) {
	cacheStats, err := cas.GetStatistics(c.CacheId, -1)
	if err != nil {
		return 0, err
	}
	num, err := strconv.ParseUint(cacheStats["Cache line size [KiB]"], 10, 8)
	if err != nil {
		return 0, err
	}

	return CacheLineSize(num), nil
}

func (cas *OpenCAS) GetVolumeFromCacheList(backendPath string) (*CachedVolume, error) {
	if caches, err := cas.ListCaches(); err != nil {
		return nil, err
	} else {
		for _, cache := range caches {
			if core, ok := cache.Volumes[backendPath]; ok {
				return core, nil
			}
		}
		return nil, nil
	}
}

func (cas *OpenCAS) RemoveByIdLink(linkName string) error {
	cmd := fmt.Sprintf("rm -f %s/%s", CsiDriverByIdDir, linkName)
	_, err := utils.ExecLocalCommand(cmd)
	if err != nil {
		return err
	}
	return nil
}

func GetByIdLinkPathForPv(pvName string) string {
	return fmt.Sprintf("%s/%s", CsiDriverByIdDir, pvName)
}

func (cas *OpenCAS) ProvideByIdLinkForPv(pvSourceName, pvLinkName string) (string, error) {
	cmd := fmt.Sprintf("find -L %s/ -type b -path '*/%s/*' -not -path '*/publish/*' -not -path '*/staging/*'",
		k8sutils.ConfiguredKubeletDir(),
		pvSourceName)
	output, err := utils.ExecLocalCommand(cmd)
	if err != nil {
		return "", err
	}
	// there should be one path (publish and stage are filtered out), but split just in case there are more paths found
	devPath := strings.TrimSpace(strings.SplitN(output.StdOut, "\n", 2)[0])
	byIdPath := GetByIdLinkPathForPv(pvLinkName)
	// link creation forced (in case there are artifacts from previous placements on this node)
	cmd = fmt.Sprintf("mkdir -p %s && ln -f -s %s %s", CsiDriverByIdDir, devPath, byIdPath)
	output, err = utils.ExecLocalCommand(cmd)
	if err != nil {
		return "", err
	}
	if output.ExitCode != 0 {
		return "", fmt.Errorf("failed to create by-id link for PV %q", pvSourceName)
	}
	return byIdPath, nil
}

func (cas *OpenCAS) GetCacheInstances() ([]*CacheInstance, error) {
	caches, err := cas.ListCaches()
	if err != nil {
		return nil, err
	}
	instances := []*CacheInstance{}
	var current *CacheInstance
	for _, cache := range caches {
		current = &CacheInstance{
			Cache: cache,
		}
		instances = append(instances, current)
	}
	return instances, nil
}

func (cas *OpenCAS) isDeviceInSystem(devicePath string) (bool, error) {
	cmd := fmt.Sprintf("ls -1 %s", devicePath)
	output, err := utils.ExecLocalCommand(cmd)
	if err != nil {
		return false, err
	}
	return output.ExitCode == 0, nil
}

func parseCacheList(output string) []*Cache {
	caches := []*Cache{}
	var cache *Cache

	r := csv.NewReader(strings.NewReader(output))
iteration:
	for params, err := r.Read(); err != io.EOF; params, err = r.Read() {
		// params: type,id,disk,status,write policy,device
		switch params[0] {
		case "type":
			continue
		case "cache":
			id, _ := strconv.Atoi(params[1])
			cache = &Cache{
				CacheId:    id,
				DevicePath: params[2],
				Status:     params[3],
				CacheMode:  CacheMode(params[4]),
				Volumes:    make(map[string]*CachedVolume),
			}
			caches = append(caches, cache)
		case "core":
			id, _ := strconv.Atoi(params[1])
			backendPath := params[2]
			cache.Volumes[backendPath] = &CachedVolume{
				CoreId:      id,
				BackendPath: backendPath,
				Status:      params[3],
				Path:        params[5],
				Cache:       cache,
			}
		case "core pool":
			break iteration
		}
	}
	// end iteration
	return caches
}

func (cas *OpenCAS) getJobName(cmd string) string {
	// shorten node, cmd and remove chars not allowed in job name
	node := strings.Split(cas.nodeId, ".")[0]
	for k, v := range replace {
		cmd = strings.ReplaceAll(cmd, k, v)
	}
	match, err := regexp.Compile(`([^-\.a-zA-Z0-9])+`)
	if err == nil {
		output := strings.ToLower(node + match.ReplaceAllString(cmd, ""))
		// k8s job name length restriction
		maxJobNameLength := 63
		if len(output) > maxJobNameLength {
			return strings.TrimRight(output[:maxJobNameLength], "-.")
		} else {
			return output
		}
	}
	return ""
}

func parseStats(output string) (map[string]string, error) {
	r := csv.NewReader(strings.NewReader(output))
	header, err := r.Read()
	if err != nil {
		return nil, err
	}
	values, err := r.Read()
	if err != nil && err != io.EOF {
		return nil, err
	}
	if len(header) != len(values) {
		return nil, fmt.Errorf("cannot parse stats, different lengths for header and values\n%s", output)
	}

	statsMap := make(map[string]string)
	for i, key := range header {
		statsMap[key] = values[i]
	}

	return statsMap, nil
}
