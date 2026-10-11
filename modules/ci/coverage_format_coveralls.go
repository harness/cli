// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package ci

import (
	"encoding/json"
	"io"
	"iter"
)

// coverageCoverallsParser decodes the Coveralls JSON report format.
type coverageCoverallsParser struct {
	err error
}

func newCoverageCoverallsParser() *coverageCoverallsParser {
	return &coverageCoverallsParser{}
}

func (p *coverageCoverallsParser) Err() error { return p.err }

type coverageCoverallsJSONSourceFile struct {
	Name     string          `json:"name"`
	Coverage json.RawMessage `json:"coverage"`
}

type coverageCoverallsJSONReport struct {
	SourceFiles []coverageCoverallsJSONSourceFile `json:"source_files"`
}

func (p *coverageCoverallsParser) Parse(reader io.Reader) (iter.Seq[coverageLineRecord], error) {
	var report coverageCoverallsJSONReport
	if err := json.NewDecoder(reader).Decode(&report); err != nil {
		p.err = err
		return func(func(coverageLineRecord) bool) {}, nil
	}

	return func(yield func(coverageLineRecord) bool) {
		for _, sf := range report.SourceFiles {
			if !p.processFile(sf, yield) {
				return
			}
		}
	}, nil
}

func (p *coverageCoverallsParser) processFile(sf coverageCoverallsJSONSourceFile, yield func(coverageLineRecord) bool) bool {
	if sf.Name == "" {
		return true
	}
	coverage, err := coverageCoverallsParseCoverage(sf.Coverage)
	if err != nil {
		p.err = err
		return false
	}
	for i, val := range coverage {
		if val == nil {
			continue
		}
		lineType := coverageLineUncovered
		if *val > 0 {
			lineType = coverageLineCovered
		}
		if !yield(coverageLineRecord{
			FilePath:   sf.Name,
			LineNumber: uint32(i + 1),
			LineType:   lineType,
		}) {
			return false
		}
	}
	return true
}

// coverageCoverallsParseCoverage handles the two shapes the "coverage" field
// takes in the wild: a plain JSON array, or that same array JSON-encoded as a string.
func coverageCoverallsParseCoverage(raw json.RawMessage) ([]*int64, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	if raw[0] == '"' {
		var inner string
		if err := json.Unmarshal(raw, &inner); err != nil {
			return nil, err
		}
		raw = json.RawMessage(inner)
	}
	var coverage []*int64
	if err := json.Unmarshal(raw, &coverage); err != nil {
		return nil, err
	}
	return coverage, nil
}
