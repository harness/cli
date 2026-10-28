// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package file

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/harness/cli/v3/modules/ci/coverage/format"
)

func chdir(t *testing.T, dir string) {
	t.Helper()
	orig, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd() error = %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("Chdir(%q) error = %v", dir, err)
	}
	t.Cleanup(func() { _ = os.Chdir(orig) })
}

func TestCoverageLookup_Phase1DirectFiles(t *testing.T) {
	tempDir := t.TempDir()
	chdir(t, tempDir)

	for _, f := range []string{"coverage.out", "lcov.info", "Dockerfile.coverage", "random.txt", "clover.xml"} {
		if err := os.WriteFile(filepath.Join(tempDir, f), []byte("dummy"), 0o644); err != nil {
			t.Fatalf("WriteFile(%q) error = %v", f, err)
		}
	}

	found, err := Lookup()
	if err != nil {
		t.Fatalf("Lookup() error = %v", err)
	}
	names := make([]string, len(found))
	for i, cf := range found {
		names[i] = filepath.Base(cf.Path)
	}
	for _, want := range []string{"coverage.out", "lcov.info", "clover.xml"} {
		if !slices.Contains(names, want) {
			t.Errorf("expected %s to be found, got: %+v", want, names)
		}
	}
	if slices.Contains(names, "Dockerfile.coverage") || slices.Contains(names, "random.txt") {
		t.Errorf("expected ignored/unmatched files to be excluded, got: %+v", names)
	}
}

