// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package format

import (
	"fmt"
	"io"
	"iter"
)

// LineType orders uncovered < partial < covered so merging two
// records for the same line can take the max.
type LineType uint32

const (
	LineUncovered LineType = iota
	LinePartial
	LineCovered
)

// LineRecord is one row of per-line coverage emitted by a Parser.
// It is also the schema of the parquet wire format sent to the coverage service.
type LineRecord struct {
	RepoRef    string   `parquet:"repo_ref,dict"`
	FilePath   string   `parquet:"file_path,dict"`
	LineNumber uint32   `parquet:"line_number"`
	LineType   LineType `parquet:"line_type,rle"`
	Diff       bool     `parquet:"diff"`
	// PathCandidates are exact repository paths the parser can infer (e.g. a
	// JaCoCo module plus source root). Not every parser populates this.
	PathCandidates []string `parquet:"-"`
}

// UnitCoverageRecord is one row of per-test coverage: the lines a single
// coverage key (test) hit in one source file. It is the schema of the
// parquet-bitmap wire format and must match the coverage service.
type UnitCoverageRecord struct {
	RepoRef         string   `parquet:"repo_ref,dict"`
	UnitKey         string   `parquet:"unit_key,dict"`
	FilePath        string   `parquet:"file_path,dict"`
	HitLines        []uint32 `parquet:"hit_lines,list"`
	PartialLines    []uint32 `parquet:"partial_lines,list"`
	ExecutableLines []uint32 `parquet:"executable_lines,list"`
}

// Parser decodes one coverage report dialect into lineRecords.
type Parser interface {
	Parse(reader io.Reader) (iter.Seq[LineRecord], error)
	Err() error
}

const (
	FormatGo        = "go"
	FormatLCOV      = "lcov"
	FormatJacoco    = "jacoco"
	FormatClover    = "clover"
	FormatCobertura = "cobertura"
	FormatCoveralls = "coveralls"

	// FormatTIRunner identifies TI Runner per-test coverage reports. Write
	// encodes them as parquet-bitmap; NewParser has no line parser for it.
	FormatTIRunner = "ti-runner"
)

// NewParser returns the Parser for format, one of the
// format* constants.
func NewParser(format string) (Parser, error) {
	switch format {
	case FormatGo:
		return newGotxtParser(), nil
	case FormatLCOV:
		return newLcovParser(), nil
	case FormatJacoco:
		return newJacocoParser(), nil
	case FormatClover:
		return newCloverParser(), nil
	case FormatCobertura:
		return newCoberturaParser(), nil
	case FormatCoveralls:
		return newCoverallsParser(), nil
	default:
		return nil, fmt.Errorf("unsupported coverage format: %s", format)
	}
}
