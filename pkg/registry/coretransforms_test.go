// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package registry

import (
	"testing"
)

func sampleColumnsRows() map[string]any {
	return map[string]any{
		"columns": []any{
			map[string]any{"name": "name", "data_type": "FIELD_TYPE_STR"},
			map[string]any{"name": "is_deleted", "data_type": "FIELD_TYPE_BOOL"},
		},
		"rows": []any{
			map[string]any{"values": []any{"alpha", false}},
			map[string]any{"values": []any{"beta", true}},
		},
		"truncated": false,
	}
}

func TestExpandColumnsRows(t *testing.T) {
	rows, fields, ok := expandColumnsRows(sampleColumnsRows())
	if !ok {
		t.Fatal("expected ok")
	}
	if len(fields) != 2 || fields[0].ID != "name" || fields[1].ID != "is_deleted" {
		t.Fatalf("fields = %+v", fields)
	}
	if fields[0].Expr != `it["name"]` {
		t.Fatalf("expr = %q", fields[0].Expr)
	}
	if len(rows) != 2 {
		t.Fatalf("len(rows) = %d", len(rows))
	}
	r0 := rows[0].(map[string]any)
	if r0["name"] != "alpha" || r0["is_deleted"] != false {
		t.Fatalf("row0 = %+v", r0)
	}
}

func TestExpandColumnsRows_NotShape(t *testing.T) {
	if _, _, ok := expandColumnsRows(map[string]any{"foo": 1}); ok {
		t.Fatal("expected !ok")
	}
	if _, _, ok := expandColumnsRows([]any{}); ok {
		t.Fatal("expected !ok for slice")
	}
	if _, _, ok := expandColumnsRows(nil); ok {
		t.Fatal("expected !ok for nil")
	}
}

func TestExpandColumnsRows_DuplicateNames(t *testing.T) {
	data := map[string]any{
		"columns": []any{
			map[string]any{"name": "x"},
			map[string]any{"name": "x_2"},
			map[string]any{"name": "x"},
		},
		"rows": []any{
			map[string]any{"values": []any{"a", "b", "c"}},
		},
	}
	rows, fields, ok := expandColumnsRows(data)
	if !ok {
		t.Fatal("expected ok")
	}
	if fields[0].ID != "x" || fields[1].ID != "x_2" || fields[2].ID != "x_3" {
		t.Fatalf("fields = %+v", fields)
	}
	r0 := rows[0].(map[string]any)
	if r0["x"] != "a" || r0["x_2"] != "b" || r0["x_3"] != "c" {
		t.Fatalf("row = %+v", r0)
	}
}

func TestExpandColumnsRows_EmptyResult(t *testing.T) {
	data := map[string]any{
		"columns": []any{},
		"rows":    []any{},
	}
	rows, fields, ok := expandColumnsRows(data)
	if !ok {
		t.Fatal("expected empty columns/rows result to be recognized")
	}
	if len(rows) != 0 || len(fields) != 0 {
		t.Fatalf("rows=%v fields=%v", rows, fields)
	}
}
