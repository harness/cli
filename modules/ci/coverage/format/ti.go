// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package format

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
)

var errMissingCoverageKey = errors.New("test entry has no coverage_key")

type tiResult struct {
	Records             []UnitCoverageRecord
	SkippedCoverageKeys []string
}

type tiReport struct {
	Tests               []tiTest `json:"tests"`
	SkippedCoverageKeys []string `json:"skipped_coverage_keys"`
}

type tiTest struct {
	TestPath string
	Key      string
	Files    map[string]tiFileCoverage
}

type tiFileCoverage struct {
	// LineNumbers is every executable line, not just the covered ones.
	LineNumbers        []uint32 `json:"line_numbers"`
	CoveredLineNumbers []uint32 `json:"covered_line_numbers"`
}

type tiRawTest struct {
	Test        string `json:"test"`
	CoverageKey string `json:"coverage_key"`
	Coverage    struct {
		Files map[string]tiFileCoverage `json:"files"`
	} `json:"coverage"`
}

func (t *tiTest) UnmarshalJSON(data []byte) error {
	var raw tiRawTest
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	if raw.CoverageKey == "" {
		return fmt.Errorf("%w: test %q", errMissingCoverageKey, raw.Test)
	}
	t.TestPath = raw.Test
	t.Key = raw.CoverageKey
	t.Files = raw.Coverage.Files
	return nil
}

// parseTIReport folds a TI Runner report into one record per (coverage key,
// source file).
func parseTIReport(repoRef string, r io.Reader) (tiResult, error) {
	var rep tiReport
	if err := json.NewDecoder(r).Decode(&rep); err != nil {
		return tiResult{}, fmt.Errorf("failed to decode TI coverage report: %w", err)
	}
	return tiResult{
		Records:             aggregateTITests(repoRef, rep.Tests),
		SkippedCoverageKeys: rep.SkippedCoverageKeys,
	}, nil
}

type tiUnitFile struct {
	key  string
	file string
}

type tiUnitFileLines struct {
	hit        map[uint32]struct{}
	executable map[uint32]struct{}
}

func addTILines(dst map[uint32]struct{}, lines []uint32) {
	for _, line := range lines {
		dst[line] = struct{}{}
	}
}

func sortedTILines(set map[uint32]struct{}) []uint32 {
	if len(set) == 0 {
		return nil
	}
	out := make([]uint32, 0, len(set))
	for line := range set {
		out = append(out, line)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// aggregateTITests fills only HitLines and ExecutableLines; TI does not send
// partial coverage. A covered line is always executable.
func aggregateTITests(repoRef string, tests []tiTest) []UnitCoverageRecord {
	merged := make(map[tiUnitFile]*tiUnitFileLines)
	for _, t := range tests {
		for filePath, coverage := range t.Files {
			id := tiUnitFile{key: t.Key, file: filePath}
			lines, ok := merged[id]
			if !ok {
				lines = &tiUnitFileLines{
					hit:        make(map[uint32]struct{}),
					executable: make(map[uint32]struct{}),
				}
				merged[id] = lines
			}
			addTILines(lines.hit, coverage.CoveredLineNumbers)
			addTILines(lines.executable, coverage.LineNumbers)
			addTILines(lines.executable, coverage.CoveredLineNumbers)
		}
	}

	ids := make([]tiUnitFile, 0, len(merged))
	for id := range merged {
		ids = append(ids, id)
	}
	// Sorted so a given report always produces byte-identical parquet.
	sort.Slice(ids, func(i, j int) bool {
		if ids[i].key != ids[j].key {
			return ids[i].key < ids[j].key
		}
		return ids[i].file < ids[j].file
	})

	records := make([]UnitCoverageRecord, 0, len(ids))
	for _, id := range ids {
		lines := merged[id]
		records = append(records, UnitCoverageRecord{
			RepoRef:         repoRef,
			UnitKey:         id.key,
			FilePath:        id.file,
			HitLines:        sortedTILines(lines.hit),
			ExecutableLines: sortedTILines(lines.executable),
		})
	}
	return records
}
