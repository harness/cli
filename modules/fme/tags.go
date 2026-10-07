// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package fme

import (
	"fmt"
	"strings"

	"github.com/harness/cli/v3/pkg/cmdctx"
	"github.com/harness/cli/v3/pkg/spec"
)

// tagsFieldType manages a feature flag's tags collection. The read shape is
// [{id, name}]; the write shape (POST/PATCH) is [{name}] only. Core's
// built-in "tags" type is a string-keyed map and doesn't fit this
// named-object shape (docs/mutation.md), so FME registers its own; Encode
// strips the read-only id before writing.
var tagsFieldType = cmdctx.FieldTypeHandler{
	Mutate: mutateTags,
	Encode: encodeTags,
}

func tagName(item any) string {
	m, ok := item.(map[string]any)
	if !ok {
		return ""
	}
	name, _ := m["name"].(string)
	return name
}

func mutateTags(_ spec.FieldDef, current any, op cmdctx.FieldMutation) (any, bool, error) {
	if op.Kind == cmdctx.MutationSet {
		return nil, false, fmt.Errorf("--set %s: use --add/--del to add or remove an individual tag", op.Key)
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
		return nil, false, fmt.Errorf("--%s %s: tags require a member (e.g. --%s tags.my-tag)", op.Kind, op.Key, op.Kind)
	}
	if op.Kind == cmdctx.MutationAdd && op.HasValue {
		return nil, false, fmt.Errorf("--add %s: tag members do not take a value", op.Key)
	}

	items, _ := current.([]any)
	next := make([]any, 0, len(items)+1)
	for _, item := range items {
		if op.Kind == cmdctx.MutationDelete && tagName(item) == member {
			continue
		}
		next = append(next, item)
	}
	if op.Kind == cmdctx.MutationAdd {
		exists := false
		for _, item := range next {
			if tagName(item) == member {
				exists = true
				break
			}
		}
		if !exists {
			next = append(next, map[string]any{"name": member})
		}
	}
	return next, true, nil
}

func encodeTags(_ spec.FieldDef, current any) (any, error) {
	items, _ := current.([]any)
	encoded := make([]any, 0, len(items))
	for _, item := range items {
		name := tagName(item)
		if name == "" {
			continue
		}
		encoded = append(encoded, map[string]any{"name": name})
	}
	return encoded, nil
}
