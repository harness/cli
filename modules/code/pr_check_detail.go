// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package code

import (
	"github.com/harness/cli/pkg/cmdctx"
	"github.com/harness/cli/pkg/registry"
	"github.com/harness/cli/pkg/spec"
)

const getPRCheckWorkflowID = "get_pr_check"

// getPRCheckHandler implements "get pr_check". There is no direct single-check detail
// endpoint, so it resolves the check the same way "get pr_check:log" does — via list
// pr_check plus an identifier match — and renders the matched item as a "get" result.
func getPRCheckHandler(ctx *cmdctx.Ctx) error {
	_, _, _, match, err := resolvePRCheck(ctx)
	if err != nil {
		return err
	}

	// get pr_check has handler_type: workflow with no endpoint: block in the spec (there's
	// nothing for CallEndpoint to hit), so this EndpointSpec is hand-built purely to drive
	// rendering via RenderSingleItem — it mirrors what an endpoint block's item_expr would be,
	// but isn't spec-derived and won't pick up any columns/text_formatter added to the YAML.
	ep := &spec.EndpointSpec{ItemExpr: "it"}
	return registry.RenderSingleItem(ctx, ep, match)
}
