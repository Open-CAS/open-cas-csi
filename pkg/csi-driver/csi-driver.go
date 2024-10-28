/*
Copyright 2017-2020 The Kubernetes Authors.
Copyright 2024 Huawei Technologies

SPDX-License-Identifier: Apache-2.0
*/

package csidriver

import (
	"context"
	"errors"
	"fmt"
	"opencas-csi/pkg/k8sutils"
	"opencas-csi/pkg/utils"
	"os"
	"strings"

	"k8s.io/klog/v2"
)

type Config struct {
	DriverName            string
	Endpoint              string
	NodeID                string
	VendorVersion         string
	ShowVersion           bool
	DeviceManager         string
	SingleCache           bool
	StartControllerServer bool
	StartNodeServer       bool
}

type csiDriver struct {
	config           Config
	controllerServer *controllerServer
	nodeServer       *nodeServer
	identityServer   *identityServer
}

var TopologyKey string

func NewCSIDriver(cfg Config) (*csiDriver, error) {
	if cfg.DriverName == "" {
		return nil, errors.New("no driver name provided")
	}

	if cfg.NodeID == "" {
		return nil, errors.New("no node id provided")
	}

	if cfg.Endpoint == "" {
		return nil, errors.New("no driver endpoint provided")
	}

	TopologyKey = fmt.Sprintf("%s/topology", cfg.DriverName)

	logger := klog.FromContext(context.Background()).WithName("Init CSI driver")

	logger.Info(fmt.Sprintf("Driver: %v", cfg.DriverName))
	logger.Info(fmt.Sprintf("Version: %s", cfg.VendorVersion))

	driver := &csiDriver{
		config: cfg,
	}
	// by default start both controller and node servers
	if cfg.StartControllerServer || !cfg.StartNodeServer {
		// controller requires casadm image address
		casadmImage := os.Getenv(k8sutils.CasadmImageEnv)
		if casadmImage == "" {
			return nil, errors.New("casadm image not configured, check config map")
		}

		logger.Info("Configuring controller server")
		driver.controllerServer = NewControllerServer(cfg)
		driver.SetupProtection()
	}
	if cfg.StartNodeServer || !cfg.StartControllerServer {
		logger.Info("Configuring node server")
		driver.nodeServer = NewNodeServer(cfg)
	}
	logger.Info("Configuring identity server")
	driver.identityServer = NewIdentityServer(cfg)

	return driver, nil
}

func (d *csiDriver) Run(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	logger := klog.FromContext(ctx).WithName("Start Open-CAS CSI driver")

	s := NewNonBlockingGRPCServer()
	// Ensure that the server is stopped before we return.
	defer func() {
		s.ForceStop()
		s.Wait()
	}()

	logger.V(5).Info("Starting GRPC server")
	s.Start(d.config.Endpoint, d.identityServer, d.controllerServer, d.nodeServer, false)
	logger.Info("Driver ready")
	s.Wait()

	return nil
}

func (d *csiDriver) SetupProtection() {
	service := strings.ReplaceAll(d.config.DriverName, ".", "-")
	// predeploy.sh <serviceName> <namespace> <secretName>
	cmd := fmt.Sprintf(
		"/predeploy.sh %s %s %s",
		service,
		k8sutils.ConfiguredProtectedNamespace(),
		d.config.DriverName+"-protect")
	output, err := utils.ExecLocalCommand(cmd)
	if err != nil {
		klog.ErrorS(err, "failed Open-CAS protection setup")
		return
	}
	if output.ExitCode != 0 {
		klog.Error("failed Open-CAS protection setup", output.StdOut)
		return
	}

	serverImage := os.Getenv(k8sutils.WebhookImageEnv)
	if serverImage == "" {
		klog.Error("failed Open-CAS protection setup, webhook server image not configured")
		return
	}

	// deploy.sh <serviceName> <namespace> <secretName> <serverImage>
	cmd = fmt.Sprintf(
		"/deploy.sh %s %s %s %s",
		service,
		k8sutils.ConfiguredProtectedNamespace(),
		d.config.DriverName+"-protect",
		serverImage,
	)
	output, err = utils.ExecLocalCommand(cmd)
	if err != nil {
		klog.ErrorS(err, "failed Open-CAS protection setup")
		return
	}
	if output.ExitCode != 0 {
		klog.Error("failed Open-CAS protection setup", output.StdOut)
		return
	}

	klog.Info("Open-CAS protection setup complete")
}
