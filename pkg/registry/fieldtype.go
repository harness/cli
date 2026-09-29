// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package registry

import (
	"fmt"
	"strings"

	"github.com/harness/cli/v3/pkg/cmdctx"
	"github.com/harness/cli/v3/pkg/spec"
)

func (r *Registry) registerCoreFieldTypes() {
	scalar := cmdctx.FieldTypeHandler{Mutate: mutateScalar}
	for _, name := range []string{"", "scalar", "multiline_text", "yaml", "ts"} {
		r.fieldTypes[name] = scalar
	}
	r.fieldTypes["tags"] = cmdctx.FieldTypeHandler{Mutate: mutateTags}
	r.fieldTypes["set"] = cmdctx.FieldTypeHandler{Mutate: mutateStringSet}
}

func (r *Registry) RegisterFieldType(id string, handler cmdctx.FieldTypeHandler) {
	prefix, name, qualified := strings.Cut(id, ":")
	if !qualified || prefix == "" || name == "" || strings.Contains(name, ":") || prefix == corePrefix {
		panic(fmt.Sprintf("registry: field type %q must have a non-core module prefix", id))
	}
	if _, exists := r.fieldTypes[id]; exists {
		panic(fmt.Sprintf("registry: duplicate field type %q", id))
	}
	r.fieldTypes[id] = handler
}

func (r *Registry) ResolveFieldType(id string) (cmdctx.FieldTypeHandler, bool) {
	handler, ok := r.fieldTypes[id]
	return handler, ok
}

func mutateScalar(_ spec.FieldDef, _ any, op cmdctx.FieldMutation) (any, bool, error) {
	switch op.Kind {
	case cmdctx.MutationSet:
		return op.Value, true, nil
	case cmdctx.MutationDelete:
		if op.HasValue {
			return nil, false, fmt.Errorf("--del %s: scalar fields do not take a value", op.Raw)
		}
		return nil, true, nil
	case cmdctx.MutationAdd:
		return nil, false, fmt.Errorf("--add %s: scalar fields do not support addition", op.Key)
	default:
		return nil, false, fmt.Errorf("unknown mutation operation %q", op.Kind)
	}
}

func mutateTags(_ spec.FieldDef, current any, op cmdctx.FieldMutation) (any, bool, error) {
	if op.Kind != cmdctx.MutationSet && op.Kind != cmdctx.MutationAdd && op.Kind != cmdctx.MutationDelete {
		return nil, false, fmt.Errorf("unknown mutation operation %q", op.Kind)
	}
	selector := op.Key
	if op.Kind == cmdctx.MutationDelete && op.HasValue {
		selector = op.Raw
	}
	_, tag, found := strings.Cut(selector, ".")
	if !found {
		if op.Kind == cmdctx.MutationDelete {
			return nil, false, fmt.Errorf("--del %s: tag fields require a key (e.g. --del tags.key)", op.Key)
		}
		return nil, false, fmt.Errorf("--%s %s: tag fields require a key (e.g. --%s tags.key=value)", op.Kind, op.Key, op.Kind)
	}
	if op.Kind == cmdctx.MutationAdd && !op.HasValue {
		return nil, false, fmt.Errorf("--add %s: tag fields require key=value", op.Key)
	}
	tags, _ := current.(map[string]any)
	if op.Kind == cmdctx.MutationDelete {
		if tags == nil {
			return nil, false, nil
		}
	}
	next := make(map[string]any, len(tags))
	for key, value := range tags {
		next[key] = value
	}
	if op.Kind == cmdctx.MutationDelete {
		delete(next, tag)
	} else {
		if op.Kind == cmdctx.MutationAdd {
			if existing, found := next[tag]; found {
				if existing == op.Value {
					return nil, false, nil
				}
				return nil, false, fmt.Errorf("--add %s: key already exists with a different value; use --set to overwrite it", op.Key)
			}
		}
		next[tag] = op.Value
	}
	return next, true, nil
}

func mutateStringSet(_ spec.FieldDef, current any, op cmdctx.FieldMutation) (any, bool, error) {
	if op.Kind != cmdctx.MutationSet && op.Kind != cmdctx.MutationAdd && op.Kind != cmdctx.MutationDelete {
		return nil, false, fmt.Errorf("unknown mutation operation %q", op.Kind)
	}
	selector := op.Key
	if op.Kind == cmdctx.MutationDelete && op.HasValue {
		selector = op.Raw
	}
	_, member, found := strings.Cut(selector, ".")
	if !found || member == "" {
		return nil, false, fmt.Errorf("--%s %s: set fields require a member (e.g. --%s modules.CD)", op.Kind, op.Key, op.Kind)
	}
	if op.Kind == cmdctx.MutationAdd && op.HasValue {
		return nil, false, fmt.Errorf("--add %s: set members do not take a value", op.Key)
	}
	var arr []any
	switch values := current.(type) {
	case []any:
		arr = append([]any(nil), values...)
	case []string:
		for _, v := range values {
			arr = append(arr, v)
		}
	}
	if op.Kind == cmdctx.MutationDelete {
		return sliceRemove(arr, member), true, nil
	}
	if !sliceContains(arr, member) {
		arr = append(arr, member)
	}
	return arr, true, nil
}
