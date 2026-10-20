// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package ci

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestCoverageParseDiff_MultiFileMultiHunk(t *testing.T) {
	diff := `diff --git a/foo.go b/foo.go
index 0000000..1111111 100644
--- a/foo.go
+++ b/foo.go
@@ -1,0 +2,3 @@
+line2
+line3
+line4
@@ -10 +12 @@
-old
+new
diff --git a/bar.go b/bar.go
index 0000000..2222222 100644
--- /dev/null
+++ b/bar.go
@@ -0,0 +1 @@
+hello
`
	result, err := coverageParseDiff(diff)
	if err != nil {
		t.Fatalf("coverageParseDiff() error = %v", err)
	}
	wantFoo := map[uint32]bool{2: true, 3: true, 4: true, 12: true}
	for line, want := range wantFoo {
		if result["foo.go"][line] != want {
			t.Errorf("foo.go line %d = %v, want %v", line, result["foo.go"][line], want)
		}
	}
	if len(result["foo.go"]) != len(wantFoo) {
		t.Errorf("foo.go changed lines = %v, want %v", result["foo.go"], wantFoo)
	}
	if !result["bar.go"][1] {
		t.Errorf("bar.go line 1 not marked changed")
	}
}

func TestCoverageParseDiff_EmptyDiffReturnsEmptyMap(t *testing.T) {
	result, err := coverageParseDiff("")
	if err != nil {
		t.Fatalf("coverageParseDiff() error = %v", err)
	}
	if len(result) != 0 {
		t.Fatalf("result = %v, want empty map for an empty diff", result)
	}
}

func TestCoverageParseDiff_EqualCountHunk(t *testing.T) {
	diff := `diff --git a/foo.go b/foo.go
index 0000000..1111111 100644
--- a/foo.go
+++ b/foo.go
@@ -5,3 +5,3 @@
-a
-b
-c
+x
+y
+z
`
	result, err := coverageParseDiff(diff)
	if err != nil {
		t.Fatalf("coverageParseDiff() error = %v", err)
	}
	want := map[uint32]bool{5: true, 6: true, 7: true}
	for line, w := range want {
		if result["foo.go"][line] != w {
			t.Errorf("foo.go line %d = %v, want %v", line, result["foo.go"][line], w)
		}
	}
	if len(result["foo.go"]) != len(want) {
		t.Errorf("foo.go changed lines = %v, want %v", result["foo.go"], want)
	}
}

func TestCoverageGetMergeBase_InvalidRefErrors(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	cmd := exec.Command("git", "init", "-b", "main")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}

	oldWD, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(oldWD) }()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}

	if _, err := coverageGetMergeBase("does-not-exist", "HEAD"); err == nil {
		t.Fatal("coverageGetMergeBase() error = nil, want error for a nonexistent ref")
	}
}

func TestCoverageChangedLines_SameRevisionShortCircuits(t *testing.T) {
	result, err := coverageChangedLines("abc123", "abc123")
	if err != nil {
		t.Fatalf("coverageChangedLines() error = %v", err)
	}
	if len(result) != 0 {
		t.Fatalf("result = %v, want empty map when base == head", result)
	}
}

func TestCoverageChangedLines_EmptyBaseRevIsError(t *testing.T) {
	if _, err := coverageChangedLines("", "HEAD"); err == nil {
		t.Fatal("coverageChangedLines() error = nil, want error for empty baseRev")
	}
}

func TestCoverageChangedLines_AgainstRealRepo(t *testing.T) {
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

	result, err := coverageChangedLines(string(trimNewline(baseSHA)), "HEAD")
	if err != nil {
		t.Fatalf("coverageChangedLines() error = %v", err)
	}
	if !result["foo.go"][5] {
		t.Fatalf("result = %v, want foo.go line 5 (func B) marked changed", result)
	}
}

func trimNewline(b []byte) []byte {
	for len(b) > 0 && (b[len(b)-1] == '\n' || b[len(b)-1] == '\r') {
		b = b[:len(b)-1]
	}
	return b
}
