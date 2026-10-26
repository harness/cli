// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package format

import (
	"bytes"
	"encoding/json"
	"os"
	"slices"
	"testing"

	"github.com/parquet-go/parquet-go"
)

// goldenManifest is independent of the parquet struct so a renamed tag fails as
// a value mismatch.
type goldenManifest struct {
	Columns []string       `json:"columns"`
	Records []goldenRecord `json:"records"`
}

type goldenRecord struct {
	RepoRef    string   `json:"repo_ref"`
	UnitKey    string   `json:"unit_key"`
	FilePath   string   `json:"file_path"`
	Hit        []uint32 `json:"hit"`
	Partial    []uint32 `json:"partial"`
	Executable []uint32 `json:"executable"`
}

// The fixture is shared with the coverage server, so schema drift on either
// side fails here instead of silently indexing empty coverage.
func TestCoverageGoldenFixtureDecodes(t *testing.T) {
	data, err := os.ReadFile("testdata/unit_coverage_v1.parquet")
	if err != nil {
		t.Fatalf("read golden parquet: %v", err)
	}
	manifestData, err := os.ReadFile("testdata/unit_coverage_v1.json")
	if err != nil {
		t.Fatalf("read golden manifest: %v", err)
	}
	var manifest goldenManifest
	if err := json.Unmarshal(manifestData, &manifest); err != nil {
		t.Fatalf("decode golden manifest: %v", err)
	}

	file, err := parquet.OpenFile(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("open golden parquet: %v", err)
	}
	var columns []string
	for _, field := range file.Schema().Fields() {
		columns = append(columns, field.Name())
	}
	if !slices.Equal(columns, manifest.Columns) {
		t.Fatalf("columns = %v, want %v", columns, manifest.Columns)
	}

	records, err := readUnitCoverageRecords(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("golden parquet is not readable: %v", err)
	}
	if len(records) != len(manifest.Records) {
		t.Fatalf("record count = %d, want %d", len(records), len(manifest.Records))
	}
	for i, want := range manifest.Records {
		got := records[i]
		if got.RepoRef != want.RepoRef || got.UnitKey != want.UnitKey || got.FilePath != want.FilePath {
			t.Errorf("record %d identity = (%q, %q, %q), want (%q, %q, %q)", i,
				got.RepoRef, got.UnitKey, got.FilePath, want.RepoRef, want.UnitKey, want.FilePath)
		}
		if !slices.Equal(got.HitLines, want.Hit) {
			t.Errorf("record %d hit = %v, want %v", i, got.HitLines, want.Hit)
		}
		if !slices.Equal(got.PartialLines, want.Partial) {
			t.Errorf("record %d partial = %v, want %v", i, got.PartialLines, want.Partial)
		}
		if !slices.Equal(got.ExecutableLines, want.Executable) {
			t.Errorf("record %d executable = %v, want %v", i, got.ExecutableLines, want.Executable)
		}
	}
}
