package types

import (
	"strings"
	"sync"
)

// ExistingIndex is a read-only-after-build snapshot of what already exists at the
// destination registry: pkg -> version -> set of LOWERCASED destination (HAR)
// file paths, exactly as returned by GetArtifactFiles.
//
// Lookups query by source-relative path (types.File.Uri); HasFile owns the
// reverse conversion from a stored HAR path back to source form (see
// harToSourcePath), so every package-type path rewrite lives in one place and
// the index build can store HAR paths verbatim.
//
// Concurrency: AddFile takes mu during the concurrent build. After
// BuildExistingIndex returns, the struct is treated as immutable and all reads
// are lock-free.
type ExistingIndex struct {
	files map[string]map[string]map[string]struct{}
	mu    sync.Mutex
}

func NewExistingIndex() *ExistingIndex {
	return &ExistingIndex{
		files: map[string]map[string]map[string]struct{}{},
	}
}

// AddFile records a destination (HAR) file path under (pkg, version); the path
// is lowercased for case-insensitive matching.
func (i *ExistingIndex) AddFile(pkg, version, harPath string) {
	lowerPkg := strings.ToLower(pkg)
	lowerVersion := strings.ToLower(version)
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.files[lowerPkg] == nil {
		i.files[lowerPkg] = map[string]map[string]struct{}{}
	}
	if i.files[lowerPkg][lowerVersion] == nil {
		i.files[lowerPkg][lowerVersion] = map[string]struct{}{}
	}
	i.files[lowerPkg][lowerVersion][strings.ToLower(harPath)] = struct{}{}
}

// Stats reports how much the index holds: distinct packages, versions across
// all packages, and files across all versions. A nil index reports zeros.
func (i *ExistingIndex) Stats() (packages, versions, files int) {
	if i == nil {
		return 0, 0, 0
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	packages = len(i.files)
	for _, versionFiles := range i.files {
		versions += len(versionFiles)
		for _, fileSet := range versionFiles {
			files += len(fileSet)
		}
	}
	return packages, versions, files
}

// HasFile reports whether the source-relative filePath already exists at the
// destination. The index stores destination (HAR) paths, so HasFile converts
// stored paths back to source form (harToSourcePath) before comparing; the
// query is lowercased to match the lowercased stored paths.
func (i *ExistingIndex) HasFile(pkg, version, filePath string, artifactType ArtifactType) bool {
	lower := strings.ToLower(filePath)
	lowerPkg := strings.ToLower(pkg)
	lowerVersion := strings.ToLower(version)

	// NPM and MAVEN flatten all packages/versions under one pseudo-bucket in
	// the source tree; scan every bucket converting stored HAR paths back.
	if artifactType == NPM || artifactType == MAVEN {
		for p, fv := range i.files {
			for v, fs := range fv {
				for harPath := range fs {
					if harToSourcePath(artifactType, harPath, p, v) == lower {
						return true
					}
				}
			}
		}
		return false
	}

	fs := i.files[lowerPkg][lowerVersion]
	if fs != nil {
		// O(1) direct lookup covers GENERIC/RAW/PYTHON/DART/PUPPET/CONAN etc.
		if _, ok := fs[lower]; ok {
			return true
		}

		// Types with a HAR prefix rewrite (NUGET) need per-entry conversion.
		if needsPathRewrite(artifactType) {
			for harPath := range fs {
				if harToSourcePath(artifactType, harPath, lowerPkg, lowerVersion) == lower {
					return true
				}
			}
		}
	}

	// Some NuGet artifacts have malformed or ambiguous names that cause the
	// source and HAR sides to derive different package/version buckets. If the
	// normal lookup misses, scan the complete index and compare the full
	// source-relative path after stripping HAR's package/version prefix. Keep
	// the parent path in the identity check: the same NuGet filename can exist
	// in multiple JFrog folders.
	if artifactType == NUGET {
		for _, versions := range i.files {
			for _, files := range versions {
				for harPath := range files {
					if harToSourcePath(NUGET, harPath, "", "") == lower {
						return true
					}
				}
			}
		}
	}
	return false
}

// harToSourcePath converts a stored HAR file path back to source-relative form
// so HasFile can compare against a source-tree query.
func harToSourcePath(artifactType ArtifactType, harPath, pkg, version string) string {
	switch artifactType {
	case NUGET:
		return stripLeadingSegments(harPath, 2)
	case NPM:
		prefix := "/" + pkg + "/" + version + "/"
		if rest, ok := strings.CutPrefix(harPath, prefix); ok {
			return "/" + pkg + "/-/" + rest
		}
		return harPath
	default:
		return harPath
	}
}

func needsPathRewrite(artifactType ArtifactType) bool {
	return artifactType == NUGET
}

func stripLeadingSegments(p string, n int) string {
	parts := strings.Split(strings.TrimPrefix(p, "/"), "/")
	if len(parts) <= n {
		return p
	}
	return "/" + strings.Join(parts[n:], "/")
}
