// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package ci

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// ---------------------------------------------------------------------------
// formatBytes
// ---------------------------------------------------------------------------

func TestFormatBytes(t *testing.T) {
	tests := []struct {
		input int64
		want  string
	}{
		{0, "0 B"},
		{1, "1 B"},
		{1023, "1023 B"},
		{1024, "1.0 kB"},
		{1536, "1.5 kB"},
		{1048576, "1.0 MB"},
		{1572864, "1.5 MB"},
		{1073741824, "1.0 GB"},
	}
	for _, tt := range tests {
		t.Run(fmt.Sprintf("%d", tt.input), func(t *testing.T) {
			got := formatBytes(tt.input)
			if got != tt.want {
				t.Errorf("formatBytes(%d) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// mergeArtifactOutputs
// ---------------------------------------------------------------------------

func TestMergeArtifactOutputs_NoOverlap(t *testing.T) {
	old := artifactOutput{
		ArtifactName: "art",
		Version:      "v1",
		Registry:     "reg",
		Files: []pushedFile{
			{Destination: "art/v1/a.zip", Size: 100},
		},
	}
	newOut := artifactOutput{
		ArtifactName: "art",
		Version:      "v1",
		Registry:     "reg",
		Files: []pushedFile{
			{Destination: "art/v1/b.zip", Size: 200},
		},
	}
	merged := mergeArtifactOutputs(old, newOut)

	if merged.FileCount != 2 {
		t.Errorf("FileCount = %d, want 2", merged.FileCount)
	}
	if merged.TotalBytes != 300 {
		t.Errorf("TotalBytes = %d, want 300", merged.TotalBytes)
	}
}

func TestMergeArtifactOutputs_NewWinsOnOverlap(t *testing.T) {
	old := artifactOutput{
		Files: []pushedFile{
			{Destination: "art/v1/file.zip", Size: 100, SHA256: "oldhash"},
		},
	}
	newOut := artifactOutput{
		Files: []pushedFile{
			{Destination: "art/v1/file.zip", Size: 200, SHA256: "newhash"},
		},
	}
	merged := mergeArtifactOutputs(old, newOut)

	if merged.FileCount != 1 {
		t.Fatalf("FileCount = %d, want 1", merged.FileCount)
	}
	if merged.Files[0].SHA256 != "newhash" {
		t.Errorf("SHA256 = %q, want %q", merged.Files[0].SHA256, "newhash")
	}
	if merged.TotalBytes != 200 {
		t.Errorf("TotalBytes = %d, want 200", merged.TotalBytes)
	}
}

func TestMergeArtifactOutputs_StableOrder(t *testing.T) {
	old := artifactOutput{
		Files: []pushedFile{
			{Destination: "art/v1/a.zip"},
			{Destination: "art/v1/b.zip"},
		},
	}
	newOut := artifactOutput{
		Files: []pushedFile{
			{Destination: "art/v1/c.zip"},
			{Destination: "art/v1/b.zip"},
		},
	}
	merged := mergeArtifactOutputs(old, newOut)

	// old entries come first in original order; new-only entries appended.
	if merged.FileCount != 3 {
		t.Fatalf("FileCount = %d, want 3", merged.FileCount)
	}
	wantOrder := []string{"art/v1/a.zip", "art/v1/b.zip", "art/v1/c.zip"}
	for i, f := range merged.Files {
		if f.Destination != wantOrder[i] {
			t.Errorf("Files[%d].Destination = %q, want %q", i, f.Destination, wantOrder[i])
		}
	}
}

func TestMergeArtifactOutputs_UUIDs(t *testing.T) {
	tests := []struct {
		name        string
		oldPkgUUID  string
		newPkgUUID  string
		wantPkgUUID string
		oldVerUUID  string
		newVerUUID  string
		wantVerUUID string
	}{
		{
			name:        "new UUID wins when both set",
			oldPkgUUID:  "old-pkg",
			newPkgUUID:  "new-pkg",
			wantPkgUUID: "new-pkg",
			oldVerUUID:  "old-ver",
			newVerUUID:  "new-ver",
			wantVerUUID: "new-ver",
		},
		{
			name:        "old UUID used as fallback when new is empty",
			oldPkgUUID:  "old-pkg",
			newPkgUUID:  "",
			wantPkgUUID: "old-pkg",
			oldVerUUID:  "old-ver",
			newVerUUID:  "",
			wantVerUUID: "old-ver",
		},
		{
			name:        "new UUID used when old is empty",
			oldPkgUUID:  "",
			newPkgUUID:  "new-pkg",
			wantPkgUUID: "new-pkg",
			oldVerUUID:  "",
			newVerUUID:  "new-ver",
			wantVerUUID: "new-ver",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			old := artifactOutput{HarPackageUUID: tt.oldPkgUUID, HarVersionUUID: tt.oldVerUUID}
			newOut := artifactOutput{HarPackageUUID: tt.newPkgUUID, HarVersionUUID: tt.newVerUUID}
			merged := mergeArtifactOutputs(old, newOut)
			if merged.HarPackageUUID != tt.wantPkgUUID {
				t.Errorf("HarPackageUUID = %q, want %q", merged.HarPackageUUID, tt.wantPkgUUID)
			}
			if merged.HarVersionUUID != tt.wantVerUUID {
				t.Errorf("HarVersionUUID = %q, want %q", merged.HarVersionUUID, tt.wantVerUUID)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// readArtifactOutput / writeArtifactOutput
// ---------------------------------------------------------------------------

func TestReadWriteArtifactOutput_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	outPath := filepath.Join(dir, "artifact.json")
	t.Setenv(envArtifactFilePath, outPath)

	output := artifactOutput{
		ArtifactName:   "my-art",
		Version:        "v42",
		Registry:       "reg",
		HarPackageUUID: "pkg-uuid",
		HarVersionUUID: "ver-uuid",
		FileCount:      1,
		TotalBytes:     512,
		Files: []pushedFile{
			{Name: "f.zip", Destination: "my-art/v42/f.zip", Size: 512, SHA256: "abc123"},
		},
	}

	if err := writeArtifactOutput(output); err != nil {
		t.Fatalf("writeArtifactOutput: %v", err)
	}

	got, err := readArtifactOutput(outPath)
	if err != nil {
		t.Fatalf("readArtifactOutput: %v", err)
	}

	if got.ArtifactName != output.ArtifactName {
		t.Errorf("ArtifactName = %q, want %q", got.ArtifactName, output.ArtifactName)
	}
	if got.HarPackageUUID != output.HarPackageUUID {
		t.Errorf("HarPackageUUID = %q, want %q", got.HarPackageUUID, output.HarPackageUUID)
	}
	if len(got.Files) != 1 || got.Files[0].SHA256 != "abc123" {
		t.Errorf("unexpected Files: %+v", got.Files)
	}
}

func TestWriteArtifactOutput_NoEnvVar(t *testing.T) {
	t.Setenv(envArtifactFilePath, "")
	// Should be a no-op — no file should be created, no error returned.
	if err := writeArtifactOutput(artifactOutput{ArtifactName: "art"}); err != nil {
		t.Errorf("expected no error when env var is unset, got: %v", err)
	}
}

func TestWriteArtifactOutput_CreatesParentDir(t *testing.T) {
	dir := t.TempDir()
	outPath := filepath.Join(dir, "nested", "sub", "artifact.json")
	t.Setenv(envArtifactFilePath, outPath)

	if err := writeArtifactOutput(artifactOutput{ArtifactName: "art"}); err != nil {
		t.Fatalf("writeArtifactOutput: %v", err)
	}
	if _, err := os.Stat(outPath); err != nil {
		t.Errorf("expected output file to exist at %q: %v", outPath, err)
	}
}

func TestWriteArtifactOutput_MergesExisting(t *testing.T) {
	dir := t.TempDir()
	outPath := filepath.Join(dir, "artifact.json")
	t.Setenv(envArtifactFilePath, outPath)

	first := artifactOutput{
		ArtifactName: "art",
		Version:      "v1",
		Registry:     "reg",
		Files:        []pushedFile{{Destination: "art/v1/a.zip", Size: 100}},
	}
	if err := writeArtifactOutput(first); err != nil {
		t.Fatalf("first write: %v", err)
	}

	second := artifactOutput{
		ArtifactName: "art",
		Version:      "v1",
		Registry:     "reg",
		Files:        []pushedFile{{Destination: "art/v1/b.zip", Size: 200}},
	}
	if err := writeArtifactOutput(second); err != nil {
		t.Fatalf("second write: %v", err)
	}

	final, err := readArtifactOutput(outPath)
	if err != nil {
		t.Fatalf("readArtifactOutput: %v", err)
	}
	if final.FileCount != 2 {
		t.Errorf("FileCount = %d, want 2 after merge", final.FileCount)
	}
	if final.TotalBytes != 300 {
		t.Errorf("TotalBytes = %d, want 300 after merge", final.TotalBytes)
	}
}

// TestWriteArtifactOutput_ConcurrentWriters is a regression test for the
// flock-serialized read-merge-write cycle: without the lock, concurrent
// writers racing to read-then-write outPath can clobber each other's
// results, silently dropping files from the final summary.
func TestWriteArtifactOutput_ConcurrentWriters(t *testing.T) {
	dir := t.TempDir()
	outPath := filepath.Join(dir, "artifact.json")
	t.Setenv(envArtifactFilePath, outPath)

	const n = 20
	var wg sync.WaitGroup
	wg.Add(n)
	errCh := make(chan error, n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			out := artifactOutput{
				ArtifactName: "art",
				Version:      "v1",
				Registry:     "reg",
				Files: []pushedFile{
					{Destination: fmt.Sprintf("art/v1/f%d.zip", i), Size: int64(i + 1)},
				},
			}
			errCh <- writeArtifactOutput(out)
		}(i)
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			t.Fatalf("writeArtifactOutput: %v", err)
		}
	}

	final, err := readArtifactOutput(outPath)
	if err != nil {
		t.Fatalf("readArtifactOutput: %v", err)
	}
	if final.FileCount != n {
		t.Fatalf("FileCount = %d, want %d — a writer's result was lost (lock not serializing writes)", final.FileCount, n)
	}
	var wantTotal int64
	for i := 0; i < n; i++ {
		wantTotal += int64(i + 1)
	}
	if final.TotalBytes != wantTotal {
		t.Errorf("TotalBytes = %d, want %d", final.TotalBytes, wantTotal)
	}
}

func TestReadArtifactOutput_InvalidJSON(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(f, []byte("{not json}"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	_, err := readArtifactOutput(f)
	if err == nil {
		t.Fatal("expected error for invalid JSON, got nil")
	}
}

func TestReadArtifactOutput_MissingFile(t *testing.T) {
	_, err := readArtifactOutput("/nonexistent/artifact.json")
	if err == nil {
		t.Fatal("expected error for missing file, got nil")
	}
}
