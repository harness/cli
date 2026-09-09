// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package plugin

import (
	"os"

	"github.com/harness/cli/v3/pkg/hbase"
)

// Resolve returns the absolute path of the binary a plugin module dispatches to.
// This is the single resolution point for every host→plugin hop (command exec,
// completion), so they can never disagree about which binary serves a module.
//
// binaryPath comes from the installed spec's provenance block, which the
// installer writes as an absolute path. A missing binary means the plugin's spec
// outlived its binary (bin dir cleaned out, home moved), which is reported as
// NotFoundError so callers can suggest reinstalling.
func Resolve(binaryPath string) (string, error) {
	if binaryPath == "" {
		return "", &NotFoundError{}
	}
	binPath := hbase.ExpandHomeDir(binaryPath)
	if _, err := os.Stat(binPath); err != nil {
		return "", &NotFoundError{Binary: binPath}
	}
	return binPath, nil
}

// NotFoundError is returned by Resolve when the plugin binary a spec points at
// cannot be found.
type NotFoundError struct {
	Binary string
}

func (e *NotFoundError) Error() string {
	if e.Binary == "" {
		return "module exec: spec records no plugin binary path"
	}
	return "module exec: plugin binary \"" + e.Binary + "\" not found"
}
