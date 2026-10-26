// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package analyze

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/harness/cli/v3/modules/ci/coverage/env"
	covfile "github.com/harness/cli/v3/modules/ci/coverage/file"
	"github.com/harness/cli/v3/modules/ci/coverage/format"
	"github.com/harness/cli/v3/pkg/console"
	"github.com/harness/cli/v3/pkg/hlog"
)

// fileStats is one source file's accumulated line counts.
type fileStats struct {
	FilePath   string
	Hits       int
	Partials   int
	Misses     int
	Percentage float64
	// Patch-specific statistics (only populated when analyzing with --patch)
	PatchHits       int
	PatchPartials   int
	PatchMisses     int
	PatchPercentage float64
}

// analysisStats is the merged result of analyzing one or more coverage files.
type analysisStats struct {
	Hits              int
	Partials          int
	Misses            int
	OverallPercentage float64
	Files             []fileStats
	ProcessedFiles    []covfile.File
	// Patch coverage statistics (only populated when --patch is used)
	PatchHits       int
	PatchPartials   int
	PatchMisses     int
	PatchPercentage float64
	HasPatchData    bool
}

func calcPercentage(hits, partials, misses int) float64 {
	total := hits + partials + misses
	if total == 0 {
		return 0
	}
	return float64(hits) / float64(total) * 100
}

// analyze parses files (or auto-discovers coverage reports when files
// is empty), optionally restricts to changed lines between base and head, and
// returns the merged per-file and overall statistics.
func analyze(files, excludePatterns []string, patch bool, base, head string) (*analysisStats, error) {
	hlog.Debug("running analyze for files", "files", files)

	compiledExcludes, err := format.CompileCLIExcludePatterns(excludePatterns)
	if err != nil {
		return nil, err
	}

	if patch && base == "" {
		vendor, vendorErr := env.Info()
		if vendorErr != nil {
			hlog.Debug("no CI vendor detected", "err", vendorErr)
		} else {
			if autoBase, baseErr := vendor.Base(); baseErr == nil && autoBase != "" {
				base = autoBase
				hlog.Debug("auto-detected base branch from CI", "base", base)
			} else if defaultBranch, branchErr := vendor.DefaultBranch(); branchErr == nil && defaultBranch != "" {
				base = defaultBranch
				hlog.Debug("using default branch as base", "base", base)
			}
		}
	}

	if patch && base == "" {
		return nil, fmt.Errorf("--patch requires --base to be specified (or auto-detected from CI environment)")
	}

	lookupFiles, err := covfile.Lookup(files...)
	if err != nil {
		return nil, fmt.Errorf("failed to lookup coverage files: %w", err)
	}
	if len(lookupFiles) == 0 {
		return nil, fmt.Errorf("no coverage files found")
	}
	hlog.Info("found coverage file(s) to analyze", "count", len(lookupFiles))

	var diffLines map[string]map[uint32]bool
	if base != "" {
		baseRef := env.EnsureRefFetched(base)
		diffLines, err = env.ChangedLines(baseRef, head)
		if err != nil {
			if patch {
				return nil, fmt.Errorf("failed to compute patch diff: %w", err)
			}
			hlog.Warn("failed to get diff lines, patch coverage will not be shown", "err", err)
			diffLines = nil
		} else {
			hlog.Debug("found files with changes", "count", len(diffLines))
		}
	}

	gitTrie := env.NewLazyTreeFromGit()

	mergedStats := &analysisStats{
		Files:          make([]fileStats, 0),
		ProcessedFiles: make([]covfile.File, 0, len(lookupFiles)),
		HasPatchData:   diffLines != nil,
	}

	accumulator := newCoverageAnalysisAccumulator()

	for _, fileInfo := range lookupFiles {
		hlog.Debug("processing coverage file", "path", fileInfo.Path, "format", fileInfo.Format)

		f, openErr := os.Open(fileInfo.Path)
		if openErr != nil {
			hlog.Warn("failed to open coverage file", "path", fileInfo.Path, "err", openErr)
			continue
		}

		parseErr := parseCoverageFileInto(
			fileInfo.Format,
			f,
			filepath.Dir(fileInfo.Path),
			gitTrie,
			diffLines,
			patch,
			compiledExcludes,
			accumulator,
		)
		_ = f.Close()
		if parseErr != nil {
			return nil, fmt.Errorf("failed to parse coverage file %s: %w", fileInfo.Path, parseErr)
		}

		mergedStats.ProcessedFiles = append(mergedStats.ProcessedFiles, fileInfo)
	}

	for _, fileStat := range accumulator.files {
		fileStat.Percentage = calcPercentage(fileStat.Hits, fileStat.Partials, fileStat.Misses)
		fileStat.PatchPercentage = calcPercentage(fileStat.PatchHits, fileStat.PatchPartials, fileStat.PatchMisses)
		mergedStats.Files = append(mergedStats.Files, *fileStat)
		mergedStats.Hits += fileStat.Hits
		mergedStats.Partials += fileStat.Partials
		mergedStats.Misses += fileStat.Misses
		mergedStats.PatchHits += fileStat.PatchHits
		mergedStats.PatchPartials += fileStat.PatchPartials
		mergedStats.PatchMisses += fileStat.PatchMisses
	}

	mergedStats.OverallPercentage = calcPercentage(mergedStats.Hits, mergedStats.Partials, mergedStats.Misses)
	mergedStats.PatchPercentage = calcPercentage(mergedStats.PatchHits, mergedStats.PatchPartials, mergedStats.PatchMisses)

	sort.Slice(mergedStats.Files, func(i, j int) bool {
		return mergedStats.Files[i].Percentage < mergedStats.Files[j].Percentage
	})

	return mergedStats, nil
}

