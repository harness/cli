// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package ci

import (
	"encoding/xml"
	"io"
	"iter"
	"strconv"
	"strings"
)

// coverageCloverParser decodes Clover XML coverage reports.
type coverageCloverParser struct {
	err error
}

func newCoverageCloverParser() *coverageCloverParser {
	return &coverageCloverParser{}
}

func (p *coverageCloverParser) Err() error { return p.err }

type coverageCloverXMLLine struct {
	Num        string `xml:"num,attr"`
	Type       string `xml:"type,attr"`
	Count      string `xml:"count,attr"`
	TrueCount  string `xml:"truecount,attr"`
	FalseCount string `xml:"falsecount,attr"`
}

type coverageCloverXMLMetrics struct {
	Loc string `xml:"loc,attr"`
}

type coverageCloverXMLFile struct {
	Name    string                     `xml:"name,attr"`
	Path    string                     `xml:"path,attr"`
	Metrics []coverageCloverXMLMetrics `xml:"metrics"`
	Lines   []coverageCloverXMLLine    `xml:"line"`
}

type coverageCloverXMLPackage struct {
	Files []coverageCloverXMLFile `xml:"file"`
}

type coverageCloverXMLProject struct {
	Packages []coverageCloverXMLPackage `xml:"package"`
	Files    []coverageCloverXMLFile    `xml:"file"`
}

type coverageCloverXMLCoverage struct {
	XMLName xml.Name                 `xml:"coverage"`
	Project coverageCloverXMLProject `xml:"project"`
	Files   []coverageCloverXMLFile  `xml:"file"`
}

func (p *coverageCloverParser) Parse(reader io.Reader) (iter.Seq[coverageLineRecord], error) {
	var coverage coverageCloverXMLCoverage
	if err := xml.NewDecoder(reader).Decode(&coverage); err != nil {
		p.err = err
		return func(func(coverageLineRecord) bool) {}, nil
	}

	var files []coverageCloverXMLFile
	files = append(files, coverage.Files...)
	files = append(files, coverage.Project.Files...)
	for _, pkg := range coverage.Project.Packages {
		files = append(files, pkg.Files...)
	}

	return func(yield func(coverageLineRecord) bool) {
		for _, file := range files {
			if !coverageCloverProcessFile(file, yield) {
				return
			}
		}
	}, nil
}

func coverageCloverProcessFile(file coverageCloverXMLFile, yield func(coverageLineRecord) bool) bool {
	filename := file.Path
	if filename == "" {
		filename = file.Name
	}
	if filename == "" {
		return true
	}
	if strings.Contains(filename, "{") {
		return true
	}
	if coverageCloverIsPHPVendorFile(filename) {
		return true
	}
	if len(file.Lines) == 0 {
		return true
	}
	eof := coverageCloverEndOfFile(filename, file.Metrics)
	for _, line := range file.Lines {
		ln, err := strconv.Atoi(line.Num)
		if err != nil || ln < 1 {
			continue
		}
		if eof > 0 && ln > eof {
			continue
		}
		if !yield(coverageLineRecord{
			FilePath:   filename,
			LineNumber: uint32(ln),
			LineType:   coverageCloverLineType(line),
		}) {
			return false
		}
	}
	return true
}

func coverageCloverIsPHPVendorFile(filename string) bool {
	if !strings.HasSuffix(filename, ".php") {
		return false
	}
	normalized := filename
	if !strings.HasPrefix(normalized, "/") {
		normalized = "/" + normalized
	}
	return strings.Contains(normalized, "/vendor/")
}

func coverageCloverEndOfFile(filename string, metrics []coverageCloverXMLMetrics) int {
	if !strings.HasSuffix(filename, ".php") {
		return 0
	}
	for _, m := range metrics {
		if loc, err := strconv.Atoi(m.Loc); err == nil && loc > 0 {
			return loc
		}
	}
	return 0
}

func coverageCloverLineType(line coverageCloverXMLLine) coverageLineType {
	if line.Type == "cond" {
		trueCount, _ := strconv.Atoi(line.TrueCount)
		falseCount, _ := strconv.Atoi(line.FalseCount)
		switch {
		case trueCount == 0 && falseCount == 0:
			return coverageLineUncovered
		case trueCount == 0 || falseCount == 0:
			return coverageLinePartial
		default:
			return coverageLineCovered
		}
	}
	count, _ := strconv.Atoi(line.Count)
	if count > 0 {
		return coverageLineCovered
	}
	return coverageLineUncovered
}
