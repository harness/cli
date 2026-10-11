// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package ci

import (
	"bufio"
	"fmt"
	"io"
	"iter"
	"strconv"
	"strings"
)

// coverageLcovParser decodes LCOV tracefiles (DA: line-hit records).
type coverageLcovParser struct {
	err error
}

func newCoverageLcovParser() *coverageLcovParser {
	return &coverageLcovParser{}
}

func (p *coverageLcovParser) Err() error { return p.err }

func (p *coverageLcovParser) Parse(reader io.Reader) (iter.Seq[coverageLineRecord], error) {
	return func(yield func(coverageLineRecord) bool) {
		scanner := bufio.NewScanner(reader)
		currFile := ""
		for scanner.Scan() {
			line := scanner.Text()
			switch {
			case strings.HasPrefix(line, "SF:"):
				currFile = strings.TrimPrefix(line, "SF:")
			case line == "end_of_record":
				currFile = ""
			case strings.HasPrefix(line, "DA:"):
				if currFile == "" {
					continue
				}
				lineNum, count, err := coverageParseLcovLineData(strings.TrimPrefix(line, "DA:"))
				if err != nil {
					p.err = err
					return
				}
				lineType := coverageLineUncovered
				if count > 0 {
					lineType = coverageLineCovered
				}
				if !yield(coverageLineRecord{
					FilePath:   currFile,
					LineNumber: uint32(lineNum),
					LineType:   lineType,
				}) {
					return
				}
			}
		}
	}, nil
}

func coverageParseLcovLineData(data string) (int, int, error) {
	parts := strings.Split(data, ",")
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("malformed DA line data: %q", data)
	}
	lineNum, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, 0, fmt.Errorf("malformed DA line data: %q", data)
	}
	count, err := strconv.Atoi(parts[1])
	if err != nil {
		return 0, 0, fmt.Errorf("malformed DA line data: %q", data)
	}
	return lineNum, count, nil
}
