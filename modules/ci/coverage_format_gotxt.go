// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package ci

import (
	"bufio"
	"fmt"
	"io"
	"iter"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// coverageGotxtParser decodes the text format `go test -coverprofile` produces.
type coverageGotxtParser struct{}

func newCoverageGotxtParser() *coverageGotxtParser {
	return &coverageGotxtParser{}
}

func (p *coverageGotxtParser) Err() error { return nil }

var coverageGoProfileLineRe = regexp.MustCompile(`^(.+):(\d+)\.(\d+),(\d+)\.(\d+) \d+ (\d+)$`)

type coverageGoBlock struct {
	startLine int
	startCol  int
	endLine   int
	endCol    int
	count     int
}

type coverageGoBlockKey struct {
	startLine, startCol, endLine, endCol int
}

func (p *coverageGotxtParser) Parse(reader io.Reader) (iter.Seq[coverageLineRecord], error) {
	scanner := bufio.NewScanner(reader)
	blocksByFile := make(map[string][]coverageGoBlock)
	blockIndexByFile := make(map[string]map[coverageGoBlockKey]int)
	var fileOrder []string
	seenFile := make(map[string]bool)
	mode := ""

	first := true
	for scanner.Scan() {
		line := scanner.Text()
		if first {
			first = false
			if !strings.HasPrefix(line, "mode:") {
				return nil, fmt.Errorf("bad go coverage profile: first line must start with \"mode:\", got %q", line)
			}
			mode = strings.TrimSpace(strings.TrimPrefix(line, "mode:"))
			continue
		}
		if strings.TrimSpace(line) == "" {
			continue
		}
		m := coverageGoProfileLineRe.FindStringSubmatch(line)
		if m == nil {
			return nil, fmt.Errorf("bad go coverage profile line: %q", line)
		}
		fileName := m[1]
		startLine, _ := strconv.Atoi(m[2])
		startCol, _ := strconv.Atoi(m[3])
		endLine, _ := strconv.Atoi(m[4])
		endCol, _ := strconv.Atoi(m[5])
		count, _ := strconv.Atoi(m[6])
		if !seenFile[fileName] {
			seenFile[fileName] = true
			fileOrder = append(fileOrder, fileName)
			blockIndexByFile[fileName] = make(map[coverageGoBlockKey]int)
		}

		key := coverageGoBlockKey{startLine, startCol, endLine, endCol}
		if idx, ok := blockIndexByFile[fileName][key]; ok {
			existing := &blocksByFile[fileName][idx]
			if mode == "set" {
				if count > 0 {
					existing.count = 1
				}
			} else {
				existing.count += count
			}
			continue
		}
		for _, b := range blocksByFile[fileName] {
			if b.startLine == startLine && b.startCol == startCol && (b.endLine != endLine || b.endCol != endCol) {
				return nil, fmt.Errorf("bad go coverage profile: mismatched block boundaries for %s at %d.%d", fileName, startLine, startCol)
			}
		}
		blockIndexByFile[fileName][key] = len(blocksByFile[fileName])
		blocksByFile[fileName] = append(blocksByFile[fileName], coverageGoBlock{startLine: startLine, startCol: startCol, endLine: endLine, endCol: endCol, count: count})
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if first {
		return nil, fmt.Errorf("bad go coverage profile: empty input")
	}

	return func(yield func(coverageLineRecord) bool) {
		for _, fileName := range fileOrder {
			lineCounts := make(map[int][]int)
			for _, block := range blocksByFile[fileName] {
				for line := block.startLine; line <= block.endLine; line++ {
					lineCounts[line] = append(lineCounts[line], block.count)
				}
			}
			lines := make([]int, 0, len(lineCounts))
			for line := range lineCounts {
				lines = append(lines, line)
			}
			sort.Ints(lines)
			for _, line := range lines {
				hasCovered := false
				hasUncovered := false
				for _, count := range lineCounts[line] {
					if count > 0 {
						hasCovered = true
					} else {
						hasUncovered = true
					}
				}
				lineType := coverageLineUncovered
				switch {
				case hasCovered && hasUncovered:
					lineType = coverageLinePartial
				case hasCovered:
					lineType = coverageLineCovered
				}
				if !yield(coverageLineRecord{
					FilePath:   fileName,
					LineNumber: uint32(line),
					LineType:   lineType,
				}) {
					return
				}
			}
		}
	}, nil
}
