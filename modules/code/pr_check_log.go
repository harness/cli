// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package code

import (
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/harness/cli/pkg/cmdctx"
	"github.com/harness/cli/pkg/endpoint"
	"github.com/harness/cli/pkg/exprenv"
	"github.com/harness/cli/pkg/extractutil"
	"github.com/harness/cli/pkg/logstream"
)

const getPRCheckLogWorkflowID = "get_pr_check_log"
const getPRCheckLogItemFnID = "get_pr_check_log_item"

// resolvePRCheck resolves <repo_id>/<pr_number>/<check_identifier> to the matching item
// from "list pr_check", by fetching the full list and matching identifier case-insensitively.
func resolvePRCheck(ctx *cmdctx.Ctx) (repoID, prNumber, checkIdentifier string, match any, err error) {
	if len(ctx.IdParts) != 3 || ctx.IdParts[0] == "" || ctx.IdParts[1] == "" || ctx.IdParts[2] == "" {
		return "", "", "", nil, fmt.Errorf("expected <repo_id>/<pr_number>/<check_identifier>")
	}
	repoID, prNumber, checkIdentifier = ctx.IdParts[0], ctx.IdParts[1], ctx.IdParts[2]

	listSpec := ctx.Resolver.GetSpec("list", "pr_check")
	if listSpec == nil || listSpec.Endpoint == nil {
		return "", "", "", nil, fmt.Errorf("list pr_check command spec not found")
	}

	listCtx := *ctx
	listCtx.ParentId = repoID + "/" + prNumber
	items, _, err := endpoint.FetchItems(&listCtx, listSpec.Endpoint, cmdctx.PagingFlags{All: true})
	if err != nil {
		return "", "", "", nil, fmt.Errorf("fetching checks for %s/%s: %w", repoID, prNumber, err)
	}

	exprEnv := exprenv.Make(ctx)
	var available []string
	for _, item := range items {
		data := extractutil.MakeDataAccessor(exprEnv, item)
		identifier := data.GetString("it.check.identifier")
		available = append(available, identifier)
		if strings.EqualFold(identifier, checkIdentifier) {
			match = item
			break
		}
	}
	if match == nil {
		return "", "", "", nil, fmt.Errorf("no check %q found on %s/%s (available: %s)", checkIdentifier, repoID, prNumber, strings.Join(available, ", "))
	}
	return repoID, prNumber, checkIdentifier, match, nil
}

// pipelineCoords is the pipeline execution location backing a PR check, extracted from its
// check payload. Shared by getPRCheckLogHandler (live tail) and getPRCheckLogItemFn (snapshot).
type pipelineCoords struct {
	org, project, pipelineID, executionID, stageID string
}

func pipelineCoordsFromCheck(ctx *cmdctx.Ctx, match any, checkIdentifier string) (pipelineCoords, error) {
	exprEnv := exprenv.Make(ctx)
	data := extractutil.MakeDataAccessor(exprEnv, match)
	pc := pipelineCoords{
		org:         data.GetString("it.check.payload.data.org_identifier"),
		project:     data.GetString("it.check.payload.data.project_identifier"),
		pipelineID:  data.GetString("it.check.payload.data.pipeline_identifier"),
		executionID: data.GetString("it.check.payload.data.execution_id"),
		stageID:     data.GetString("it.check.payload.data.stage_identifier"),
	}
	if pc.pipelineID == "" || pc.executionID == "" {
		return pc, fmt.Errorf("check %q has no associated pipeline execution (not a Harness pipeline check)", checkIdentifier)
	}
	return pc, nil
}

func scopedCtxForPipeline(ctx *cmdctx.Ctx, pc pipelineCoords) *cmdctx.Ctx {
	scopedAuth := *ctx.Auth
	scopedAuth.OrgID = pc.org
	scopedAuth.ProjectID = pc.project
	scopedCtx := *ctx
	scopedCtx.Auth = &scopedAuth
	return &scopedCtx
}

// getPRCheckLogHandler implements "get pr_check:log". It resolves a PR status check
// to its backing pipeline execution and fetches that execution's stage logs, scoped
// to the pipeline's own org/project (which may differ from the ambient auth scope).
func getPRCheckLogHandler(ctx *cmdctx.Ctx) error {
	_, _, checkIdentifier, match, err := resolvePRCheck(ctx)
	if err != nil {
		return err
	}

	pc, err := pipelineCoordsFromCheck(ctx, match, checkIdentifier)
	if err != nil {
		return err
	}

	fmt.Fprintf(os.Stderr, "check:      %s\n", checkIdentifier)
	fmt.Fprintf(os.Stderr, "pipeline:   %s  (org: %s, project: %s)\n", pc.pipelineID, pc.org, pc.project)
	fmt.Fprintf(os.Stderr, "execution:  %s\n", pc.executionID)
	fmt.Fprintf(os.Stderr, "stage:      %s\n", pc.stageID)

	scopedCtx := scopedCtxForPipeline(ctx, pc)
	if err := logstream.FollowMulti(scopedCtx, pc.executionID, pc.stageID, "", logstream.MultiStyleMarkers, nil); err != nil {
		return fmt.Errorf("fetching logs from %s/%s: %w (you may not have access to this org/project)", pc.org, pc.project, err)
	}
	return nil
}

// getPRCheckLogItemFn resolves "get pr_check:log"'s target check to a one-shot snapshot of
// its pipeline stage's logs. Registered as the spec's item_fn so the TUI's detail-pane
// drilldown (pkg/registry/uitableview.go's fetchDetail) can render something for the "get
// check logs" pane inline; unlike getPRCheckLogHandler it fetches once rather than following,
// since a still-open text pane has no mechanism to tail forever.
func getPRCheckLogItemFn(ctx *cmdctx.Ctx) (any, error) {
	_, _, checkIdentifier, match, err := resolvePRCheck(ctx)
	if err != nil {
		return nil, err
	}

	pc, err := pipelineCoordsFromCheck(ctx, match, checkIdentifier)
	if err != nil {
		return nil, err
	}

	scopedCtx := scopedCtxForPipeline(ctx, pc)
	entries, _, err := logstream.FetchLogKeys(scopedCtx, pc.executionID)
	if err != nil {
		return nil, fmt.Errorf("fetching logs from %s/%s: %w (you may not have access to this org/project)", pc.org, pc.project, err)
	}
	entries = logstream.StageSubtreeEntries(entries, pc.stageID)

	hc := &http.Client{Timeout: 30 * time.Second}
	fmtFlag := ctx.FormatFlags.Format
	multiLog := len(entries) > 1
	var out strings.Builder
	for _, e := range entries {
		label := e.FQN
		if label == "" {
			label = e.LogKey
		}
		var buf strings.Builder
		hasContent, fetchErr := logstream.FetchAndPrintLog(hc, scopedCtx.Auth, e.LogKey, fmtFlag, ctx.IsPty, &buf)
		if fetchErr != nil {
			fmt.Fprintf(&out, "== %s ==\n(error: %v)\n", label, fetchErr)
			continue
		}
		if !hasContent {
			continue
		}
		if multiLog {
			fmt.Fprintf(&out, "== %s ==\n", label)
		}
		out.WriteString(buf.String())
	}

	logs := out.String()
	if logs == "" {
		logs = "(no log content yet)"
	}
	return map[string]any{"logs": logs}, nil
}
