// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package ci

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// maxFileSizeBytes is the hard per-file upload limit (5 GiB).
const maxFileSizeBytes = 5 * 1024 * 1024 * 1024

// uploadEntry represents a single file to upload together with the registry
// destination path it should land at.
type uploadEntry struct {
	localPath string // absolute path on disk
	destPath  string // full artifact path: /{name}/{version}/[destination/][dir-basename/]filename
	size      int64
}

// resolveFilePath resolves a file path pattern (glob wildcards or regex).
// Returns all matched paths (files and directories alike) — the caller decides
// how to handle each type.
func resolveFilePath(pattern string) ([]string, error) {
	if !containsPathPattern(pattern) {
		if _, err := os.Stat(pattern); err != nil {
			return nil, fmt.Errorf("failed to access file: %w", err)
		}
		return []string{pattern}, nil
	}

	matches, err := filepath.Glob(pattern)
	if err == nil && len(matches) > 0 {
		return matches, nil
	}

	dir := filepath.Dir(pattern)
	if dir == "" || dir == "." {
		dir = "."
	}
	regexPattern := filepath.Base(pattern)

	re, err := regexp.Compile(regexPattern)
	if err != nil {
		return nil, fmt.Errorf("invalid pattern: %w", err)
	}

	var regexMatches []string
	err = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if re.MatchString(filepath.Base(path)) {
			regexMatches = append(regexMatches, path)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("failed to walk directory: %w", err)
	}
	if len(regexMatches) == 0 {
		return nil, fmt.Errorf("no files matched pattern: %s", pattern)
	}

	return regexMatches, nil
}

func containsPathPattern(pattern string) bool {
	for _, c := range pattern {
		switch c {
		case '*', '?', '[', '(', '|', '+', '\\':
			return true
		}
	}
	return false
}

// collectEntries resolves all user-supplied source patterns into uploadEntry
// values. Each source may be a file path, a glob/regex pattern, or a directory.
// Directories are walked recursively; their basename is preserved in the
// destination path so "./cache" → name/version/[dest/]cache/<rel>.
// All files including hidden ones (names starting with ".") are included.
func collectEntries(
	patterns []string,
	artifactName, version, destination string,
) ([]uploadEntry, error) {
	var entries []uploadEntry

	for _, pattern := range patterns {
		paths, err := resolveFilePath(pattern)
		if err != nil {
			return nil, err
		}
		for _, p := range paths {
			abs, err := filepath.Abs(p)
			if err != nil {
				return nil, fmt.Errorf("cannot resolve %q: %w", p, err)
			}
			info, err := os.Stat(abs)
			if err != nil {
				return nil, fmt.Errorf("cannot access %q: %w", p, err)
			}

			if info.IsDir() {
				dirEntries, err := walkDir(abs, artifactName, version, destination)
				if err != nil {
					return nil, err
				}
				entries = append(entries, dirEntries...)
			} else if info.Mode().IsRegular() {
				if info.Size() > maxFileSizeBytes {
					return nil, fmt.Errorf("file %q (%s) exceeds the 5 GiB per-file upload limit",
						abs, formatBytes(info.Size()))
				}
				entries = append(entries, fileEntry(abs, info.Size(), artifactName, version, destination))
			}
			// non-regular files (symlinks, sockets, etc.) are silently skipped
		}
	}
	return entries, nil
}

// fileEntry builds an uploadEntry for a single regular file.
// destPath format: "/{artifactName}/{version}/[{destination}/]{filename}"
func fileEntry(absPath string, size int64, artifactName, version, destination string) uploadEntry {
	filename := filepath.Base(absPath)
	var destPath string
	if destination != "" {
		destPath = "/" + artifactName + "/" + version + "/" + destination + "/" + filename
	} else {
		destPath = "/" + artifactName + "/" + version + "/" + filename
	}
	return uploadEntry{localPath: absPath, destPath: destPath, size: size}
}

// walkDir walks srcDir recursively and returns one uploadEntry per regular file.
// The directory's basename is preserved as a prefix so uploading "./cache"
// produces paths like name/version/[dest/]cache/subdir/file.
// All files including hidden ones are included.
func walkDir(srcDir, artifactName, version, destination string) ([]uploadEntry, error) {
	var entries []uploadEntry

	dirBase := filepath.ToSlash(filepath.Base(srcDir))
	if dirBase == "." || dirBase == "/" {
		dirBase = ""
	}

	err := filepath.WalkDir(srcDir, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == srcDir {
			return nil
		}
		if d.IsDir() {
			return nil
		}

		info, err := d.Info()
		if err != nil {
			return fmt.Errorf("stat %s: %w", path, err)
		}
		if !info.Mode().IsRegular() {
			return nil
		}

		relPath, err := filepath.Rel(srcDir, path)
		if err != nil {
			return fmt.Errorf("relative path for %s: %w", path, err)
		}
		relPath = filepath.ToSlash(relPath)
		if strings.HasPrefix(relPath, "../") {
			return fmt.Errorf("refusing %s: path escapes source directory", path)
		}

		var jobRelPath string
		if dirBase != "" {
			jobRelPath = dirBase + "/" + relPath
		} else {
			jobRelPath = relPath
		}

		var destPath string
		if destination != "" {
			destPath = "/" + artifactName + "/" + version + "/" + destination + "/" + jobRelPath
		} else {
			destPath = "/" + artifactName + "/" + version + "/" + jobRelPath
		}

		if info.Size() > maxFileSizeBytes {
			return fmt.Errorf("file %q (%s) exceeds the 5 GiB per-file upload limit",
				path, formatBytes(info.Size()))
		}
		entries = append(entries, uploadEntry{
			localPath: path,
			destPath:  destPath,
			size:      info.Size(),
		})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("failed to walk %s: %w", srcDir, err)
	}
	return entries, nil
}

// formatBytes formats a byte count as a human-readable string
// using lowercase SI-style suffixes (e.g. "80.8 kB", "1.2 MB").
func formatBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(b)/float64(div), "kMGTPE"[exp])
}
