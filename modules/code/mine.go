// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package code

import (
	"fmt"
	"maps"

	"github.com/harness/cli/pkg/cmdctx"
	"github.com/harness/cli/pkg/endpoint"
	"github.com/harness/cli/pkg/spec"
)

const (
	listMinePRQueryParamsFnID = "list_mine_pr_query_params"
	listMinePRFetchFnID       = "list_mine_pr_fetch"
)

// listMinePRQueryParamsFn resolves the current user's Code numeric principal ID
// and returns it as the author_id query param for the cross-repo PR list endpoint.
func listMinePRQueryParamsFn(ctx *cmdctx.Ctx) (map[string]string, error) {
	id, err := CurrentUserPrincipalID(ctx)
	if err != nil {
		return nil, err
	}
	return map[string]string{"author_id": fmt.Sprintf("%d", id)}, nil
}

// listMinePRFetchFn delegates to HTTPFetchFn (which picks up the author_id via
// query_params_fn), then flattens each response item from
// {"pull_request": {...}, "repository": {...}} into a single map with
// "repository" nested inside, matching what the pr noun fields expect.
func listMinePRFetchFn(ctx *cmdctx.Ctx, ep *spec.EndpointSpec, wantStart, wantCount int, cursor any) (*cmdctx.PageResult, error) {
	result, err := endpoint.HTTPFetchFn(ctx, ep, wantStart, wantCount, cursor)
	if err != nil {
		return nil, err
	}
	for i, raw := range result.Items {
		m, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		pr, ok := m["pull_request"].(map[string]any)
		if !ok {
			continue
		}
		flat := make(map[string]any, len(pr)+1)
		maps.Copy(flat, pr)
		flat["repository"] = m["repository"]
		result.Items[i] = flat
	}
	return result, nil
}
