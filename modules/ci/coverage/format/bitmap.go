// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package format

import (
	"fmt"
	"io"
	"sort"

	"github.com/parquet-go/parquet-go"
	"github.com/parquet-go/parquet-go/compress/snappy"

	"github.com/harness/cli/v3/modules/ci/coverage/env"
	"github.com/harness/cli/v3/pkg/hlog"
)

// writeParquetBitmap encodes TI Runner coverage as parquet-bitmap.
func writeParquetBitmap(
	repoRef string,
	r io.Reader,
	w io.Writer,
	options *writeOptions,
) (EncodeResult, error) {
	report, err := parseTIReport(repoRef, r)
	if err != nil {
		return EncodeResult{}, err
	}

	excludePatterns, err := options.excludeMatchers()
	if err != nil {
		return EncodeResult{}, err
	}
	logExcludePatterns(excludePatterns)
	excludedFiles := make(map[string]struct{})

	gitRoot, rootErr := env.RepositoryRoot()
	if rootErr != nil {
		gitRoot = ""
	}
	// Bitmap rows stay full-file, but their paths must still join diff_by_file.
	// In patch mode an unresolved path is therefore fatal.
	records, err := resolveUnitRepositoryPaths(report.Records, RepositoryPathOptions{
		Tree:     options.pathResolver,
		HintDirs: options.hintDirs,
		GitRoot:  gitRoot,
		Patch:    options.patch,
	}, excludePatterns, excludedFiles)
	if err != nil {
		return EncodeResult{}, err
	}

	ranKeys := distinctUnitKeys(records)

	diff, err := loadChangedLineIndex(options)
	if err != nil {
		// --patch still fails when the diff is missing, so a green upload
		// cannot mean the patch diff was omitted.
		if options.patch {
			return EncodeResult{}, fmt.Errorf("failed to compute patch diff: %w", err)
		}
		hlog.Warn("failed to get diff lines, continuing without diff tracking", "err", err)
		diff = nil
	}
	if options.patch && diff == nil {
		return EncodeResult{}, fmt.Errorf("patch coverage requires a diff base")
	}

	// Covers every changed file whether or not a test ran for it, which is
	// what makes patch coverage correct for TI-skipped files.
	diffByFile := buildDiffByFile(diff, excludePatterns, excludedFiles)

	if err := writeUnitCoverageRecords(w, records); err != nil {
		return EncodeResult{}, err
	}

	skippedKeys := reconcileSkippedKeys(ranKeys, report.SkippedCoverageKeys)

	hlog.Debug("encoded parquet-bitmap coverage",
		"records", len(records), "skipped_keys", len(skippedKeys), "diff_files", len(diffByFile))

	return parquetBitmapEncode(skippedKeys, diffByFile), nil
}

func buildDiffByFile(
	diff *changedLineIndex,
	excludePatterns []ExcludePattern,
	excludedFiles map[string]struct{},
) map[string][]uint32 {
	if diff == nil || len(diff.lines) == 0 {
		return nil
	}

	paths := make([]string, 0, len(diff.lines))
	for path := range diff.lines {
		if matchesAnyExcludePattern(path, excludePatterns) {
			logExcludedFileOnce(excludedFiles, path)
			continue
		}
		paths = append(paths, path)
	}
	sort.Strings(paths)

	result := make(map[string][]uint32, len(paths))
	for _, path := range paths {
		fileLines := diff.lines[path]
		lines := make([]uint32, 0, len(fileLines))
		for line, changed := range fileLines {
			if changed {
				lines = append(lines, line)
			}
		}
		if len(lines) == 0 {
			continue
		}
		sort.Slice(lines, func(i, j int) bool { return lines[i] < lines[j] })
		result[path] = lines
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

func distinctUnitKeys(records []UnitCoverageRecord) []string {
	seen := make(map[string]struct{}, len(records))
	keys := make([]string, 0, len(records))
	for _, record := range records {
		if _, ok := seen[record.UnitKey]; ok {
			continue
		}
		seen[record.UnitKey] = struct{}{}
		keys = append(keys, record.UnitKey)
	}
	return keys
}

// reconcileSkippedKeys dedupes and sorts skipped keys and drops any that also
// ran, so no key lands in both the parquet and the skipped list.
func reconcileSkippedKeys(ranKeys []string, skippedKeys []string) []string {
	if len(skippedKeys) == 0 {
		return nil
	}

	ran := make(map[string]struct{}, len(ranKeys))
	for _, key := range ranKeys {
		ran[key] = struct{}{}
	}

	seen := make(map[string]struct{}, len(skippedKeys))
	reconciled := make([]string, 0, len(skippedKeys))
	for _, key := range skippedKeys {
		if _, isRan := ran[key]; isRan {
			hlog.Debug("dropping skipped key that also ran in this report", "key", key)
			continue
		}
		if _, isDuplicate := seen[key]; isDuplicate {
			continue
		}
		seen[key] = struct{}{}
		reconciled = append(reconciled, key)
	}

	if len(reconciled) == 0 {
		return nil
	}
	sort.Strings(reconciled)
	return reconciled
}

// resolveUnitRepositoryPaths rewrites TI file paths to repository-relative
// paths and drops records that are excluded or resolve to nothing.
func resolveUnitRepositoryPaths(
	records []UnitCoverageRecord,
	opts RepositoryPathOptions,
	patterns []ExcludePattern,
	excludedFiles map[string]struct{},
) ([]UnitCoverageRecord, error) {
	if len(records) == 0 {
		return records, nil
	}
	paths := NewRepositoryPathResolver(opts)
	kept := make([]UnitCoverageRecord, 0, len(records))
	var pathErrs []error
	seenPathErrs := make(map[string]struct{})
	for _, record := range records {
		choice, err := paths.Select(record.FilePath, patterns, excludedFiles)
		if err != nil {
			pathErrs = AppendPathError(pathErrs, seenPathErrs, record.FilePath, err)
			continue
		}
		if choice.Skip {
			continue
		}
		record.FilePath = choice.Path
		kept = append(kept, record)
	}
	if err := JoinPathErrors(pathErrs); err != nil {
		return nil, err
	}
	return kept, nil
}

// writeUnitCoverageRecords writes zero records as a schema-only file, which
// an all-skipped upload still needs.
func writeUnitCoverageRecords(w io.Writer, records []UnitCoverageRecord) error {
	pw := parquet.NewGenericWriter[UnitCoverageRecord](w, parquet.Compression(&snappy.Codec{}))
	if len(records) > 0 {
		if _, err := pw.Write(records); err != nil {
			_ = pw.Close()
			return fmt.Errorf("failed to write parquet-bitmap records: %w", err)
		}
	}
	if err := pw.Close(); err != nil {
		return fmt.Errorf("failed to close parquet-bitmap writer: %w", err)
	}
	return nil
}
