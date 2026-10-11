// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package ci

import (
	"strings"
	"testing"
)

func TestCoverageCoberturaParser_Parse(t *testing.T) {
	input := `<coverage>
  <sources>
    <source>src/main</source>
  </sources>
  <packages>
    <package>
      <classes>
        <class filename="pkg/Foo.go">
          <lines>
            <line number="1" hits="1"/>
            <line number="2" hits="0"/>
            <line number="3" hits="5" branch="true" condition-coverage="50% (1/2)"/>
            <line number="4" hits="5" branch="true" condition-coverage="100% (2/2)"/>
            <line number="5" hits="5" branch="true" condition-coverage="0% (0/2)"/>
            <line number="undefined" hits="0"/>
          </lines>
        </class>
      </classes>
    </package>
  </packages>
</coverage>`
	records := collectCoverageCobertura(t, input)
	want := map[uint32]coverageLineType{
		1: coverageLineCovered,
		2: coverageLineUncovered,
		3: coverageLinePartial,
		4: coverageLineCovered,
		5: coverageLineUncovered,
	}
	if len(records) != len(want) {
		t.Fatalf("got %d records, want %d (undefined line number must be skipped)", len(records), len(want))
	}
	for _, r := range records {
		if lt, ok := want[r.LineNumber]; !ok || lt != r.LineType {
			t.Errorf("line %d LineType = %v, want %v", r.LineNumber, r.LineType, want[r.LineNumber])
		}
		if len(r.PathCandidates) != 1 || r.PathCandidates[0] != "src/main/pkg/Foo.go" {
			t.Errorf("line %d PathCandidates = %v, want [src/main/pkg/Foo.go]", r.LineNumber, r.PathCandidates)
		}
	}
}

func TestCoverageCoberturaParser_BranchFallsBackToHitsOnInvalidConditionCoverage(t *testing.T) {
	input := `<coverage>
  <packages>
    <package>
      <classes>
        <class filename="Foo.go">
          <lines>
            <line number="1" hits="3" branch="true" condition-coverage=""/>
          </lines>
        </class>
      </classes>
    </package>
  </packages>
</coverage>`
	records := collectCoverageCobertura(t, input)
	if len(records) != 1 || records[0].LineType != coverageLineCovered {
		t.Fatalf("records = %+v, want 1 Covered record (falls back to hits)", records)
	}
}

func TestCoverageCoberturaParser_SkipsEmptyFilename(t *testing.T) {
	input := `<coverage>
  <packages>
    <package>
      <classes>
        <class filename="">
          <lines>
            <line number="1" hits="1"/>
          </lines>
        </class>
      </classes>
    </package>
  </packages>
</coverage>`
	records := collectCoverageCobertura(t, input)
	if len(records) != 0 {
		t.Fatalf("records = %+v, want none", records)
	}
}

func TestCoverageCoberturaParser_ClassWithBothLinesAndStatements(t *testing.T) {
	input := `<coverage>
  <packages>
    <package>
      <classes>
        <class filename="Foo.scala">
          <lines>
            <line number="1" hits="1"/>
          </lines>
          <statements>
            <statement line="2" invocation-count="0" ignored="false"/>
            <statement line="3" invocation-count="4" ignored="false"/>
            <statement line="4" invocation-count="1" ignored="true"/>
          </statements>
        </class>
      </classes>
    </package>
  </packages>
</coverage>`
	records := collectCoverageCobertura(t, input)
	want := map[uint32]coverageLineType{1: coverageLineCovered, 2: coverageLineUncovered, 3: coverageLineCovered}
	if len(records) != len(want) {
		t.Fatalf("got %d records, want %d (ignored statement must be skipped)", len(records), len(want))
	}
	for _, r := range records {
		if lt, ok := want[r.LineNumber]; !ok || lt != r.LineType {
			t.Errorf("line %d LineType = %v, want %v", r.LineNumber, r.LineType, want[r.LineNumber])
		}
	}
}

func TestCoverageCoberturaParser_ScoverageRootElement(t *testing.T) {
	input := `<scoverage>
  <packages>
    <package>
      <classes>
        <class filename="Foo.scala">
          <statements>
            <statement line="1" invocation-count="1" ignored="false"/>
            <statement line="2" invocation-count="0" ignored="false"/>
          </statements>
        </class>
      </classes>
    </package>
  </packages>
</scoverage>`
	records := collectCoverageCobertura(t, input)
	want := map[uint32]coverageLineType{1: coverageLineCovered, 2: coverageLineUncovered}
	if len(records) != len(want) {
		t.Fatalf("got %d records, want %d", len(records), len(want))
	}
}

func TestCoverageCoberturaParser_AbsolutePathClassGetsNoCandidates(t *testing.T) {
	input := `<coverage>
  <sources>
    <source>src/main</source>
  </sources>
  <packages>
    <package>
      <classes>
        <class filename="/abs/Foo.go">
          <lines>
            <line number="1" hits="1"/>
          </lines>
        </class>
      </classes>
    </package>
  </packages>
</coverage>`
	records := collectCoverageCobertura(t, input)
	if len(records) != 1 || records[0].PathCandidates != nil {
		t.Fatalf("records = %+v, want 1 record with nil PathCandidates for an absolute filename", records)
	}
}

func TestCoverageCoberturaParser_WindowsStyleSourceAndFilename(t *testing.T) {
	input := `<coverage>
  <sources>
    <source>src\main</source>
  </sources>
  <packages>
    <package>
      <classes>
        <class filename="pkg\Foo.cs">
          <lines>
            <line number="1" hits="1"/>
          </lines>
        </class>
      </classes>
    </package>
  </packages>
</coverage>`
	records := collectCoverageCobertura(t, input)
	if len(records) != 1 {
		t.Fatalf("got %d records, want 1", len(records))
	}
	if records[0].FilePath != "pkg/Foo.cs" {
		t.Errorf("FilePath = %q, want pkg/Foo.cs (backslashes normalized)", records[0].FilePath)
	}
	if len(records[0].PathCandidates) != 1 || records[0].PathCandidates[0] != "src/main/pkg/Foo.cs" {
		t.Errorf("PathCandidates = %v, want [src/main/pkg/Foo.cs]", records[0].PathCandidates)
	}
}

func TestCoverageCoberturaParser_WindowsAbsolutePathClassGetsNoCandidates(t *testing.T) {
	input := `<coverage>
  <sources>
    <source>src/main</source>
  </sources>
  <packages>
    <package>
      <classes>
        <class filename="C:\Users\dev\Foo.cs">
          <lines>
            <line number="1" hits="1"/>
          </lines>
        </class>
      </classes>
    </package>
  </packages>
</coverage>`
	records := collectCoverageCobertura(t, input)
	if len(records) != 1 || records[0].PathCandidates != nil {
		t.Fatalf("records = %+v, want 1 record with nil PathCandidates for a Windows absolute filename", records)
	}
}

func TestCoverageCoberturaParser_MalformedXML(t *testing.T) {
	p := newCoverageCoberturaParser()
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

func TestCoverageCoberturaParser_EmptyInput(t *testing.T) {
	p := newCoverageCoberturaParser()
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

func collectCoverageCobertura(t *testing.T, input string) []coverageLineRecord {
	t.Helper()
	p := newCoverageCoberturaParser()
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
