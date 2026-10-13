// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

//go:build windows

package ci

import "golang.org/x/sys/windows"

func lockFile(fd uintptr) error {
	ol := new(windows.Overlapped)
	return windows.LockFileEx(windows.Handle(fd), windows.LOCKFILE_EXCLUSIVE_LOCK, 0, 1, 0, ol)
}

func unlockFile(fd uintptr) error {
	ol := new(windows.Overlapped)
	return windows.UnlockFileEx(windows.Handle(fd), 0, 1, 0, ol)
}
