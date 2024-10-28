/*
Copyright 2017-2020 The Kubernetes Authors.
Copyright 2024 Huawei Technologies

SPDX-License-Identifier: Apache-2.0
*/

package csidriver

import (
	"context"
	"fmt"
	"opencas-csi/pkg/k8sutils"
	"os"
	"strings"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"k8s.io/utils/keymutex"
)

const backendScParam = "backend-sc-name"

type controllerServer struct {
	config Config
	mutex  keymutex.KeyMutex
}

func NewControllerServer(config Config) *controllerServer {
	return &controllerServer{
		config: config,
		mutex:  keymutex.NewHashed(-1),
	}
}

// ControllerGetCapabilities implements csi.ControllerServer.
func (cs *controllerServer) ControllerGetCapabilities(ctx context.Context, req *csi.ControllerGetCapabilitiesRequest) (*csi.ControllerGetCapabilitiesResponse, error) {
	// check for the situation where csi-provisioner side-container	is added (unnecessarily) to node-only plugin
	if cs != nil {
		return &csi.ControllerGetCapabilitiesResponse{Capabilities: cs.controllerCapabilities()}, nil
	}

	return nil, status.Error(codes.Unimplemented, "Controller server is not enabled - csi-provisioner error is expected.")
}

// CreateVolume implements csi.ControllerServer.
func (cs *controllerServer) CreateVolume(ctx context.Context, req *csi.CreateVolumeRequest) (*csi.CreateVolumeResponse, error) {
	// validateControllerServiceRequest returns status.Error
	if err := cs.validateControllerServiceRequest(csi.ControllerServiceCapability_RPC_CREATE_DELETE_VOLUME); err != nil {
		return nil, err
	}

	name := req.GetName()
	if len(name) == 0 {
		return nil, status.Error(codes.InvalidArgument, "create volume - name must be provided in request")
	}

	if req.GetVolumeCapabilities() == nil {
		return nil, status.Error(codes.InvalidArgument, "create volume - volume capabilities must be provided in request")
	}

	// isValidVolumeCapabilities returns status.Error
	if err := isValidVolumeCapabilities(req.GetVolumeCapabilities()); err != nil {
		return nil, err
	}

	parameters := req.GetParameters()
	if parameters == nil {
		parameters = make(map[string]string)
	}

	var subProvisionerStorageClass = ""
	// validate parameters (case-insensitive)
	for k, v := range parameters {
		param := strings.ToLower(k)
		switch param {
		case backendScParam:
			subProvisionerStorageClass = v
		default:
			return nil, status.Errorf(codes.InvalidArgument, "create volume - invalid parameter %q in storage class", k)
		}
	}

	if subProvisionerStorageClass == "" {
		return nil, status.Errorf(codes.InvalidArgument, "create volume - parameter 'backend-sc-name' required in Open-CAS storage class")
	}

	pvcConfig := k8sutils.PvcConfig{
		Name:             getPvcNameForPv(name),
		StorageClassName: subProvisionerStorageClass,
		Params:           parameters,
		RequiredBytes:    req.GetCapacityRange().GetRequiredBytes(),
		LimitBytes:       req.GetCapacityRange().GetLimitBytes(),
	}

	cs.mutex.LockKey(name)
	defer cs.mutex.UnlockKey(name)

	k8s, err := k8sutils.NewK8sUtils()
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	sc, err := k8s.GetScByName(subProvisionerStorageClass)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "create volume - cannot check if backend storage class %q exists", subProvisionerStorageClass)
	}
	if sc == nil {
		return nil, status.Errorf(codes.NotFound, "create volume - backend storage class %q does not exist", subProvisionerStorageClass)
	}

	pvc, err := k8s.GetSubproviderPvcByName(pvcConfig.Name)
	if err != nil && !strings.Contains(err.Error(), "not found") {
		return nil, status.Errorf(codes.Internal, "create volume - cannot check if PVC %q exists", pvcConfig.Name)
	}
	if pvc == nil {
		_, err = k8s.CreateSubproviderPvc(pvcConfig)
		if err != nil {
			return nil, status.Errorf(codes.Internal, "create volume - cannot create PVC %q: %v", pvcConfig.Name, err)
		}
	}

	topology := []*csi.Topology{{
		Segments: map[string]string{
			// TopologyKey: nodeId,
		}}}

	resp := &csi.CreateVolumeResponse{
		Volume: &csi.Volume{
			VolumeId:           name,
			CapacityBytes:      req.GetCapacityRange().GetRequiredBytes(),
			AccessibleTopology: topology,
			VolumeContext:      parameters,
		},
	}

	return resp, nil
}

