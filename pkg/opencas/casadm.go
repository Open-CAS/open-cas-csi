/*
Copyright 2024 Huawei Technologies

SPDX-License-Identifier: Apache-2.0
*/

package opencas

import (
	"fmt"
	"strings"
)

const (
	bin = "casadm"

	startCacheCommand   = "--start-cache"
	stopCacheCommand    = "--stop-cache"
	addCoreCommand      = "--add-core"
	removeCoreCommand   = "--remove-core"
	listCachesCommand   = "--list-caches"
	setCacheModeCommand = "--set-cache-mode"
	statsCommand        = "--stats"

	cacheDeviceParam   = "--cache-device"
	coreDeviceParam    = "--core-device"
	cacheIdParam       = "--cache-id"
	coreIdParam        = "--core-id"
	cacheModeParam     = "--cache-mode"
	cacheLineSizeParam = "--cache-line-size"
	ioClassIdParam     = "--io-class-id"

	byIdPathFlag = "--by-id-path"
	forceFlag    = "--force"

	outputFormatCsv = "--output-format csv"
)

var replace = map[string]string{
	bin: "",

	startCacheCommand:   "-S",
	stopCacheCommand:    "-T",
	addCoreCommand:      "-A",
	removeCoreCommand:   "-R",
	listCachesCommand:   "-L",
	setCacheModeCommand: "-Q",
	statsCommand:        "-P",

	cacheDeviceParam:   "-d",
	coreDeviceParam:    "-d",
	cacheIdParam:       "-i",
	coreIdParam:        "-j",
	cacheModeParam:     "-c",
	cacheLineSizeParam: "-x",
	ioClassIdParam:     "-d",

	byIdPathFlag: "",
	forceFlag:    "",

	outputFormatCsv: "",

	CsiDriverByIdDir: "",
}

// use cacheId = -1 for automatic cache id assignment
func startCacheCmd(path string, mode CacheMode, cls CacheLineSize, cacheId int) string {
	cmd := []string{bin, startCacheCommand, forceFlag}
	cmd = append(cmd, cacheDeviceParam, path)
	if cacheId != -1 {
		cmd = append(cmd, cacheIdParam, fmt.Sprint(cacheId))
	}
	if mode != "" {
		cmd = append(cmd, cacheModeParam, fmt.Sprint(mode))
	}
	if cls != 0 {
		cmd = append(cmd, cacheLineSizeParam, fmt.Sprint(cls))
	}
	return strings.Join(cmd, " ")
}

func stopCacheCmd(cacheId int) string {
	cmd := []string{bin, stopCacheCommand}
	cmd = append(cmd, cacheIdParam, fmt.Sprint(cacheId))
	return strings.Join(cmd, " ")
}

// use coreId = -1 for automatic core id assignment
func addCoreCmd(cacheId int, path string, coreId int) string {
	cmd := []string{bin, addCoreCommand}
	cmd = append(cmd, cacheIdParam, fmt.Sprint(cacheId))
	cmd = append(cmd, coreDeviceParam, path)
	if coreId != -1 {
		cmd = append(cmd, coreIdParam, fmt.Sprint(coreId))
	}
	return strings.Join(cmd, " ")
}

func removeCoreCmd(cacheId, coreId int) string {
	cmd := []string{bin, removeCoreCommand}
	cmd = append(cmd, cacheIdParam, fmt.Sprint(cacheId))
	cmd = append(cmd, coreIdParam, fmt.Sprint(coreId))
	return strings.Join(cmd, " ")
}

func listCachesCmd() string {
	cmd := []string{bin, listCachesCommand, byIdPathFlag, outputFormatCsv}
	return strings.Join(cmd, " ")
}

func statsCmd(cacheId, coreId int) string {
	cmd := []string{bin, statsCommand, outputFormatCsv}
	cmd = append(cmd, cacheIdParam, fmt.Sprint(cacheId))
	if coreId != -1 {
		cmd = append(cmd, coreIdParam, fmt.Sprint(coreId))
	}
	return strings.Join(cmd, " ")
}

func setCacheModeCmd(cacheId int, mode CacheMode) string {
	cmd := []string{bin, setCacheModeCommand}
	cmd = append(cmd, cacheIdParam, fmt.Sprint(cacheId))
	cmd = append(cmd, cacheModeParam, fmt.Sprint(mode))
	return strings.Join(cmd, " ")
}
