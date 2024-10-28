/*
Copyright 2024 Huawei Technologies

SPDX-License-Identifier: Apache-2.0
*/

package main

import (
	"os"

	csidriver "opencas-csi/pkg/csi-driver"
)

func main() {
	os.Exit(csidriver.Main())
}
