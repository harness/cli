// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package format

import (
	"strings"
	"testing"
)

func TestCoverageCoverallsParser_Parse(t *testing.T) {
	input := `{"source_files":[{"name":"foo.go","coverage":[1,0,null,3]}]}`
	records := collectCoverageCoveralls(t, input)
	want := map[uint32]LineType{1: LineCovered, 2: LineUncovered, 4: LineCovered}
	if len(records) != len(want) {
		t.Fatalf("got %d records, want %d (null entry at line 3 must be skipped)", len(records), len(want))
	}
	for _, r := range records {
		if r.FilePath != "foo.go" {
			t.Errorf("FilePath = %q, want foo.go", r.FilePath)
		}
		if lt, ok := want[r.LineNumber]; !ok || lt != r.LineType {
			t.Errorf("line %d LineType = %v, want %v", r.LineNumber, r.LineType, want[r.LineNumber])
		}
	}
}

func TestCoverageCoverallsParser_CoverageAsJSONEncodedString(t *testing.T) {
	input := `{"source_files":[{"name":"foo.go","coverage":"[1,0]"}]}`
	records := collectCoverageCoveralls(t, input)
	if len(records) != 2 {
		t.Fatalf("got %d records, want 2", len(records))
	}
}

func TestCoverageCoverallsParser_AllNullCoverageSkipsAll(t *testing.T) {
	records := collectCoverageCoveralls(t, `{"source_files":[{"name":"foo.go","coverage":[null,null]}]}`)
	if len(records) != 0 {
		t.Fatalf("records = %+v, want none", records)
	}
}

func TestCoverageCoverallsParser_MultipleSourceFiles(t *testing.T) {
	input := `{"source_files":[{"name":"a.go","coverage":[1]},{"name":"b.go","coverage":[0]}]}`
	records := collectCoverageCoveralls(t, input)
	if len(records) != 2 || records[0].FilePath != "a.go" || records[1].FilePath != "b.go" {
		t.Fatalf("records = %+v, want a.go then b.go", records)
	}
}

func TestCoverageCoverallsParser_SkipsEmptyName(t *testing.T) {
	records := collectCoverageCoveralls(t, `{"source_files":[{"name":"","coverage":[1]}]}`)
	if len(records) != 0 {
		t.Fatalf("records = %+v, want none", records)
	}
}

func TestCoverageCoverallsParser_EmptySourceFiles(t *testing.T) {
	records := collectCoverageCoveralls(t, `{"source_files":[]}`)
	if len(records) != 0 {
		t.Fatalf("records = %+v, want none", records)
	}
}

func TestCoverageCoverallsParser_EmptyCoverageArray(t *testing.T) {
	records := collectCoverageCoveralls(t, `{"source_files":[{"name":"foo.go","coverage":[]}]}`)
	if len(records) != 0 {
		t.Fatalf("records = %+v, want none", records)
	}
}

func TestCoverageCoverallsParser_MalformedJSON(t *testing.T) {
	p := newCoverallsParser()
	seq, err := p.Parse(strings.NewReader("{not json"))
	if err != nil {
		t.Fatalf("Parse() error = %v, want nil top-level error", err)
	}
	for range seq {
		t.Fatal("expected no records from malformed JSON")
	}
	if p.Err() == nil {
		t.Fatal("Err() = nil, want error after malformed JSON")
	}
}

func TestCoverageCoverallsParser_EmptyInput(t *testing.T) {
	p := newCoverallsParser()
	seq, err := p.Parse(strings.NewReader(""))
	if err != nil {
		t.Fatalf("Parse() error = %v, want nil top-level error", err)
	}
	for range seq {
	}
	if p.Err() == nil {
		t.Fatal("Err() = nil, want error on empty input")
	}
}

func TestCoverageCoverallsParser_CoverageStringWithInvalidJSONInside(t *testing.T) {
	p := newCoverallsParser()
	seq, err := p.Parse(strings.NewReader(`{"source_files":[{"name":"foo.go","coverage":"not json"}]}`))
	if err != nil {
		t.Fatalf("Parse() error = %v, want nil top-level error", err)
	}
	for range seq {
	}
	if p.Err() == nil {
		t.Fatal("Err() = nil, want error for invalid JSON inside the coverage string")
	}
}

func collectCoverageCoveralls(t *testing.T, input string) []LineRecord {
	t.Helper()
	p := newCoverallsParser()
	seq, err := p.Parse(strings.NewReader(input))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	var records []LineRecord
	for r := range seq {
		records = append(records, r)
	}
	if err := p.Err(); err != nil {
		t.Fatalf("Err() = %v, want nil", err)
	}
	return records
}
