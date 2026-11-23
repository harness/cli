// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package format

import (
	"encoding/xml"
	"io"
	"iter"
	"strconv"
	"strings"
)

// cloverParser decodes Clover XML coverage reports.
type cloverParser struct {
	err error
}

func newCloverParser() *cloverParser {
	return &cloverParser{}
}

func (p *cloverParser) Err() error { return p.err }

type cloverXMLLine struct {
	Num        string `xml:"num,attr"`
	Type       string `xml:"type,attr"`
	Count      string `xml:"count,attr"`
	TrueCount  string `xml:"truecount,attr"`
	FalseCount string `xml:"falsecount,attr"`
}

type cloverXMLMetrics struct {
	Loc string `xml:"loc,attr"`
}

type cloverXMLFile struct {
	Name    string             `xml:"name,attr"`
	Path    string             `xml:"path,attr"`
	Metrics []cloverXMLMetrics `xml:"metrics"`
	Lines   []cloverXMLLine    `xml:"line"`
}

type cloverXMLPackage struct {
	Files []cloverXMLFile `xml:"file"`
}

type cloverXMLProject struct {
	Packages []cloverXMLPackage `xml:"package"`
	Files    []cloverXMLFile    `xml:"file"`
}

type cloverXMLCoverage struct {
	XMLName xml.Name         `xml:"coverage"`
	Project cloverXMLProject `xml:"project"`
	Files   []cloverXMLFile  `xml:"file"`
}

func (p *cloverParser) Parse(reader io.Reader) (iter.Seq[LineRecord], error) {
	var coverage cloverXMLCoverage
	if err := xml.NewDecoder(reader).Decode(&coverage); err != nil {
		p.err = err
		return func(func(LineRecord) bool) {}, nil
	}

	var files []cloverXMLFile
	files = append(files, coverage.Files...)
	files = append(files, coverage.Project.Files...)
	for _, pkg := range coverage.Project.Packages {
		files = append(files, pkg.Files...)
	}

	return func(yield func(LineRecord) bool) {
		for _, file := range files {
			if !cloverProcessFile(file, yield) {
				return
			}
		}
	}, nil
}

func cloverProcessFile(file cloverXMLFile, yield func(LineRecord) bool) bool {
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
	if cloverIsPHPVendorFile(filename) {
		return true
	}
	if len(file.Lines) == 0 {
		return true
	}
	eof := cloverEndOfFile(filename, file.Metrics)
	for _, line := range file.Lines {
		ln, err := strconv.Atoi(line.Num)
		if err != nil || ln < 1 {
			continue
		}
		if eof > 0 && ln > eof {
			continue
		}
		if !yield(LineRecord{
			FilePath:   filename,
			LineNumber: uint32(ln),
			LineType:   cloverLineType(line),
		}) {
			return false
		}
	}
	return true
}

func cloverIsPHPVendorFile(filename string) bool {
	if !strings.HasSuffix(filename, ".php") {
		return false
	}
	normalized := filename
	if !strings.HasPrefix(normalized, "/") {
		normalized = "/" + normalized
	}
	return strings.Contains(normalized, "/vendor/")
}

func cloverEndOfFile(filename string, metrics []cloverXMLMetrics) int {
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

func cloverLineType(line cloverXMLLine) LineType {
	if line.Type == "cond" {
		trueCount, _ := strconv.Atoi(line.TrueCount)
		falseCount, _ := strconv.Atoi(line.FalseCount)
		switch {
		case trueCount == 0 && falseCount == 0:
			return LineUncovered
		case trueCount == 0 || falseCount == 0:
			return LinePartial
		default:
			return LineCovered
		}
	}
	count, _ := strconv.Atoi(line.Count)
	if count > 0 {
		return LineCovered
	}
	return LineUncovered
}
