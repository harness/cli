// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package gitops

import (
	"fmt"
	"strings"

	"github.com/harness/cli/v3/pkg/cmdctx"
)

const bulkSyncBodyFnID = "gitops_bulk_sync_body"

func bulkSyncBody(ctx *cmdctx.Ctx) (any, error) {
	rawTargets := cmdctx.GetStringSlice(ctx.FlagValues, "target")
	targets := make([]map[string]any, 0, len(rawTargets))
	for _, raw := range rawTargets {
		agent, app, ok := strings.Cut(raw, "/")
		if !ok || agent == "" || app == "" || strings.Contains(app, "/") {
			return nil, fmt.Errorf("--target %q must be <agent/app>", raw)
		}
		targets = append(targets, map[string]any{"agentIdentifier": agent, "applicationName": app})
	}
	body := map[string]any{"applicationTargets": targets}
	if cmdctx.GetBool(ctx.FlagValues, "prune") {
		body["prune"] = true
	}
	if cmdctx.GetBool(ctx.FlagValues, "dry-run") {
		body["dryRun"] = true
	}
	if opts := cmdctx.GetStringSlice(ctx.FlagValues, "sync-option"); len(opts) > 0 {
		body["syncOptions"] = map[string]any{"items": opts}
	}
	return body, nil
}
