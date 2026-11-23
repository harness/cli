// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package format

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/parquet-go/parquet-go"
)

func TestCoverageMergeLineType(t *testing.T) {
	tests := []struct {
		left, right LineType
		want        LineType
	}{
		{LineCovered, LineCovered, LineCovered},
		{LineUncovered, LineUncovered, LineUncovered},
		{LineCovered, LineUncovered, LinePartial},
		{LineUncovered, LineCovered, LinePartial},
		{LinePartial, LineCovered, LinePartial},
		{LineCovered, LinePartial, LinePartial},
	}
	for _, tt := range tests {
		if got := MergeLineType(tt.left, tt.right); got != tt.want {
			t.Errorf("MergeLineType(%v, %v) = %v, want %v", tt.left, tt.right, got, tt.want)
		}
	}
}

func TestCoverageWriteParquet_RoundTrip(t *testing.T) {
	input := `mode: set
example.com/pkg/file.go:3.1,3.10 1 1
example.com/pkg/file.go:4.1,4.10 1 0
`
	var buf bytes.Buffer
	result, err := Write("repo-ref", FormatGo, strings.NewReader(input), &buf)
	if err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if result.Format != formatParquet {
		t.Fatalf("result.Format = %q, want %q", result.Format, formatParquet)
	}

	reader := parquet.NewGenericReader[LineRecord](bytes.NewReader(buf.Bytes()))
	defer func() { _ = reader.Close() }()
	rows := make([]LineRecord, 2)
	n, err := reader.Read(rows)
	if n != 2 {
		t.Fatalf("read %d rows (err=%v), want 2", n, err)
	}
	want := map[uint32]LineType{3: LineCovered, 4: LineUncovered}
	for _, row := range rows {
		if row.RepoRef != "repo-ref" {
			t.Errorf("row.RepoRef = %q, want %q", row.RepoRef, "repo-ref")
		}
		if row.FilePath != "example.com/pkg/file.go" {
			t.Errorf("row.FilePath = %q", row.FilePath)
		}
		if lt, ok := want[row.LineNumber]; !ok || lt != row.LineType {
			t.Errorf("line %d LineType = %v, want %v", row.LineNumber, row.LineType, want[row.LineNumber])
		}
	}
}

func TestCoverageWriteParquet_PatchWithoutDiffBaseFails(t *testing.T) {
	input := "mode: set\nexample.com/pkg/file.go:3.1,3.10 1 1\n"
	var buf bytes.Buffer
	if _, err := Write("repo-ref", FormatGo, strings.NewReader(input), &buf, WithPatch(true)); err == nil {
		t.Fatal("Write() error = nil, want error: patch coverage requires a diff base")
	}
}

func TestCoverageWrite_UnsupportedFormatErrors(t *testing.T) {
	var buf bytes.Buffer
	if _, err := Write("repo-ref", "bogus-format", strings.NewReader(""), &buf); err == nil {
		t.Fatal("Write() error = nil, want error for an unsupported format")
	}
}

func TestCoverageWrite_WithExcludeDropsMatchingFile(t *testing.T) {
	input := `mode: set
pkg/kept.go:1.1,1.10 1 1
pkg/excluded.go:1.1,1.10 1 1
`
	var buf bytes.Buffer
	if _, err := Write("repo-ref", FormatGo, strings.NewReader(input), &buf, WithExclude([]string{"pkg/excluded.go"})); err != nil {
		t.Fatalf("Write() error = %v", err)
	}

	reader := parquet.NewGenericReader[LineRecord](bytes.NewReader(buf.Bytes()))
	defer func() { _ = reader.Close() }()
	rows := make([]LineRecord, 2)
	n, _ := reader.Read(rows)
	if n != 1 {
		t.Fatalf("read %d rows, want exactly 1 (the excluded file must be dropped)", n)
	}
	if rows[0].FilePath != "pkg/kept.go" {
		t.Fatalf("rows[0].FilePath = %q, want pkg/kept.go", rows[0].FilePath)
	}
}

func TestCoverageWrite_WithDiffAnnotatesChangedLines(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@test.com",
			"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@test.com",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	run("init", "-b", "main")
	if err := os.WriteFile(filepath.Join(dir, "foo.go"), []byte("package foo\n\nfunc A() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "foo.go")
	run("commit", "-m", "base")

	cmd := exec.Command("git", "rev-parse", "HEAD")
	cmd.Dir = dir
	baseSHA, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(dir, "foo.go"), []byte("package foo\n\nfunc A() {}\n\nfunc B() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "foo.go")
	run("commit", "-m", "add B")

	oldWD, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(oldWD) }()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}

	input := `mode: set
foo.go:3.1,3.10 1 1
foo.go:5.1,5.10 1 1
`
	var buf bytes.Buffer
	_, err = Write("repo-ref", FormatGo, strings.NewReader(input), &buf,
		WithDiff(string(trimNewline(baseSHA)), "HEAD"))
	if err != nil {
		t.Fatalf("Write() error = %v", err)
	}

	reader := parquet.NewGenericReader[LineRecord](bytes.NewReader(buf.Bytes()))
	defer func() { _ = reader.Close() }()
	rows := make([]LineRecord, 2)
	n, errRead := reader.Read(rows)
	if n != 2 {
		t.Fatalf("read %d rows (err=%v), want 2", n, errRead)
	}
	diffByLine := map[uint32]bool{}
	for _, row := range rows {
		diffByLine[row.LineNumber] = row.Diff
	}
	if diffByLine[3] {
		t.Error("line 3 Diff = true, want false (unchanged line)")
	}
	if !diffByLine[5] {
		t.Error("line 5 Diff = false, want true (introduced by the head commit)")
	}
}

func trimNewline(b []byte) []byte {
	for len(b) > 0 && (b[len(b)-1] == '\n' || b[len(b)-1] == '\r') {
		b = b[:len(b)-1]
	}
	return b
}
