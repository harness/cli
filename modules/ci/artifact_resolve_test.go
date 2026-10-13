// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package ci

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func TestContainsPathPattern(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"file.txt", false},
		{"path/to/file.txt", false},
		{"nospecialchars", false},
		{"*.txt", true},
		{"file?.log", true},
		{"file[0-9].txt", true},
		{"(group).txt", true},
		{"a|b.txt", true},
		{"a+.txt", true},
		{`a\b`, true},
		{"./dist/output.zip", false},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := containsPathPattern(tt.input)
			if got != tt.want {
				t.Errorf("containsPathPattern(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestFileEntry(t *testing.T) {
	tests := []struct {
		name         string
		absPath      string
		size         int64
		artifactName string
		version      string
		destination  string
		wantDestPath string
	}{
		{
			name:         "no destination",
			absPath:      "/tmp/build/app.zip",
			size:         1024,
			artifactName: "my-artifact",
			version:      "v1",
			destination:  "",
			wantDestPath: "/my-artifact/v1/app.zip",
		},
		{
			name:         "with destination",
			absPath:      "/tmp/build/app.zip",
			size:         2048,
			artifactName: "my-artifact",
			version:      "v2",
			destination:  "dist",
			wantDestPath: "/my-artifact/v2/dist/app.zip",
		},
		{
			name:         "nested destination path",
			absPath:      "/home/user/reports/coverage.xml",
			size:         512,
			artifactName: "pipeline-x",
			version:      "v42",
			destination:  "reports/xml",
			wantDestPath: "/pipeline-x/v42/reports/xml/coverage.xml",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := fileEntry(tt.absPath, tt.size, tt.artifactName, tt.version, tt.destination)
			if e.localPath != tt.absPath {
				t.Errorf("localPath = %q, want %q", e.localPath, tt.absPath)
			}
			if e.destPath != tt.wantDestPath {
				t.Errorf("destPath = %q, want %q", e.destPath, tt.wantDestPath)
			}
			if e.size != tt.size {
				t.Errorf("size = %d, want %d", e.size, tt.size)
			}
		})
	}
}

func TestResolveFilePath_ExactFile(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "file.txt")
	writeTestFile(t, f, "content")

	paths, err := resolveFilePath(f)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(paths) != 1 || paths[0] != f {
		t.Errorf("got %v, want [%q]", paths, f)
	}
}

func TestResolveFilePath_NonExistentExact(t *testing.T) {
	_, err := resolveFilePath("/nonexistent/path/file.txt")
	if err == nil {
		t.Fatal("expected error for non-existent file, got nil")
	}
}

func TestResolveFilePath_GlobPattern(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "a.zip"), "a")
	writeTestFile(t, filepath.Join(dir, "b.zip"), "b")
	writeTestFile(t, filepath.Join(dir, "c.txt"), "c")

	paths, err := resolveFilePath(filepath.Join(dir, "*.zip"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(paths) != 2 {
		t.Errorf("expected 2 matches, got %d: %v", len(paths), paths)
	}
}

func TestResolveFilePath_GlobNoMatch(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "a.txt"), "a")

	// Pattern with no glob match falls back to regex; regex matches no file
	// either, so we expect an error.
	_, err := resolveFilePath(filepath.Join(dir, "*.zip"))
	if err == nil {
		t.Fatal("expected error when no files match glob/regex, got nil")
	}
}

func TestResolveFilePath_RegexFallback(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "report-123.xml"), "r1")
	writeTestFile(t, filepath.Join(dir, "report-abc.xml"), "r2")
	writeTestFile(t, filepath.Join(dir, "other.txt"), "o")

	// "report-[0-9]+.xml" is not a glob pattern Go's filepath.Glob can match
	// (Go glob doesn't support "+" quantifiers), so this exercises the regex
	// fallback path, not the glob path.
	paths, err := resolveFilePath(filepath.Join(dir, `report-[0-9]+\.xml`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(paths) != 1 || filepath.Base(paths[0]) != "report-123.xml" {
		t.Errorf("got %v, want exactly [report-123.xml]", paths)
	}
}

func TestWalkDir_BasicTree(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "a.txt"), "a")
	writeTestFile(t, filepath.Join(dir, "sub", "b.txt"), "b")
	writeTestFile(t, filepath.Join(dir, "sub", "nested", "c.txt"), "c")
	writeTestFile(t, filepath.Join(dir, ".hidden"), "hidden")

	entries, err := walkDir(dir, "art", "v1", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(entries) != 4 {
		t.Fatalf("expected 4 entries (including hidden), got %d", len(entries))
	}

	dirBase := filepath.Base(dir)
	for _, e := range entries {
		prefix := "/" + "art" + "/" + "v1" + "/" + dirBase + "/"
		if !strings.HasPrefix(e.destPath, prefix) {
			t.Errorf("destPath %q should start with %q", e.destPath, prefix)
		}
	}
}

