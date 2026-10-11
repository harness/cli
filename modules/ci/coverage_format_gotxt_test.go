// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package ci

import (
	"strings"
	"testing"
)

func TestCoverageGotxtParser_Err(t *testing.T) {
	p := newCoverageGotxtParser()
	if err := p.Err(); err != nil {
		t.Fatalf("Err() = %v, want nil", err)
	}
}

func TestCoverageGotxtParser_Parse(t *testing.T) {
	input := `mode: set
example.com/pkg/file.go:3.14,5.2 1 1
example.com/pkg/file.go:7.14,9.2 1 0
`
	p := newCoverageGotxtParser()
	seq, err := p.Parse(strings.NewReader(input))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	var records []coverageLineRecord
	for r := range seq {
		records = append(records, r)
	}
	if len(records) < 2 {
		t.Fatalf("got %d records, want at least 2", len(records))
	}
	for _, r := range records {
		if r.FilePath == "" {
			t.Errorf("record has empty FilePath: %+v", r)
		}
		if r.LineNumber == 0 {
			t.Errorf("record has zero LineNumber: %+v", r)
		}
	}
}

func TestCoverageGotxtParser_Parse_InvalidInput(t *testing.T) {
	p := newCoverageGotxtParser()
	if _, err := p.Parse(strings.NewReader("not a coverage profile")); err == nil {
		t.Fatal("Parse() error = nil, want error")
	}
}

func TestCoverageGotxtParser_MergesDuplicateSetModeBlocks(t *testing.T) {
	input := `mode: set
example.com/pkg/file.go:3.14,5.2 1 0
example.com/pkg/file.go:3.14,5.2 1 1
`
	p := newCoverageGotxtParser()
	seq, err := p.Parse(strings.NewReader(input))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	var records []coverageLineRecord
	for r := range seq {
		records = append(records, r)
	}
	for _, r := range records {
		if r.LineType != coverageLineCovered {
			t.Errorf("line %d LineType = %v, want Covered (duplicate block OR'd under set mode)", r.LineNumber, r.LineType)
		}
	}
}

func TestCoverageGotxtParser_MergesDuplicateCountModeBlocks(t *testing.T) {
	input := `mode: count
example.com/pkg/file.go:3.14,5.2 1 2
example.com/pkg/file.go:3.14,5.2 1 3
`
	p := newCoverageGotxtParser()
	seq, err := p.Parse(strings.NewReader(input))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	for r := range seq {
		if r.LineType != coverageLineCovered {
			t.Errorf("line %d LineType = %v, want Covered (summed counts stay positive)", r.LineNumber, r.LineType)
		}
	}
}

func TestCoverageGotxtParser_MismatchedBlockBoundariesError(t *testing.T) {
	input := `mode: set
example.com/pkg/file.go:3.14,5.2 1 1
example.com/pkg/file.go:3.14,9.2 1 1
`
	p := newCoverageGotxtParser()
	if _, err := p.Parse(strings.NewReader(input)); err == nil {
		t.Fatal("Parse() error = nil, want error for a block whose start coordinates repeat with a different end")
	}
}
