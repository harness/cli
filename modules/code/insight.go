// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package code

import (
	"fmt"

	"github.com/harness/cli/pkg/cmdctx"
	"github.com/harness/cli/pkg/hlog"
	"github.com/harness/cli/pkg/registry"
)

const getPRWorkflowID = "get_pr"

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
	if _, err := registry.RunEndpoint(ctx, baseSpec.Endpoint); err != nil {
		return err
	}

	if isMachineFormat(ctx.FormatFlags.Format) || cmdctx.GetBool(ctx.FlagValues, "list-fields") {
		return nil
	}

	origNoun, origFieldsNoun := ctx.Noun, ctx.FieldsNoun
	defer func() { ctx.Noun, ctx.FieldsNoun = origNoun, origFieldsNoun }()

	for _, section := range insightSections {
		cs := ctx.Resolver.GetSpec(section.verb, section.noun)
		if cs == nil || cs.Endpoint == nil {
			hlog.Warn("aicr section spec not found, omitting from get pr", "verb", section.verb, "noun", section.noun)
			continue
		}
		ctx.Noun, ctx.FieldsNoun = cs.Noun, cs.FieldsNoun
		// Probe with a fetch-only call first so a failure never prints a section
		// header with nothing under it; RunEndpoint's own render then re-fetches
		// (cheap: these are all idempotent GETs).
		if _, err := registry.CallEndpoint(ctx, cs.Endpoint); err != nil {
			hlog.Warn("aicr fetch failed, omitting from get pr", "section", section.label, "err", err)
			continue
		}
		fmt.Printf("\n--- %s ---\n", section.label)
		if _, err := registry.RunEndpoint(ctx, cs.Endpoint); err != nil {
			hlog.Warn("aicr fetch failed, omitting from get pr", "section", section.label, "err", err)
		}
	}
	return nil
}
