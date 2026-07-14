// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package har

import (
	"fmt"
	"strings"

	"github.com/harness/cli/pkg/cmdctx"
)

// inferPackageType returns a package type string based on the file extension.
// Returns "" if the extension is not recognised. Used by pull to infer type
// from the --filename hint.
func inferPackageType(filePath string) string {
	lower := strings.ToLower(filePath)
	switch {
	case strings.HasSuffix(lower, ".jar"), strings.HasSuffix(lower, ".war"):
		return "maven"
	case strings.HasSuffix(lower, ".whl"):
		return "python"
	case strings.HasSuffix(lower, ".tar.gz"), strings.HasSuffix(lower, ".tgz"):
		return "npm"
	case strings.HasSuffix(lower, ".nupkg"):
		return "nuget"
	case strings.HasSuffix(lower, ".rpm"):
		return "rpm"
	case strings.HasSuffix(lower, ".crate"):
		return "cargo"
	case strings.HasSuffix(lower, ".conda"), strings.HasSuffix(lower, ".tar.bz2"):
		return "conda"
	case strings.HasSuffix(lower, ".zip"):
		return "composer"
	default:
		return ""
	}
}

func pushHelmArtifact(_ *cmdctx.Ctx) error {
	return fmt.Errorf("push helm artifact: not yet implemented")
}

func pushDockerArtifact(_ *cmdctx.Ctx) error {
	return fmt.Errorf("push docker artifact: not yet implemented")
}
