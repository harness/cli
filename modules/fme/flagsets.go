// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package fme

import (
	"fmt"
	"strings"

	"github.com/harness/cli/v3/pkg/cmdctx"
	"github.com/harness/cli/v3/pkg/spec"
)

// flagSetsFieldType manages a feature flag definition's flag set associations.
// The read/write shape is [{id}] (FlagSetReference) — a flag set is identified
// only by id; name/type are not accepted or returned. Structurally identical
// to tagsFieldType, just keyed on "id" instead of "name".
var flagSetsFieldType = cmdctx.FieldTypeHandler{
	Mutate: mutateFlagSets,
	Encode: encodeFlagSets,
}

func flagSetID(item any) string {
	m, ok := item.(map[string]any)
	if !ok {
		return ""
	}
	id, _ := m["id"].(string)
	return id
}

func mutateFlagSets(_ spec.FieldDef, current any, op cmdctx.FieldMutation) (any, bool, error) {
	if op.Kind == cmdctx.MutationSet {
		return nil, false, fmt.Errorf("--set %s: use --add/--del to add or remove an individual flag set", op.Key)
	}
	if op.Kind != cmdctx.MutationAdd && op.Kind != cmdctx.MutationDelete {
		return nil, false, fmt.Errorf("unknown mutation operation %q", op.Kind)
	}
	selector := op.Key
	if op.Kind == cmdctx.MutationDelete && op.HasValue {
		selector = op.Raw
	}
	_, member, found := strings.Cut(selector, ".")
	if !found || member == "" {
		return nil, false, fmt.Errorf("--%s %s: flag_sets require a member (e.g. --%s flag_sets.my-flag-set)", op.Kind, op.Key, op.Kind)
	}
	if op.Kind == cmdctx.MutationAdd && op.HasValue {
		return nil, false, fmt.Errorf("--add %s: flag set members do not take a value", op.Key)
	}

	items, _ := current.([]any)
	next := make([]any, 0, len(items)+1)
	for _, item := range items {
		if op.Kind == cmdctx.MutationDelete && flagSetID(item) == member {
			continue
		}
		next = append(next, item)
	}
	if op.Kind == cmdctx.MutationAdd {
		exists := false
		for _, item := range next {
			if flagSetID(item) == member {
				exists = true
				break
			}
		}
		if !exists {
			next = append(next, map[string]any{"id": member})
		}
	}
	return next, true, nil
}

func encodeFlagSets(_ spec.FieldDef, current any) (any, error) {
	items, _ := current.([]any)
	encoded := make([]any, 0, len(items))
	for _, item := range items {
		id := flagSetID(item)
		if id == "" {
			continue
		}
		encoded = append(encoded, map[string]any{"id": id})
	}
	return encoded, nil
}
