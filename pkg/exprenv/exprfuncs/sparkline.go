// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package exprfuncs

import (
	"strings"

	"github.com/harness/cli/pkg/format"
)

const sparklineWidth = 10

func sparklineGlyph(status string, isPty bool) string {
	s := format.BucketStyles[format.ClassifyExecutionStatus(status)]
	if isPty {
		return s.SparklinePty
	}
	return s.SparklinePlain
}

// NewPipelineSparkline returns a sparkline closure using ANSI escapes (pty) or plain ASCII.
func NewPipelineSparkline(isPty bool) func(any) string {
	noData := format.BucketStyles[format.StatusNoData]
	pad := noData.SparklinePlain
	if isPty {
		pad = noData.SparklinePty
	}
	return func(v any) string {
		items, ok := v.([]any)
		if !ok || len(items) == 0 {
			return strings.Repeat(pad, sparklineWidth)
		}

		n := len(items)
		if n > sparklineWidth {
			n = sparklineWidth
			items = items[:n]
		}

		glyphs := make([]string, sparklineWidth)
		padCount := sparklineWidth - n
		for i := range padCount {
			glyphs[i] = pad
		}
		// items[0] is most recent → rightmost; items[n-1] is oldest → leftmost
		for i := 0; i < n; i++ {
			status := ""
			if m, ok := items[n-1-i].(map[string]any); ok {
				if s, ok := m["execution_status"].(string); ok {
					status = s
				}
			}
			glyphs[padCount+i] = sparklineGlyph(status, isPty)
		}

		return strings.Join(glyphs, "")
	}
}
