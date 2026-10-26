// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package format

import (
	"strings"
	"testing"
)

func TestCoverageCloverParser_Parse(t *testing.T) {
	input := `<coverage>
  <project>
    <package>
      <file name="foo.go" path="src/foo.go">
        <line num="1" type="stmt" count="2"/>
        <line num="2" type="stmt" count="0"/>
        <line num="3" type="cond" count="1" truecount="1" falsecount="0"/>
        <line num="4" type="cond" count="1" truecount="0" falsecount="0"/>
      </file>
    </package>
  </project>
</coverage>`
	records := collectCoverageClover(t, input)
	want := map[uint32]LineType{
		1: LineCovered,
		2: LineUncovered,
		3: LinePartial,
		4: LineUncovered,
	}
	if len(records) != len(want) {
		t.Fatalf("got %d records, want %d", len(records), len(want))
	}
	for _, r := range records {
		if r.FilePath != "src/foo.go" {
			t.Errorf("FilePath = %q, want %q (path attr preferred over name)", r.FilePath, "src/foo.go")
		}
		if lt, ok := want[r.LineNumber]; !ok || lt != r.LineType {
			t.Errorf("line %d LineType = %v, want %v", r.LineNumber, r.LineType, want[r.LineNumber])
		}
	}
}

func TestCoverageCloverParser_FileDirectlyUnderRoot(t *testing.T) {
	input := `<coverage>
  <file name="foo.go">
    <line num="1" type="stmt" count="1"/>
  </file>
</coverage>`
	records := collectCoverageClover(t, input)
	if len(records) != 1 || records[0].FilePath != "foo.go" {
		t.Fatalf("records = %+v, want 1 record for foo.go", records)
	}
}

func TestCoverageCloverParser_SkipsTemplateAndVendorAndEmpty(t *testing.T) {
	input := `<coverage>
  <file name="{template}.php">
    <line num="1" type="stmt" count="1"/>
  </file>
  <file name="/app/vendor/pkg/foo.php">
    <line num="1" type="stmt" count="1"/>
  </file>
  <file name="empty.go">
  </file>
  <file name="real.go">
    <line num="1" type="stmt" count="1"/>
  </file>
</coverage>`
	records := collectCoverageClover(t, input)
	if len(records) != 1 || records[0].FilePath != "real.go" {
		t.Fatalf("records = %+v, want only real.go's line", records)
	}
}

func TestCoverageCloverParser_PHPEndOfFileTruncation(t *testing.T) {
	input := `<coverage>
  <file name="foo.php">
    <metrics loc="2"/>
    <line num="1" type="stmt" count="1"/>
    <line num="2" type="stmt" count="1"/>
    <line num="3" type="stmt" count="1"/>
  </file>
</coverage>`
	records := collectCoverageClover(t, input)
	if len(records) != 2 {
		t.Fatalf("got %d records, want 2 (line 3 is beyond eof=2 and must be truncated)", len(records))
	}
	for _, r := range records {
		if r.LineNumber > 2 {
			t.Errorf("unexpected line beyond eof: %+v", r)
		}
	}
}

func TestCoverageCloverParser_SkipsLineNumberLessThanOne(t *testing.T) {
	input := `<coverage>
  <file name="foo.go">
    <line num="0" type="stmt" count="1"/>
    <line num="1" type="stmt" count="1"/>
  </file>
</coverage>`
	records := collectCoverageClover(t, input)
	if len(records) != 1 || records[0].LineNumber != 1 {
		t.Fatalf("records = %+v, want only line 1", records)
	}
}

func TestCoverageCloverParser_MalformedXML(t *testing.T) {
	p := newCloverParser()
	seq, err := p.Parse(strings.NewReader("<coverage><unterminated"))
	if err != nil {
		t.Fatalf("Parse() error = %v, want nil top-level error", err)
	}
	for range seq {
		t.Fatal("expected no records from a malformed document")
	}
	if p.Err() == nil {
		t.Fatal("Err() = nil, want error after malformed XML")
	}
}

func TestCoverageCloverParser_EmptyInput(t *testing.T) {
	p := newCloverParser()
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

func collectCoverageClover(t *testing.T, input string) []LineRecord {
	t.Helper()
	p := newCloverParser()
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
