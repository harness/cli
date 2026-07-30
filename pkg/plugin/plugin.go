// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package plugin

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/harness/cli/pkg/hbase"
)

var semverRe = regexp.MustCompile(`\d+\.\d+\.\d+\S*`)

// Resolve returns the absolute path of the binary a plugin module dispatches to.
// This is the single resolution point for every host→plugin hop (command exec,
// completion), so they can never disagree about which binary serves a module:
//   - binaryPath set (dynamically-installed plugin, from the home spec's
//     provenance): that exact path.
//   - binaryPath empty (build-time external_binary): extBin located via FindBinary.
func Resolve(extBin, binaryPath string) (string, error) {
	if binaryPath != "" {
		return hbase.ExpandHomeDir(binaryPath), nil
	}
	binPath, err := FindBinary(extBin)
	if err != nil {
		return "", err
	}
	return binPath, nil
}

// FindBinary resolves extBin to an absolute path. It first checks the directory
// containing the current executable, then falls back to exec.LookPath.
func FindBinary(extBin string) (string, error) {
	if self, err := os.Executable(); err == nil {
		candidate := filepath.Join(filepath.Dir(self), extBin)
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}
	}
	binPath, err := exec.LookPath(extBin)
	if err != nil {
		return "", &NotFoundError{Binary: extBin}
	}
	return binPath, nil
}

// QueryVersion runs `[binPath] version` and returns the semver string (e.g. "1.2.3-dev")
// extracted from its output. Returns "" if the binary exits non-zero or no semver is found.
func QueryVersion(binPath string) string {
	out, err := exec.Command(binPath, "version").Output()
	if err != nil {
		return ""
	}
	if m := semverRe.FindString(strings.TrimSpace(string(out))); m != "" {
		return m
	}
	return ""
}

// NotFoundError is returned by FindBinary when the binary cannot be located.
type NotFoundError struct {
	Binary string
}

func (e *NotFoundError) Error() string {
	return "module exec: \"" + e.Binary + "\" not found on PATH"
}
