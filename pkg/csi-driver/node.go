/*
Copyright 2017 The Kubernetes Authors.
Copyright 2024 Huawei Technologies

SPDX-License-Identifier: Apache-2.0
*/

package csidriver

import (
	"context"
	"opencas-csi/pkg/k8sutils"
	"opencas-csi/pkg/opencas"
	"opencas-csi/pkg/utils"
	"os"
	"strconv"
	"strings"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"golang.org/x/exp/slices"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog/v2"
	mount "k8s.io/mount-utils"
	"k8s.io/utils/keymutex"
)

const defaultFilesystem = "ext4"

var mounter = mount.New("")

type nodeServer struct {
	config Config
	mutex  keymutex.KeyMutex
}

func NewNodeServer(config Config) *nodeServer {
	return &nodeServer{
		config: config,
		mutex:  keymutex.NewHashed(-1),
	}
}

// NodeGetCapabilities implements csi.NodeServer.
func (ns *nodeServer) NodeGetCapabilities(ctx context.Context, req *csi.NodeGetCapabilitiesRequest) (*csi.NodeGetCapabilitiesResponse, error) {
	caps := []csi.NodeServiceCapability_RPC_Type{
		csi.NodeServiceCapability_RPC_STAGE_UNSTAGE_VOLUME,
		csi.NodeServiceCapability_RPC_SINGLE_NODE_MULTI_WRITER,
	}

	var nsc []*csi.NodeServiceCapability

	for _, cap := range caps {
		nsc = append(nsc, &csi.NodeServiceCapability{
			Type: &csi.NodeServiceCapability_Rpc{
				Rpc: &csi.NodeServiceCapability_RPC{
					Type: cap,
				},
			},
		})
	}
	return &csi.NodeGetCapabilitiesResponse{Capabilities: nsc}, nil
}

// NodeGetInfo implements csi.NodeServer.
func (ns *nodeServer) NodeGetInfo(ctx context.Context, req *csi.NodeGetInfoRequest) (*csi.NodeGetInfoResponse, error) {
	return &csi.NodeGetInfoResponse{
		NodeId: ns.config.NodeID,
		/*AccessibleTopology: &csi.Topology{
			Segments: map[string]string{
				TopologyKey: ns.config.NodeID,
			},
		},*/
	}, nil
}

