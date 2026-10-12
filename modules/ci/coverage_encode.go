// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package ci

import (
	"fmt"
	"io"

	"github.com/parquet-go/parquet-go"
	"github.com/parquet-go/parquet-go/compress/snappy"

	"github.com/harness/cli/v3/pkg/hlog"
)

// Sent verbatim in the presign request, so this must match the coverage
// server's types.CoverageFormat value.
const coverageFormatParquet = "parquet"

// coverageEncodeResult describes what coverageWrite produced for the server
// (mirrors format.IndexResult).
type coverageEncodeResult struct {
	Format      string
	SkippedKeys []string
	DiffByFile  map[string][]uint32
}

// coverageParquetEncode builds the result for the Parquet format.
func coverageParquetEncode() coverageEncodeResult {
	return coverageEncodeResult{Format: coverageFormatParquet}
}

// coverageWriteOption is a functional option for configuring coverageWrite.
type coverageWriteOption func(*coverageWriteOptions)

// coverageWriteOptions contains configuration for coverageWrite.
type coverageWriteOptions struct {
	baseRev            string
	headRev            string
	patch              bool
	hintDirs           []string // report directories walked upward to a project manifest
	excludePatterns    []string
	compiledExcludes   []coverageExcludePattern
	hasCompiledExclude bool
	pathResolver       *coverageTree
}

func (o *coverageWriteOptions) excludeMatchers() ([]coverageExcludePattern, error) {
	if o.hasCompiledExclude {
		return o.compiledExcludes, nil
	}
	return coverageCompileExcludePatterns(o.excludePatterns)
}

// coverageWithDiff enables diff tracking against the specified base revision.
// baseRev can be a branch name (e.g., "main"), remote branch (e.g.,
// "origin/main"), or commit SHA. headRev is optional - if provided, uses
// explicit commit range (baseRev..headRev) for precise diff. If baseRev is
// empty, diff tracking is disabled.
func coverageWithDiff(baseRev, headRev string) coverageWriteOption {
	return func(o *coverageWriteOptions) {
		o.baseRev = baseRev
		o.headRev = headRev
	}
}

// coverageWithPatch enables patch coverage mode, which only includes coverage
// data for lines that are part of the diff. When enabled, lines not in the
// diff are excluded from the output. Requires coverageWithDiff to be set.
func coverageWithPatch(v bool) coverageWriteOption {
	return func(o *coverageWriteOptions) {
		o.patch = v
	}
}

// coverageWithHintDirs provides coverage report directories. Repository path
// resolution walks upward from them to tracked project manifests; it never
// uses a report directory itself as a source path prefix.
func coverageWithHintDirs(dirs ...string) coverageWriteOption {
	return func(o *coverageWriteOptions) {
		o.hintDirs = append(o.hintDirs, dirs...)
	}
}

// coverageWithExclude excludes source files whose paths match any of the
// given patterns. Patterns prefixed with "regex:" are compiled as regular
// expressions; all others are treated as doublestar glob patterns.
func coverageWithExclude(patterns []string) coverageWriteOption {
	return func(o *coverageWriteOptions) {
		o.excludePatterns = patterns
	}
}

// coverageWithExcludePatterns applies already compiled exclude patterns so
// coverageWrite does not compile them a second time.
func coverageWithExcludePatterns(patterns []coverageExcludePattern) coverageWriteOption {
	return func(o *coverageWriteOptions) {
		o.compiledExcludes = patterns
		o.hasCompiledExclude = true
	}
}

// coverageWithPathResolver supplies a shared git path inventory for
// repository path resolution, excludes, and patch matching at write time.
func coverageWithPathResolver(resolver *coverageTree) coverageWriteOption {
	return func(o *coverageWriteOptions) {
		o.pathResolver = resolver
	}
}

// coverageWrite encodes coverage data from r into the parquet stream w. The
// returned coverageEncodeResult tells the caller what it produced and what
// metadata the coverage server needs with it.
//
// Per-test (TI runner) coverage, which bypasses the line parsers for a
// bitmap-shaped schema, is not yet ported; every format here goes through
// the conventional line-coverage parquet encoding.
func coverageWrite(
	repoRef string,
	format string,
	r io.Reader,
	w io.Writer,
	opts ...coverageWriteOption,
) (coverageEncodeResult, error) {
	hlog.Debug("writing coverage data to stream", "format", format)
	defer hlog.Debug("finished writing coverage data to stream", "format", format)

	options := &coverageWriteOptions{}
	for _, opt := range opts {
		opt(options)
	}

	return coverageWriteParquet(repoRef, format, r, w, options)
}

