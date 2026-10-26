// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package file

import (
	"errors"
	"fmt"
	"github.com/harness/cli/v3/modules/ci/coverage/format"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/harness/cli/v3/pkg/hlog"
)

// File is a discovered coverage report and its inferred format.
type File struct {
	Path     string
	Language string
	Format   string
}

var filePatterns = map[string]struct {
	Language string
	Format   string
}{
	"coverage.out":         {"Golang", format.FormatGo},
	"cover.out":            {"Golang", format.FormatGo},
	".cov":                 {"Golang", format.FormatGo},
	".out":                 {"Golang", format.FormatGo},
	"lcov.info":            {"JavaScript/TypeScript", format.FormatLCOV},
	"coverage.info":        {"C/C++", format.FormatLCOV},
	"jacoco.xml":           {"Java/Kotlin", format.FormatJacoco},
	"jacocoTestReport.xml": {"Java/Kotlin", format.FormatJacoco},
	"jacocoReport.xml":     {"Java/Kotlin", format.FormatJacoco},
	"clover.xml":           {"PHP/Java/JavaScript", format.FormatClover},
	"coverage.xml":         {"Python/C#/C++/JavaScript", format.FormatCobertura},
	"cobertura.xml":        {"Java/Python/C++", format.FormatCobertura},
	"scoverage.xml":        {"Scala", format.FormatCobertura},
	"coveralls.json":       {"JavaScript/TypeScript/Ruby/Python", format.FormatCoveralls},
	"_tiRunner.json":       {"TI Runner", format.FormatTIRunner},
}

var searchDirs = []string{
	".",
	"coverage",
	"test",
	"tests",
	"jacoco",
	"build/reports/jacoco/test",
	"target/site/jacoco",
}

var ignoreFileNames = []string{
	"Dockerfile.coverage",
}

// skipDirs lists directories Lookup's recursive fallback never
// descends into: hidden dirs plus dependency/build output across Node, Go,
// Java/Kotlin, Python, Ruby, and C#/.NET toolchains.
var skipDirs = map[string]bool{
	".git":    true,
	".svn":    true,
	".hg":     true,
	".idea":   true,
	".vscode": true,

	"node_modules": true,

	"vendor": true,

	"target":  true,
	"build":   true,
	".gradle": true,

	"venv":          true,
	".venv":         true,
	"__pycache__":   true,
	".pytest_cache": true,
	".tox":          true,
	".mypy_cache":   true,

	".bundle": true,

	"bin": true,
	"obj": true,

	"dist":   true,
	"out":    true,
	".cache": true,
	".next":  true,
	".nuxt":  true,
}

// Lookup discovers coverage report files. With explicit files it
// validates each exists and matches a known pattern, erroring otherwise.
// With none, it searches searchDirs non-recursively, then those same
// dirs nested one level inside each top-level subdirectory (multi-module
// layouts), falling back to a full recursive walk only if both find nothing.
func Lookup(files ...string) ([]File, error) {
	hlog.Debug("looking up coverage files", "explicit", files)

	var found []File

	checkFile := func(path string) {
		for pattern, meta := range filePatterns {
			if strings.HasSuffix(path, pattern) || filepath.Base(path) == pattern {
				found = append(found, File{
					Path:     path,
					Language: meta.Language,
					Format:   meta.Format,
				})
				return
			}
		}
	}

	if len(files) > 0 {
		var missingFiles []string
		for _, file := range files {
			if _, err := os.Stat(file); os.IsNotExist(err) {
				missingFiles = append(missingFiles, file)
				continue
			}
			checkFile(file)
		}

		if len(missingFiles) > 0 {
			return nil, fmt.Errorf("coverage files not found: %v", missingFiles)
		}

		if len(found) == 0 {
			return nil, fmt.Errorf("no valid coverage files found in: %v", files)
		}

		return found, nil
	}

	for _, dir := range searchDirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, fmt.Errorf("reading dir %s: %w", dir, err)
		}

		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			name := entry.Name()
			if slices.Contains(ignoreFileNames, name) {
				continue
			}
			checkFile(filepath.Join(dir, name))
		}
	}

	if len(found) == 0 {
		topEntries, err := os.ReadDir(".")
		if err == nil {
			for _, topEntry := range topEntries {
				if !topEntry.IsDir() || strings.HasPrefix(topEntry.Name(), ".") {
					continue
				}
				for _, subDir := range searchDirs {
					if subDir == "." {
						continue
					}
					moduleDir := filepath.Join(topEntry.Name(), subDir)
					entries, err := os.ReadDir(moduleDir)
					if err != nil {
						continue
					}
					for _, entry := range entries {
						if entry.IsDir() {
							continue
						}
						name := entry.Name()
						if slices.Contains(ignoreFileNames, name) {
							continue
						}
						checkFile(filepath.Join(moduleDir, entry.Name()))
					}
				}
			}
		}
	}

	if len(found) > 0 {
		hlog.Debug("found coverage files in common directories", "count", len(found))
		return found, nil
	}

	hlog.Warn("no coverage files found in common directories, performing recursive search")

	err := filepath.WalkDir(".", func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if d.IsDir() {
			name := d.Name()
			if name != "." {
				if strings.HasPrefix(name, ".") || skipDirs[name] {
					return filepath.SkipDir
				}
			}
			return nil
		}

		name := d.Name()
		if slices.Contains(ignoreFileNames, name) {
			return nil
		}

		checkFile(path)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walking directory tree: %w", err)
	}

	if len(found) == 0 {
		return nil, errors.New("no coverage files found")
	}

	return found, nil
}
