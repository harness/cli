// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package analyze

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/harness/cli/v3/modules/ci/coverage/env"
	"github.com/harness/cli/v3/modules/ci/coverage/format"
	"github.com/harness/cli/v3/pkg/console"
)

func TestCoverageCalcPercentage(t *testing.T) {
	tests := []struct {
		name     string
		hits     int
		partials int
		misses   int
		want     float64
	}{
		{"no lines", 0, 0, 0, 0},
		{"all hits", 8, 0, 2, 80},
		{"partials excluded from numerator", 4, 4, 2, 40},
		{"all misses", 0, 0, 10, 0},
		{"all partials", 0, 10, 0, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := calcPercentage(tt.hits, tt.partials, tt.misses); got != tt.want {
				t.Errorf("calcPercentage(%d,%d,%d) = %v, want %v", tt.hits, tt.partials, tt.misses, got, tt.want)
			}
		})
	}
}

func TestCoverageGetPercentageColor(t *testing.T) {
	tests := []struct {
		name       string
		percentage float64
		want       console.Color
	}{
		{"high coverage", 85.0, console.ColorGreen},
		{"medium coverage", 60.0, console.ColorYellow},
		{"low coverage", 30.0, console.ColorRed},
		{"boundary 80", 80.0, console.ColorGreen},
		{"boundary 50", 50.0, console.ColorYellow},
		{"boundary 49.9", 49.9, console.ColorRed},
		{"full coverage", 100.0, console.ColorGreen},
		{"zero coverage", 0.0, console.ColorRed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := getPercentageColor(tt.percentage); got != tt.want {
				t.Errorf("getPercentageColor(%v) = %v, want %v", tt.percentage, got, tt.want)
			}
		})
	}
}

func TestCoverageAdjustLineCounts(t *testing.T) {
	stats := &fileStats{}

	adjustLineCounts(stats, format.LineCovered, 1, true)
	if stats.Hits != 1 || stats.PatchHits != 1 {
		t.Fatalf("after covered+patch: %+v, want Hits=1 PatchHits=1", stats)
	}

	adjustLineCounts(stats, format.LinePartial, 1, false)
	if stats.Partials != 1 || stats.PatchPartials != 0 {
		t.Fatalf("after partial, no patch: %+v, want Partials=1 PatchPartials=0", stats)
	}

	adjustLineCounts(stats, format.LineUncovered, 1, true)
	if stats.Misses != 1 || stats.PatchMisses != 1 {
		t.Fatalf("after uncovered+patch: %+v, want Misses=1 PatchMisses=1", stats)
	}

	// Decrementing (the merge-rebalance path) must undo exactly what was added.
	adjustLineCounts(stats, format.LineCovered, -1, true)
	if stats.Hits != 0 || stats.PatchHits != 0 {
		t.Fatalf("after decrementing covered+patch: %+v, want Hits=0 PatchHits=0", stats)
	}
}

func TestCoverageParseCoverageFileInto_MergesSameLineAcrossReports(t *testing.T) {
	accumulator := newCoverageAnalysisAccumulator()
	tree := env.NewTree([]string{"src/service.ts"})
	reports := []string{
		"TN:\nSF:src/service.ts\nDA:10,1\nend_of_record\n",
		"TN:\nSF:src/service.ts\nDA:10,0\nend_of_record\n",
	}
	for _, report := range reports {
		err := parseCoverageFileInto(
			format.FormatLCOV,
			strings.NewReader(report),
			".",
			tree,
			nil,
			false,
			nil,
			accumulator,
		)
		if err != nil {
			t.Fatalf("parseCoverageFileInto() error = %v", err)
		}
	}

	stats := accumulator.files["src/service.ts"]
	if stats == nil {
		t.Fatal("missing merged file statistics")
	}
	if stats.Hits != 0 || stats.Partials != 1 || stats.Misses != 0 {
		t.Fatalf("merged stats = hits:%d partials:%d misses:%d, want 0/1/0", stats.Hits, stats.Partials, stats.Misses)
	}
}

func TestCoveragePadRight(t *testing.T) {
	tests := []struct {
		name  string
		s     string
		width int
		want  string
	}{
		{"plain text needs padding", "foo", 10, "foo       "},
		{"plain text no padding needed", "hello world", 5, "hello world"},
		{"ANSI codes padded by visual width", "\x1b[31mhi\x1b[0m", 5, "\x1b[31mhi\x1b[0m   "},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := padRight(tt.s, tt.width); got != tt.want {
				t.Errorf("padRight(%q, %d) = %q, want %q", tt.s, tt.width, got, tt.want)
			}
		})
	}
}

// captureCoverageDisplayStats captures stdout produced by displayStats.
// NOTE: mutates os.Stdout — do not call from parallel subtests.
func captureCoverageDisplayStats(stats *analysisStats) string {
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	var wg sync.WaitGroup
	var buf bytes.Buffer
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, _ = io.Copy(&buf, r)
	}()

	displayStats(stats)

	_ = w.Close()
	os.Stdout = old
	wg.Wait()
	return buf.String()
}

