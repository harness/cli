package types

import (
	"slices"
	"strings"
)

// Pattern-filterability classification for includePatterns/excludePatterns.
//
// This lives in the types package (not migrate/util) so config validation can
// reject scope controls that would be silently ignored, without an import
// cycle. migrate/util's IsFileLevelFilterableArtifact /
// IsPackageLevelFilterableArtifact delegate here — keep the classification in
// sync with the actual filter application sites in migratable/registry.go.

var (
	fileLevelPatternTypes = []ArtifactType{
		GENERIC, RAW, PYTHON, MAVEN, NUGET, NPM, DART, GO, RUBY, PUPPET, TERRAFORM,
	}
	packageLevelPatternTypes = []ArtifactType{
		DOCKER, HELM, HELM_LEGACY, HELM_HTTP, RPM, CONDA, COMPOSER, SWIFT, CONAN, DEBIAN, CRAN,
	}
)

// IsFileLevelPatternFilterable reports whether include/exclude patterns are
// applied to individual file URIs for this artifact type.
func IsFileLevelPatternFilterable(t ArtifactType) bool {
	return slices.Contains(fileLevelPatternTypes, t)
}

// IsPackageLevelPatternFilterable reports whether include/exclude patterns are
// applied to package names for this artifact type.
func IsPackageLevelPatternFilterable(t ArtifactType) bool {
	return slices.Contains(packageLevelPatternTypes, t)
}

// IsPatternFilterable reports whether include/exclude patterns have ANY effect
// for this artifact type (at either file or package level).
func IsPatternFilterable(t ArtifactType) bool {
	return IsFileLevelPatternFilterable(t) || IsPackageLevelPatternFilterable(t)
}

func joinArtifactTypes(ts []ArtifactType) string {
	parts := make([]string, len(ts))
	for i, t := range ts {
		parts[i] = string(t)
	}
	return strings.Join(parts, ", ")
}
