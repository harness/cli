// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package code

import (
	"fmt"
	"strings"

	"github.com/harness/cli/pkg/cmdctx"
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

	nounForFields := ctx.Noun
	if ctx.FieldsNoun != "" {
		nounForFields = ctx.FieldsNoun
	}
	var labeledFields []spec.FieldDef
	if nd := ctx.Resolver.GetNoun(nounForFields); nd != nil {
		for _, f := range nd.Fields {
			if f.FieldType == "multiline_text" || f.FieldType == "yaml" {
				continue
			}
			labeledFields = append(labeledFields, f)
		}
	}

	exprEnv := exprenv.Make(ctx)
	interpolate := func(tmpl string, item any) string {
		s, _ := exprenv.ResolvePath(exprenv.WithIt(exprEnv, item), tmpl)
		return s
	}
	data := extractutil.MakeDataAccessor(exprEnv, pr)

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
