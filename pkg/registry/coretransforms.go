// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package registry

import (
	"fmt"

	"github.com/harness/cli/v3/pkg/cmdctx"
	"github.com/harness/cli/v3/pkg/spec"
)

// registerCoreTransforms registers all built-in "core:*" list transform fns.
// These are available to any module via list_transform_fn: core:<name>.
func (r *Registry) registerCoreTransforms() {
	r.RegisterListTransformFn("core:columns-rows", columnsRowsListTransform)
}

// columnsRowsListTransform converts a {columns, rows} query result (HQL and similar)
// into list-rendering inputs. Use with list_transform_fn: core:columns-rows.
func columnsRowsListTransform(ctx *cmdctx.Ctx, data any) ([]any, []spec.FieldDef, cmdctx.PageMeta, error) {
	rows, fields, err := expandColumnsRows(data)
	if err != nil {
		return nil, nil, cmdctx.PageMeta{}, err
	}
	var meta cmdctx.PageMeta
	if m, ok := data.(map[string]any); ok {
		if truncated, _ := m["truncated"].(bool); truncated {
			meta.Notice = "(truncated)"
		}
	}
	return rows, fields, meta, nil
}

// expandColumnsRows converts a {columns, rows} query result into inputs for
// FormatArrayOutput / FormatList. Returns an error when data is not that shape.
//
// Expected shape (HQL executeQuery and similar):
//
//	{
//	  "columns": [{"name": "col", "data_type": "FIELD_TYPE_STR"}, ...],
//	  "rows":    [{"values": [v0, v1, ...]}, ...],
//	  "truncated": false
//	}
func expandColumnsRows(data any) ([]any, []spec.FieldDef, error) {
	m, ok := data.(map[string]any)
	if !ok {
		return nil, nil, fmt.Errorf("response is not an object, got %T", data)
	}
	colsRaw, hasCols := m["columns"]
	rowsRaw, hasRows := m["rows"]
	if !hasCols || !hasRows {
		return nil, nil, fmt.Errorf("response is missing \"columns\" and/or \"rows\" fields")
	}
	colsSlice, ok1 := colsRaw.([]any)
	rowsSlice, ok2 := rowsRaw.([]any)
	if !ok1 {
		return nil, nil, fmt.Errorf("response \"columns\" field is not an array, got %T", colsRaw)
	}
	if !ok2 {
		return nil, nil, fmt.Errorf("response \"rows\" field is not an array, got %T", rowsRaw)
	}

	names := make([]string, 0, len(colsSlice))
	fields := make([]spec.FieldDef, 0, len(colsSlice))
	used := make(map[string]bool, len(colsSlice))
	nextSuffix := make(map[string]int, len(colsSlice))
	for i, c := range colsSlice {
		base := columnName(c, i)
		name := base
		if used[name] {
			suffix := nextSuffix[base]
			if suffix < 2 {
				suffix = 2
			}
			for {
				name = fmt.Sprintf("%s_%d", base, suffix)
				suffix++
				if !used[name] {
					break
				}
			}
			nextSuffix[base] = suffix
		}
		used[name] = true
		names = append(names, name)
		fields = append(fields, spec.FieldDef{
			ID:    name,
			Label: name,
			Expr:  fmt.Sprintf("it[%q]", name),
		})
	}

	rows := make([]any, 0, len(rowsSlice))
	for _, r := range rowsSlice {
		rm, _ := r.(map[string]any)
		vals, _ := rm["values"].([]any)
		row := make(map[string]any, len(names))
		for i, name := range names {
			if i < len(vals) {
				row[name] = vals[i]
			}
		}
		rows = append(rows, row)
	}
	return rows, fields, nil
}

func columnName(col any, i int) string {
	m, ok := col.(map[string]any)
	if !ok {
		return fmt.Sprintf("col_%d", i)
	}
	name, _ := m["name"].(string)
	if name == "" {
		return fmt.Sprintf("col_%d", i)
	}
	return name
}
