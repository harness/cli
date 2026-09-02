// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package console

import (
	"regexp"
	"strings"
)

var (
	mdFence      = regexp.MustCompile("^```")
	mdHeader     = regexp.MustCompile(`^(#{1,6})\s+(.*)$`)
	mdBlockquote = regexp.MustCompile(`^>\s?(.*)$`)
	mdBullet     = regexp.MustCompile(`^(\s*)[-*+]\s+(.*)$`)
	mdNumbered   = regexp.MustCompile(`^(\s*)(\d+)([.)])\s+(.*)$`)

	mdInlineCode = regexp.MustCompile("`([^`]+)`")
	mdLink       = regexp.MustCompile(`\[([^\]]+)\]\(([^)]+)\)`)
	mdBold       = regexp.MustCompile(`\*\*([^*]+)\*\*|__([^_]+)__`)
	mdItalic     = regexp.MustCompile(`\*([^*]+)\*`)
)

// RenderMarkdown renders a lightweight subset of Markdown (bold, italic, inline
// code, fenced code blocks, headers, lists, blockquotes, links) as ANSI-styled
// plain text for terminal display. It is a line-oriented best-effort renderer,
// not a full CommonMark implementation: nested/overlapping emphasis, tables,
// raw HTML, footnotes/reference-style links, checkbox lists, and cross-line
// paragraph rewrapping are not specially handled and degrade to plain text.
// Styling delegates to the With* helpers in this package, which already no-op
// to plain text when stdout isn't a TTY.
func RenderMarkdown(text string) string {
	lines := strings.Split(text, "\n")
	out := make([]string, 0, len(lines))
	inFence := false

	for _, line := range lines {
		switch {
		case mdFence.MatchString(strings.TrimSpace(line)):
			inFence = !inFence
		case inFence:
			out = append(out, WithColor(ColorBrightBlack, "  "+line))
		case mdHeader.MatchString(line):
			m := mdHeader.FindStringSubmatch(line)
			hashes := m[1]
			if len(hashes) <= 2 {
				out = append(out, hashes+" "+WithBoldColor(ColorCyan, renderInline(m[2])))
			} else {
				out = append(out, hashes+" "+WithColor(ColorCyan, renderInline(m[2])))
			}
		case mdBlockquote.MatchString(line):
			m := mdBlockquote.FindStringSubmatch(line)
			out = append(out, WithColor(ColorBrightBlack, "│ "+renderInline(m[1])))
		case mdBullet.MatchString(line):
			m := mdBullet.FindStringSubmatch(line)
			out = append(out, m[1]+"• "+renderInline(m[2]))
		case mdNumbered.MatchString(line):
			m := mdNumbered.FindStringSubmatch(line)
			out = append(out, m[1]+m[2]+m[3]+" "+renderInline(m[4]))
		default:
			out = append(out, renderInline(line))
		}
	}

	return strings.Join(out, "\n")
}

// renderInline applies inline styling (code spans, links, bold, italic) to a
// single line of non-code, non-heading text. Order matters: code spans are
// protected first so markers inside them are never treated as emphasis, then
// links, then bold before italic (so "**x**" isn't partially matched by the
// italic regex).
func renderInline(line string) string {
	line = mdLink.ReplaceAllStringFunc(line, func(m string) string {
		sub := mdLink.FindStringSubmatch(m)
		text, url := sub[1], sub[2]
		return WithColor(ColorBrightBlue, text) + " (" + WithColor(ColorBrightBlack, url) + ")"
	})
	line = mdInlineCode.ReplaceAllStringFunc(line, func(m string) string {
		inner := mdInlineCode.FindStringSubmatch(m)[1]
		return WithHighlight(inner)
	})
	line = mdBold.ReplaceAllStringFunc(line, func(m string) string {
		sub := mdBold.FindStringSubmatch(m)
		if sub[1] != "" {
			return WithBold(sub[1])
		}
		return WithBold(sub[2])
	})
	line = mdItalic.ReplaceAllStringFunc(line, func(m string) string {
		sub := mdItalic.FindStringSubmatch(m)
		return WithItalic(sub[1])
	})
	return line
}
