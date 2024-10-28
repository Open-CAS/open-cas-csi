/*
Copyright 2017-2020 The Kubernetes Authors.
Copyright 2024 Huawei Technologies

SPDX-License-Identifier: Apache-2.0
*/

package csidriver

import (
	"context"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	wrappers "google.golang.org/protobuf/types/known/wrapperspb"
)

type identityServer struct {
	config Config
}

func NewIdentityServer(config Config) *identityServer {
	return &identityServer{
		config: config,
	}
}

// GetPluginCapabilities implements csi.IdentityServer.
func (is *identityServer) GetPluginCapabilities(ctx context.Context, req *csi.GetPluginCapabilitiesRequest) (*csi.GetPluginCapabilitiesResponse, error) {
	return &csi.GetPluginCapabilitiesResponse{
		Capabilities: []*csi.PluginCapability{
			{
				Type: &csi.PluginCapability_Service_{
					Service: &csi.PluginCapability_Service{
						Type: csi.PluginCapability_Service_CONTROLLER_SERVICE,
					},
				},
			},
			{
				Type: &csi.PluginCapability_Service_{
					Service: &csi.PluginCapability_Service{
						Type: csi.PluginCapability_Service_VOLUME_ACCESSIBILITY_CONSTRAINTS,
					},
				},
			},
		},
	}, nil
}

// GetPluginInfo implements csi.IdentityServer.
func (is *identityServer) GetPluginInfo(ctx context.Context, req *csi.GetPluginInfoRequest) (*csi.GetPluginInfoResponse, error) {
	if is.config.DriverName == "" {
		return nil, status.Error(codes.Unavailable, "Driver name not configured")
	}

	if is.config.VendorVersion == "" {
		return nil, status.Error(codes.Unavailable, "Driver is missing version")
	}

	return &csi.GetPluginInfoResponse{
		Name:          is.config.DriverName,
		VendorVersion: is.config.VendorVersion,
	}, nil
}

// Probe implements csi.IdentityServer.
func (is *identityServer) Probe(ctx context.Context, res *csi.ProbeRequest) (*csi.ProbeResponse, error) {
	return &csi.ProbeResponse{Ready: &wrappers.BoolValue{Value: true}}, nil
}
