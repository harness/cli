// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

// Package splitio wires up the body_fn used by the splitio spec module (pkg/spec/splitio.spec.yaml)
// to manage Harness FME feature flags via the underlying Split.io Admin API.
package splitio

import (
	"github.com/harness/cli/pkg/cmdctx"
	"github.com/harness/cli/pkg/registry"
)

const updateFlagBodyFnID = "splitio_update_flag_body"

func ModuleInit(reg registry.ModuleRegistrar) {
	reg.RegisterBodyFn(updateFlagBodyFnID, updateFlagBodyFn)
}

// updateFlagBodyFn builds the RFC 6902 JSON Patch array the Split Admin API requires for
// "update feature flag" (PATCH /splits/ws/{workspace}/{name}). Only flags the user actually
// passed are included, each as one patch operation.
func updateFlagBodyFn(ctx *cmdctx.Ctx) (any, error) {
	var ops []map[string]any

	if v := cmdctx.GetString(ctx.FlagValues, "description"); v != "" {
		ops = append(ops, map[string]any{"op": "replace", "path": "/description", "value": v})
	}
	if tags := cmdctx.GetStringSlice(ctx.FlagValues, "tag"); len(tags) > 0 {
		tagObjs := make([]map[string]string, len(tags))
		for i, t := range tags {
			tagObjs[i] = map[string]string{"name": t}
		}
		ops = append(ops, map[string]any{"op": "replace", "path": "/tags", "value": tagObjs})
	}
	if v := cmdctx.GetString(ctx.FlagValues, "rollout-status"); v != "" {
		ops = append(ops, map[string]any{"op": "replace", "path": "/rolloutStatus/id", "value": v})
	}

	return ops, nil
}