func getPodNameForPv(pvName, nodeId string) string {
	return fmt.Sprintf("%s-%s", nodeId, pvName)
}

func getPvcNameForPv(pvName string) string {
	return fmt.Sprintf("opencas-csi-%s", pvName)
}

// DeleteVolume implements csi.ControllerServer.
func (cs *controllerServer) DeleteVolume(ctx context.Context, req *csi.DeleteVolumeRequest) (*csi.DeleteVolumeResponse, error) {
	if err := cs.validateControllerServiceRequest(csi.ControllerServiceCapability_RPC_CREATE_DELETE_VOLUME); err != nil {
		return nil, err
	}

	volId := req.GetVolumeId()
	if len(volId) == 0 {
		return nil, status.Error(codes.InvalidArgument, "delete volume - volume ID missing in request")
	}

	cs.mutex.LockKey(volId)
	defer cs.mutex.UnlockKey(volId)

	k8s, err := k8sutils.NewK8sUtils()
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	pvc, err := k8s.GetSubproviderPvcByName(getPvcNameForPv(volId))
	if err != nil && !strings.Contains(err.Error(), "not found") {
		return nil, status.Errorf(codes.Internal, "delete volume - cannot check if PVC %q exists", getPvcNameForPv(volId))
	}
	if pvc != nil {
		if err := k8s.DeleteSubproviderPvc(pvc.Name); err != nil && !strings.Contains(err.Error(), "not found") {
			return nil, status.Errorf(codes.Internal, "delete volume - cannot delete PVC %q, err: %s", pvc.Name, err.Error())
		}
	}

	return &csi.DeleteVolumeResponse{}, nil
}

// ValidateVolumeCapabilities implements csi.ControllerServer.
func (cs *controllerServer) ValidateVolumeCapabilities(ctx context.Context, req *csi.ValidateVolumeCapabilitiesRequest) (*csi.ValidateVolumeCapabilitiesResponse, error) {
	volId := req.GetVolumeId()
	if len(volId) == 0 {
		return nil, status.Error(codes.InvalidArgument, "validate volume capabilities - volume ID missing in request")
	}
	if /*cs.state.GetVolumeById(id)*/ req == nil {
		return nil, status.Errorf(codes.NotFound, "validate volume capabilities - no volume found with ID %s", volId)
	}
	if err := isValidVolumeCapabilities(req.GetVolumeCapabilities()); err != nil {
		return nil, err
	}

	return &csi.ValidateVolumeCapabilitiesResponse{
		Confirmed: &csi.ValidateVolumeCapabilitiesResponse_Confirmed{
			VolumeContext:      req.GetVolumeContext(),
			VolumeCapabilities: req.GetVolumeCapabilities(),
			Parameters:         req.GetParameters(),
		},
	}, nil
}

// isValidVolumeCapabilities validates the given VolumeCapability array is valid
func isValidVolumeCapabilities(volCaps []*csi.VolumeCapability) error {
	if len(volCaps) == 0 {
		return status.Error(codes.InvalidArgument, "volume capabilities missing in request")
	}
	for _, c := range volCaps {
		if c.GetMount() == nil && c.GetBlock() == nil {
			return status.Error(codes.InvalidArgument, "volume access type must be defined")
		}
	}
	return nil
}

func (cs *controllerServer) validateControllerServiceRequest(c csi.ControllerServiceCapability_RPC_Type) error {
	if c == csi.ControllerServiceCapability_RPC_UNKNOWN {
		return nil
	}

	for _, cap := range cs.controllerCapabilities() {
		if c == cap.GetRpc().GetType() {
			return nil
		}
	}
	return status.Error(codes.InvalidArgument, c.String())
}

