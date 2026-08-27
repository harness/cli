// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package code

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/harness/cli/pkg/cmdctx"
	"github.com/harness/cli/pkg/console"
	"github.com/harness/cli/pkg/exprenv"
	"github.com/harness/cli/pkg/extractutil"
	"github.com/harness/cli/pkg/format"
	"github.com/harness/cli/pkg/hlog"
	"github.com/harness/cli/pkg/registry"
	"github.com/harness/cli/pkg/spec"
)

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

	activityEndpoint := &spec.EndpointSpec{
		Path: "/code/api/v1/repos/{{ctx.idParts[0]}}/pullreq/{{ctx.idParts[1]}}/activities",
	}
	activityData, err := registry.CallEndpoint(ctx, activityEndpoint)
	if err != nil {
		hlog.Warn("activity fetch failed, omitting comments summary from get pr", "err", err)
		activityData = nil
	}

	return renderPR(ctx, baseSpec, pr, insightSpec, insightData, activityData)
}

// renderPR prints "get pr" output in parts so the AI Code Review (Insight) and
// comments sections can sit where they belong: labeled fields, then the insight
// section (best-effort — a failure here still only omits it), then the description
// text block, then a collapsed comments summary (best-effort), then the footer.
func renderPR(ctx *cmdctx.Ctx, baseSpec *spec.CommandSpec, pr any, insightSpec *spec.CommandSpec, insightData any, activityData any) error {
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

	if insightSpec != nil {
		sectionData := extractutil.MakeDataAccessor(exprEnv, insightData)
		if err := insightTextFormatter(w, sectionData); err != nil {
			hlog.Warn("insight render failed, omitting from get pr", "err", err)
		}
	}

	if desc := strings.TrimSpace(data.GetString("it.description")); desc != "" {
		fmt.Fprintf(w, "\n%s\n", console.RenderMarkdown(desc))
	}

	if activities, ok := activityData.([]any); ok {
		fmt.Fprintln(w)
		renderCommentsSummary(w, activities, time.Now(), "harness list pr_comment "+ctx.Id)
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

	when := d.GetInt64("it.created")
	if merged := d.GetInt64("it.merged"); merged > 0 {
		when = merged
	}

	fmt.Fprintf(w, "%s %s  %s\n",
		console.WithBold(title),
		fmt.Sprintf("#%d", number),
		relativeTime(when),
	)

	author := d.GetString("it.author.display_name")
	source := d.GetString("it.source_branch")
	target := d.GetString("it.target_branch")
	fmt.Fprintf(w, "%s • %s wants to merge %s into %s\n", 
	console.WithColor(prStateColor(badge), strings.ToUpper(badge)),author, source, target)

	files := d.GetInt64("it.stats.files_changed")
	commits := d.GetInt64("it.stats.commits")
	additions := d.GetInt64("it.stats.additions")
	deletions := d.GetInt64("it.stats.deletions")
	fmt.Fprintf(w, "%d file%s changed · %d commit%s · %s %s ",
		files, plural(files), commits, plural(commits),
		console.WithColor(console.ColorGreen, fmt.Sprintf("+%d", additions)),
		console.WithColor(console.ColorRed, fmt.Sprintf("-%d", deletions)),
	)

	var extras []string
	if check := d.GetString("it.merge_check_status"); check != "" {
		extras = append(extras, checkStatusText(check))
	}
	if required := d.GetInt64("it.stats.reviews.required_count"); required > 0 {
		approved := d.GetInt64("it.stats.reviews.latest_approvals")
		extras = append(extras, fmt.Sprintf("Reviews: %d/%d approved", approved, required))
	}
	if len(extras) > 0 {
		fmt.Fprintln(w, strings.Join(extras, " · "))
	}

	fmt.Fprintln(w)
}

// relativeTime renders an epoch-ms timestamp as a coarse "N units ago" string,
// gh pr view style. Returns "" for a zero/missing timestamp.
func relativeTime(epochMs int64) string {
	if epochMs <= 0 {
		return ""
	}
	d := time.Since(time.UnixMilli(epochMs))
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		n := int64(d.Minutes())
		return fmt.Sprintf("• %d min%s ago", n, plural(n))
	case d < 24*time.Hour:
		n := int64(d.Hours())
		return fmt.Sprintf("• %d hr%s ago", n, plural(n))
	case d < 30*24*time.Hour:
		n := int64(d.Hours() / 24)
		return fmt.Sprintf("• %d day%s ago", n, plural(n))
	case d < 365*24*time.Hour:
		n := int64(d.Hours() / 24 / 30)
		return fmt.Sprintf("• %d mon%s ago", n, plural(n))
	default:
		n := int64(d.Hours() / 24 / 365)
		return fmt.Sprintf("• %d yr%s ago", n, plural(n))
	}
}

// checkStatusText renders a merge_check_status value as a colorized one-liner,
// following gh pr view's "✓ Checks passing" style.
func checkStatusText(status string) string {
	switch strings.ToLower(status) {
	case "mergeable":
		return console.WithColor(console.ColorGreen, "• ✓ Checks passing")
	case "unchecked", "checking":
		return console.WithColor(console.ColorYellow, "• … Checks running")
	default:
		return console.WithColor(console.ColorRed, "• ✗ Checks failing ("+status+")")
	}
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