func TestCoverageDisplayStats_PatchFilter(t *testing.T) {
	t.Run("patch mode: only files with patch lines shown", func(t *testing.T) {
		stats := &analysisStats{
			Hits: 90, Misses: 10, OverallPercentage: 90.0,
			HasPatchData: true, PatchHits: 5, PatchPercentage: 100.0,
			Files: []fileStats{
				{FilePath: "a.go", Hits: 80, Misses: 20, Percentage: 80.0, PatchHits: 5, PatchPercentage: 100.0},
				{FilePath: "b.go", Hits: 10, Percentage: 100.0},
			},
		}
		out := captureCoverageDisplayStats(stats)
		if !strings.Contains(out, "patch files only") {
			t.Errorf("expected heading to contain 'patch files only'\ngot: %q", out)
		}
		if !strings.Contains(out, "a.go") {
			t.Error("expected a.go (has patch lines) to appear in output")
		}
		if strings.Contains(out, "b.go") {
			t.Error("expected b.go (no patch lines) to be hidden from output")
		}
	})

	t.Run("patch mode: no files with patch lines shows message instead of table", func(t *testing.T) {
		stats := &analysisStats{
			Hits: 50, Misses: 50, OverallPercentage: 50.0,
			HasPatchData: true,
			Files:        []fileStats{{FilePath: "a.go", Hits: 50, Misses: 50, Percentage: 50.0}},
		}
		out := captureCoverageDisplayStats(stats)
		if !strings.Contains(out, "patch files only") {
			t.Error("expected heading to contain 'patch files only'")
		}
		if !strings.Contains(out, "No changed files with coverable lines found in diff") {
			t.Error("expected no-results message when no files have patch lines")
		}
		if strings.Contains(out, "a.go") {
			t.Error("expected a.go to be hidden (no patch lines)")
		}
		if strings.Contains(out, strings.Repeat("-", 90)) {
			t.Error("expected no table separators when there are no patch files")
		}
	})

	t.Run("non-patch mode: all files shown with original heading", func(t *testing.T) {
		stats := &analysisStats{
			Hits: 80, Misses: 20, OverallPercentage: 80.0,
			Files: []fileStats{
				{FilePath: "a.go", Hits: 80, Misses: 20, Percentage: 80.0},
				{FilePath: "b.go", Misses: 10},
			},
		}
		out := captureCoverageDisplayStats(stats)
		if strings.Contains(out, "patch files only") {
			t.Error("expected no 'patch files only' heading in non-patch mode")
		}
		if !strings.Contains(out, "a.go") || !strings.Contains(out, "b.go") {
			t.Error("expected both files to appear in non-patch mode")
		}
	})
}

func TestCoverageAnalyze_NoFilesFound(t *testing.T) {
	orig, _ := os.Getwd()
	t.Chdir(t.TempDir())
	t.Cleanup(func() { _ = os.Chdir(orig) })

	if _, err := analyze(nil, nil, false, "", ""); err == nil {
		t.Fatal("analyze() error = nil, want error when no coverage files are found")
	}
}

func TestCoverageAnalyze_PatchRequiresBase(t *testing.T) {
	dir := t.TempDir()
	coverFile := filepath.Join(dir, "coverage.out")
	if err := os.WriteFile(coverFile, []byte("mode: set\nexample.com/zzz_test_fixture.go:1.1,2.1 1 1\n"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	_, err := analyze([]string{coverFile}, nil, true, "", "")
	if err == nil || !strings.Contains(err.Error(), "--patch requires --base") {
		t.Fatalf("analyze() error = %v, want \"--patch requires --base\"", err)
	}
}

func TestCoverageAnalyze_ExcludeFiltersFiles(t *testing.T) {
	dir := t.TempDir()
	lcovFile := filepath.Join(dir, "lcov.info")
	content := "SF:zzz_test_fixture_keep.go\nDA:1,1\nend_of_record\n" +
		"SF:zzz_test_fixture_drop.go\nDA:1,1\nend_of_record\n"
	if err := os.WriteFile(lcovFile, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	stats, err := analyze([]string{lcovFile}, []string{"zzz_test_fixture_drop.go"}, false, "", "")
	if err != nil {
		t.Fatalf("analyze() error = %v", err)
	}

	if len(stats.Files) != 1 || stats.Files[0].FilePath != "zzz_test_fixture_keep.go" {
		t.Fatalf("stats.Files = %+v, want only zzz_test_fixture_keep.go", stats.Files)
	}
}

func TestCoverageAnalyze_MergesAcrossMultipleFiles(t *testing.T) {
	dir := t.TempDir()
	fileA := filepath.Join(dir, "a_lcov.info")
	fileB := filepath.Join(dir, "b_lcov.info")
	if err := os.WriteFile(fileA, []byte("SF:zzz_test_fixture_merge.go\nDA:1,1\nend_of_record\n"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if err := os.WriteFile(fileB, []byte("SF:zzz_test_fixture_merge.go\nDA:1,0\nend_of_record\n"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	stats, err := analyze([]string{fileA, fileB}, nil, false, "", "")
	if err != nil {
		t.Fatalf("analyze() error = %v", err)
	}

	if len(stats.Files) != 1 {
		t.Fatalf("stats.Files = %+v, want exactly one merged file", stats.Files)
	}
	merged := stats.Files[0]
	if merged.Hits != 0 || merged.Partials != 1 || merged.Misses != 0 {
		t.Fatalf("merged file stats = %+v, want hits:0 partials:1 misses:0 (covered+uncovered collapses to partial)", merged)
	}
	if len(stats.ProcessedFiles) != 2 {
		t.Fatalf("stats.ProcessedFiles = %+v, want 2 processed files", stats.ProcessedFiles)
	}
}
