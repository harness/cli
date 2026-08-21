// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package console

import (
	"fmt"
	"io"
)

// TextBox is a lightweight section marker: a colorized header line (optionally
// preceded by Icon, suppressed when stdout isn't a TTY), then Text verbatim,
// then a dimmed fixed-width closing rule.
type TextBox struct {
	Icon        string
	Header      string
	HeaderColor Color
	Text        string
}

// RenderTextBox writes box to w. Text is written verbatim — callers must not
// wrap it themselves; wrapping is left to the terminal so words and URLs are
// never split, and the output stays copy-paste clean.
func RenderTextBox(w io.Writer, box TextBox) {
	header := box.Header + ":"
	if box.Icon != "" && ensureStdoutTTY() {
		header = box.Icon + " " + header
	}
	fmt.Fprintln(w, WithBoldColor(box.HeaderColor, header))
	fmt.Fprintln(w, box.Text)
	fmt.Fprintln(w, WithColor(ColorBrightBlack, "──────────"))
}
