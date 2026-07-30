// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package util

import (
	"path"
	"regexp"
	"strings"
)

const puppetTarGzExt = ".tar.gz"

var puppetSemVerRegex = regexp.MustCompile(
	`^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)` +
		`(?:-((?:0|[1-9]\d*|\d*[a-zA-Z-][0-9a-zA-Z-]*)` +
		`(?:\.(?:0|[1-9]\d*|\d*[a-zA-Z-][0-9a-zA-Z-]*))*))?` +
		`(?:\+([0-9a-zA-Z-]+(?:\.[0-9a-zA-Z-]+)*))?$`,
)

var puppetModuleNameRegex = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_]*-[a-zA-Z][a-zA-Z0-9_]*$`)

// ParsePuppetFileNameWithPath parses a Puppet module tarball filename of the
// form "<author>-<module>-<version>.tar.gz" and returns the module name
// ("<author>-<module>") plus version. The version boundary is found by scanning
// hyphen positions left-to-right and accepting the first split where both
// sides match their respective patterns.
func ParsePuppetFileNameWithPath(filePath string) (string, string, bool) {
	fileName := path.Base(filePath)
	if !strings.HasSuffix(fileName, puppetTarGzExt) {
		return "", "", false
	}
	base := strings.TrimSuffix(fileName, puppetTarGzExt)

	for i := 0; i < len(base); i++ {
		if base[i] != '-' {
			continue
		}
		candidateName := base[:i]
		candidateVersion := base[i+1:]
		if !puppetModuleNameRegex.MatchString(candidateName) {
			continue
		}
		if !puppetSemVerRegex.MatchString(candidateVersion) {
			continue
		}
		return candidateName, candidateVersion, true
	}
	return "", "", false
}
