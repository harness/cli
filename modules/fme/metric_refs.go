// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package fme

import (
	"fmt"
	"strings"

	"github.com/harness/cli/v3/pkg/cmdctx"
	"github.com/harness/cli/v3/pkg/spec"
)

// metricRefsFieldType manages an experiment's keyMetrics/supportingMetrics
// collections. The read shape is [{id, name}]; the write shape (POST/PATCH)
// is [id] — plain metric id strings. Normalize drops the display name so
// Mutate can treat the collection as a set of id members, matching core's
// built-in "set" semantics; Encode is a no-op since the normalized value is
// already the write shape.
var metricRefsFieldType = cmdctx.FieldTypeHandler{
	Normalize: normalizeMetricRefs,
	Mutate:    mutateMetricRefs,
}

func normalizeMetricRefs(_ spec.FieldDef, current any) (any, error) {
	items, _ := current.([]any)
	next := make([]any, 0, len(items))
	for _, item := range items {
		switch v := item.(type) {
		case map[string]any:
			if id, ok := v["id"].(string); ok && id != "" {
				next = append(next, id)
			}
		case string:
			next = append(next, v)
		}
	}
	return next, nil
}

func mutateMetricRefs(_ spec.FieldDef, current any, op cmdctx.FieldMutation) (any, bool, error) {
	if op.Kind != cmdctx.MutationSet && op.Kind != cmdctx.MutationAdd && op.Kind != cmdctx.MutationDelete {
		return nil, false, fmt.Errorf("unknown mutation operation %q", op.Kind)
	}
	selector := op.Key
	if op.Kind == cmdctx.MutationDelete && op.HasValue {
		selector = op.Raw
	}
	_, member, found := strings.Cut(selector, ".")
	if !found || member == "" {
		return nil, false, fmt.Errorf("--%s %s: requires a metric id (e.g. --%s %s.<metric-id>)", op.Kind, op.Key, op.Kind, op.Key)
	}
	if op.Kind == cmdctx.MutationAdd && op.HasValue {
		return nil, false, fmt.Errorf("--add %s: metric members do not take a value", op.Key)
	}

	items, _ := current.([]any)
	next := make([]any, 0, len(items)+1)
	for _, item := range items {
		id, _ := item.(string)
		if op.Kind == cmdctx.MutationDelete && id == member {
			continue
		}
		next = append(next, item)
	}
	if op.Kind != cmdctx.MutationDelete {
		exists := false
		for _, item := range next {
			if id, _ := item.(string); id == member {
				exists = true
				break
			}
		}
		if !exists {
			next = append(next, member)
		}
	}
	return next, true, nil
}
