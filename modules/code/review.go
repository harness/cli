// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package code

import (
	"fmt"

	"github.com/harness/cli/v3/pkg/client"
	"github.com/harness/cli/v3/pkg/cmdctx"
)

const reviewPRBodyFnID = "review_pr_body"

// cliToAPIReviewDecision maps the CLI's --decision values to the Code API's
// EnumPullReqReviewDecision values.
var cliToAPIReviewDecision = map[string]string{
	"approve":   "approved",
	"changereq": "changereq",
}

// reviewPRBodyFn builds the review-submission request body for execute pr:review.
// The API requires commit_sha as a safety check, so we fetch the PR first (same
// pattern as mergePRBodyFn).
func reviewPRBodyFn(ctx *cmdctx.Ctx) (any, error) {
	if len(ctx.IdParts) < 2 {
		return nil, fmt.Errorf("expected <repo_id>/<pr_number>")
	}
	repoID := ctx.IdParts[0]
	prNumber := ctx.IdParts[1]

	decision := cmdctx.GetString(ctx.FlagValues, "decision")
	apiDecision, ok := cliToAPIReviewDecision[decision]
	if !ok {
		return nil, fmt.Errorf("--decision must be %q or %q, got %q", "approve", "changereq", decision)
	}

	c := client.New(ctx)
	params := map[string]string{
		"accountIdentifier": ctx.Auth.AccountID,
		"orgIdentifier":     ctx.Auth.OrgID,
		"projectIdentifier": ctx.Auth.ProjectID,
	}
	raw, _, err := c.Get(fmt.Sprintf("/code/api/v1/repos/%s/pullreq/%s", repoID, prNumber), params)
	if err != nil {
		return nil, fmt.Errorf("fetching PR to get source SHA: %w", err)
	}

	m, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("unexpected PR response type")
	}
	sourceSHA, _ := m["source_sha"].(string)
	if sourceSHA == "" {
		return nil, fmt.Errorf("PR response missing source_sha")
	}

	return map[string]any{
		"commit_sha": sourceSHA,
		"decision":   apiDecision,
	}, nil
}
