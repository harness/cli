// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package util

import (
	"path"
	"strings"

	"github.com/Masterminds/semver/v3"
)

const helmChartExt = ".tgz"
const helmProvExt = ".prov"

// ParseChartFileName splits a Helm chart file name like "{name}-{version}.tgz"
// (or "{name}-{version}.tgz.prov") into its name and version components.
// The version boundary is found by trying each hyphen left-to-right and
// accepting the first split whose right-hand side is a valid SemVer 2 string.
func ParseChartFileName(filename string) (name, version string, ok bool) {
	base := path.Base(filename)
	base = strings.TrimSuffix(base, helmProvExt)
	base = strings.TrimSuffix(base, helmChartExt)

	for i := 0; i < len(base); i++ {
		if base[i] != '-' {
			continue
		}
		candidateName := base[:i]
		candidateVersion := base[i+1:]
		if candidateName == "" || candidateVersion == "" {
			continue
		}
		if _, err := semver.NewVersion(candidateVersion); err == nil {
			return candidateName, candidateVersion, true
		}
	}
	return "", "", false
}

// GetChartFileName returns the canonical chart archive file name: "<name>-<version>.tgz".
func GetChartFileName(name, version string) string {
	return name + "-" + version + helmChartExt
}

// GetChartProvFileName returns the provenance sidecar file name: "<name>-<version>.tgz.prov".
func GetChartProvFileName(name, version string) string {
	return GetChartFileName(name, version) + helmProvExt
}

// IsHelmChartArchive reports whether a file name is a Helm chart archive (.tgz)
// and not a provenance sidecar (.tgz.prov).
func IsHelmChartArchive(name string) bool {
	return strings.HasSuffix(name, helmChartExt) && !strings.HasSuffix(name, helmChartExt+helmProvExt)
}
