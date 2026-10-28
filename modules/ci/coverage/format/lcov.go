// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package format

import (
	"bufio"
	"fmt"
	"io"
	"iter"
	"strconv"
	"strings"
)

// lcovParser decodes LCOV tracefiles (DA: line-hit records).
type lcovParser struct {
	err error
}

func newLcovParser() *lcovParser {
	return &lcovParser{}
}

func (p *lcovParser) Err() error { return p.err }

func (p *lcovParser) Parse(reader io.Reader) (iter.Seq[LineRecord], error) {
	return func(yield func(LineRecord) bool) {
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
				lineNum, count, err := parseLcovLineData(strings.TrimPrefix(line, "DA:"))
				if err != nil {
					p.err = err
					return
				}
				lineType := LineUncovered
				if count > 0 {
					lineType = LineCovered
				}
				if !yield(LineRecord{
					FilePath:   currFile,
					LineNumber: uint32(lineNum),
					LineType:   lineType,
				}) {
					return
				}
			}
		}
		if err := scanner.Err(); err != nil {
			p.err = fmt.Errorf("error reading lcov file: %w", err)
		}
	}, nil
}

func parseLcovLineData(data string) (int, int, error) {
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
