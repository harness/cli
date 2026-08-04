package util

import (
	"fmt"
	"strings"
	"time"

	"github.com/harness/cli/modules/har/pkg/har/migrate/types"

	"github.com/rs/zerolog/log"
)

// IsTimeBasedFilterPresent reports whether the mapping has a non-nil DateFilter.
func IsTimeBasedFilterPresent(mapping *types.RegistryMapping) bool {
	return mapping.DateFilter != nil
}

// ValidateDateFilter returns an error if the DateFilter is misconfigured.
func ValidateDateFilter(df *types.DateFilter) error {
	if df.Match != types.DateFilterMatchAny && df.Match != types.DateFilterMatchAll {
		log.Error().Msgf("dateFilter.match must be 'ANY' or 'ALL', got %q", df.Match)
		return fmt.Errorf("dateFilter.match must be 'ANY' or 'ALL', got %q", df.Match)
	}
	if df.CreatedAfter == nil && df.DownloadedAfter == nil {
		log.Error().Msg("dateFilter is present but neither createdAfter nor downloadedAfter is specified")
		return fmt.Errorf("dateFilter is present but neither createdAfter nor downloadedAfter is specified")
	}
	return nil
}

// CreateMapOfFilteredFile builds a URI set of files that satisfy the date filter.
func CreateMapOfFilteredFile(searchedFiles []types.SearchedFile, mapping *types.RegistryMapping) map[string]struct{} {
	result := map[string]struct{}{}
	if mapping.DateFilter == nil {
		return result
	}

	df := mapping.DateFilter
	hasCreated := df.CreatedAfter != nil
	hasDownloaded := df.DownloadedAfter != nil

	log.Info().Msgf("Filtering files by dateFilter (match: %s, createdAfter: %v, downloadedAfter: %v)",
		df.Match, df.CreatedAfter, df.DownloadedAfter)

	for _, f := range searchedFiles {
		var matchedCreated, matchedDownloaded bool

		if hasCreated {
			created, err := parseDate(f.Created)
			if err != nil {
				log.Warn().Msgf("File %s: failed to parse created date %q: %v", f.Name, f.Created, err)
			} else {
				matchedCreated = onOrAfter(created, *df.CreatedAfter)
			}
		}

		if hasDownloaded {
			for _, stat := range f.Stats {
				downloaded, err := parseDate(stat.Downloaded)
				if err != nil {
					log.Warn().Msgf("File %s: failed to parse downloaded date %q: %v", f.Name, stat.Downloaded, err)
					continue
				}
				if onOrAfter(downloaded, *df.DownloadedAfter) {
					matchedDownloaded = true
					break
				}
			}
		}

		var include bool
		switch df.Match {
		case types.DateFilterMatchAny:
			include = (hasCreated && matchedCreated) || (hasDownloaded && matchedDownloaded)
		case types.DateFilterMatchAll:
			include = true
			if hasCreated && !matchedCreated {
				include = false
			}
			if hasDownloaded && !matchedDownloaded {
				include = false
			}
		}

		if include {
			result[buildURI(f.Path, f.Name)] = struct{}{}
		}
	}

	return result
}

// FilterFilesByDate returns only files whose URI is in filteredURIs.
func FilterFilesByDate(files []types.File, filteredURIs map[string]struct{}) []types.File {
	var result []types.File
	for _, f := range files {
		if _, ok := filteredURIs[f.Uri]; ok {
			result = append(result, f)
		}
	}
	return result
}

// FilterPackagesByFileName keeps packages whose bare URI matches any date-filtered file.
// Used for metadata-driven types (RPM, DEBIAN) where GetPackages reads a metadata file
// that lists every package regardless of the filtered tree.
func FilterPackagesByFileName(pkgs []types.Package, dateFilteredFiles []types.File) []types.Package {
	uriSet := make(map[string]struct{}, len(dateFilteredFiles))
	for _, f := range dateFilteredFiles {
		uriSet[strings.TrimPrefix(f.Uri, "/")] = struct{}{}
	}

	var result []types.Package
	for _, pkg := range pkgs {
		if _, ok := uriSet[strings.TrimPrefix(pkg.URL, "/")]; ok {
			result = append(result, pkg)
		}
	}
	return result
}

// IsPackageIndexFile reports whether uri is a repository index/metadata file exempt from
// date filtering. Such files are needed for package enumeration and are typically too old
// to survive a createdAfter/downloadedAfter cutoff.
func IsPackageIndexFile(artifactType types.ArtifactType, uri string) bool {
	normalized := strings.TrimPrefix(uri, "/")
	switch artifactType {
	case types.PYTHON:
		return strings.HasPrefix(normalized, ".pypi/")
	default:
		return false
	}
}

// IsAtomicVersionArtifact reports whether a single logical version of this type may span
// multiple distribution files (e.g. PyPI sdist + wheels). For such types, date filtering
// can keep some distributions and prune others; Package.Migrate uses an unfilteredRoot to
// recover pruned distributions so partial versions are never published.
func IsAtomicVersionArtifact(artifactType types.ArtifactType) bool {
	switch artifactType {
	case types.PYTHON:
		return true
	default:
		return false
	}
}

func parseDate(s string) (time.Time, error) {
	layouts := []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02T15:04:05.000Z07:00",
		"2006-01-02T15:04:05Z07:00",
	}
	for _, layout := range layouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("unable to parse date: %q", s)
}

func buildURI(path, name string) string {
	path = strings.TrimPrefix(path, "/")
	path = strings.TrimSuffix(path, "/")
	if path == "" || path == "." {
		return "/" + name
	}
	return "/" + path + "/" + name
}

func onOrAfter(t, threshold time.Time) bool {
	return !t.Before(threshold)
}
