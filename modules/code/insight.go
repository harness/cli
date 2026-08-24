// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package code

import (
	"fmt"
	"io"
	"strings"

	"github.com/harness/cli/pkg/cmdctx"
	"github.com/harness/cli/pkg/console"
)

const (
	reviewGroupTextFormatterID = "pr_review_group_text"
	insightTextFormatterID     = "pr_insight_text"
)

// reviewGroupTextFormatter renders the risk-bucketed review groups for a pull
// request as a readable report: one block per group with its title, risk,
// description, and the full list of changed file paths.
func reviewGroupTextFormatter(w io.Writer, d cmdctx.DataAccessor) error {
	groups := d.GetSlice("it.groups")
	if len(groups) == 0 {
		fmt.Fprintln(w, "No review groups.")
	}
	for _, raw := range groups {
		g, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		title, _ := g["title"].(string)
		desc, _ := g["description"].(string)
		var risk string
		if tags, ok := g["tags"].(map[string]any); ok {
			risk, _ = tags["risk"].(string)
		}
		riskTag := ""
		if risk != "" {
			riskTag = fmt.Sprintf(" [%s]", risk)
		}
		line := fmt.Sprintf("● %s%s", title, riskTag)
		if c := riskColor(risk); c != 0 {
			line = console.WithColor(c, line)
		}
		fmt.Fprintf(w, "\n%s\n", line)
		if desc != "" {
			fmt.Fprintln(w, desc)
		}
		files, _ := g["files"].([]any)
		if len(files) == 0 {
			continue
		}
		fmt.Fprintln(w, "Files:")
		for _, fRaw := range files {
			fm, ok := fRaw.(map[string]any)
			if !ok {
				continue
			}
			if path, ok := fm["path"].(string); ok {
				fmt.Fprintf(w, "  - %s\n", path)
			}
		}
	}
	if url := d.GetString("url(it)"); url != "" {
		fmt.Fprintf(w, "\n%s\n", url)
	}
	return nil
}

// riskColor maps a risk bucket ("low"/"medium"/"high", case-insensitive) to the
// color it's displayed in. Returns 0 (no color) for any other value, including empty.
func riskColor(risk string) console.Color {
	switch strings.ToLower(risk) {
	case "low":
		return console.ColorGreen
	case "medium":
		return console.ColorYellow
	case "high":
		return console.ColorRed
	default:
		return 0
	}
}

// insightTextFormatter renders the AI code-review overview for a pull request as a
// colorized (by risk) header/footer section marker around the review content.
// The content is printed verbatim: wrapping is left to the terminal so words and
// URLs are never split, and the output stays copy-paste clean.
func insightTextFormatter(w io.Writer, d cmdctx.DataAccessor) error {
	renderInsight(w, d.GetString("it.risk"), d.GetString("it.content"))
	return nil
}

// renderInsight is the shared rendering step behind insightTextFormatter and
// DebugRenderInsightHandler, so the debug command exercises the exact same
// styling without needing a fake cmdctx.DataAccessor.
func renderInsight(w io.Writer, risk, content string) {
	heading := "AI Code Review"
	if risk != "" {
		heading += fmt.Sprintf(" [%s risk]", risk)
	}
	console.RenderTextBox(w, console.TextBox{
		Icon:        "✨",
		Header:      heading,
		HeaderColor: riskColor(risk),
		Text:        strings.TrimSpace(content),
	})
}
