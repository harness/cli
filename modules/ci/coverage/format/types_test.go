// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package format

import "testing"

func TestNewCoverageParser(t *testing.T) {
	for _, format := range []string{
		FormatGo, FormatLCOV, FormatJacoco,
		FormatClover, FormatCobertura, FormatCoveralls,
	} {
		if _, err := NewParser(format); err != nil {
			t.Errorf("NewParser(%q) = %v, want nil error", format, err)
		}
	}

	for _, format := range []string{"unknown", ""} {
		if _, err := NewParser(format); err == nil {
			t.Errorf("NewParser(%q) = nil error, want error", format)
		}
	}
}
