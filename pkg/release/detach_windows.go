// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

//go:build windows

package release

import (
	"os/exec"
	"syscall"
)

func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP,
	}
}
