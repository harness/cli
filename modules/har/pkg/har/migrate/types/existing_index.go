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
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.files[pkg] == nil {
		i.files[pkg] = map[string]map[string]struct{}{}
	}
	if i.files[pkg][version] == nil {
		i.files[pkg][version] = map[string]struct{}{}
	}
	i.files[pkg][version][strings.ToLower(harPath)] = struct{}{}
}

// HasFile reports whether the source-relative filePath already exists at the
// destination. The index stores destination (HAR) paths, so HasFile converts
// stored paths back to source form (harToSourcePath) before comparing.
func (i *ExistingIndex) HasFile(pkg, version, filePath string, artifactType ArtifactType) bool {
	lower := strings.ToLower(filePath)

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

	fs := i.files[pkg][version]
	if fs == nil {
		return false
	}

	// O(1) direct lookup covers GENERIC/RAW/PYTHON/DART/PUPPET/CONAN etc.
	if _, ok := fs[lower]; ok {
		return true
	}

	// Types with a HAR prefix rewrite (NUGET) need per-entry conversion.
	if needsPathRewrite(artifactType) {
		for harPath := range fs {
			if harToSourcePath(artifactType, harPath, pkg, version) == lower {
				return true
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
		p := strings.ToLower(pkg)
		prefix := "/" + p + "/" + strings.ToLower(version) + "/"
		if rest, ok := strings.CutPrefix(harPath, prefix); ok {
			return "/" + p + "/-/" + rest
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

// FilesFor returns the lowercased HAR file-path set for (pkg, version), or nil.
// The returned map must be treated as read-only.
func (i *ExistingIndex) FilesFor(pkg, version string) map[string]struct{} {
	if fv, ok := i.files[pkg]; ok {
		return fv[version]
	}
	return nil
}
