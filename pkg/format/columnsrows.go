// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package format

import (
	"fmt"
	"io"

	"github.com/harness/cli/pkg/cmdctx"
	"github.com/harness/cli/pkg/spec"
)

// tabularSingleFormats are output formats that FormatSingleOutput can honor when the
// payload matches the columns/rows shape (via ExpandColumnsRows → FormatArrayOutput).
var tabularSingleFormats = map[string]bool{
	"table": true, "csv": true, "tsv": true, "markdown": true, "jsonl": true,
}

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
	seen := make(map[string]int, len(colsSlice))
	for i, c := range colsSlice {
		base := columnName(c, i)
		name := base
		if n := seen[base]; n > 0 {
			name = fmt.Sprintf("%s_%d", base, n+1)
		}
		seen[base]++
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

// FormatColumnsRowsArray expands a columns/rows payload and renders it via FormatArrayOutput.
// Returns (false, nil) when data is not a columns/rows payload.
func FormatColumnsRowsArray(flags cmdctx.FormatFlags, isPty bool, data any, exprEnv map[string]any) (bool, error) {
	rows, fields, ok := ExpandColumnsRows(data)
	if !ok {
		return false, nil
	}
	return true, FormatArrayOutput(flags, isPty, rows, "it", fieldsToTableSpec(fields), fields, exprEnv, nil)
}

// WriteColumnsRows renders a columns/rows payload as a borderless table.
// Returns false when data is not that shape (caller should fall back).
func WriteColumnsRows(w io.Writer, data any, noHeaders bool) (bool, error) {
	rows, fields, ok := ExpandColumnsRows(data)
	if !ok {
		return false, nil
	}
	tspec := fieldsToTableSpec(fields)
	t, err := BuildTable(tspec, "it", rows, noHeaders, map[string]any{})
	if err != nil {
		return true, err
	}
	t.SetOutputMirror(w)
	t.Render()

	if m, ok := data.(map[string]any); ok {
		if truncated, _ := m["truncated"].(bool); truncated {
			fmt.Fprintln(w, "(truncated)")
		}
	}
	return true, nil
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

func fieldsToTableSpec(fields []spec.FieldDef) *spec.TableSpec {
	if len(fields) == 0 {
		return &spec.TableSpec{}
	}
	cols := make([]spec.TableColumn, len(fields))
	for i, f := range fields {
		header := f.Label
		if header == "" {
			header = f.ID
		}
		cols[i] = spec.TableColumn{Header: header, Expr: f.Expr, Align: f.Align, FieldType: f.FieldType, WidthMax: f.WidthMax}
	}
	return &spec.TableSpec{Columns: cols}
}
