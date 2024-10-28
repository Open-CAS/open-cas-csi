/*
Copyright 2024 Huawei Technologies

SPDX-License-Identifier: Apache-2.0
*/

package utils

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"k8s.io/klog/v2"
	"k8s.io/mount-utils"
)

type Output struct {
	StdOut   string `json:"result"`
	ExitCode int    `json:"exit_code"`
}

func (o *Output) String() string {
	out := []string{
		"result: " + o.StdOut,
		fmt.Sprintf("exit_code: %d", o.ExitCode),
	}

	return strings.Join(out, "\n")
}

func ExecLocalCommand(cmd string) (*Output, error) {
	klog.V(5).Infof("execute %q", cmd)

	executor := exec.Command("/bin/sh", "-c", cmd)
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	executor.Stdout = stdout
	executor.Stderr = stderr
	err := executor.Run()

	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); !ok {
			return nil, fmt.Errorf("failed to run command %q: %w", cmd, err)
		} else {
			return &Output{
				StdOut:   stdout.String(),
				ExitCode: exitErr.ExitCode(),
			}, nil
		}
	}

	return &Output{
		StdOut:   stdout.String(),
		ExitCode: 0,
	}, nil
}

func GetDeviceSizeFromPath(path string) (int64, error) {
	cmd := exec.Command("lsblk", path, "--bytes", "--nodeps", "--noheadings", "--output", "SIZE")
	out, err := cmd.Output()
	if err == nil {
		klog.V(5).Infof("%s size: %sB", path, strings.TrimSpace(string(out)))
		return strconv.ParseInt(strings.TrimSpace(string(out)), 10, 64)
	}

	return 0, err
}

func GetDeviceFsType(path string) (string, error) {
	cmd := exec.Command("lsblk", path, "--bytes", "--nodeps", "--noheadings", "--output", "FSTYPE")
	out, err := cmd.Output()
	if err == nil {
		fsType := strings.TrimSpace(string(out))
		klog.V(5).Infof("%s filesystem: %q", path, fsType)
		return fsType, nil
	}

	return "", err
}

func PrepareFs(path string, fsType string) error {
	if len(fsType) == 0 {
		return fmt.Errorf("no file system specified for device %q", path)
	}

	existingFsType, err := GetDeviceFsType(path)
	if err != nil {
		return err
	}
	if len(existingFsType) == 0 {
		return CreateFs(path, fsType, []string{})
	}
	if existingFsType == fsType {
		return nil
	} else {
		return fmt.Errorf("file system %q found, requested file system %q", existingFsType, fsType)
	}
}

func CreateFs(path string, fsType string, params []string) error {
	cmdBin := "mkfs." + fsType
	cmdArgs := []string{path}
	if len(params) != 0 {
		cmdArgs = append(cmdArgs, params...)
	}
	cmd := exec.Command(cmdBin, cmdArgs...)
	_, err := cmd.Output()
	return err
}

func Mount(mounter mount.Interface, sourcePath, targetPath, fsType string, mountOptions []string, rawBlock bool) error {
	notMountPoint, err := mounter.IsLikelyNotMountPoint(targetPath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("cannot determine if %q is a valid mount point: %v", targetPath, err)
	}
	if !notMountPoint {
		return nil
	}

	if rawBlock {
		f, err := os.OpenFile(targetPath, os.O_CREATE, os.FileMode(0644))
		if err == nil {
			defer f.Close()
		} else if !os.IsExist(err) {
			return fmt.Errorf("create target device file: %w", err)
		}
	} else {
		if err := os.Mkdir(targetPath, os.FileMode(0755)); err != nil && !os.IsExist(err) {
			return fmt.Errorf("cannot create target directory: %w", err)
		}
	}

	return mounter.Mount(sourcePath, targetPath, fsType, mountOptions)
}
