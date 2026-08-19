// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package code

import (
	"fmt"
	"io"
	"strings"

	"github.com/harness/cli/pkg/cmdctx"
	"github.com/harness/cli/pkg/console"
	"github.com/harness/cli/pkg/exprenv"
	"github.com/harness/cli/pkg/hlog"
	"github.com/harness/cli/pkg/registry"
)

const (
	getPRWorkflowID            = "get_pr"
	reviewGroupTextFormatterID = "pr_review_group_text"
	insightTextFormatterID     = "pr_insight_text"
)

// insightSections lists the Harness Code review-insight sub-commands appended to "get pr" output,
// each best-effort: a failure only omits that section, it never fails the command.
var insightSections = []struct {
	verb, noun, label string
}{
	{"get", "pr:insight", "Insight"},
}

// isMachineFormat mirrors exprenv.isMachineFormat (unexported): these formats are
// meant for structured consumption, so insight sections (extra, ad hoc text) are skipped.
func isMachineFormat(format string) bool {
	switch format {
	case "json", "jsonl", "csv", "tsv", "markdown", "ui":
		return true
	}
	return false
}

// GetPRWorkflow implements "get pr". It fetches and renders the base pull request
// exactly as the old handler_type: endpoint command did (hard fail on error, unchanged
// output), then best-effort appends Harness Code review-insight sections below it. Any insight
// endpoint failure is logged as a warning and the section is omitted — it never fails
// the command.
func GetPRWorkflow(ctx *cmdctx.Ctx) error {
	baseSpec := ctx.Resolver.GetSpec("get", "pr")
	if baseSpec == nil || baseSpec.Endpoint == nil {
		return fmt.Errorf("get pr command spec not found")
	}

	if isMachineFormat(ctx.FormatFlags.Format) || cmdctx.GetBool(ctx.FlagValues, "list-fields") {
		_, err := registry.RunEndpoint(ctx, baseSpec.Endpoint)
		return err
	}

	// Fetch (hard-fail on error, same as before) but don't render yet — the base
	// PR block now prints last, under "PR Details", after the Insight section.
	pr, err := registry.CallEndpoint(ctx, baseSpec.Endpoint)
	if err != nil {
		return err
	}

	origNoun, origFieldsNoun := ctx.Noun, ctx.FieldsNoun
	defer func() { ctx.Noun, ctx.FieldsNoun = origNoun, origFieldsNoun }()

	for _, section := range insightSections {
		cs := ctx.Resolver.GetSpec(section.verb, section.noun)
		if cs == nil || cs.Endpoint == nil {
			hlog.Warn("insight section spec not found, omitting from get pr", "verb", section.verb, "noun", section.noun)
			continue
		}
		ctx.Noun, ctx.FieldsNoun = cs.Noun, cs.FieldsNoun
		// Probe with a fetch-only call first so a failure never prints a section
		// header with nothing under it; RunEndpoint's own render then re-fetches
		// (cheap: these are all idempotent GETs).
		if _, err := registry.CallEndpoint(ctx, cs.Endpoint); err != nil {
			hlog.Warn("insight fetch failed, omitting from get pr", "section", section.label, "err", err)
			continue
		}
		ep := *cs.Endpoint
		ep.TextFooter = ""
		if _, err := registry.RunEndpoint(ctx, &ep); err != nil {
			hlog.Warn("insight fetch failed, omitting from get pr", "section", section.label, "err", err)
		}
	}

	ctx.Noun, ctx.FieldsNoun = origNoun, origFieldsNoun

	fmt.Print("\n--- PR Details ---\n")
	baseEP := *baseSpec.Endpoint
	baseEP.TextFooter = ""
	if _, err := registry.RunEndpoint(ctx, &baseEP); err != nil {
		return err
	}

	if footer := baseSpec.Endpoint.TextFooter; footer != "" {
		env := exprenv.WithIt(exprenv.Make(ctx), pr)
		if text, err := exprenv.ResolvePath(env, footer); err == nil {
			fmt.Print(text)
		}
	}
	return nil
}

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
		bullet := "●"
		riskTag := ""
		if risk != "" {
			riskTag = fmt.Sprintf(" [%s]", risk)
		}
		if c := riskColor(risk); c != 0 {
			bullet = console.WithColor(c, bullet)
			riskTag = console.WithColor(c, riskTag)
		}
		fmt.Fprintf(w, "\n%s %s%s\n", bullet, title, riskTag)
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
// box: the heading (colorized by risk) sits in the top border, and the review
// content is word-wrapped inside.
func insightTextFormatter(w io.Writer, d cmdctx.DataAccessor) error {
	risk := d.GetString("it.risk")
	content := d.GetString("it.content")

	heading := "AI Code Overview"
	if risk != "" {
		heading += fmt.Sprintf(" [%s]", risk)
	}
	styledHeading := heading
	if c := riskColor(risk); c != 0 {
		styledHeading = console.WithColor(c, heading)
	}

	boxWidth := min(max(console.TerminalWidth(80), 40), 150)
	contentWidth := boxWidth - 4

	fillLen := boxWidth - len([]rune(heading))-6
	if fillLen < 1 {
		fillLen = 1
	}
	fmt.Fprintf(w, "┌── %s %s┐\n", styledHeading, strings.Repeat("─", fillLen))

	for _, line := range wrapText(content, contentWidth) {
		fmt.Fprintf(w, "│ %-*s │\n", contentWidth, line)
	}

	fmt.Fprintf(w, "└%s┘\n", strings.Repeat("─", boxWidth-2))
	return nil
}

// wrapText word-wraps s to width, treating each existing line as its own
// paragraph so blank lines are preserved as paragraph breaks.
func wrapText(s string, width int) []string {
	if width < 1 {
		width = 1
	}
	var lines []string
	for _, paragraph := range strings.Split(s, "\n") {
		if strings.TrimSpace(paragraph) == "" {
			lines = append(lines, "")
			continue
		}
		var cur string
		for _, word := range strings.Fields(paragraph) {
			switch {
			case cur == "":
				cur = word
			case len([]rune(cur))+1+len([]rune(word)) <= width:
				cur += " " + word
			default:
				lines = append(lines, cur)
				cur = word
			}
		}
		if cur != "" {
			lines = append(lines, cur)
		}
	}
	return lines
}
