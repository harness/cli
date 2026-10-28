// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package format

import (
	"encoding/xml"
	"io"
	"iter"
	"path"
	"regexp"
	"strconv"
	"strings"
)

// coberturaParser decodes Cobertura (and scoverage, which shares the
// same shape under a differently-named root element) XML coverage reports.
type coberturaParser struct {
	err error
}

func newCoberturaParser() *coberturaParser {
	return &coberturaParser{}
}

func (p *coberturaParser) Err() error { return p.err }

var conditionCoverageRe = regexp.MustCompile(`\((\d+)/(\d+)\)`)

type coberturaXMLLine struct {
	Number            string `xml:"number,attr"`
	Hits              string `xml:"hits,attr"`
	Branch            string `xml:"branch,attr"`
	ConditionCoverage string `xml:"condition-coverage,attr"`
}

type coberturaXMLStatement struct {
	Line            string `xml:"line,attr"`
	InvocationCount string `xml:"invocation-count,attr"`
	Ignored         string `xml:"ignored,attr"`
}

type coberturaXMLClass struct {
	Filename   string                  `xml:"filename,attr"`
	Lines      []coberturaXMLLine      `xml:"lines>line"`
	Statements []coberturaXMLStatement `xml:"statements>statement"`
}

type coberturaXMLPackage struct {
	Classes []coberturaXMLClass `xml:"classes>class"`
}

// coberturaXMLReport has no XMLName constraint so it decodes equally
// well against a <coverage> (Cobertura) or <scoverage> root element.
type coberturaXMLReport struct {
	Sources  []string              `xml:"sources>source"`
	Packages []coberturaXMLPackage `xml:"packages>package"`
}

func (p *coberturaParser) Parse(reader io.Reader) (iter.Seq[LineRecord], error) {
	var report coberturaXMLReport
	if err := xml.NewDecoder(reader).Decode(&report); err != nil {
		p.err = err
		return func(func(LineRecord) bool) {}, nil
	}

	return func(yield func(LineRecord) bool) {
		for _, pkg := range report.Packages {
			for _, class := range pkg.Classes {
				if !coberturaProcessClass(class, report.Sources, yield) {
					return
				}
			}
		}
	}, nil
}

func coberturaProcessClass(class coberturaXMLClass, sources []string, yield func(LineRecord) bool) bool {
	if class.Filename == "" {
		return true
	}
	filename := strings.ReplaceAll(class.Filename, `\`, "/")
	candidates := coberturaSourceCandidates(sources, filename)
	for _, line := range class.Lines {
		if line.Number == "undefined" || line.Number == "" {
			continue
		}
		ln, err := strconv.Atoi(line.Number)
		if err != nil || ln <= 0 {
			continue
		}
		if !yield(LineRecord{
			FilePath:       filename,
			LineNumber:     uint32(ln),
			LineType:       coberturaLineType(line),
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
		lineType := LineUncovered
		if count > 0 {
			lineType = LineCovered
		}
		if !yield(LineRecord{
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

func coberturaLineType(line coberturaXMLLine) LineType {
	if line.Branch == "true" {
		if m := conditionCoverageRe.FindStringSubmatch(line.ConditionCoverage); m != nil {
			return coberturaBranchLineType(m)
		}
	}
	hits, _ := strconv.Atoi(line.Hits)
	if hits > 0 {
		return LineCovered
	}
	return LineUncovered
}

func coberturaBranchLineType(conditionMatch []string) LineType {
	covered, _ := strconv.Atoi(conditionMatch[1])
	total, _ := strconv.Atoi(conditionMatch[2])
	switch {
	case total == 0 || covered == 0:
		return LineUncovered
	case covered == total:
		return LineCovered
	default:
		return LinePartial
	}
}

// coberturaIsAbsPath reports whether filename is absolute, either in
// the Unix sense (leading "/") or the Windows sense (a drive letter, e.g.
// "C:/Users/foo.cs") — the latter shows up in reports from .NET/OpenCover
// producers even when parsed on a non-Windows host, where path.IsAbs and
// filepath.IsAbs both only recognize the leading-"/" form.
func coberturaIsAbsPath(filename string) bool {
	if path.IsAbs(filename) {
		return true
	}
	if len(filename) >= 3 && filename[1] == ':' && filename[2] == '/' {
		c := filename[0]
		return (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z')
	}
	return false
}

func coberturaSourceCandidates(sources []string, filename string) []string {
	if coberturaIsAbsPath(filename) {
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
