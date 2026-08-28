// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package code

import (
	"fmt"
	"os"
	"strings"

	"github.com/harness/cli/pkg/cmdctx"
	"github.com/harness/cli/pkg/endpoint"
	"github.com/harness/cli/pkg/exprenv"
	"github.com/harness/cli/pkg/extractutil"
	"github.com/harness/cli/pkg/logstream"
)

const getPRCheckLogWorkflowID = "get_pr_check_log"

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

// getPRCheckLogHandler implements "get pr_check:log". It resolves a PR status check
// to its backing pipeline execution and fetches that execution's stage logs, scoped
// to the pipeline's own org/project (which may differ from the ambient auth scope).
func getPRCheckLogHandler(ctx *cmdctx.Ctx) error {
	_, _, checkIdentifier, match, err := resolvePRCheck(ctx)
	if err != nil {
		return err
	}

	exprEnv := exprenv.Make(ctx)
	data := extractutil.MakeDataAccessor(exprEnv, match)
	pipelineOrg := data.GetString("it.check.payload.data.org_identifier")
	pipelineProject := data.GetString("it.check.payload.data.project_identifier")
	pipelineID := data.GetString("it.check.payload.data.pipeline_identifier")
	executionID := data.GetString("it.check.payload.data.execution_id")
	stageID := data.GetString("it.check.payload.data.stage_identifier")

	if pipelineID == "" || executionID == "" {
		return fmt.Errorf("check %q has no associated pipeline execution (not a Harness pipeline check)", checkIdentifier)
	}

	fmt.Fprintf(os.Stderr, "check:      %s\n", checkIdentifier)
	fmt.Fprintf(os.Stderr, "pipeline:   %s  (org: %s, project: %s)\n", pipelineID, pipelineOrg, pipelineProject)
	fmt.Fprintf(os.Stderr, "execution:  %s\n", executionID)
	fmt.Fprintf(os.Stderr, "stage:      %s\n", stageID)

	scopedAuth := *ctx.Auth
	scopedAuth.OrgID = pipelineOrg
	scopedAuth.ProjectID = pipelineProject
	scopedCtx := *ctx
	scopedCtx.Auth = &scopedAuth

	if err := logstream.FollowMulti(&scopedCtx, executionID, stageID, "", logstream.MultiStyleMarkers, nil); err != nil {
		return fmt.Errorf("fetching logs from %s/%s: %w (you may not have access to this org/project)", pipelineOrg, pipelineProject, err)
	}
	return nil
}