// seenLine is the merged state of every coverage observation seen so
// far for one file+line, used to adjust Hits/Partials/Misses deltas as later
// reports merge in rather than double-counting.
type seenLine struct {
	lineType format.LineType
	inPatch  bool
}

// analysisAccumulator merges per-line coverage observations from
// potentially multiple report files into shared per-file statistics.
type analysisAccumulator struct {
	files    map[string]*fileStats
	lines    map[string]seenLine
	excluded map[string]struct{}
}

func newCoverageAnalysisAccumulator() *analysisAccumulator {
	return &analysisAccumulator{
		files:    make(map[string]*fileStats),
		lines:    make(map[string]seenLine),
		excluded: make(map[string]struct{}),
	}
}

// parseCoverageFileInto parses one coverage report into accumulator,
// merging per-line observations across potentially multiple report files for
// the same file+line (e.g. a covered line in one report and uncovered in
// another collapses to partial).
func parseCoverageFileInto(
	formatType string,
	reader io.Reader,
	hintDir string,
	tree *env.Tree,
	diffLines map[string]map[uint32]bool,
	patch bool,
	excludes []format.ExcludePattern,
	accumulator *analysisAccumulator,
) error {
	parser, err := format.NewParser(formatType)
	if err != nil {
		return err
	}

	lines, err := parser.Parse(reader)
	if err != nil {
		return err
	}

	gitRoot, rootErr := env.RepositoryRoot()
	if rootErr != nil {
		gitRoot = ""
	}
	paths := format.NewRepositoryPathResolver(format.RepositoryPathOptions{
		Tree:     tree,
		HintDirs: []string{hintDir},
		GitRoot:  gitRoot,
		Patch:    patch,
		Format:   formatType,
	})

	var pathErrs []error
	seenPathErrs := make(map[string]struct{})
	for line := range lines {
		choice, selectErr := paths.Select(line.FilePath, excludes, accumulator.excluded, line.PathCandidates...)
		if selectErr != nil {
			pathErrs = format.AppendPathError(pathErrs, seenPathErrs, line.FilePath, selectErr)
			continue
		}
		if choice.Skip {
			continue
		}
		filePath := choice.Path

		if accumulator.files[filePath] == nil {
			accumulator.files[filePath] = &fileStats{FilePath: filePath}
		}
		stats := accumulator.files[filePath]

		inPatch := false
		if diffLines != nil {
			fileLines, exists := diffLines[filePath]
			inPatch = exists && fileLines[line.LineNumber]
		}

		key := filePath + "\x00" + strconv.FormatUint(uint64(line.LineNumber), 10)
		if prev, ok := accumulator.lines[key]; ok {
			merged := format.MergeLineType(prev.lineType, line.LineType)
			adjustLineCounts(stats, prev.lineType, -1, prev.inPatch)
			nowPatch := prev.inPatch || inPatch
			adjustLineCounts(stats, merged, 1, nowPatch)
			accumulator.lines[key] = seenLine{lineType: merged, inPatch: nowPatch}
			continue
		}
		adjustLineCounts(stats, line.LineType, 1, inPatch)
		accumulator.lines[key] = seenLine{lineType: line.LineType, inPatch: inPatch}
	}

	if err := parser.Err(); err != nil {
		return fmt.Errorf("failed to parse %s coverage report: %w", formatType, err)
	}
	return format.JoinPathErrors(pathErrs)
}

