// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package ci

import "testing"

func TestNewCoverageParser(t *testing.T) {
	for _, format := range []string{
		coverageFormatGo, coverageFormatLCOV, coverageFormatJacoco,
		coverageFormatClover, coverageFormatCobertura, coverageFormatCoveralls,
	} {
		if _, err := newCoverageParser(format); err != nil {
			t.Errorf("newCoverageParser(%q) = %v, want nil error", format, err)
		}
	}

	for _, format := range []string{"unknown", ""} {
		if _, err := newCoverageParser(format); err == nil {
			t.Errorf("newCoverageParser(%q) = nil error, want error", format)
		}
	}
}
