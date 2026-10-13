// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

//go:build !windows

package ci

import "syscall"

func lockFile(fd uintptr) error {
	return syscall.Flock(int(fd), syscall.LOCK_EX)
}

func unlockFile(fd uintptr) error {
	return syscall.Flock(int(fd), syscall.LOCK_UN)
}
