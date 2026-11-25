// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"strings"

	"charm.land/lipgloss/v2"
)

// OverlayCenter draws box over the middle of background, leaving the rows above
// and below it and the columns to its left untouched. A background shorter than
// h rows is padded so the box is never clipped. Text to the right of the
// box on its rows is dropped.
func OverlayCenter(background, box string, w, h int) string {
	bgLines := strings.Split(background, "\n")
	for len(bgLines) < h {
		bgLines = append(bgLines, "")
	}
	boxLines := strings.Split(box, "\n")

	boxW := 0
	for _, l := range boxLines {
		boxW = max(boxW, lipgloss.Width(l))
	}
	startRow := (h - len(boxLines)) / 2
	startCol := max((w-boxW)/2, 0)

	for i, boxLine := range boxLines {
		row := startRow + i
		if row < 0 || row >= len(bgLines) {
			continue
		}
		prefix := TruncateANSI(bgLines[row], startCol)
		if pad := startCol - lipgloss.Width(prefix); pad > 0 {
			prefix += strings.Repeat(" ", pad)
		}
		bgLines[row] = prefix + "\x1b[0m" + boxLine
	}
	return strings.Join(bgLines, "\n")
}