func adjustLineCounts(stats *fileStats, lineType format.LineType, delta int, inPatch bool) {
	switch lineType {
	case format.LineCovered:
		stats.Hits += delta
		if inPatch {
			stats.PatchHits += delta
		}
	case format.LinePartial:
		stats.Partials += delta
		if inPatch {
			stats.PatchPartials += delta
		}
	default:
		stats.Misses += delta
		if inPatch {
			stats.PatchMisses += delta
		}
	}
}

// displayStats prints a colored terminal summary of stats.
func displayStats(stats *analysisStats) {
	fmt.Println()
	fmt.Println(console.WithBoldColor(console.ColorCyan, "Coverage Report Summary"))
	fmt.Println(strings.Repeat("=", 80))

	if len(stats.ProcessedFiles) == 1 {
		fileInfo := stats.ProcessedFiles[0]
		fmt.Printf("File:     %s\n", console.WithColor(console.ColorCyan, fileInfo.Path))
		fmt.Printf("Format:   %s\n", console.WithColor(console.ColorCyan, fileInfo.Format))
		fmt.Printf("Language: %s\n", console.WithColor(console.ColorCyan, fileInfo.Language))
	} else {
		fmt.Printf("Processed Files: %s\n", console.WithColor(console.ColorCyan, strconv.Itoa(len(stats.ProcessedFiles))))
		for i, fileInfo := range stats.ProcessedFiles {
			fmt.Printf("  %d. %s (%s - %s)\n", i+1,
				console.WithColor(console.ColorCyan, fileInfo.Path),
				console.WithColor(console.ColorCyan, fileInfo.Format),
				console.WithColor(console.ColorCyan, fileInfo.Language))
		}
	}
	fmt.Println()

	fmt.Println(console.WithBold("Overall Coverage:"))
	fmt.Printf("  Hits:      %s\n", console.WithColor(console.ColorGreen, strconv.Itoa(stats.Hits)))
	fmt.Printf("  Partials:  %s\n", console.WithColor(console.ColorYellow, strconv.Itoa(stats.Partials)))
	fmt.Printf("  Misses:    %s\n", console.WithColor(console.ColorRed, strconv.Itoa(stats.Misses)))
	fmt.Printf("  Coverage:  %s\n", console.WithColor(getPercentageColor(stats.OverallPercentage), fmt.Sprintf("%.2f%%", stats.OverallPercentage)))
	fmt.Println()

	if stats.HasPatchData {
		fmt.Println(console.WithBold("Patch Coverage:"))
		fmt.Printf("  Hits:      %s\n", console.WithColor(console.ColorGreen, strconv.Itoa(stats.PatchHits)))
		fmt.Printf("  Partials:  %s\n", console.WithColor(console.ColorYellow, strconv.Itoa(stats.PatchPartials)))
		fmt.Printf("  Misses:    %s\n", console.WithColor(console.ColorRed, strconv.Itoa(stats.PatchMisses)))
		patchTotal := stats.PatchHits + stats.PatchPartials + stats.PatchMisses
		if patchTotal == 0 {
			fmt.Printf("  Coverage:  %s\n", console.WithColor(console.ColorYellow, "N/A (no new lines in diff)"))
		} else {
			fmt.Printf("  Coverage:  %s\n", console.WithColor(getPercentageColor(stats.PatchPercentage), fmt.Sprintf("%.2f%%", stats.PatchPercentage)))
		}
		fmt.Println()
	}

	if len(stats.Files) > 0 {
		if stats.HasPatchData {
			displayPatchFileTable(stats.Files)
		} else {
			displayFileTable(stats.Files)
		}
	}

	fmt.Println()
}

