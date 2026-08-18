// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package code

import (
	"fmt"
	"io"

	"github.com/harness/cli/pkg/cmdctx"
	"github.com/harness/cli/pkg/exprenv"
	"github.com/harness/cli/pkg/hlog"
	"github.com/harness/cli/pkg/registry"
)

const (
	getPRWorkflowID            = "get_pr"
	reviewGroupTextFormatterID = "pr_review_group_text"
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
		fmt.Printf("\n--- %s ---\n", section.label)
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
	for i, raw := range groups {
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
		fmt.Fprintf(w, "\nGroup %d: %s", i+1, title)
		if risk != "" {
			fmt.Fprintf(w, " [risk: %s]", risk)
		}
		fmt.Fprintln(w)
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
