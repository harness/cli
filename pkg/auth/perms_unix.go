// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

//go:build !windows

package auth

// restrictToCurrentUser is a no-op on POSIX systems: os.MkdirAll(0700) and
// os.WriteFile(0600) already restrict access to the current user via
// standard permission bits.
func restrictToCurrentUser(path string) error {
	return nil
}
