// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

//go:build windows

package plugin

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/harness/cli/v3/pkg/hbase"
)

// Exec runs binPath as a child process (Windows has no execve equivalent).
func Exec(binPath string, args []string) error {
	child := exec.Command(binPath, args...)
	child.Stdin = os.Stdin
	child.Stdout = os.Stdout
	child.Stderr = os.Stderr
	if err := child.Run(); err != nil {
		if exit, ok := err.(*exec.ExitError); ok {
			hbase.Exit(exit.ExitCode())
		}
		return fmt.Errorf("plugin %q: %w", binPath, err)
	}
	return nil
}