func (cs *controllerServer) controllerCapabilities() []*csi.ControllerServiceCapability {
	caps := []csi.ControllerServiceCapability_RPC_Type{
		csi.ControllerServiceCapability_RPC_CREATE_DELETE_VOLUME,
		csi.ControllerServiceCapability_RPC_PUBLISH_UNPUBLISH_VOLUME,
		csi.ControllerServiceCapability_RPC_SINGLE_NODE_MULTI_WRITER,
	}

	var csc []*csi.ControllerServiceCapability

	for _, cap := range caps {
		csc = append(csc, &csi.ControllerServiceCapability{
			Type: &csi.ControllerServiceCapability_Rpc{
				Rpc: &csi.ControllerServiceCapability_RPC{
					Type: cap,
				},
			},
		})
	}
	return csc
}

// ControllerPublishVolume implements csi.ControllerServer.
func (cs *controllerServer) ControllerPublishVolume(ctx context.Context, req *csi.ControllerPublishVolumeRequest) (*csi.ControllerPublishVolumeResponse, error) {
	// validateControllerServiceRequest returns status.Error
	if err := cs.validateControllerServiceRequest(csi.ControllerServiceCapability_RPC_PUBLISH_UNPUBLISH_VOLUME); err != nil {
		return nil, err
	}

	volId := req.GetVolumeId()
	if len(volId) == 0 {
		return nil, status.Error(codes.InvalidArgument, "controller publish volume - volume ID must be provided in request")
	}

	nodeId := req.GetNodeId()
	if len(nodeId) == 0 {
		return nil, status.Error(codes.InvalidArgument, "controller publish volume - node ID must be provided in request")
	}

	if req.GetVolumeCapability() == nil {
		return nil, status.Error(codes.InvalidArgument, "controller publish volume - volume capability must be provided in request")
	}

	cs.mutex.LockKey(volId)
	defer cs.mutex.UnlockKey(volId)

	k8s, err := k8sutils.NewK8sUtils()
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	pvcName := getPvcNameForPv(volId)
	pvc, err := k8s.GetSubproviderPvcByName(pvcName)
	if err != nil {
		if !strings.Contains(err.Error(), "not found") {
			return nil, status.Errorf(codes.Internal, "controller publish volume - cannot check if PVC %q exists", pvcName)
		} else {
			return nil, status.Errorf(codes.NotFound, "controller publish volume - PVC %q does not exist", pvcName)
		}
	}

	podConfig := k8sutils.PodConfig{
		Name:        getPodNameForPv(volId, nodeId),
		Pvc:         pvc,
		CasadmImage: os.Getenv(k8sutils.CasadmImageEnv),
		NodeId:      nodeId,
		Namespace:   k8sutils.ConfiguredProtectedNamespace(),
	}
	pod, err := k8s.GetIntermediatePodByName(podConfig.Name, podConfig.Namespace)
	if err != nil && !strings.Contains(err.Error(), "not found") {
		return nil, status.Errorf(codes.Internal, "controller publish volume - cannot check if Pod %q exists", podConfig.Name)
	}
	if pod == nil {
		pod, err = k8s.CreateIntermediatePod(podConfig)
		if err != nil {
			if pod == nil {
				return nil, status.Errorf(codes.Internal, "controller publish volume - cannot create Pod %q, err: %s", podConfig.Name, err.Error())
			} else {
				k8s.DeleteIntermediatePod(pod.Name, pod.Namespace)
				return nil, status.Errorf(
					codes.Aborted,
					"controller publish volume - intermediate Pod %q not started correctly, err: %s", podConfig.Name, err.Error(),
				)
			}
		}
	}

	// get refreshed PVC bound to PV
	pvc, err = k8s.GetSubproviderPvcByName(pvcName)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "controller publish volume - cannot refresh PVC %q", pvcName)
	}

	if pvc.Spec.VolumeName == "" {
		return nil, status.Errorf(codes.NotFound, "controller publish volume - PV not bound for PVC %q", pvcName)
	}

	pv, err := k8s.GetPvByName(pvc.Spec.VolumeName)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "controller publish volume - cannot get PV %q", pvc.Spec.VolumeName)
	}

	publishContext := make(map[string]string)
	publishContext["backendPvName"] = pv.Name

	return &csi.ControllerPublishVolumeResponse{
		PublishContext: publishContext,
	}, nil
}

