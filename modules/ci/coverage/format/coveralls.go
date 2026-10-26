// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package format

import (
	"encoding/json"
	"io"
	"iter"
)

// coverallsParser decodes the Coveralls JSON report format.
type coverallsParser struct {
	err error
}

func newCoverallsParser() *coverallsParser {
	return &coverallsParser{}
}

func (p *coverallsParser) Err() error { return p.err }

type coverallsJSONSourceFile struct {
	Name     string          `json:"name"`
	Coverage json.RawMessage `json:"coverage"`
}

type coverallsJSONReport struct {
	SourceFiles []coverallsJSONSourceFile `json:"source_files"`
}

func (p *coverallsParser) Parse(reader io.Reader) (iter.Seq[LineRecord], error) {
	var report coverallsJSONReport
	if err := json.NewDecoder(reader).Decode(&report); err != nil {
		p.err = err
		return func(func(LineRecord) bool) {}, nil
	}

	return func(yield func(LineRecord) bool) {
		for _, sf := range report.SourceFiles {
			if !p.processFile(sf, yield) {
				return
			}
		}
	}, nil
}

func (p *coverallsParser) processFile(sf coverallsJSONSourceFile, yield func(LineRecord) bool) bool {
	if sf.Name == "" {
		return true
	}
	coverage, err := coverallsParseCoverage(sf.Coverage)
	if err != nil {
		p.err = err
		return false
	}
	for i, val := range coverage {
		if val == nil {
			continue
		}
		lineType := LineUncovered
		if *val > 0 {
			lineType = LineCovered
		}
		if !yield(LineRecord{
			FilePath:   sf.Name,
			LineNumber: uint32(i + 1),
			LineType:   lineType,
		}) {
			return false
		}
	}
	return true
}

// coverallsParseCoverage handles the two shapes the "coverage" field
// takes in the wild: a plain JSON array, or that same array JSON-encoded as a string.
func coverallsParseCoverage(raw json.RawMessage) ([]*int64, error) {
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
