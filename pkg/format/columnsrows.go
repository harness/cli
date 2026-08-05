// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package format

import (
	"fmt"

	"github.com/harness/cli/pkg/spec"
)

// ExpandColumnsRows converts a {columns, rows} query result into inputs for
// FormatArrayOutput / FormatList. Returns ok=false when data is not that shape.
//
// Expected shape (HQL executeQuery and similar):
//
//	{
//	  "columns": [{"name": "col", "data_type": "FIELD_TYPE_STR"}, ...],
//	  "rows":    [{"values": [v0, v1, ...]}, ...],
//	  "truncated": false
//	}
func ExpandColumnsRows(data any) (rows []any, fields []spec.FieldDef, ok bool) {
	m, ok := data.(map[string]any)
	if !ok {
		return nil, nil, false
	}
	colsRaw, hasCols := m["columns"]
	rowsRaw, hasRows := m["rows"]
	if !hasCols || !hasRows {
		return nil, nil, false
	}
	colsSlice, ok1 := colsRaw.([]any)
	rowsSlice, ok2 := rowsRaw.([]any)
	if !ok1 || !ok2 {
		return nil, nil, false
	}

	names := make([]string, 0, len(colsSlice))
	fields = make([]spec.FieldDef, 0, len(colsSlice))
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

	rows = make([]any, 0, len(rowsSlice))
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
	return rows, fields, true
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