// ControllerUnpublishVolume implements csi.ControllerServer.
func (cs *controllerServer) ControllerUnpublishVolume(ctx context.Context, req *csi.ControllerUnpublishVolumeRequest) (*csi.ControllerUnpublishVolumeResponse, error) {
	if err := cs.validateControllerServiceRequest(csi.ControllerServiceCapability_RPC_PUBLISH_UNPUBLISH_VOLUME); err != nil {
		return nil, err
	}

	volId := req.GetVolumeId()
	if len(volId) == 0 {
		return nil, status.Error(codes.InvalidArgument, "controller unpublish volume - volume ID missing in request")
	}

	nodeId := req.GetNodeId()
	if len(nodeId) == 0 {
		return nil, status.Error(codes.InvalidArgument, "controller unpublish volume - node ID must be provided in request")
	}

	cs.mutex.LockKey(volId)
	defer cs.mutex.UnlockKey(volId)

	k8s, err := k8sutils.NewK8sUtils()
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	podName := getPodNameForPv(volId, nodeId)
	namespace := k8sutils.ConfiguredProtectedNamespace()
	pod, err := k8s.GetIntermediatePodByName(podName, namespace)
	if err != nil && !strings.Contains(err.Error(), "not found") {
		return nil, status.Errorf(codes.Internal, "controller unpublish volume - cannot check if Pod %q exists", podName)
	}
	if pod != nil {
		if err := k8s.DeleteIntermediatePod(pod.Name, pod.Namespace); err != nil && !strings.Contains(err.Error(), "not found") {
			return nil, status.Errorf(codes.Internal, "controller unpublish volume - cannot delete Pod %q, err: %s", pod.Name, err.Error())
		}
	}

	return &csi.ControllerUnpublishVolumeResponse{}, nil
}

// UNUSED

// ControllerGetVolume implements csi.ControllerServer.
func (cs *controllerServer) ControllerGetVolume(ctx context.Context, req *csi.ControllerGetVolumeRequest) (*csi.ControllerGetVolumeResponse, error) {
	return nil, status.Error(codes.Unimplemented, "ControllerGetVolume")
}

// GetCapacity implements csi.ControllerServer.
func (cs *controllerServer) GetCapacity(ctx context.Context, req *csi.GetCapacityRequest) (*csi.GetCapacityResponse, error) {
	return nil, status.Error(codes.Unimplemented, "GetCapacity")
}

// ListVolumes implements csi.ControllerServer.
func (cs *controllerServer) ListVolumes(ctx context.Context, req *csi.ListVolumesRequest) (*csi.ListVolumesResponse, error) {
	return nil, status.Error(codes.Unimplemented, "ListVolumes")
}

// ControllerExpandVolume implements csi.ControllerServer.
func (cs *controllerServer) ControllerExpandVolume(ctx context.Context, req *csi.ControllerExpandVolumeRequest) (*csi.ControllerExpandVolumeResponse, error) {
	return nil, status.Error(codes.Unimplemented, "ControllerExpandVolume")
}

// CreateSnapshot implements csi.ControllerServer.
func (cs *controllerServer) CreateSnapshot(ctx context.Context, req *csi.CreateSnapshotRequest) (*csi.CreateSnapshotResponse, error) {
	return nil, status.Error(codes.Unimplemented, "CreateSnapshot")
}

// DeleteSnapshot implements csi.ControllerServer.
func (cs *controllerServer) DeleteSnapshot(ctx context.Context, req *csi.DeleteSnapshotRequest) (*csi.DeleteSnapshotResponse, error) {
	return nil, status.Error(codes.Unimplemented, "DeleteSnapshot")
}

// ListSnapshots implements csi.ControllerServer.
func (cs *controllerServer) ListSnapshots(ctx context.Context, req *csi.ListSnapshotsRequest) (*csi.ListSnapshotsResponse, error) {
	return nil, status.Error(codes.Unimplemented, "ListSnapshots")
}

// ControllerModifyVolume implements csi.ControllerServer.
func (*controllerServer) ControllerModifyVolume(ctx context.Context, req *csi.ControllerModifyVolumeRequest) (*csi.ControllerModifyVolumeResponse, error) {
	return nil, status.Error(codes.Unimplemented, "ControllerModifyVolume")
}
