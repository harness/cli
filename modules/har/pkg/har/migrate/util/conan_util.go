// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package util

import (
	"path"
	"sort"
	"strings"

	"github.com/harness/cli/modules/har/pkg/har/migrate/types"
)

// Conan v2 JFrog storage layout (repo-relative Uri):
//
//	{name}/{version}/{user}/{channel}/{rrev}/export/{file}                 -> recipe layer
//	{name}/{version}/{user}/{channel}/{rrev}/package/{pkgid}/{prev}/{file} -> package layer
//
// "_" is the placeholder JFrog uses for an absent user/channel.
const (
	ConanPlaceholder  = "_"
	ConanManifestFile = "conanmanifest.txt"
	ConanFilePy       = "conanfile.py"
	ConanInfoTxt      = "conaninfo.txt"

	ConanTarballExport  = "conan_export"
	ConanTarballSources = "conan_sources"
	ConanTarballPackage = "conan_package"

	conanExportMarker = "export"
	conanPkgMarker    = "package"

	// segments preceding the layer marker: name/version/user/channel/rrev
	conanRefSegmentsBeforeMarker = 5
)

var conanTarballExtensions = map[string]bool{".tgz": true, ".txz": true, ".tzst": true}

// ConanLayer identifies whether a file belongs to the recipe or package layer.
type ConanLayer string

const (
	ConanLayerRecipe  ConanLayer = "recipe"
	ConanLayerPackage ConanLayer = "package"
)

// ConanRef holds the coordinates of a Conan reference (name/version[@user/channel]).
type ConanRef struct {
	Name    string
	Version string
	User    string
	Channel string
}

// BasePath is the repo-relative subtree path: /name/version/user/channel.
func (r ConanRef) BasePath() string {
	return "/" + r.Name + "/" + r.Version + "/" + r.User + "/" + r.Channel
}

// Display renders the reference as name/version[@user/channel], omitting placeholder user/channel.
func (r ConanRef) Display() string {
	if r.User == ConanPlaceholder && r.Channel == ConanPlaceholder {
		return r.Name + "/" + r.Version
	}
	return r.Name + "/" + r.Version + "@" + r.User + "/" + r.Channel
}

// ConanFileEntry is a single migratable Conan file with the coordinates needed to re-upload it.
type ConanFileEntry struct {
	Reference ConanRef
	Layer     ConanLayer
	RRev      string
	PkgID     string
	PRev      string
	FileName  string
	Uri       string
	SHA1      string
	Size      int
}

// ParseConanFileURI parses a repo-relative JFrog Conan v2 Uri into a ConanFileEntry.
// ok is false for paths that are not canonical recipe or package files.
func ParseConanFileURI(uri string) (ConanFileEntry, bool) {
	trimmed := strings.Trim(uri, "/")
	if trimmed == "" {
		return ConanFileEntry{}, false
	}
	parts := strings.Split(trimmed, "/")

	marker := -1
	for i, p := range parts {
		if p == conanExportMarker || p == conanPkgMarker {
			marker = i
			break
		}
	}
	if marker < conanRefSegmentsBeforeMarker {
		return ConanFileEntry{}, false
	}

	ref := ConanRef{
		Name:    parts[marker-5],
		Version: parts[marker-4],
		User:    parts[marker-3],
		Channel: parts[marker-2],
	}
	rrev := parts[marker-1]
	filename := parts[len(parts)-1]

	switch parts[marker] {
	case conanExportMarker:
		if len(parts) <= marker+1 {
			return ConanFileEntry{}, false
		}
		if !IsConanRecipeFile(filename) {
			return ConanFileEntry{}, false
		}
		return ConanFileEntry{
			Reference: ref,
			Layer:     ConanLayerRecipe,
			RRev:      rrev,
			FileName:  filename,
			Uri:       uri,
		}, true
	case conanPkgMarker:
		if len(parts) < marker+4 {
			return ConanFileEntry{}, false
		}
		if !IsConanPackageFile(filename) {
			return ConanFileEntry{}, false
		}
		return ConanFileEntry{
			Reference: ref,
			Layer:     ConanLayerPackage,
			RRev:      rrev,
			PkgID:     parts[marker+1],
			PRev:      parts[marker+2],
			FileName:  filename,
			Uri:       uri,
		}, true
	default:
		return ConanFileEntry{}, false
	}
}

// GetConanPackages returns one package per distinct Conan reference found in the file list.
func GetConanPackages(files []*types.File, registry string) []types.Package {
	seen := make(map[string]bool)
	var packages []types.Package
	for _, f := range files {
		if f == nil || f.Folder {
			continue
		}
		entry, ok := ParseConanFileURI(f.Uri)
		if !ok {
			continue
		}
		key := entry.Reference.BasePath()
		if seen[key] {
			continue
		}
		seen[key] = true
		packages = append(packages, types.Package{
			Registry: registry,
			Path:     key,
			Name:     entry.Reference.Display(),
			Size:     -1,
			Metadata: map[string]string{
				"name":    entry.Reference.Name,
				"version": entry.Reference.Version,
				"user":    entry.Reference.User,
				"channel": entry.Reference.Channel,
			},
		})
	}
	return packages
}

// ParseConanEntries converts a reference's files into upload-ready entries,
// ordered so every conanmanifest.txt is uploaded last within its layer/revision group.
func ParseConanEntries(files []*types.File) []ConanFileEntry {
	var entries []ConanFileEntry
	for _, f := range files {
		if f == nil || f.Folder {
			continue
		}
		entry, ok := ParseConanFileURI(f.Uri)
		if !ok {
			continue
		}
		entry.SHA1 = f.SHA1
		entry.Size = f.Size
		entries = append(entries, entry)
	}
	sortConanEntries(entries)
	return entries
}

func sortConanEntries(entries []ConanFileEntry) {
	sort.SliceStable(entries, func(i, j int) bool {
		a, b := entries[i], entries[j]
		if a.Layer != b.Layer {
			return a.Layer == ConanLayerRecipe
		}
		aKey := a.RRev + "|" + a.PkgID + "|" + a.PRev
		bKey := b.RRev + "|" + b.PkgID + "|" + b.PRev
		if aKey != bKey {
			return aKey < bKey
		}
		aManifest := a.FileName == ConanManifestFile
		bManifest := b.FileName == ConanManifestFile
		if aManifest != bManifest {
			return !aManifest
		}
		return a.FileName < b.FileName
	})
}

// IsConanRecipeFile reports whether name is a canonical recipe-layer file.
func IsConanRecipeFile(name string) bool {
	name = path.Base(name)
	if name == ConanFilePy || name == ConanManifestFile {
		return true
	}
	prefix, ok := conanTarballPrefix(name)
	return ok && (prefix == ConanTarballExport || prefix == ConanTarballSources)
}

// IsConanPackageFile reports whether name is a canonical package-layer file.
func IsConanPackageFile(name string) bool {
	name = path.Base(name)
	if name == ConanInfoTxt || name == ConanManifestFile {
		return true
	}
	prefix, ok := conanTarballPrefix(name)
	return ok && prefix == ConanTarballPackage
}

func conanTarballPrefix(name string) (string, bool) {
	ext := path.Ext(name)
	if !conanTarballExtensions[ext] {
		return "", false
	}
	return strings.TrimSuffix(name, ext), true
}
