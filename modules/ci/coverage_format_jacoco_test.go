// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package ci

import (
	"strings"
	"testing"
)

func TestCoverageJacocoParser_Parse(t *testing.T) {
	input := `<report name="myproj">
  <package name="com/example">
    <sourcefile name="Foo.java">
      <line nr="1" mi="0" ci="2" mb="0" cb="0"/>
      <line nr="2" mi="2" ci="0" mb="0" cb="0"/>
      <line nr="3" mi="0" ci="0" mb="1" cb="1"/>
      <line nr="4" mi="0" ci="0" mb="0" cb="0"/>
    </sourcefile>
  </package>
</report>`
	records := collectCoverageJacoco(t, input)
	want := map[uint32]coverageLineType{1: coverageLineCovered, 2: coverageLineUncovered, 3: coverageLinePartial}
	if len(records) != len(want) {
		t.Fatalf("got %d records, want %d (line 4 has no coverage data and should be skipped)", len(records), len(want))
	}
	for _, r := range records {
		if lt, ok := want[r.LineNumber]; !ok || lt != r.LineType {
			t.Errorf("line %d LineType = %v, want %v", r.LineNumber, r.LineType, want[r.LineNumber])
		}
	}
}

func TestCoverageJacocoParser_BranchTakesPrecedenceOverInstruction(t *testing.T) {
	input := `<report name="p">
  <package name="pkg">
    <sourcefile name="F.java">
      <line nr="1" mi="0" ci="5" mb="1" cb="1"/>
    </sourcefile>
  </package>
</report>`
	records := collectCoverageJacoco(t, input)
	if len(records) != 1 || records[0].LineType != coverageLinePartial {
		t.Fatalf("records = %+v, want 1 Partial record (branch overrides instruction)", records)
	}
}

func TestCoverageJacocoParser_MissingLineAttributes(t *testing.T) {
	input := `<report name="p">
  <package name="pkg">
    <sourcefile name="F.java">
      <line/>
    </sourcefile>
  </package>
</report>`
	records := collectCoverageJacoco(t, input)
	if len(records) != 0 {
		t.Fatalf("records = %+v, want none", records)
	}
}

func TestCoverageJacocoParser_MalformedXML(t *testing.T) {
	p := newCoverageJacocoParser()
	seq, err := p.Parse(strings.NewReader("<report><unterminated"))
	if err != nil {
		t.Fatalf("Parse() error = %v, want nil top-level error", err)
	}
	for range seq {
	}
	if p.Err() == nil {
		t.Fatal("Err() = nil, want error after malformed XML")
	}
}

func TestCoverageJacocoParser_ResolvesAggregatedGroupPaths(t *testing.T) {
	input := `<report name="agg">
  <group name="apps/module-a">
    <package name="com/example">
      <sourcefile name="Foo.java">
        <line nr="1" mi="0" ci="1" mb="0" cb="0"/>
      </sourcefile>
    </package>
  </group>
  <group name="apps/module-b">
    <package name="com/example">
      <sourcefile name="Foo.java">
        <line nr="1" mi="0" ci="1" mb="0" cb="0"/>
      </sourcefile>
    </package>
  </group>
</report>`
	records := collectCoverageJacoco(t, input)
	if len(records) != 2 {
		t.Fatalf("got %d records, want 2", len(records))
	}
	if records[0].FilePath != records[1].FilePath {
		t.Fatalf("expected both groups to normalize to the same package-relative path, got %q and %q", records[0].FilePath, records[1].FilePath)
	}
	candidatesA := strings.Join(records[0].PathCandidates, ",")
	candidatesB := strings.Join(records[1].PathCandidates, ",")
	if candidatesA == candidatesB {
		t.Fatalf("expected different PathCandidates per group, both got %q", candidatesA)
	}
	if !strings.Contains(candidatesA, "apps/module-a") {
		t.Errorf("records[0].PathCandidates = %v, want a candidate under apps/module-a", records[0].PathCandidates)
	}
	if !strings.Contains(candidatesB, "apps/module-b") {
		t.Errorf("records[1].PathCandidates = %v, want a candidate under apps/module-b", records[1].PathCandidates)
	}
}

func TestCoverageJacocoParser_BackslashTraversalInGroupNameRejected(t *testing.T) {
	input := `<report name="agg">
  <group name="apps\..\..\etc">
    <package name="com/example">
      <sourcefile name="Foo.java">
        <line nr="1" mi="0" ci="1" mb="0" cb="0"/>
      </sourcefile>
    </package>
  </group>
</report>`
	records := collectCoverageJacoco(t, input)
	if len(records) != 1 {
		t.Fatalf("got %d records, want 1", len(records))
	}
	for _, c := range records[0].PathCandidates {
		if strings.Contains(c, "..") {
			t.Errorf("PathCandidates = %v, want no \"..\" traversal segment from the backslash-separated group name", records[0].PathCandidates)
		}
	}
}

func collectCoverageJacoco(t *testing.T, input string) []coverageLineRecord {
	t.Helper()
	p := newCoverageJacocoParser()
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