func TestWalkDir_WithDestination(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "file.zip"), "data")

	entries, err := walkDir(dir, "art", "v1", "cache")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if !strings.Contains(entries[0].destPath, "/cache/") {
		t.Errorf("destPath %q should contain '/cache/'", entries[0].destPath)
	}
}

func TestWalkDir_EmptyDir(t *testing.T) {
	dir := t.TempDir()
	entries, err := walkDir(dir, "art", "v1", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("expected 0 entries for empty dir, got %d", len(entries))
	}
}

func TestCollectEntries_SingleFile(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "app.zip")
	writeTestFile(t, f, "binary")

	entries, err := collectEntries([]string{f}, "art", "v1", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].size != int64(len("binary")) {
		t.Errorf("size = %d, want %d", entries[0].size, len("binary"))
	}
}

func TestCollectEntries_Directory(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "a.txt"), "aaa")
	writeTestFile(t, filepath.Join(dir, "b.txt"), "bbb")

	entries, err := collectEntries([]string{dir}, "art", "v1", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
}

func TestCollectEntries_GlobPattern(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "report1.xml"), "r1")
	writeTestFile(t, filepath.Join(dir, "report2.xml"), "r2")
	writeTestFile(t, filepath.Join(dir, "skip.json"), "j")

	entries, err := collectEntries([]string{filepath.Join(dir, "*.xml")}, "art", "v1", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d: %v", len(entries), entries)
	}
	var names []string
	for _, e := range entries {
		names = append(names, filepath.Base(e.localPath))
	}
	sort.Strings(names)
	if names[0] != "report1.xml" || names[1] != "report2.xml" {
		t.Errorf("unexpected file names: %v", names)
	}
}

func TestCollectEntries_MultiplePatterns(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "a.zip"), "a")
	writeTestFile(t, filepath.Join(dir, "b.txt"), "b")

	entries, err := collectEntries(
		[]string{filepath.Join(dir, "*.zip"), filepath.Join(dir, "*.txt")},
		"art", "v1", "",
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries across both patterns, got %d: %v", len(entries), entries)
	}
}

func TestCollectEntries_NonExistentFile(t *testing.T) {
	_, err := collectEntries([]string{"/does/not/exist.zip"}, "art", "v1", "")
	if err == nil {
		t.Fatal("expected error for non-existent file, got nil")
	}
}

func TestCollectEntries_DestinationPropagated(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "output.tar.gz")
	writeTestFile(t, f, "tarball")

	entries, err := collectEntries([]string{f}, "art", "v1", "results")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if !strings.Contains(entries[0].destPath, "/results/") {
		t.Errorf("destPath %q should contain '/results/'", entries[0].destPath)
	}
}

func TestWalkDir_RejectsSymlinkEscape(t *testing.T) {
	if runtimeIsWindows() {
		t.Skip("symlinks require elevated privileges on Windows")
	}
	dir := t.TempDir()
	outside := t.TempDir()
	writeTestFile(t, filepath.Join(outside, "secret.txt"), "s")

	// A symlink inside dir pointing outside it must not be followed into an
	// escaping relative path — WalkDir doesn't follow symlinks by default,
	// and the entry is skipped because it isn't a regular file.
	if err := os.Symlink(filepath.Join(outside, "secret.txt"), filepath.Join(dir, "link.txt")); err != nil {
		t.Fatalf("Symlink: %v", err)
	}

	entries, err := walkDir(dir, "art", "v1", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("expected symlink to be skipped, got %d entries: %v", len(entries), entries)
	}
}

func runtimeIsWindows() bool {
	return os.PathSeparator == '\\'
}

// writeTestFile is a test helper that creates a file (and parent dirs) with the given content.
func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
}
