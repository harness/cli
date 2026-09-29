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
	if op.Kind == cmdctx.MutationDelete {
		return nil, true, nil
	}
	return op.Value, true, nil
}

func mutateTags(_ spec.FieldDef, current any, op cmdctx.FieldMutation) (any, bool, error) {
	_, tag, found := strings.Cut(op.Key, ".")
	if !found {
		if op.Kind == cmdctx.MutationDelete {
			return nil, false, fmt.Errorf("--del %s: tag fields require a key (e.g. --del tags.key)", op.Key)
		}
		return nil, false, fmt.Errorf("--set %s: tag fields require a key (e.g. --set tags.key=value)", op.Key)
	}
	tags, _ := current.(map[string]any)
	if op.Kind == cmdctx.MutationDelete {
		if tags == nil {
			return nil, false, nil
		}
		delete(tags, tag)
		return tags, true, nil
	}
	if tags == nil {
		tags = map[string]any{}
	}
	tags[tag] = op.Value
	return tags, true, nil
}

func mutateStringSet(_ spec.FieldDef, current any, op cmdctx.FieldMutation) (any, bool, error) {
	_, member, found := strings.Cut(op.Key, ".")
	if !found || (op.Kind == cmdctx.MutationSet && member == "") {
		return nil, false, fmt.Errorf("--%s %s: set fields require a member (e.g. --%s modules.CD)", op.Kind, op.Key, op.Kind)
	}
	var arr []any
	switch values := current.(type) {
	case []any:
		arr = values
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
