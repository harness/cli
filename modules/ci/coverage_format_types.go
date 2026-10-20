// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package ci

import (
	"fmt"
	"io"
	"iter"
)

// coverageLineType orders uncovered < partial < covered so merging two
// records for the same line can take the max.
type coverageLineType uint32

const (
	coverageLineUncovered coverageLineType = iota
	coverageLinePartial
	coverageLineCovered
)

// coverageLineRecord is one row of per-line coverage emitted by a coverageParser.
// It is also the schema of the parquet wire format sent to the coverage service.
type coverageLineRecord struct {
	RepoRef    string           `parquet:"repo_ref,dict"`
	FilePath   string           `parquet:"file_path,dict"`
	LineNumber uint32           `parquet:"line_number"`
	LineType   coverageLineType `parquet:"line_type,rle"`
	Diff       bool             `parquet:"diff"`
	// PathCandidates are exact repository paths the parser can infer (e.g. a
	// JaCoCo module plus source root). Not every parser populates this.
	PathCandidates []string `parquet:"-"`
}

// coverageParser decodes one coverage report dialect into coverageLineRecords.
type coverageParser interface {
	Parse(reader io.Reader) (iter.Seq[coverageLineRecord], error)
	Err() error
}

const (
	coverageFormatGo        = "go"
	coverageFormatLCOV      = "lcov"
	coverageFormatJacoco    = "jacoco"
	coverageFormatClover    = "clover"
	coverageFormatCobertura = "cobertura"
	coverageFormatCoveralls = "coveralls"
)

// newCoverageParser returns the coverageParser for format, one of the
// coverageFormat* constants.
func newCoverageParser(format string) (coverageParser, error) {
	switch format {
	case coverageFormatGo:
		return newCoverageGotxtParser(), nil
	case coverageFormatLCOV:
		return newCoverageLcovParser(), nil
	case coverageFormatJacoco:
		return newCoverageJacocoParser(), nil
	case coverageFormatClover:
		return newCoverageCloverParser(), nil
	case coverageFormatCobertura:
		return newCoverageCoberturaParser(), nil
	case coverageFormatCoveralls:
		return newCoverageCoverallsParser(), nil
	default:
		return nil, fmt.Errorf("unsupported coverage format: %s", format)
	}
}
