/*
Copyright 2024 Huawei Technologies

SPDX-License-Identifier: Apache-2.0
*/

package main

import (
	"opencas-csi/pkg/webhook"
	"os"
)

func main() {
	os.Exit(webhook.Main())
}
