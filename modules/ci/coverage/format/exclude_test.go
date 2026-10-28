// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package format

import (
	"strings"
	"testing"
)

func TestCoverageCompileExcludePatterns(t *testing.T) {
	tests := []struct {
		name    string
		pattern string
		path    string
		want    bool
	}{
		{"glob match", "**/vendor/**", "pkg/vendor/foo.go", true},
		{"glob no match", "**/vendor/**", "pkg/foo.go", false},
		{"regex prefix match", "regex:^generated_.*\\.go$", "generated_foo.go", true},
		{"regex prefix no match", "regex:^generated_.*\\.go$", "foo.go", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			compiled, err := compileExcludePatterns([]string{tt.pattern})
			if err != nil {
				t.Fatalf("compileExcludePatterns() error = %v", err)
			}
			if got := matchesAnyExcludePattern(tt.path, compiled); got != tt.want {
				t.Errorf("MatchesAnyExcludePattern(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}

func TestCoverageCompileExcludePatterns_InvalidRegexErrors(t *testing.T) {
	if _, err := compileExcludePatterns([]string{"regex:("}); err == nil {
		t.Fatal("error = nil, want error for invalid regex")
	}
}

func TestCoverageCompileExcludePatterns_InvalidGlobErrors(t *testing.T) {
	if _, err := compileExcludePatterns([]string{"["}); err == nil {
		t.Fatal("error = nil, want error for invalid glob")
	}
}

func TestCoverageCompileExcludePatterns_NilAndEmptyList(t *testing.T) {
	compiled, err := compileExcludePatterns(nil)
	if err != nil || len(compiled) != 0 {
		t.Fatalf("compileExcludePatterns(nil) = (%v, %v), want (empty, nil)", compiled, err)
	}
	compiled, err = compileExcludePatterns([]string{})
	if err != nil || len(compiled) != 0 {
		t.Fatalf("compileExcludePatterns([]) = (%v, %v), want (empty, nil)", compiled, err)
	}
}

func TestCoverageCompileExcludePatterns_EmptyRegexExpressionMatchesEverything(t *testing.T) {
	compiled, err := compileExcludePatterns([]string{"regex:"})
	if err != nil {
		t.Fatalf("compileExcludePatterns() error = %v", err)
	}
	if !matchesAnyExcludePattern("anything.go", compiled) {
		t.Fatal("empty regex expression should match any path")
	}
}

func TestCoverageMatchesAnyExcludePattern(t *testing.T) {
	tests := []struct {
		name    string
		pattern string
		path    string
		want    bool
	}{
		{"single star does not cross directories", "*.go", "pkg/foo.go", false},
		{"single star matches within one segment", "*.go", "foo.go", true},
		{"question mark matches single char", "fo?.go", "foo.go", true},
		{"question mark does not match multiple chars", "fo?.go", "fooo.go", false},
		{"regex anchored prefix excludes mid-string match", "regex:^foo", "barfoo.go", false},
		{"regex anchored prefix matches start", "regex:^foo", "foo.go", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			compiled, err := compileExcludePatterns([]string{tt.pattern})
			if err != nil {
				t.Fatalf("compileExcludePatterns() error = %v", err)
			}
			if got := matchesAnyExcludePattern(tt.path, compiled); got != tt.want {
				t.Errorf("MatchesAnyExcludePattern(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}

func TestCoverageMatchesAnyExcludePattern_ShortCircuitsOnFirstMatch(t *testing.T) {
	compiled, err := compileExcludePatterns([]string{"**/vendor/**", "regex:^generated_"})
	if err != nil {
		t.Fatalf("compileExcludePatterns() error = %v", err)
	}
	if !matchesAnyExcludePattern("pkg/vendor/foo.go", compiled) {
		t.Error("want match via first (glob) pattern")
	}
	if !matchesAnyExcludePattern("generated_foo.go", compiled) {
		t.Error("want match via second (regex) pattern")
	}
	if matchesAnyExcludePattern("pkg/foo.go", compiled) {
		t.Error("want no match when neither pattern applies")
	}
}

func TestCoverageLogExcludedFileOnce_DedupesByPath(t *testing.T) {
	seen := map[string]struct{}{}
	logExcludedFileOnce(seen, "pkg/foo.go")
	logExcludedFileOnce(seen, "pkg/foo.go")
	if len(seen) != 1 {
		t.Fatalf("seen = %v, want exactly one entry after logging the same path twice", seen)
	}
	logExcludedFileOnce(seen, "pkg/bar.go")
	if len(seen) != 2 {
		t.Fatalf("seen = %v, want two entries after logging a second distinct path", seen)
	}
}

func TestCoverageLogExcludedFileOnce_NilSeenMapDoesNotPanic(t *testing.T) {
	logExcludedFileOnce(nil, "pkg/foo.go")
}

func stringPointer(value string) *string {
	return &value
}

func TestCoverageParseExcludeText(t *testing.T) {
	text := "  # generated files\n\n vendor/** \nregex:.*_gen\\.go$\n"
	want := []string{"vendor/**", "regex:.*_gen\\.go$"}
	got := parseExcludeText(text)
	if len(got) != len(want) {
		t.Fatalf("parseExcludeText() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("parseExcludeText() = %v, want %v", got, want)
		}
	}
}

func TestCoveragePatternsFromSettings(t *testing.T) {
	cli := []string{"cli/**"}
	serverText := "# generated\nserver/**\nregex:.*_gen\\.go$"
	got, err := PatternsFromSettings(&serverText, cli)
	if err != nil {
		t.Fatalf("configured settings error = %v", err)
	}
	if len(got) != 2 ||
		!matchesAnyExcludePattern("server/file.go", got) ||
		!matchesAnyExcludePattern("src/file_gen.go", got) ||
		matchesAnyExcludePattern("cli/file.go", got) {
		t.Errorf("configured settings did not use only server patterns, got %+v", got)
	}

	got, err = PatternsFromSettings(nil, cli)
	if err != nil {
		t.Fatalf("unset settings error = %v", err)
	}
	if len(got) != 1 || !matchesAnyExcludePattern("cli/file.go", got) {
		t.Errorf("unset settings did not use CLI patterns, got %+v", got)
	}
}

func TestCoveragePatternsFromSettings_RejectsUncompilablePatterns(t *testing.T) {
	cli := []string{"cli/**"}
	for _, serverText := range []string{"regex:[unclosed", "[unclosed", "server/**\nregex:[unclosed"} {
		got, err := PatternsFromSettings(&serverText, cli)
		if err == nil {
			t.Fatalf("PatternsFromSettings(%q) error = nil, want invalid settings error", serverText)
		}
		if !strings.Contains(err.Error(), "invalid coverage repository exclude settings") {
			t.Fatalf("PatternsFromSettings(%q) error = %v, want invalid coverage repository exclude settings", serverText, err)
		}
		if got != nil {
			t.Errorf("PatternsFromSettings(%q) = %v, want nil patterns", serverText, got)
		}
	}
}

func TestCoveragePatternsFromSettings_ClearedTextIgnoresCLI(t *testing.T) {
	cli := []string{"cli/**"}
	for _, text := range []string{"", "# no exclusions\n\n"} {
		got, err := PatternsFromSettings(stringPointer(text), cli)
		if err != nil {
			t.Fatalf("PatternsFromSettings(%q) error = %v", text, err)
		}
		if len(got) != 0 {
			t.Errorf("PatternsFromSettings(%q) = %v, want no patterns", text, got)
		}
	}
}

func TestCoverageCompileCLIExcludePatterns_Empty(t *testing.T) {
	got, err := CompileCLIExcludePatterns(nil)
	if err != nil {
		t.Fatalf("CompileCLIExcludePatterns(nil) error = %v", err)
	}
	if got != nil {
		t.Errorf("CompileCLIExcludePatterns(nil) = %v, want nil", got)
	}
}