// NodeStageVolume implements csi.NodeServer.
func (ns *nodeServer) NodeStageVolume(ctx context.Context, req *csi.NodeStageVolumeRequest) (*csi.NodeStageVolumeResponse, error) {
	volId := req.GetVolumeId()
	if len(volId) == 0 {
		return nil, status.Error(codes.InvalidArgument, "node stage volume - volume ID missing in request")
	}
	stagingTargetPath := req.GetStagingTargetPath()
	if len(stagingTargetPath) == 0 {
		return nil, status.Error(codes.InvalidArgument, "node stage volume - staging target path missing in request")
	}
	volumeCapability := req.GetVolumeCapability()
	if volumeCapability == nil {
		return nil, status.Error(codes.InvalidArgument, "node stage volume - volume capabilities missing in request")
	}

	parameters := req.GetPublishContext()

	backendPvName := parameters["backendPvName"]
	if backendPvName == "" {
		return nil, status.Error(codes.Aborted, "node stage volume - volume parameters do not contain backend PV name")
	}

	ns.mutex.LockKey(volId)
	defer ns.mutex.UnlockKey(volId)

	k8s, err := k8sutils.NewK8sUtils()
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	podName := getPodNameForPv(volId, ns.config.NodeID)
	namespace := k8sutils.ConfiguredProtectedNamespace()
	pod, err := k8s.GetIntermediatePodByName(podName, namespace)
	if err != nil && !strings.Contains(err.Error(), "not found") {
		return nil, status.Errorf(codes.Internal, "node stage volume - cannot check if Pod %q exists", podName)
	}
	if pod == nil {
		return nil, status.Errorf(codes.NotFound, "node stage volume - intermediate Pod %q does not exist", podName)
	}

	cas, err := opencas.NewOpenCasManager(context.TODO(), ns.config.NodeID, namespace)
	if err != nil {
		return nil, status.Errorf(
			codes.Internal, "node stage volume - cannot use casadm in intermediate Pod %q\n err: %s", pod.Spec.NodeName, err.Error(),
		)
	}
	byIdPath, err := cas.ProvideByIdLinkForPv(backendPvName, volId)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "%s", err.Error())
	}
	cachedVolume, err := cas.GetVolumeFromCacheList(byIdPath)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "node stage volume - cannot check if cachedVolume exists in intermediate Pod %q", pod.Name)
	}
	if cachedVolume == nil {
		instances, err := cas.GetCacheInstances()
		if err != nil {
			return nil, status.Errorf(codes.Internal, "node stage volume - cannot list caches on Node %q", pod.Spec.NodeName)
		}
		var cache *opencas.Cache
		if len(instances) == 0 {
			return nil, status.Errorf(codes.NotFound, "node stage volume - no caches running on Node %q", pod.Spec.NodeName)
		} else {
			if node, err := k8s.GetClient().CoreV1().Nodes().Get(ctx, ns.config.NodeID, metav1.GetOptions{}); err == nil {
				if idString, ok := node.GetLabels()[k8sutils.OperatingCacheLabelKey]; ok {
					// if label exists, but is empty, then current cache is being removed
					if idString == "" {
						return nil, status.Errorf(codes.Unavailable,
							"node stage volume - Node %q caches are being reconciled", pod.Spec.NodeName)
					}
					if id, err := strconv.Atoi(idString); err == nil {
						for _, i := range instances {
							if i.Cache.CacheId == id {
								cache = i.Cache
							}
						}
					}
				} else if len(instances) == 1 {
					// if label doesn't exist, then caches were started manually
					cache = instances[0].Cache
				}
			}
			if cache == nil {
				return nil, status.Errorf(codes.InvalidArgument,
					"node stage volume - Node %q has multiple caches - cannot determine which should be used", pod.Spec.NodeName)
			}
		}
		cachedVolume, err = cas.AddCore(cache, byIdPath)
		if err != nil {
			return nil, status.Errorf(
				codes.Internal, "node stage volume - cannot create cachedVolume in intermediate Pod %q\n err: %s", pod.Name, err.Error(),
			)
		}
	}

	if blk := volumeCapability.GetBlock(); blk != nil {
		return &csi.NodeStageVolumeResponse{}, nil
	}

	fsType := defaultFilesystem
	mntOpts := []string{}
	if mnt := volumeCapability.GetMount(); mnt != nil {
		if mnt.FsType != "" {
			fsType = mnt.FsType
		}
		mountFlags := mnt.GetMountFlags()
		mntOpts = append(mntOpts, mountFlags...)
	}

	existingFsType, err := utils.GetDeviceFsType(cachedVolume.Path)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "node stage volume - cannot check file system for volume %q: %v", volId, err)
	}
	if existingFsType != "" {
		if existingFsType == fsType {
			klog.V(5).Infof("requested file system %q already present on volume %q", fsType, volId)
		} else {
			return nil, status.Errorf(codes.AlreadyExists, "node stage volume - file system %q found, requested file system %q", existingFsType, fsType)
		}
	} else if err := utils.PrepareFs(cachedVolume.Path, fsType); err != nil {
		return nil, status.Errorf(codes.Internal, "node stage volume - cannot prepare volume %q: %v", volId, err)
	}

	if err := utils.Mount(mounter, cachedVolume.Path, stagingTargetPath, fsType, mntOpts, false); err != nil {
		return nil, status.Errorf(codes.Internal, "node stage volume - cannot mount volume %q: %v", volId, err)
	}

	return &csi.NodeStageVolumeResponse{}, nil
}

