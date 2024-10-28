/*
Copyright 2024 Huawei Technologies

SPDX-License-Identifier: Apache-2.0
*/

package main

import (
	"os"

	csioperator "opencas-csi/pkg/csi-operator"
)

func main() {
	os.Exit(csioperator.Main())
}
