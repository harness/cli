// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package format

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"testing"

	"github.com/parquet-go/parquet-go"

	"github.com/harness/cli/v3/modules/ci/coverage/env"
)

const tiBitmapReportJSON = `{
  "tests": [
    {
      "test": "tests/test_calculator.py",
      "coverage_key": "key-a",
      "coverage": {
        "files": {
          "src/calculator.py": {"line_numbers": [1, 2, 5], "covered_line_numbers": [2]},
          "vendor/lib.py": {"line_numbers": [7], "covered_line_numbers": [7]}
        }
      }
    }
  ],
  "skipped_coverage_keys": ["key-b"]
}`

func writeTI(t *testing.T, report string, opts ...WriteOption) (EncodeResult, []UnitCoverageRecord, error) {
	t.Helper()
	var buf bytes.Buffer
	enc, err := Write("owner/repo", FormatTIRunner, strings.NewReader(report), &buf, opts...)
	if err != nil {
		return enc, nil, err
	}
	records, readErr := readUnitCoverageRecords(bytes.NewReader(buf.Bytes()))
	if readErr != nil {
		t.Fatalf("output is not a valid parquet-bitmap stream: %v", readErr)
	}
	return enc, records, nil
}

func TestCoverageWriteTIRunner(t *testing.T) {
	enc, records, err := writeTI(t, tiBitmapReportJSON)
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if enc.Format != formatParquetBitmap {
		t.Errorf("format = %q, want %q", enc.Format, formatParquetBitmap)
	}
	if !slices.Equal(enc.SkippedKeys, []string{"key-b"}) {
		t.Errorf("skipped keys = %v, want [key-b]", enc.SkippedKeys)
	}
	if enc.DiffByFile != nil {
		t.Errorf("DiffByFile = %v, want nil without a diff base", enc.DiffByFile)
	}
	if len(records) != 2 {
		t.Fatalf("records = %d, want 2", len(records))
	}
	for _, r := range records {
		if r.RepoRef != "owner/repo" || r.UnitKey != "key-a" {
			t.Errorf("record = (%q, %q), want (owner/repo, key-a)", r.RepoRef, r.UnitKey)
		}
	}
}

// --patch must not narrow the rows: the server derives patch totals from the
// per-record bitmap and reuses the full history for skipped keys.
func TestCoverageWriteTIRunner_PatchDoesNotNarrowPayload(t *testing.T) {
	_, want, err := writeTI(t, tiBitmapReportJSON)
	if err != nil {
		t.Fatalf("Write: %v", err)
	}

	enc, got, err := writeTI(t, tiBitmapReportJSON,
		WithPatch(true),
		WithDiff("HEAD", "HEAD"),
		WithPathResolver(env.NewTree([]string{"src/calculator.py", "vendor/lib.py"})),
	)
	if err != nil {
		t.Fatalf("Write with patch: %v", err)
	}
	if len(got) != len(want) {
		t.Fatalf("records with --patch = %d, want %d", len(got), len(want))
	}
	for i := range got {
		if got[i].FilePath != want[i].FilePath ||
			!slices.Equal(got[i].HitLines, want[i].HitLines) ||
			!slices.Equal(got[i].ExecutableLines, want[i].ExecutableLines) {
			t.Errorf("record %d was changed by --patch: %+v vs %+v", i, got[i], want[i])
		}
	}
	if !slices.Equal(enc.SkippedKeys, []string{"key-b"}) {
		t.Errorf("skipped keys = %v, want [key-b]", enc.SkippedKeys)
	}
}

func TestCoverageWriteTIRunner_PatchFailsWithoutDiff(t *testing.T) {
	_, _, err := writeTI(t, tiBitmapReportJSON,
		WithPatch(true), WithDiff("not-a-real-base", "not-a-real-head"))
	if err == nil {
		t.Fatal("want an error when --patch has no resolvable diff")
	}
}

func TestCoverageWriteTIRunner_PatchFailsForUnresolvedPath(t *testing.T) {
	_, _, err := writeTI(t, tiBitmapReportJSON,
		WithPatch(true),
		WithDiff("HEAD", "HEAD"),
		WithPathResolver(env.NewTree([]string{"different/file.py"})),
	)
	if err == nil || !strings.Contains(err.Error(), "was not found in the repository") {
		t.Fatalf("err = %v, want unresolved repository path", err)
	}
}

