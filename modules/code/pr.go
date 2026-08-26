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
	"github.com/harness/cli/pkg/extractutil"
	"github.com/harness/cli/pkg/format"
	"github.com/harness/cli/pkg/hlog"
	"github.com/harness/cli/pkg/registry"
	"github.com/harness/cli/pkg/spec"
)

// prHeaderFieldIDs are the noun fields folded into renderPRHeader's title/badge,
// branch, and stats lines, so they aren't repeated in the labeled-field dump below.
var prHeaderFieldIDs = map[string]bool{
	"number": true, "title": true, "state": true, "is_draft": true,
	"source_branch": true, "target_branch": true, "author": true,
	"commits": true, "files_changed": true, "additions": true, "deletions": true,
}

const getPRWorkflowID = "get_pr"

// isMachineFormat mirrors exprenv.isMachineFormat (unexported): these formats are
// meant for structured consumption, so the insight section (extra, ad hoc text) is skipped.
func isMachineFormat(format string) bool {
	switch format {
	case "json", "jsonl", "csv", "tsv", "markdown", "ui":
		return true
	}
	return false
}

// GetPRWorkflow implements "get pr". It fetches the base pull request (hard fail on
// error) and the "pr:insight" section (best-effort — a failure only omits it, it never
// fails the command), then hands everything fetched to renderPR.
func GetPRWorkflow(ctx *cmdctx.Ctx) error {
	baseSpec := ctx.Resolver.GetSpec("get", "pr")
	if baseSpec == nil || baseSpec.Endpoint == nil {
		return fmt.Errorf("get pr command spec not found")
	}

	if isMachineFormat(ctx.FormatFlags.Format) || cmdctx.GetBool(ctx.FlagValues, "list-fields") {
		_, err := registry.RunEndpoint(ctx, baseSpec.Endpoint)
		return err
	}

	pr, err := registry.CallEndpoint(ctx, baseSpec.Endpoint)
	if err != nil {
		return err
	}

	insightSpec := ctx.Resolver.GetSpec("get", "pr:insight")
	var insightData any
	if insightSpec == nil || insightSpec.Endpoint == nil {
		hlog.Warn("insight section spec not found, omitting from get pr")
		insightSpec = nil
	} else {
		insightData, err = registry.CallEndpoint(ctx, insightSpec.Endpoint)
		if err != nil {
			hlog.Warn("insight fetch failed, omitting from get pr", "err", err)
			insightSpec = nil
		}
	}

	return renderPR(ctx, baseSpec, pr, insightSpec, insightData)
}

// renderPR prints "get pr" output in three parts so the AI Code Review (Insight)
// section can sit between them: labeled fields, then the insight section (best-effort
// — a failure here still only omits it), then the description text block and footer.
func renderPR(ctx *cmdctx.Ctx, baseSpec *spec.CommandSpec, pr any, insightSpec *spec.CommandSpec, insightData any) error {
	w, closeW, err := format.OpenWriter(ctx.FormatFlags.OutFile)
	if err != nil {
		return err
	}
	defer closeW()

	exprEnv := exprenv.Make(ctx)
	interpolate := func(tmpl string, item any) string {
		s, _ := exprenv.ResolvePath(exprenv.WithIt(exprEnv, item), tmpl)
		return s
	}
	data := extractutil.MakeDataAccessor(exprEnv, pr)

	renderPRHeader(w, data)

	nounForFields := ctx.Noun
	if ctx.FieldsNoun != "" {
		nounForFields = ctx.FieldsNoun
	}
	var labeledFields []spec.FieldDef
	if nd := ctx.Resolver.GetNoun(nounForFields); nd != nil {
		for _, f := range nd.Fields {
			if f.FieldType == "multiline_text" || f.FieldType == "yaml" || prHeaderFieldIDs[f.ID] {
				continue
			}
			labeledFields = append(labeledFields, f)
		}
	}

	if err := format.BuildTextFieldFormatter(labeledFields, "", "", interpolate)(w, data); err != nil {
		return err
	}

	if insightSpec != nil {
		sectionData := extractutil.MakeDataAccessor(exprEnv, insightData)
		if err := insightTextFormatter(w, sectionData); err != nil {
			hlog.Warn("insight render failed, omitting from get pr", "err", err)
		}
	}

	if desc := strings.TrimSpace(data.GetString("it.description")); desc != "" {
		fmt.Fprintf(w, "\n%s\n", desc)
	}
	_, err = fmt.Fprint(w, interpolate(baseSpec.Endpoint.TextFooter, pr))
	return err
}

