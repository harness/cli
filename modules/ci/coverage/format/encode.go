// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package format

import (
	"fmt"
	"github.com/harness/cli/v3/modules/ci/coverage/env"
	"io"

	"github.com/parquet-go/parquet-go"
	"github.com/parquet-go/parquet-go/compress/snappy"

	"github.com/harness/cli/v3/pkg/hlog"
)

// Sent verbatim in the presign request, so this must match the coverage
// server's types.CoverageFormat value.
const formatParquet = "parquet"

// EncodeResult describes what Write produced for the server
// (mirrors format.IndexResult).
type EncodeResult struct {
	Format      string
	SkippedKeys []string
	DiffByFile  map[string][]uint32
}

// parquetEncode builds the result for the Parquet format.
func parquetEncode() EncodeResult {
	return EncodeResult{Format: formatParquet}
}

// WriteOption is a functional option for configuring Write.
type WriteOption func(*writeOptions)

// writeOptions contains configuration for Write.
type writeOptions struct {
	baseRev            string
	headRev            string
	patch              bool
	hintDirs           []string // report directories walked upward to a project manifest
	excludePatterns    []string
	compiledExcludes   []ExcludePattern
	hasCompiledExclude bool
	pathResolver       *env.Tree
}

func (o *writeOptions) excludeMatchers() ([]ExcludePattern, error) {
	if o.hasCompiledExclude {
		return o.compiledExcludes, nil
	}
	return compileExcludePatterns(o.excludePatterns)
}

// WithDiff enables diff tracking against the specified base revision.
// baseRev can be a branch name (e.g., "main"), remote branch (e.g.,
// "origin/main"), or commit SHA. headRev is optional - if provided, uses
// explicit commit range (baseRev..headRev) for precise diff. If baseRev is
// empty, diff tracking is disabled.
func WithDiff(baseRev, headRev string) WriteOption {
	return func(o *writeOptions) {
		o.baseRev = baseRev
		o.headRev = headRev
	}
}

// WithPatch enables patch coverage mode, which only includes coverage
// data for lines that are part of the diff. When enabled, lines not in the
// diff are excluded from the output. Requires WithDiff to be set.
func WithPatch(v bool) WriteOption {
	return func(o *writeOptions) {
		o.patch = v
	}
}

// WithHintDirs provides coverage report directories. Repository path
// resolution walks upward from them to tracked project manifests; it never
// uses a report directory itself as a source path prefix.
func WithHintDirs(dirs ...string) WriteOption {
	return func(o *writeOptions) {
		o.hintDirs = append(o.hintDirs, dirs...)
	}
}

// WithExclude excludes source files whose paths match any of the
// given patterns. Patterns prefixed with "regex:" are compiled as regular
// expressions; all others are treated as doublestar glob patterns.
func WithExclude(patterns []string) WriteOption {
	return func(o *writeOptions) {
		o.excludePatterns = patterns
	}
}

// WithExcludePatterns applies already compiled exclude patterns so
// Write does not compile them a second time.
func WithExcludePatterns(patterns []ExcludePattern) WriteOption {
	return func(o *writeOptions) {
		o.compiledExcludes = patterns
		o.hasCompiledExclude = true
	}
}

// WithPathResolver supplies a shared git path inventory for
// repository path resolution, excludes, and patch matching at write time.
func WithPathResolver(resolver *env.Tree) WriteOption {
	return func(o *writeOptions) {
		o.pathResolver = resolver
	}
}

// Write encodes coverage data from r into the parquet stream w. The
// returned EncodeResult tells the caller what it produced and what
// metadata the coverage server needs with it.
//
// Per-test (TI runner) coverage, which bypasses the line parsers for a
// bitmap-shaped schema, is not yet ported; every format here goes through
// the conventional line-coverage parquet encoding.
func Write(
	repoRef string,
	format string,
	r io.Reader,
	w io.Writer,
	opts ...WriteOption,
) (EncodeResult, error) {
	hlog.Debug("writing coverage data to stream", "format", format)
	defer hlog.Debug("finished writing coverage data to stream", "format", format)

	options := &writeOptions{}
	for _, opt := range opts {
		opt(options)
	}

	return writeParquet(repoRef, format, r, w, options)
}

type lineRecordKey struct {
	path string
	line uint32
}

// writeParquet encodes one of the conventional line-coverage formats
// as parquet: parse to LineRecord, annotate with diff state, and
// write one row per source line.
func writeParquet(
	repoRef string,
	format string,
	r io.Reader,
	w io.Writer,
	options *writeOptions,
) (EncodeResult, error) {
	parser, err := NewParser(format)
	if err != nil {
		return EncodeResult{}, err
	}

	excludePatterns, err := options.excludeMatchers()
	if err != nil {
		return EncodeResult{}, err
	}
	logExcludePatterns(excludePatterns)
	excludedFiles := make(map[string]struct{})

	pw := parquet.NewGenericWriter[LineRecord](w, parquet.Compression(&snappy.Codec{}))
	defer func() { _ = pw.Close() }()

	diff, err := loadChangedLineIndex(options)
	if err != nil {
		if options.patch {
			return EncodeResult{}, fmt.Errorf("failed to compute patch diff: %w", err)
		}
		hlog.Warn("failed to get diff lines, continuing without diff tracking", "err", err)
		diff = nil
	}
	if options.patch && diff == nil {
		return EncodeResult{}, fmt.Errorf("patch coverage requires a diff base")
	}

	hlog.Debug("parsing coverage data")
	defer hlog.Debug("finished parsing coverage data")

	lines, err := parser.Parse(r)
	if err != nil {
		return EncodeResult{}, err
	}
	gitRoot, rootErr := env.RepositoryRoot()
	if rootErr != nil {
		gitRoot = ""
	}
	paths := NewRepositoryPathResolver(RepositoryPathOptions{
		Tree:     options.pathResolver,
		HintDirs: options.hintDirs,
		GitRoot:  gitRoot,
		Patch:    options.patch,
		Format:   format,
	})
	covLines := make([]LineRecord, 0, 1000)
	lineIndex := make(map[lineRecordKey]int)
	var pathErrs []error
	seenPathErrs := make(map[string]struct{})
	for line := range lines {
		choice, err := paths.Select(line.FilePath, excludePatterns, excludedFiles, line.PathCandidates...)
		if err != nil {
			pathErrs = AppendPathError(pathErrs, seenPathErrs, line.FilePath, err)
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

		key := lineRecordKey{path: line.FilePath, line: line.LineNumber}
		if existing, ok := lineIndex[key]; ok {
			covLines[existing].LineType = MergeLineType(covLines[existing].LineType, line.LineType)
			covLines[existing].Diff = covLines[existing].Diff || line.Diff
			continue
		}
		lineIndex[key] = len(covLines)
		covLines = append(covLines, line)
	}

	// Check for errors after consuming the iterator.
	if err := parser.Err(); err != nil {
		return EncodeResult{}, fmt.Errorf("failed to parse %s coverage report: %w", format, err)
	}
	if err := JoinPathErrors(pathErrs); err != nil {
		return EncodeResult{}, err
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
			return EncodeResult{}, err
		}
	}

	if err := pw.Flush(); err != nil {
		return EncodeResult{}, err
	}

	return parquetEncode(), nil
}

// MergeLineType combines two observations of the same source line.
// Any disagreement, including covered plus uncovered, becomes partial.
func MergeLineType(left, right LineType) LineType {
	if left == LinePartial || right == LinePartial || left != right {
		return LinePartial
	}
	return left
}
