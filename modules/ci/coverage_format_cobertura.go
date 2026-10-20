// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package ci

import (
	"encoding/xml"
	"io"
	"iter"
	"path"
	"regexp"
	"strconv"
	"strings"
)

// coverageCoberturaParser decodes Cobertura (and scoverage, which shares the
// same shape under a differently-named root element) XML coverage reports.
type coverageCoberturaParser struct {
	err error
}

func newCoverageCoberturaParser() *coverageCoberturaParser {
	return &coverageCoberturaParser{}
}

func (p *coverageCoberturaParser) Err() error { return p.err }

var coverageConditionCoverageRe = regexp.MustCompile(`\((\d+)/(\d+)\)`)

type coverageCoberturaXMLLine struct {
	Number            string `xml:"number,attr"`
	Hits              string `xml:"hits,attr"`
	Branch            string `xml:"branch,attr"`
	ConditionCoverage string `xml:"condition-coverage,attr"`
}

type coverageCoberturaXMLStatement struct {
	Line            string `xml:"line,attr"`
	InvocationCount string `xml:"invocation-count,attr"`
	Ignored         string `xml:"ignored,attr"`
}

type coverageCoberturaXMLClass struct {
	Filename   string                          `xml:"filename,attr"`
	Lines      []coverageCoberturaXMLLine      `xml:"lines>line"`
	Statements []coverageCoberturaXMLStatement `xml:"statements>statement"`
}

type coverageCoberturaXMLPackage struct {
	Classes []coverageCoberturaXMLClass `xml:"classes>class"`
}

// coverageCoberturaXMLReport has no XMLName constraint so it decodes equally
// well against a <coverage> (Cobertura) or <scoverage> root element.
type coverageCoberturaXMLReport struct {
	Sources  []string                      `xml:"sources>source"`
	Packages []coverageCoberturaXMLPackage `xml:"packages>package"`
}

func (p *coverageCoberturaParser) Parse(reader io.Reader) (iter.Seq[coverageLineRecord], error) {
	var report coverageCoberturaXMLReport
	if err := xml.NewDecoder(reader).Decode(&report); err != nil {
		p.err = err
		return func(func(coverageLineRecord) bool) {}, nil
	}

	return func(yield func(coverageLineRecord) bool) {
		for _, pkg := range report.Packages {
			for _, class := range pkg.Classes {
				if !coverageCoberturaProcessClass(class, report.Sources, yield) {
					return
				}
			}
		}
	}, nil
}

func coverageCoberturaProcessClass(class coverageCoberturaXMLClass, sources []string, yield func(coverageLineRecord) bool) bool {
	if class.Filename == "" {
		return true
	}
	filename := strings.ReplaceAll(class.Filename, `\`, "/")
	candidates := coverageCoberturaSourceCandidates(sources, filename)
	for _, line := range class.Lines {
		if line.Number == "undefined" || line.Number == "" {
			continue
		}
		ln, err := strconv.Atoi(line.Number)
		if err != nil || ln <= 0 {
			continue
		}
		if !yield(coverageLineRecord{
			FilePath:       filename,
			LineNumber:     uint32(ln),
			LineType:       coverageCoberturaLineType(line),
			PathCandidates: candidates,
		}) {
			return false
		}
	}
	for _, stmt := range class.Statements {
		if stmt.Ignored == "true" {
			continue
		}
		ln, err := strconv.Atoi(stmt.Line)
		if err != nil || ln <= 0 {
			continue
		}
		count, _ := strconv.Atoi(stmt.InvocationCount)
		lineType := coverageLineUncovered
		if count > 0 {
			lineType = coverageLineCovered
		}
		if !yield(coverageLineRecord{
			FilePath:       filename,
			LineNumber:     uint32(ln),
			LineType:       lineType,
			PathCandidates: candidates,
		}) {
			return false
		}
	}
	return true
}

func coverageCoberturaLineType(line coverageCoberturaXMLLine) coverageLineType {
	if line.Branch == "true" {
		if m := coverageConditionCoverageRe.FindStringSubmatch(line.ConditionCoverage); m != nil {
			return coverageCoberturaBranchLineType(m)
		}
	}
	hits, _ := strconv.Atoi(line.Hits)
	if hits > 0 {
		return coverageLineCovered
	}
	return coverageLineUncovered
}

func coverageCoberturaBranchLineType(conditionMatch []string) coverageLineType {
	covered, _ := strconv.Atoi(conditionMatch[1])
	total, _ := strconv.Atoi(conditionMatch[2])
	switch {
	case total == 0 || covered == 0:
		return coverageLineUncovered
	case covered == total:
		return coverageLineCovered
	default:
		return coverageLinePartial
	}
}

// coverageCoberturaIsAbsPath reports whether filename is absolute, either in
// the Unix sense (leading "/") or the Windows sense (a drive letter, e.g.
// "C:/Users/foo.cs") — the latter shows up in reports from .NET/OpenCover
// producers even when parsed on a non-Windows host, where path.IsAbs and
// filepath.IsAbs both only recognize the leading-"/" form.
func coverageCoberturaIsAbsPath(filename string) bool {
	if path.IsAbs(filename) {
		return true
	}
	if len(filename) >= 3 && filename[1] == ':' && filename[2] == '/' {
		c := filename[0]
		return (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z')
	}
	return false
}

func coverageCoberturaSourceCandidates(sources []string, filename string) []string {
	if coverageCoberturaIsAbsPath(filename) {
		return nil
	}
	cleanFilename := path.Clean(filename)
	var candidates []string
	seen := make(map[string]bool)
	for _, source := range sources {
		trimmed := strings.ReplaceAll(strings.TrimSpace(source), `\`, "/")
		if trimmed == "" {
			continue
		}
		joined := path.Join(trimmed, filename)
		if joined == cleanFilename || seen[joined] {
			continue
		}
		seen[joined] = true
		candidates = append(candidates, joined)
	}
	return candidates
}