func TestCoverageWriteTIRunner_ExcludesFiles(t *testing.T) {
	_, records, err := writeTI(t, tiBitmapReportJSON, WithExclude([]string{"vendor/**"}))
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if len(records) != 1 || records[0].FilePath != "src/calculator.py" {
		t.Fatalf("records = %+v, want only src/calculator.py", records)
	}
}

// A report with no coverage must still produce a readable stream, since it is
// uploaded like any other.
func TestCoverageWriteTIRunner_Empty(t *testing.T) {
	enc, records, err := writeTI(t, `{"tests":[]}`)
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if enc.Format != formatParquetBitmap || enc.SkippedKeys != nil || len(records) != 0 {
		t.Errorf("got format=%q skipped=%v records=%d", enc.Format, enc.SkippedKeys, len(records))
	}
}

// With no rows, the skipped keys still have to reach the server.
func TestCoverageWriteTIRunner_AllSkipped(t *testing.T) {
	enc, _, err := writeTI(t, `{"skipped_coverage_keys":["k1"]}`)
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if !slices.Equal(enc.SkippedKeys, []string{"k1"}) {
		t.Errorf("skipped keys = %v, want [k1]", enc.SkippedKeys)
	}
}

func TestCoverageWriteTIRunner_InvalidJSON(t *testing.T) {
	if _, _, err := writeTI(t, `{"tests":`); err == nil {
		t.Error("want an error on a truncated TI report")
	}
}

// A key that ran never also appears in skipped_keys.
func TestCoverageReconcileSkippedKeys(t *testing.T) {
	got := reconcileSkippedKeys([]string{"key-a"}, []string{"key-c", "key-a", "key-b", "key-c"})
	if want := []string{"key-b", "key-c"}; !slices.Equal(got, want) {
		t.Errorf("skipped = %v, want %v (key-a ran, key-c duplicated)", got, want)
	}
	if got := reconcileSkippedKeys([]string{"key-a"}, []string{"key-a"}); got != nil {
		t.Errorf("skipped = %v, want nil when every key ran", got)
	}
}

func TestCoverageBuildDiffByFile(t *testing.T) {
	diff := &changedLineIndex{lines: map[string]map[uint32]bool{
		"src/a.go":    {9: true, 3: true, 5: false},
		"src/none.go": {1: false},
		"vendor/x.go": {1: true},
	}}
	patterns, err := compileExcludePatterns([]string{"vendor/**"})
	if err != nil {
		t.Fatalf("compileExcludePatterns: %v", err)
	}

	got := buildDiffByFile(diff, patterns, map[string]struct{}{})
	if len(got) != 1 || !slices.Equal(got["src/a.go"], []uint32{3, 9}) {
		t.Errorf("diffByFile = %v, want only src/a.go: [3 9]", got)
	}
	if got := buildDiffByFile(&changedLineIndex{lines: map[string]map[uint32]bool{"a": {1: false}}}, nil, nil); got != nil {
		t.Errorf("diffByFile = %v, want nil when nothing changed", got)
	}
}

func TestCoverageResolveUnitRepositoryPaths(t *testing.T) {
	records := []UnitCoverageRecord{
		{FilePath: "vendor/generated.go"},
		{FilePath: ""},
		{FilePath: "src/calculator.go"},
	}
	patterns, err := compileExcludePatterns([]string{"vendor/**"})
	if err != nil {
		t.Fatalf("compileExcludePatterns: %v", err)
	}

	got, err := resolveUnitRepositoryPaths(records, RepositoryPathOptions{}, patterns, map[string]struct{}{})
	if err != nil {
		t.Fatalf("resolveUnitRepositoryPaths: %v", err)
	}
	if len(got) != 1 || got[0].FilePath != "src/calculator.go" {
		t.Fatalf("got %v, want only src/calculator.go", got)
	}
	if records[0].FilePath != "vendor/generated.go" || records[2].FilePath != "src/calculator.go" {
		t.Errorf("input was mutated: %v", records)
	}
}

func readUnitCoverageRecords(r io.ReaderAt) ([]UnitCoverageRecord, error) {
	reader := parquet.NewGenericReader[UnitCoverageRecord](r)
	defer func() { _ = reader.Close() }()

	if reader.NumRows() == 0 {
		return nil, nil
	}
	var records []UnitCoverageRecord
	buf := make([]UnitCoverageRecord, 1024)
	for {
		n, err := reader.Read(buf)
		records = append(records, buf[:n]...)
		if errors.Is(err, io.EOF) || (err == nil && n == 0) {
			return records, nil
		}
		if err != nil {
			return nil, fmt.Errorf("failed to read parquet-bitmap records: %w", err)
		}
	}
}