// NodeUnstageVolume implements csi.NodeServer.
func (ns *nodeServer) NodeUnstageVolume(ctx context.Context, req *csi.NodeUnstageVolumeRequest) (*csi.NodeUnstageVolumeResponse, error) {
	volId := req.GetVolumeId()
	if len(volId) == 0 {
		return nil, status.Error(codes.InvalidArgument, "node unstage volume - volume ID missing in request")
	}
	stagingTargetPath := req.GetStagingTargetPath()
	if len(stagingTargetPath) == 0 {
		return nil, status.Error(codes.InvalidArgument, "node unstage volume - staging target path missing in request")
	}

	ns.mutex.LockKey(volId)
	defer ns.mutex.UnlockKey(volId)

	mountedDev, _, err := mount.GetDeviceNameFromMount(mounter, stagingTargetPath)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "node unstage volume - %v", err)
	}
	if mountedDev != "" {
		if err := mounter.Unmount(stagingTargetPath); err != nil {
			return nil, status.Errorf(codes.Internal, "node unstage volume - %v", err)
		}
	}

	byIdPath := opencas.GetByIdLinkPathForPv(volId)
	cleanupByIdDir := func() {
		os.Remove(byIdPath)
		os.Remove(opencas.CsiDriverByIdDir) // does not remove dir if not empty
	}
	// unstage for unattached PV should succeed (e.g. when node is restored after failure)
	// no by-id link or broken by-id link
	if _, err := os.Stat(byIdPath); err != nil {
		klog.ErrorS(err, "err: ")
		if os.IsNotExist(err) {
			cleanupByIdDir()
			return &csi.NodeUnstageVolumeResponse{}, nil
		} else {
			return nil, status.Error(codes.Internal, err.Error())
		}
	}

	k8s, err := k8sutils.NewK8sUtils()
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	pv, err := k8s.GetPvByName(volId)
	if err != nil && !strings.Contains(err.Error(), "not found") {
		return nil, status.Errorf(codes.Internal, "node unstage volume - cannot retrieve PV %q", volId)
	}

	if pv == nil {
		cleanupByIdDir()
		return &csi.NodeUnstageVolumeResponse{}, nil
	}

	podName := getPodNameForPv(volId, ns.config.NodeID)
	namespace := k8sutils.ConfiguredProtectedNamespace()
	pod, err := k8s.GetIntermediatePodByName(podName, namespace)
	if err != nil && !strings.Contains(err.Error(), "not found") {
		return nil, status.Errorf(codes.Internal, "node unstage volume - cannot check if Pod %q exists", podName)
	}

	if pod == nil {
		return nil, status.Errorf(
			codes.FailedPrecondition,
			"node unstage volume - cannot remove existing PV %q, because Pod %q does not exist",
			volId, podName)
	}

	cas, err := opencas.NewOpenCasManager(context.TODO(), ns.config.NodeID, namespace)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "node unstage volume - cannot use casadm in intermediate Pod %q", pod.Spec.NodeName)
	}

	volume, err := cas.GetVolumeFromCacheList(byIdPath)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "node unstage volume - cannot get cached volume in intermediate Pod %q", pod.Spec.NodeName)
	}

	if volume != nil {
		err = cas.RemoveCore(volume)
		if err != nil {
			return nil, status.Errorf(codes.Internal, "node unstage volume - cannot remove cached volume in intermediate Pod %q", pod.Spec.NodeName)
		}
	}

	cleanupByIdDir()
	return &csi.NodeUnstageVolumeResponse{}, nil
}

// NodePublishVolume implements csi.NodeServer.
func (ns *nodeServer) NodePublishVolume(ctx context.Context, req *csi.NodePublishVolumeRequest) (*csi.NodePublishVolumeResponse, error) {
	volId := req.GetVolumeId()
	if len(volId) == 0 {
		return nil, status.Error(codes.InvalidArgument, "node publish volume - volume ID missing in request")
	}
	volumeCapability := req.GetVolumeCapability()
	if volumeCapability == nil {
		return nil, status.Error(codes.InvalidArgument, "node publish volume - volume capabilities missing in request")
	}
	targetPath := req.GetTargetPath()
	if len(targetPath) == 0 {
		return nil, status.Error(codes.InvalidArgument, "node publish volume - target path missing in request")
	}

	sourcePath := req.GetStagingTargetPath()
	fsType := volumeCapability.GetMount().GetFsType()
	readOnly := req.GetReadonly()
	mountFlags := volumeCapability.GetMount().GetMountFlags()
	mountFlags = append(mountFlags, "bind")
	if readOnly {
		mountFlags = append(mountFlags, "ro")
	}

	ns.mutex.LockKey(volId)
	defer ns.mutex.UnlockKey(volId)

	rawBlock := false

	if blk := volumeCapability.GetBlock(); blk != nil {
		rawBlock = true

		k8s, err := k8sutils.NewK8sUtils()
		if err != nil {
			return nil, status.Error(codes.Internal, err.Error())
		}

		podName := getPodNameForPv(volId, ns.config.NodeID)
		namespace := k8sutils.ConfiguredProtectedNamespace()
		pod, err := k8s.GetIntermediatePodByName(podName, namespace)
		if err != nil {
			klog.ErrorS(err, "GET POD ERROR")
			return nil, status.Errorf(codes.Internal, "node publish volume - cannot check if Pod %q exists", podName)
		}
		if pod == nil {
			return nil, status.Errorf(codes.NotFound, "node publish volume - intermediate Pod %q does not exist", podName)
		}

		cas, err := opencas.NewOpenCasManager(context.TODO(), ns.config.NodeID, namespace)
		if err != nil {
			return nil, status.Errorf(codes.Internal, "node publish volume - cannot use casadm in intermediate Pod %q", pod.Spec.NodeName)
		}
		byIdPath := opencas.GetByIdLinkPathForPv(volId)
		cachedVolume, err := cas.GetVolumeFromCacheList(byIdPath)
		if err != nil {
			return nil, status.Errorf(codes.Internal, "node publish volume - cannot check if cachedVolume exists in intermediate Pod %q", pod.Name)
		}
		if cachedVolume == nil {
			return nil, status.Errorf(codes.NotFound, "node publish volume - cached volume %q does not exist", byIdPath)
		}
		// For block volumes, source path is the actual Device path
		sourcePath = cachedVolume.Path
	} else if mnt := volumeCapability.GetMount(); mnt != nil {
		if len(sourcePath) == 0 {
			return nil, status.Error(codes.FailedPrecondition, "node publish volume - staging target path missing in request")
		}
		isMnt, err := mounter.IsMountPoint(targetPath)
		if err != nil && !os.IsNotExist(err) {
			return nil, status.Errorf(codes.Internal, "node publish volume - cannot validate target path: %v", err)
		}
		if isMnt {
			// Check if mount is compatible. Return OK if these match:
			// 1) Requested target path MUST match the published path of that volume ID
			// 2) VolumeCapability MUST match
			//    VolumeCapability/Mountflags must match used flags.
			//    VolumeCapability/fsType (if present in request) must match used fsType.
			// 3) Readonly MUST match
			// If there is mismatch of any of above, we return ALREADY_EXISTS error.
			mountPoints, err := mounter.List()
			if err != nil {
				return nil, status.Errorf(
					codes.Internal, "node publish volume - cannot fetch existing mount points while checking %q: %v", targetPath, err,
				)
			}
			for i := range mountPoints {
				if mountPoints[i].Path == targetPath {
					if (fsType == "" || mountPoints[i].Type == fsType) && findMountFlags(mountFlags, mountPoints[i].Opts) {
						return &csi.NodePublishVolumeResponse{}, nil
					}
					return nil, status.Errorf(codes.AlreadyExists, "node publish volume - volume %q is published but incompatible", volId)
				}
			}
		}
	}

	if err := utils.Mount(mounter, sourcePath, targetPath, fsType, mountFlags, rawBlock); err != nil {
		return nil, status.Errorf(codes.Internal, "node publish volume - cannot mount target path %q: %v", targetPath, err)
	}

	return &csi.NodePublishVolumeResponse{}, nil
}

