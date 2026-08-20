// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package code

import (
	"github.com/harness/cli/pkg/cmdctx"
	"github.com/harness/cli/pkg/endpoint"
	"github.com/harness/cli/pkg/spec"
)

const codeownersPRFetchFnID = "codeowners_pr_fetch"

// codeownersPRFetchFn delegates to HTTPFetchFn (which wraps the single
// TypesCodeOwnerEvaluation response as a one-item list via items_expr: "[it]"),
// then flattens evaluation_entries/owner_evaluations/user_group_owner_evaluations
// into one flat row per (pattern, owner) for the pr_codeowner noun's fields to consume.
func codeownersPRFetchFn(ctx *cmdctx.Ctx, ep *spec.EndpointSpec, wantStart, wantCount int, cursor any) (*cmdctx.PageResult, error) {
	result, err := endpoint.HTTPFetchFn(ctx, ep, wantStart, wantCount, cursor)
	if err != nil {
		return nil, err
	}
	var rows []any
	for _, raw := range result.Items {
		m, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		entries, _ := m["evaluation_entries"].([]any)
		for _, e := range entries {
			entry, ok := e.(map[string]any)
			if !ok {
				continue
			}
			pattern, _ := entry["pattern"].(string)

			owners, _ := entry["owner_evaluations"].([]any)
			for _, o := range owners {
				rows = append(rows, ownerRow(pattern, "user", "", o))
			}

			groups, _ := entry["user_group_owner_evaluations"].([]any)
			for _, g := range groups {
				gm, ok := g.(map[string]any)
				if !ok {
					continue
				}
				groupName, _ := gm["name"].(string)
				evals, _ := gm["evaluations"].([]any)
				for _, o := range evals {
					rows = append(rows, ownerRow(pattern, "group", groupName, o))
				}
			}
		}
	}
	result.Items = rows
	result.Last = true
	return result, nil
}

// ownerRow builds one flat pr_codeowner row from a TypesOwnerEvaluation-shaped map.
func ownerRow(pattern, ownerType, groupName string, raw any) map[string]any {
	om, _ := raw.(map[string]any)
	owner, _ := om["owner"].(map[string]any)
	return map[string]any{
		"pattern":         pattern,
		"owner_type":      ownerType,
		"display_name":    owner["display_name"],
		"email":           owner["email"],
		"group_name":      groupName,
		"review_decision": om["review_decision"],
	}
}