// renderPRHeader prints a gh-pr-view-style header: "#<number> <title>" with a
// colorized state/draft badge, then compact author/branch and stat summary lines.
func renderPRHeader(w io.Writer, d cmdctx.DataAccessor) {
	number := d.GetInt64("it.number")
	title := d.GetString("it.title")
	state := strings.ToLower(d.GetString("it.state"))

	badge := state
	if d.GetBool("it.is_draft") && state == "open" {
		badge = "draft"
	}
	fmt.Fprintf(w, "%s %s  %s\n",
	title,
	console.WithBold(fmt.Sprintf("#%d", number)),
	console.WithBoldColor(prStateColor(badge), strings.ToUpper(badge)),
	)

	author := d.GetString("it.author.display_name")
	source := d.GetString("it.source_branch")
	target := d.GetString("it.target_branch")
	fmt.Fprintf(w, "%s wants to merge %s into %s\n", author, source, target)

	files := d.GetInt64("it.stats.files_changed")
	commits := d.GetInt64("it.stats.commits")
	additions := d.GetInt64("it.stats.additions")
	deletions := d.GetInt64("it.stats.deletions")
	fmt.Fprintf(w, "%d file%s changed · %d commit%s · %s %s\n\n",
		files, plural(files), commits, plural(commits),
		console.WithColor(console.ColorGreen, fmt.Sprintf("+%d", additions)),
		console.WithColor(console.ColorRed, fmt.Sprintf("-%d", deletions)),
	)
}

// plural returns "s" unless n is exactly 1.
func plural(n int64) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// prStateColor maps a PR state/badge ("open"/"draft"/"merged"/"closed",
// case-insensitive) to the color it's displayed in, following riskColor's
// green/yellow/red conventions. Returns 0 (no color) for any other value.
func prStateColor(state string) console.Color {
	switch strings.ToLower(state) {
	case "open":
		return console.ColorGreen
	case "draft":
		return console.ColorYellow
	case "merged":
		return console.ColorMagenta
	case "closed":
		return console.ColorRed
	default:
		return 0
	}
}

// createPRBodyFn builds the pull request create body.
// Required: --set title=<title> source_branch=<branch> target_branch=<branch>
// Optional: --set description=<text>  OR  -f <file> for multi-line description.
func createPRBodyFn(ctx *cmdctx.Ctx) (any, error) {
	title := ctx.SetArgs["title"]
	if title == "" {
		return nil, fmt.Errorf("--set title=<title> is required")
	}
	sourceBranch := ctx.SetArgs["source_branch"]
	if sourceBranch == "" {
		return nil, fmt.Errorf("--set source_branch=<branch> is required")
	}
	targetBranch := ctx.SetArgs["target_branch"]
	if targetBranch == "" {
		targetBranch = "main"
	}

	description := ctx.SetArgs["description"]

	// -f / --file overrides --set description when provided
	if fileText, err := cmdctx.SlurpInputFile(ctx.FlagValues); err == nil && strings.TrimSpace(fileText) != "" {
		description = strings.TrimRight(fileText, "\n")
	}

	isDraft := ctx.SetArgs["is_draft"] == "true"

	body := map[string]any{
		"title":         title,
		"source_branch": sourceBranch,
		"target_branch": targetBranch,
		"description":   description,
		"is_draft":      isDraft,
	}
	return body, nil
}