func TestCoverageLookup_Phase2CommonDirectory(t *testing.T) {
	tempDir := t.TempDir()
	chdir(t, tempDir)

	dir := filepath.Join(tempDir, "coverage")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatalf("Mkdir() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "lcov.info"), []byte("dummy"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	found, err := Lookup()
	if err != nil {
		t.Fatalf("Lookup() error = %v", err)
	}
	if len(found) != 1 || !strings.Contains(found[0].Path, "lcov.info") {
		t.Errorf("expected exactly lcov.info to be found, got: %+v", found)
	}
}

func TestCoverageLookup_Phase2point5MultiModule(t *testing.T) {
	tempDir := t.TempDir()
	chdir(t, tempDir)

	// Not in searchDirs at the top level, but "module/coverage" is a
	// nested common dir inside a top-level subdirectory.
	nested := filepath.Join(tempDir, "module", "coverage")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(nested, "jacoco.xml"), []byte("dummy"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	found, err := Lookup()
	if err != nil {
		t.Fatalf("Lookup() error = %v", err)
	}
	if len(found) != 1 || !strings.Contains(found[0].Path, "jacoco.xml") {
		t.Errorf("expected jacoco.xml to be found via multi-module search, got: %+v", found)
	}
}

func TestCoverageLookup_Phase3RecursiveFallback(t *testing.T) {
	tempDir := t.TempDir()
	chdir(t, tempDir)

	nestedDir := filepath.Join(tempDir, "src", "main", "java", "reports")
	if err := os.MkdirAll(nestedDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(nestedDir, "jacoco.xml"), []byte("dummy"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	found, err := Lookup()
	if err != nil {
		t.Fatalf("Lookup() error = %v", err)
	}
	if len(found) != 1 || !strings.Contains(found[0].Path, "jacoco.xml") {
		t.Errorf("expected jacoco.xml to be found via recursive fallback, got: %+v", found)
	}
}

func TestCoverageLookup_RecursiveFallbackSkipsSkipDirs(t *testing.T) {
	tempDir := t.TempDir()
	chdir(t, tempDir)

	skipped := filepath.Join(tempDir, "node_modules", "pkg")
	if err := os.MkdirAll(skipped, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(skipped, "jacoco.xml"), []byte("dummy"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	_, err := Lookup()
	if err == nil || err.Error() != "no coverage files found" {
		t.Errorf("expected a report inside node_modules to be skipped, got err = %v", err)
	}
}

func TestCoverageLookup_NoFilesFound(t *testing.T) {
	chdir(t, t.TempDir())

	_, err := Lookup()
	if err == nil || err.Error() != "no coverage files found" {
		t.Errorf("Lookup() error = %v, want \"no coverage files found\"", err)
	}
}

func TestCoverageLookup_IgnoredFileIsSkipped(t *testing.T) {
	tempDir := t.TempDir()
	chdir(t, tempDir)

	if err := os.WriteFile(filepath.Join(tempDir, "Dockerfile.coverage"), []byte("dummy"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	_, err := Lookup()
	if err == nil || err.Error() != "no coverage files found" {
		t.Errorf("expected the ignored file to be skipped, got err = %v", err)
	}
}

func TestCoverageLookup_StrictValidation(t *testing.T) {
	tempDir := t.TempDir()

	existingFile := filepath.Join(tempDir, "coverage.out")
	if err := os.WriteFile(existingFile, []byte("mode: set"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	missingFile := filepath.Join(tempDir, "missing.out")

	if _, err := Lookup(missingFile); err == nil || !strings.Contains(err.Error(), "coverage files not found") {
		t.Errorf("Lookup(missing) error = %v, want \"coverage files not found\"", err)
	}

	if _, err := Lookup(existingFile, missingFile); err == nil || !strings.Contains(err.Error(), "coverage files not found") {
		t.Errorf("Lookup(mix) error = %v, want \"coverage files not found\"", err)
	}

	found, err := Lookup(existingFile)
	if err != nil {
		t.Fatalf("Lookup(existing) error = %v", err)
	}
	if len(found) != 1 || found[0].Path != existingFile {
		t.Errorf("Lookup(existing) = %+v, want exactly %q", found, existingFile)
	}

	unknownFile := filepath.Join(tempDir, "unknown.xyz")
	if err := os.WriteFile(unknownFile, []byte("data"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if _, err := Lookup(unknownFile); err == nil || !strings.Contains(err.Error(), "no valid coverage files found") {
		t.Errorf("Lookup(unknown) error = %v, want \"no valid coverage files found\"", err)
	}
}

func TestCoverageLookup_CoberturaAndCoverallsPatterns(t *testing.T) {
	tests := []struct {
		filename string
		format   string
	}{
		{"coverage.xml", format.FormatCobertura},
		{"cobertura.xml", format.FormatCobertura},
		{"scoverage.xml", format.FormatCobertura},
		{"coveralls.json", format.FormatCoveralls},
	}
	for _, tt := range tests {
		t.Run(tt.filename, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, tt.filename)
			if err := os.WriteFile(path, []byte("dummy"), 0o644); err != nil {
				t.Fatalf("WriteFile() error = %v", err)
			}
			found, err := Lookup(path)
			if err != nil {
				t.Fatalf("Lookup() error = %v", err)
			}
			if len(found) != 1 || found[0].Format != tt.format {
				t.Errorf("found = %+v, want a single %q entry", found, tt.format)
			}
		})
	}
}

func TestCoverageLookup_TIRunnerAlongsideCoverageFile(t *testing.T) {
	dir := t.TempDir()
	coverPath := filepath.Join(dir, "coverage.out")
	tiPath := filepath.Join(dir, "_tiRunner.json")
	if err := os.WriteFile(coverPath, []byte("mode: atomic\n"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if err := os.WriteFile(tiPath, []byte("{}"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	found, err := Lookup(coverPath, tiPath)
	if err != nil {
		t.Fatalf("Lookup() error = %v", err)
	}
	if len(found) != 2 {
		t.Fatalf("found %d files, want 2: %+v", len(found), found)
	}
	formats := map[string]string{}
	for _, cf := range found {
		formats[filepath.Base(cf.Path)] = cf.Format
	}
	if formats["coverage.out"] != format.FormatGo {
		t.Errorf("coverage.out format = %q, want %q", formats["coverage.out"], format.FormatGo)
	}
	if formats["_tiRunner.json"] != format.FormatTIRunner {
		t.Errorf("_tiRunner.json format = %q, want %q", formats["_tiRunner.json"], format.FormatTIRunner)
	}
}

func TestCoverageLookup_TIRunnerAutoDiscovered(t *testing.T) {
	tempDir := t.TempDir()
	chdir(t, tempDir)

	if err := os.WriteFile(filepath.Join(tempDir, "_tiRunner.json"), []byte("{}"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	found, err := Lookup()
	if err != nil {
		t.Fatalf("Lookup() error = %v", err)
	}
	if len(found) != 1 || found[0].Format != format.FormatTIRunner {
		t.Errorf("found = %+v, want a single %q entry discovered without --file", found, format.FormatTIRunner)
	}
}

func TestCoverageLookup_TIRunnerByName(t *testing.T) {
	dir := t.TempDir()
	tests := []struct {
		name  string
		match bool
	}{
		{"_tiRunner.json", true},
		{"payments_tiRunner.json", true},
		{"module-a_tiRunner.json", true},
		{"per-test.json", false},
		{"output-1234.json", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(dir, tt.name)
			if err := os.WriteFile(path, []byte("{}"), 0o644); err != nil {
				t.Fatalf("WriteFile() error = %v", err)
			}
			found, err := Lookup(path)
			if !tt.match {
				if err == nil {
					t.Fatalf("expected %q to be rejected, got: %+v", tt.name, found)
				}
				return
			}
			if err != nil {
				t.Fatalf("Lookup() error = %v", err)
			}
			if len(found) != 1 || found[0].Format != format.FormatTIRunner {
				t.Errorf("found = %+v, want a single %q entry", found, format.FormatTIRunner)
			}
		})
	}
}
