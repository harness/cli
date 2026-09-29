// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package registry

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/harness/cli/v3/pkg/cmdctx"
	"github.com/harness/cli/v3/pkg/spec"
)

func jsonCopyMap(value any) (map[string]any, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("marshaling JSON: %w", err)
	}
	var copy map[string]any
	if err := json.Unmarshal(data, &copy); err != nil {
		return nil, fmt.Errorf("unmarshaling JSON object: %w", err)
	}
	return copy, nil
}

// effectiveMutations retains last-set-wins and set-before-delete precedence.
func effectiveMutations(sets map[string]string, dels []string, captured []cmdctx.FieldMutation) []cmdctx.FieldMutation {
	lastSet := map[string]cmdctx.FieldMutation{}
	for _, op := range captured {
		if op.Kind == cmdctx.MutationSet {
			lastSet[op.Key] = op
		}
	}
	ops := make([]cmdctx.FieldMutation, 0, len(sets)+len(dels))
	for key, value := range sets {
		op, found := lastSet[key]
		if !found || op.Value != value {
			op = cmdctx.FieldMutation{Kind: cmdctx.MutationSet, Raw: key + "=" + value, Key: key, Value: value, HasValue: true}
		}
		ops = append(ops, op)
	}
	for _, key := range dels {
		// Delete operands still treat '=' as part of the target until the separate grammar migration.
		ops = append(ops, cmdctx.FieldMutation{Kind: cmdctx.MutationDelete, Raw: key, Key: key})
	}
	return ops
}

// buildMutationBody constructs a new JSON-shaped body without modifying its inputs.
func buildMutationBody(base map[string]any, fields map[string]spec.FieldDef, handlers map[string]cmdctx.FieldTypeHandler, ops []cmdctx.FieldMutation, wrap string, extra map[string]any) (map[string]any, error) {
	mutable := cloneMutationMap(base)
	for _, op := range ops {
		fieldID, _, _ := strings.Cut(op.Key, ".")
		field, found := fields[fieldID]
		if !found {
			return nil, fmt.Errorf("unknown or read-only field %q; use --list-fields to see mutable fields", fieldID)
		}
		handler, found := handlers[field.FieldType]
		if !found || handler.Mutate == nil {
			return nil, fmt.Errorf("field %q: field_type %q has no registered mutator", field.ID, field.FieldType)
		}
		current := cloneMutationValue(getDotPathValue(mutable, field.MutablePath))
		if handler.Normalize != nil {
			var err error
			current, err = handler.Normalize(field, current)
			if err != nil {
				return nil, err
			}
		}
		next, write, err := handler.Mutate(field, current, op)
		if err != nil {
			return nil, err
		}
		if !write {
			continue
		}
		if handler.Encode != nil {
			next, err = handler.Encode(field, next)
			if err != nil {
				return nil, err
			}
		}
		setDotPath(mutable, field.MutablePath, cloneMutationValue(next))
	}
	body := mutable
	if wrap != "" {
		body = map[string]any{wrap: mutable}
	}
	for path, value := range extra {
		if value != nil {
			setDotPath(body, path, cloneMutationValue(value))
		}
	}
	return body, nil
}

func cloneMutationMap(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for key, value := range m {
		out[key] = cloneMutationValue(value)
	}
	return out
}

func cloneMutationValue(value any) any {
	switch v := value.(type) {
	case map[string]any:
		return cloneMutationMap(v)
	case []any:
		if v == nil {
			return []any(nil)
		}
		out := make([]any, len(v))
		for i, element := range v {
			out[i] = cloneMutationValue(element)
		}
		return out
	case []string:
		if v == nil {
			return []string(nil)
		}
		return append([]string{}, v...)
	default:
		return value
	}
}

func getDotPathValue(m map[string]any, path string) any {
	first, rest, nested := strings.Cut(path, ".")
	if !nested {
		return m[first]
	}
	child, _ := m[first].(map[string]any)
	if child == nil {
		return nil
	}
	return getDotPathValue(child, rest)
}