type coverageLineRecordKey struct {
	path string
	line uint32
}

// coverageWriteParquet encodes one of the conventional line-coverage formats
// as parquet: parse to coverageLineRecord, annotate with diff state, and
// write one row per source line.
func coverageWriteParquet(
	repoRef string,
	format string,
	r io.Reader,
	w io.Writer,
	options *coverageWriteOptions,
) (coverageEncodeResult, error) {
	parser, err := newCoverageParser(format)
	if err != nil {
		return coverageEncodeResult{}, err
	}

	excludePatterns, err := options.excludeMatchers()
	if err != nil {
		return coverageEncodeResult{}, err
	}
	coverageLogExcludePatterns(excludePatterns)
	excludedFiles := make(map[string]struct{})

	pw := parquet.NewGenericWriter[coverageLineRecord](w, parquet.Compression(&snappy.Codec{}))
	defer func() { _ = pw.Close() }()

	diff, err := coverageLoadChangedLineIndex(options)
	if err != nil {
		if options.patch {
			return coverageEncodeResult{}, fmt.Errorf("failed to compute patch diff: %w", err)
		}
		hlog.Warn("failed to get diff lines, continuing without diff tracking", "err", err)
		diff = nil
	}
	if options.patch && diff == nil {
		return coverageEncodeResult{}, fmt.Errorf("patch coverage requires a diff base")
	}

	hlog.Debug("parsing coverage data")
	defer hlog.Debug("finished parsing coverage data")

	lines, err := parser.Parse(r)
	if err != nil {
		return coverageEncodeResult{}, err
	}
	gitRoot, rootErr := coverageRepositoryRoot()
	if rootErr != nil {
		gitRoot = ""
	}
	paths := newCoverageRepositoryPathResolver(coverageRepositoryPathOptions{
		Tree:     options.pathResolver,
		HintDirs: options.hintDirs,
		GitRoot:  gitRoot,
		Patch:    options.patch,
		Format:   format,
	})
	covLines := make([]coverageLineRecord, 0, 1000)
	lineIndex := make(map[coverageLineRecordKey]int)
	var pathErrs []error
	seenPathErrs := make(map[string]struct{})
	for line := range lines {
		choice, err := paths.Select(line.FilePath, excludePatterns, excludedFiles, line.PathCandidates...)
		if err != nil {
			pathErrs = coverageAppendPathError(pathErrs, seenPathErrs, line.FilePath, err)
			continue
		}
		if choice.Skip {
			continue
		}

		line.RepoRef = repoRef
		line.FilePath = choice.Path
		if fileLines, exists := diff.linesFor(choice.Path); exists {
			line.Diff = fileLines[line.LineNumber]
		}

		if options.patch && !line.Diff {
			continue
		}

		key := coverageLineRecordKey{path: line.FilePath, line: line.LineNumber}
		if existing, ok := lineIndex[key]; ok {
			covLines[existing].LineType = coverageMergeLineType(covLines[existing].LineType, line.LineType)
			covLines[existing].Diff = covLines[existing].Diff || line.Diff
			continue
		}
		lineIndex[key] = len(covLines)
		covLines = append(covLines, line)
	}

	// Check for errors after consuming the iterator.
	if err := parser.Err(); err != nil {
		return coverageEncodeResult{}, fmt.Errorf("failed to parse %s coverage report: %w", format, err)
	}
	if err := coverageJoinPathErrors(pathErrs); err != nil {
		return coverageEncodeResult{}, err
	}

	for len(covLines) > 0 {
		batch := covLines
		if len(batch) > 1000 {
			batch = covLines[:1000]
			covLines = covLines[1000:]
		} else {
			covLines = nil
		}
		if _, err = pw.Write(batch); err != nil {
			return coverageEncodeResult{}, err
		}
	}

	if err := pw.Flush(); err != nil {
		return coverageEncodeResult{}, err
	}

	return coverageParquetEncode(), nil
}

// coverageMergeLineType combines two observations of the same source line.
// Any disagreement, including covered plus uncovered, becomes partial.
func coverageMergeLineType(left, right coverageLineType) coverageLineType {
	if left == coverageLinePartial || right == coverageLinePartial || left != right {
		return coverageLinePartial
	}
	return left
}
