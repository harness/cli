// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package ci

import (
	"errors"
	"strings"
	"testing"
)

func TestCoverageLcovParser_ParseSingleFile(t *testing.T) {
	input := `SF:foo.go
DA:1,1
DA:2,0
DA:3,1
end_of_record
`
	records := collectCoverageLcov(t, input)
	want := map[uint32]coverageLineType{1: coverageLineCovered, 2: coverageLineUncovered, 3: coverageLineCovered}
	if len(records) != len(want) {
		t.Fatalf("got %d records, want %d", len(records), len(want))
	}
	for _, r := range records {
		if r.FilePath != "foo.go" {
			t.Errorf("FilePath = %q, want %q", r.FilePath, "foo.go")
		}
		if lt, ok := want[r.LineNumber]; !ok || lt != r.LineType {
			t.Errorf("line %d LineType = %v, want %v", r.LineNumber, r.LineType, want[r.LineNumber])
		}
	}
}

func TestCoverageLcovParser_ParseMultipleFiles(t *testing.T) {
	input := `SF:a.go
DA:1,1
end_of_record
SF:b.go
DA:1,0
end_of_record
`
	records := collectCoverageLcov(t, input)
	if len(records) != 2 {
		t.Fatalf("got %d records, want 2", len(records))
	}
	if records[0].FilePath != "a.go" || records[1].FilePath != "b.go" {
		t.Errorf("records = %+v, want files a.go then b.go", records)
	}
}

func TestCoverageLcovParser_ParseNoEndOfRecord(t *testing.T) {
	input := "SF:a.go\nDA:1,1\n"
	records := collectCoverageLcov(t, input)
	if len(records) != 1 || records[0].FilePath != "a.go" {
		t.Fatalf("records = %+v, want 1 record for a.go", records)
	}
}

func TestCoverageLcovParser_ParseEmptyFile(t *testing.T) {
	records := collectCoverageLcov(t, "")
	if len(records) != 0 {
		t.Fatalf("records = %+v, want none", records)
	}
}

func TestCoverageLcovParser_ParseEmptyLines(t *testing.T) {
	input := "SF:a.go\n\nDA:1,1\n\nend_of_record\n"
	records := collectCoverageLcov(t, input)
	if len(records) != 1 {
		t.Fatalf("got %d records, want 1", len(records))
	}
}

func TestCoverageLcovParser_IgnoreOtherFields(t *testing.T) {
	input := `SF:a.go
FN:1,myFunc
FNDA:1,myFunc
FNF:1
FNH:1
BRF:0
BRH:0
DA:1,1
end_of_record
`
	records := collectCoverageLcov(t, input)
	if len(records) != 1 || records[0].LineNumber != 1 {
		t.Fatalf("records = %+v, want 1 record for line 1", records)
	}
}

func TestCoverageLcovParser_ParseDAWithoutSF(t *testing.T) {
	input := `DA:1,1
SF:a.go
DA:2,1
end_of_record
`
	records := collectCoverageLcov(t, input)
	if len(records) != 1 || records[0].LineNumber != 2 {
		t.Fatalf("records = %+v, want only line 2 attributed to a.go", records)
	}
}

func TestCoverageLcovParser_Parse_MalformedDALine(t *testing.T) {
	input := "SF:a.go\nDA:bad\nend_of_record\n"
	p := newCoverageLcovParser()
	seq, err := p.Parse(strings.NewReader(input))
	if err != nil {
		t.Fatalf("Parse() error = %v, want nil top-level error", err)
	}
	for range seq {
	}
	if p.Err() == nil {
		t.Fatal("Err() = nil, want error after malformed DA line")
	}
}

// errReader fails on the first Read, simulating a mid-file I/O failure.
type errReader struct{ err error }

func (r errReader) Read([]byte) (int, error) { return 0, r.err }

func TestCoverageLcovParser_Parse_ScannerErrorIsSurfaced(t *testing.T) {
	wantErr := errors.New("boom")
	p := newCoverageLcovParser()
	seq, err := p.Parse(errReader{err: wantErr})
	if err != nil {
		t.Fatalf("Parse() error = %v, want nil top-level error", err)
	}
	for range seq {
	}
	if p.Err() == nil || !errors.Is(p.Err(), wantErr) {
		t.Fatalf("Err() = %v, want it to wrap %v", p.Err(), wantErr)
	}
}

func TestCoverageParseLcovLineData_InvalidFormat(t *testing.T) {
	cases := []string{"42", "42,5,6", "", "abc,5", "42,abc"}
	for _, c := range cases {
		if _, _, err := coverageParseLcovLineData(c); err == nil {
			t.Errorf("coverageParseLcovLineData(%q) = nil error, want error", c)
		}
	}
}

func TestCoverageParseLcovLineData_LargeNumbers(t *testing.T) {
	ln, count, err := coverageParseLcovLineData("1000000,999999")
	if err != nil || ln != 1000000 || count != 999999 {
		t.Fatalf("coverageParseLcovLineData large numbers = (%d, %d, %v)", ln, count, err)
	}
}

func collectCoverageLcov(t *testing.T, input string) []coverageLineRecord {
	t.Helper()
	p := newCoverageLcovParser()
	seq, err := p.Parse(strings.NewReader(input))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	var records []coverageLineRecord
	for r := range seq {
		records = append(records, r)
	}
	if err := p.Err(); err != nil {
		t.Fatalf("Err() = %v, want nil", err)
	}
	return records
}
