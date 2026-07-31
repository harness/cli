// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package plugin

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
)

var semverRe = regexp.MustCompile(`\d+\.\d+\.\d+\S*`)

// FindBinary resolves extBin to an absolute path. It first checks the directory
// containing the current executable, then falls back to exec.LookPath.
func FindBinary(extBin string) (string, error) {
	if self, err := os.Executable(); err == nil {
		for _, name := range siblingBinaryNames(extBin) {
			candidate := filepath.Join(filepath.Dir(self), name)
			if _, err := os.Stat(candidate); err == nil {
				return candidate, nil
			}
		}
	}
	binPath, err := exec.LookPath(extBin)
	if err != nil {
		return "", &NotFoundError{Binary: extBin}
	}
	return binPath, nil
}

func siblingBinaryNames(extBin string) []string {
	if runtime.GOOS != "windows" || strings.HasSuffix(strings.ToLower(extBin), ".exe") {
		return []string{extBin}
	}
	return []string{extBin, extBin + ".exe"}
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

// QueryModuleHelp runs `[binPath] --modulehelp` and returns its stdout.
// Returns "" if the binary exits non-zero or produces no output.
func QueryModuleHelp(binPath string) string {
	out, err := exec.Command(binPath, "--modulehelp").Output()
	if err != nil {
		return ""
	}
	return string(out)
}

// NotFoundError is returned by FindBinary when the binary cannot be located.
type NotFoundError struct {
	Binary string
}

func (e *NotFoundError) Error() string {
	return "module exec: \"" + e.Binary + "\" not found on PATH"
}
