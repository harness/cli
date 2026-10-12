// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package ci

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
			compiled, err := coverageCompileExcludePatterns([]string{tt.pattern})
			if err != nil {
				t.Fatalf("coverageCompileExcludePatterns() error = %v", err)
			}
			if got := coverageMatchesAnyExcludePattern(tt.path, compiled); got != tt.want {
				t.Errorf("MatchesAnyExcludePattern(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}

func TestCoverageCompileExcludePatterns_InvalidRegexErrors(t *testing.T) {
	if _, err := coverageCompileExcludePatterns([]string{"regex:("}); err == nil {
		t.Fatal("error = nil, want error for invalid regex")
	}
}

func TestCoverageCompileExcludePatterns_InvalidGlobErrors(t *testing.T) {
	if _, err := coverageCompileExcludePatterns([]string{"["}); err == nil {
		t.Fatal("error = nil, want error for invalid glob")
	}
}

func TestCoverageCompileExcludePatterns_NilAndEmptyList(t *testing.T) {
	compiled, err := coverageCompileExcludePatterns(nil)
	if err != nil || len(compiled) != 0 {
		t.Fatalf("coverageCompileExcludePatterns(nil) = (%v, %v), want (empty, nil)", compiled, err)
	}
	compiled, err = coverageCompileExcludePatterns([]string{})
	if err != nil || len(compiled) != 0 {
		t.Fatalf("coverageCompileExcludePatterns([]) = (%v, %v), want (empty, nil)", compiled, err)
	}
}

func TestCoverageCompileExcludePatterns_EmptyRegexExpressionMatchesEverything(t *testing.T) {
	compiled, err := coverageCompileExcludePatterns([]string{"regex:"})
	if err != nil {
		t.Fatalf("coverageCompileExcludePatterns() error = %v", err)
	}
	if !coverageMatchesAnyExcludePattern("anything.go", compiled) {
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
			compiled, err := coverageCompileExcludePatterns([]string{tt.pattern})
			if err != nil {
				t.Fatalf("coverageCompileExcludePatterns() error = %v", err)
			}
			if got := coverageMatchesAnyExcludePattern(tt.path, compiled); got != tt.want {
				t.Errorf("MatchesAnyExcludePattern(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}

func TestCoverageMatchesAnyExcludePattern_ShortCircuitsOnFirstMatch(t *testing.T) {
	compiled, err := coverageCompileExcludePatterns([]string{"**/vendor/**", "regex:^generated_"})
	if err != nil {
		t.Fatalf("coverageCompileExcludePatterns() error = %v", err)
	}
	if !coverageMatchesAnyExcludePattern("pkg/vendor/foo.go", compiled) {
		t.Error("want match via first (glob) pattern")
	}
	if !coverageMatchesAnyExcludePattern("generated_foo.go", compiled) {
		t.Error("want match via second (regex) pattern")
	}
	if coverageMatchesAnyExcludePattern("pkg/foo.go", compiled) {
		t.Error("want no match when neither pattern applies")
	}
}

func TestCoverageLogExcludedFileOnce_DedupesByPath(t *testing.T) {
	seen := map[string]struct{}{}
	coverageLogExcludedFileOnce(seen, "pkg/foo.go")
	coverageLogExcludedFileOnce(seen, "pkg/foo.go")
	if len(seen) != 1 {
		t.Fatalf("seen = %v, want exactly one entry after logging the same path twice", seen)
	}
	coverageLogExcludedFileOnce(seen, "pkg/bar.go")
	if len(seen) != 2 {
		t.Fatalf("seen = %v, want two entries after logging a second distinct path", seen)
	}
}

func TestCoverageLogExcludedFileOnce_NilSeenMapDoesNotPanic(t *testing.T) {
	coverageLogExcludedFileOnce(nil, "pkg/foo.go")
}

func coverageStringPointer(value string) *string {
	return &value
}

func TestCoverageParseExcludeText(t *testing.T) {
	text := "  # generated files\n\n vendor/** \nregex:.*_gen\\.go$\n"
	want := []string{"vendor/**", "regex:.*_gen\\.go$"}
	got := coverageParseExcludeText(text)
	if len(got) != len(want) {
		t.Fatalf("coverageParseExcludeText() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("coverageParseExcludeText() = %v, want %v", got, want)
		}
	}
}

func TestCoveragePatternsFromSettings(t *testing.T) {
	cli := []string{"cli/**"}
	serverText := "# generated\nserver/**\nregex:.*_gen\\.go$"
	got, err := coveragePatternsFromSettings(&serverText, cli)
	if err != nil {
		t.Fatalf("configured settings error = %v", err)
	}
	if len(got) != 2 ||
		!coverageMatchesAnyExcludePattern("server/file.go", got) ||
		!coverageMatchesAnyExcludePattern("src/file_gen.go", got) ||
		coverageMatchesAnyExcludePattern("cli/file.go", got) {
		t.Errorf("configured settings did not use only server patterns, got %+v", got)
	}

	got, err = coveragePatternsFromSettings(nil, cli)
	if err != nil {
		t.Fatalf("unset settings error = %v", err)
	}
	if len(got) != 1 || !coverageMatchesAnyExcludePattern("cli/file.go", got) {
		t.Errorf("unset settings did not use CLI patterns, got %+v", got)
	}
}

func TestCoveragePatternsFromSettings_RejectsUncompilablePatterns(t *testing.T) {
	cli := []string{"cli/**"}
	for _, serverText := range []string{"regex:[unclosed", "[unclosed", "server/**\nregex:[unclosed"} {
		got, err := coveragePatternsFromSettings(&serverText, cli)
		if err == nil {
			t.Fatalf("coveragePatternsFromSettings(%q) error = nil, want invalid settings error", serverText)
		}
		if !strings.Contains(err.Error(), "invalid coverage repository exclude settings") {
			t.Fatalf("coveragePatternsFromSettings(%q) error = %v, want invalid coverage repository exclude settings", serverText, err)
		}
		if got != nil {
			t.Errorf("coveragePatternsFromSettings(%q) = %v, want nil patterns", serverText, got)
		}
	}
}

func TestCoveragePatternsFromSettings_ClearedTextIgnoresCLI(t *testing.T) {
	cli := []string{"cli/**"}
	for _, text := range []string{"", "# no exclusions\n\n"} {
		got, err := coveragePatternsFromSettings(coverageStringPointer(text), cli)
		if err != nil {
			t.Fatalf("coveragePatternsFromSettings(%q) error = %v", text, err)
		}
		if len(got) != 0 {
			t.Errorf("coveragePatternsFromSettings(%q) = %v, want no patterns", text, got)
		}
	}
}

func TestCoverageCompileCLIExcludePatterns_Empty(t *testing.T) {
	got, err := coverageCompileCLIExcludePatterns(nil)
	if err != nil {
		t.Fatalf("coverageCompileCLIExcludePatterns(nil) error = %v", err)
	}
	if got != nil {
		t.Errorf("coverageCompileCLIExcludePatterns(nil) = %v, want nil", got)
	}
}