// NodeUnpublishVolume implements csi.NodeServer.
func (ns *nodeServer) NodeUnpublishVolume(ctx context.Context, req *csi.NodeUnpublishVolumeRequest) (*csi.NodeUnpublishVolumeResponse, error) {
	volId := req.GetVolumeId()
	if len(volId) == 0 {
		return nil, status.Error(codes.InvalidArgument, "node unpublish volume - volume ID missing in request")
	}
	targetPath := req.GetTargetPath()
	if len(targetPath) == 0 {
		return nil, status.Error(codes.InvalidArgument, "node unpublish volume - target path missing in request")
	}

	ns.mutex.LockKey(volId)
	defer ns.mutex.UnlockKey(volId)

	notMnt, err := mounter.IsLikelyNotMountPoint(targetPath)
	if err != nil {
		if os.IsNotExist(err) {
			// no mountpoint -> OK
			return &csi.NodeUnpublishVolumeResponse{}, nil
		}

		return nil, status.Errorf(codes.Internal, "node unpublish volume - cannot determine if %q is a valid mount point: %v", targetPath, err)
	}

	// targetPath exists
	if !notMnt {
		// unmount if mounted
		if err := mounter.Unmount(targetPath); err != nil {
			return nil, status.Errorf(codes.Internal, "node unpublish volume - cannot unmount target path %q: %v", targetPath, err)
		}
	}

	err = os.Remove(targetPath)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "node unpublish volume - cannot remove target path %q: %v", targetPath, err)
	}

	return &csi.NodeUnpublishVolumeResponse{}, nil
}

func findMountFlags(flags []string, findIn []string) bool {
	for _, f := range flags {
		if idx := slices.IndexFunc(findIn, func(fIn string) bool {
			return f == "dax=always" && fIn == "dax" ||
				f == "dax" && fIn == "dax=always" ||
				f == fIn
		}); idx < 0 {
			return false
		}
	}

	return true
}

// NOT USED

// NodeExpandVolume implements csi.NodeServer.
func (ns *nodeServer) NodeExpandVolume(ctx context.Context, req *csi.NodeExpandVolumeRequest) (*csi.NodeExpandVolumeResponse, error) {
	return nil, status.Error(codes.Unimplemented, "NodeExpandVolume")
}

// NodeGetVolumeStats implements csi.NodeServer.
func (ns *nodeServer) NodeGetVolumeStats(ctx context.Context, req *csi.NodeGetVolumeStatsRequest) (*csi.NodeGetVolumeStatsResponse, error) {
	return nil, status.Error(codes.Unimplemented, "NodeGetVolumeStats")
}