func displayFileTable(files []fileStats) {
	fmt.Println(console.WithBold("Coverage by File:"))

	const tableWidth = 90
	fmt.Println(strings.Repeat("-", tableWidth))
	fmt.Printf("%s %s %s %s %s\n",
		padRight(console.WithBold("File"), 45),
		padRight(console.WithBold("Hits"), 8),
		padRight(console.WithBold("Partials"), 10),
		padRight(console.WithBold("Misses"), 8),
		padRight(console.WithBold("Coverage"), 10),
	)
	fmt.Println(strings.Repeat("-", tableWidth))

	for _, fileStat := range files {
		displayPath := fileStat.FilePath
		if len(displayPath) > 43 {
			displayPath = "..." + displayPath[len(displayPath)-40:]
		}
		fmt.Printf("%s %s %s %s %s\n",
			padRight(displayPath, 45),
			padRight(console.WithColor(console.ColorGreen, strconv.Itoa(fileStat.Hits)), 8),
			padRight(console.WithColor(console.ColorYellow, strconv.Itoa(fileStat.Partials)), 10),
			padRight(console.WithColor(console.ColorRed, strconv.Itoa(fileStat.Misses)), 8),
			padRight(console.WithColor(getPercentageColor(fileStat.Percentage), fmt.Sprintf("%.2f%%", fileStat.Percentage)), 10),
		)
	}
	fmt.Println(strings.Repeat("-", tableWidth))
}

func displayPatchFileTable(files []fileStats) {
	filteredFiles := make([]fileStats, 0, len(files))
	for _, f := range files {
		if f.PatchHits+f.PatchPartials+f.PatchMisses > 0 {
			filteredFiles = append(filteredFiles, f)
		}
	}

	fmt.Println(console.WithBold("Coverage by File (patch files only):"))

	if len(filteredFiles) == 0 {
		fmt.Println("  No changed files with coverable lines found in diff")
		return
	}

	const tableWidth = 110
	fmt.Println(strings.Repeat("-", tableWidth))
	fmt.Printf("%s %s %s %s %s %s %s %s %s\n",
		padRight(console.WithBold("File"), 35),
		padRight(console.WithBold("Hits"), 7),
		padRight(console.WithBold("Partials"), 10),
		padRight(console.WithBold("Misses"), 8),
		padRight(console.WithBold("Cov%"), 8),
		padRight(console.WithBold("PHits"), 7),
		padRight(console.WithBold("PPartials"), 11),
		padRight(console.WithBold("PMisses"), 9),
		padRight(console.WithBold("PCov%"), 8),
	)
	fmt.Println(strings.Repeat("-", tableWidth))

	for _, fileStat := range filteredFiles {
		displayPath := fileStat.FilePath
		if len(displayPath) > 33 {
			displayPath = "..." + displayPath[len(displayPath)-30:]
		}
		patchTotal := fileStat.PatchHits + fileStat.PatchPartials + fileStat.PatchMisses
		pCovStr := console.WithColor(console.ColorBrightBlack, "-")
		if patchTotal > 0 {
			pCovStr = console.WithColor(getPercentageColor(fileStat.PatchPercentage), fmt.Sprintf("%.0f%%", fileStat.PatchPercentage))
		}
		fmt.Printf("%s %s %s %s %s %s %s %s %s\n",
			padRight(displayPath, 35),
			padRight(console.WithColor(console.ColorGreen, strconv.Itoa(fileStat.Hits)), 7),
			padRight(console.WithColor(console.ColorYellow, strconv.Itoa(fileStat.Partials)), 10),
			padRight(console.WithColor(console.ColorRed, strconv.Itoa(fileStat.Misses)), 8),
			padRight(console.WithColor(getPercentageColor(fileStat.Percentage), fmt.Sprintf("%.0f%%", fileStat.Percentage)), 8),
			padRight(console.WithColor(console.ColorGreen, strconv.Itoa(fileStat.PatchHits)), 7),
			padRight(console.WithColor(console.ColorYellow, strconv.Itoa(fileStat.PatchPartials)), 11),
			padRight(console.WithColor(console.ColorRed, strconv.Itoa(fileStat.PatchMisses)), 9),
			padRight(pCovStr, 8),
		)
	}
	fmt.Println(strings.Repeat("-", tableWidth))
}

// getPercentageColor buckets a coverage percentage into a display color: >=80 green, >=50 yellow, else red.
func getPercentageColor(percentage float64) console.Color {
	switch {
	case percentage >= 80:
		return console.ColorGreen
	case percentage >= 50:
		return console.ColorYellow
	default:
		return console.ColorRed
	}
}

// padRight pads s to width, accounting for ANSI color codes so colored strings still align.
func padRight(s string, width int) string {
	visualWidth := len(console.StripANSI(s))
	if visualWidth >= width {
		return s
	}
	return s + strings.Repeat(" ", width-visualWidth)
}
