// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package format

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

// tiReportJSON is the document shape TI produces, as agreed with the TI team.
const tiReportJSON = `{
  "tests": [
    {
      "test": "examples/per_test_coverage/tests/test_calculator.py",
      "coverage_key": "a6d49e94f218bd485917bcba54fa47ce41ffb72a",
      "coverage": {
        "files": {
          "pytest-plugin/examples/per_test_coverage/src/calculator.py": {
            "line_numbers": [1, 2, 5, 6, 9, 10, 13, 14, 15, 16],
            "covered_line_numbers": [2, 6, 10, 14, 16]
          }
        }
      }
    },
    {
      "test": "examples/per_test_coverage/tests/test_strings.py",
      "coverage_key": "88ed7f9a2c0fc0d91da595d433a970d42adb275c",
      "coverage": {
        "files": {
          "pytest-plugin/examples/per_test_coverage/src/strings.py": {
            "line_numbers": [1, 2, 5, 6, 9, 10, 11, 12],
            "covered_line_numbers": [2, 6, 10, 12]
          }
        }
      }
    }
  ]
}`

func TestCoverageParseTIReport(t *testing.T) {
	result, err := parseTIReport("owner/repo", strings.NewReader(tiReportJSON))
	if err != nil {
		t.Fatalf("parseTIReport: %v", err)
	}
	if len(result.Records) != 2 {
		t.Fatalf("records = %d, want 2", len(result.Records))
	}
	if len(result.SkippedCoverageKeys) != 0 {
		t.Errorf("skipped keys = %v, want none", result.SkippedCoverageKeys)
	}

	// Records are sorted by (unit key, file path), so ordering is deterministic.
	if got := result.Records[0].UnitKey; got != "88ed7f9a2c0fc0d91da595d433a970d42adb275c" {
		t.Errorf("first unit key = %q", got)
	}

	want := UnitCoverageRecord{
		RepoRef:         "owner/repo",
		UnitKey:         "a6d49e94f218bd485917bcba54fa47ce41ffb72a",
		FilePath:        "pytest-plugin/examples/per_test_coverage/src/calculator.py",
		HitLines:        []uint32{2, 6, 10, 14, 16},
		ExecutableLines: []uint32{1, 2, 5, 6, 9, 10, 13, 14, 15, 16},
	}
	if got := result.Records[1]; !reflect.DeepEqual(got, want) {
		t.Errorf("calculator record = %+v, want %+v", got, want)
	}
}

func TestCoverageParseTIReport_MissingCoverageKey(t *testing.T) {
	_, err := parseTIReport("owner/repo", strings.NewReader(
		`{"tests":[{"test":"t.py","coverage":{"files":{}}}]}`,
	))
	if !errors.Is(err, errMissingCoverageKey) {
		t.Fatalf("err = %v, want errMissingCoverageKey", err)
	}
}

func TestCoverageParseTIReport_SkippedCoverageKeys(t *testing.T) {
	const withSkipped = `{
      "tests": [{"test":"t.py","coverage_key":"key-1","coverage":{"files":{}}}],
      "skipped_coverage_keys": ["key-d", "key-e"]
    }`

	result, err := parseTIReport("owner/repo", strings.NewReader(withSkipped))
	if err != nil {
		t.Fatalf("parseTIReport: %v", err)
	}
	if want := []string{"key-d", "key-e"}; !reflect.DeepEqual(result.SkippedCoverageKeys, want) {
		t.Errorf("skipped keys = %v, want %v", result.SkippedCoverageKeys, want)
	}
}

func TestCoverageParseTIReport_SkippedCoverageKeysOnly(t *testing.T) {
	result, err := parseTIReport("owner/repo", strings.NewReader(`{"tests":[],"skipped_coverage_keys":["key-d"]}`))
	if err != nil {
		t.Fatalf("parseTIReport: %v", err)
	}
	if len(result.Records) != 0 {
		t.Errorf("records = %v, want none", result.Records)
	}
	if want := []string{"key-d"}; !reflect.DeepEqual(result.SkippedCoverageKeys, want) {
		t.Errorf("skipped keys = %v, want %v", result.SkippedCoverageKeys, want)
	}
}

func TestCoverageParseTIReport_Invalid(t *testing.T) {
	if _, err := parseTIReport("owner/repo", strings.NewReader("not json")); err == nil {
		t.Fatal("want error for invalid JSON")
	}
}

func TestCoverageAggregateTITests_MultipleFilesPerTest(t *testing.T) {
	tests := []tiTest{{
		Key: "key-1",
		Files: map[string]tiFileCoverage{
			"src/b.py": {LineNumbers: []uint32{1, 2}, CoveredLineNumbers: []uint32{2}},
			"src/a.py": {LineNumbers: []uint32{3, 4}, CoveredLineNumbers: []uint32{3}},
		},
	}}

	records := aggregateTITests("owner/repo", tests)
	if len(records) != 2 {
		t.Fatalf("records = %d, want 2", len(records))
	}
	if records[0].FilePath != "src/a.py" || records[1].FilePath != "src/b.py" {
		t.Errorf("paths = %q, %q; want sorted a.py, b.py", records[0].FilePath, records[1].FilePath)
	}
}

func TestCoverageAggregateTITests_DuplicateUnitFileIsMerged(t *testing.T) {
	// The same (key, file) can appear twice, e.g. after a retry.
	tests := []tiTest{
		{Key: "key-1", Files: map[string]tiFileCoverage{
			"src/a.py": {LineNumbers: []uint32{1, 2, 3}, CoveredLineNumbers: []uint32{1}},
		}},
		{Key: "key-1", Files: map[string]tiFileCoverage{
			"src/a.py": {LineNumbers: []uint32{1, 2, 3, 4}, CoveredLineNumbers: []uint32{3}},
		}},
	}

	records := aggregateTITests("owner/repo", tests)
	if len(records) != 1 {
		t.Fatalf("records = %d, want 1", len(records))
	}
	if want := []uint32{1, 3}; !reflect.DeepEqual(records[0].HitLines, want) {
		t.Errorf("hit lines = %v, want %v", records[0].HitLines, want)
	}
	if want := []uint32{1, 2, 3, 4}; !reflect.DeepEqual(records[0].ExecutableLines, want) {
		t.Errorf("executable lines = %v, want %v", records[0].ExecutableLines, want)
	}
}

func TestCoverageAggregateTITests_CoveredLinesAreAlwaysExecutable(t *testing.T) {
	// Line 7 is reported as hit but missing from line_numbers.
	tests := []tiTest{{
		Key: "key-1",
		Files: map[string]tiFileCoverage{
			"src/a.py": {LineNumbers: []uint32{1, 2}, CoveredLineNumbers: []uint32{2, 7}},
		},
	}}

	records := aggregateTITests("owner/repo", tests)
	if len(records) != 1 {
		t.Fatalf("records = %d, want 1", len(records))
	}
	if want := []uint32{1, 2, 7}; !reflect.DeepEqual(records[0].ExecutableLines, want) {
		t.Errorf("executable lines = %v, want %v", records[0].ExecutableLines, want)
	}
}

func TestCoverageAggregateTITests_Empty(t *testing.T) {
	cases := []struct {
		name  string
		tests []tiTest
	}{
		{name: "no tests"},
		{name: "test with no files", tests: []tiTest{{Key: "key-1"}}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if records := aggregateTITests("owner/repo", tc.tests); len(records) != 0 {
				t.Errorf("records = %v, want none", records)
			}
		})
	}
}
