/*
Copyright 2024 Huawei Technologies

SPDX-License-Identifier: Apache-2.0
*/

package csidriver

import (
	"context"
	"flag"
	"fmt"

	"k8s.io/klog/v2"
)

var (
	config      = Config{}
	showVersion = flag.Bool("version", false, "Show release version and exit")
	version     = "dev" // Set during build (TODO)
)

func init() {
	flag.StringVar(&config.DriverName, "drivername", "csi.open-cas.com", "name of the driver")
	flag.StringVar(&config.NodeID, "nodeid", "", "node id")
	flag.StringVar(&config.Endpoint, "endpoint", "unix:///tmp/csi.sock", "Open-CAS CSI endpoint")
	flag.BoolVar(&config.StartControllerServer, "controllerserver", false, "start Open-CAS CSI controller server")
	flag.BoolVar(&config.StartNodeServer, "nodeserver", false, "start Open-CAS CSI node server")

	klog.InitFlags(nil)
}

func Main() int {
	flag.Parse()
	if *showVersion {
		fmt.Println(version)
		return 0
	}

	ctx := context.Background()
	logger := klog.FromContext(ctx).WithName("Initialization")
	ctx = klog.NewContext(ctx, logger)

	logger.Info("Open-CAS CSI driver started", "version", version)
	defer logger.Info("Open-CAS CSI driver stopped")

	config.VendorVersion = version
	driver, err := NewCSIDriver(config)
	if err != nil {
		logger.Error(err, "failed to initialize driver")
		return 1
	}
	logger.V(5).Info("Open-CAS CSI driver initialized")

	if err = driver.Run(ctx); err != nil {
		logger.Error(err, "failed to start driver")
		return 1
	}

	return 0
}
