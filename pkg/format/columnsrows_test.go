// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package format

import (
	"bytes"
	"strings"
	"testing"

	"github.com/harness/cli/pkg/cmdctx"
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
	rows, fields, ok := ExpandColumnsRows(sampleColumnsRows())
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
	if _, _, ok := ExpandColumnsRows(map[string]any{"foo": 1}); ok {
		t.Fatal("expected !ok")
	}
	if _, _, ok := ExpandColumnsRows([]any{}); ok {
		t.Fatal("expected !ok for slice")
	}
	if _, _, ok := ExpandColumnsRows(nil); ok {
		t.Fatal("expected !ok for nil")
	}
}

func TestExpandColumnsRows_DuplicateNames(t *testing.T) {
	data := map[string]any{
		"columns": []any{
			map[string]any{"name": "x"},
			map[string]any{"name": "x"},
		},
		"rows": []any{
			map[string]any{"values": []any{"a", "b"}},
		},
	}
	rows, fields, ok := ExpandColumnsRows(data)
	if !ok {
		t.Fatal("expected ok")
	}
	if fields[0].ID != "x" || fields[1].ID != "x_2" {
		t.Fatalf("fields = %+v", fields)
	}
	r0 := rows[0].(map[string]any)
	if r0["x"] != "a" || r0["x_2"] != "b" {
		t.Fatalf("row = %+v", r0)
	}
}

func TestWriteColumnsRows(t *testing.T) {
	var buf bytes.Buffer
	ok, err := WriteColumnsRows(&buf, sampleColumnsRows(), false)
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	out := buf.String()
	if !strings.Contains(out, "name") || !strings.Contains(out, "alpha") || !strings.Contains(out, "beta") {
		t.Fatalf("output = %q", out)
	}
}

func TestWriteColumnsRows_Truncated(t *testing.T) {
	data := sampleColumnsRows()
	data["truncated"] = true
	var buf bytes.Buffer
	ok, err := WriteColumnsRows(&buf, data, false)
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if !strings.Contains(buf.String(), "(truncated)") {
		t.Fatalf("output = %q", buf.String())
	}
}

func TestFormatSingleOutput_ColumnsRowsTable(t *testing.T) {
	out := t.TempDir() + "/out.txt"
	data := map[string]any{"result": sampleColumnsRows()}
	err := FormatSingleOutput(cmdctx.FormatFlags{Format: "table", OutFile: out}, false, data, "it.result", "", nil, nil, map[string]any{})
	if err != nil {
		t.Fatalf("FormatSingleOutput table: %v", err)
	}

	err = FormatSingleOutput(cmdctx.FormatFlags{Format: "table"}, false, map[string]any{"x": 1}, "it", "", nil, nil, map[string]any{})
	if err == nil || !strings.Contains(err.Error(), "not supported here") {
		t.Fatalf("err = %v, want not supported", err)
	}
}

func TestFormatColumnsRowsArray_CSV(t *testing.T) {
	out := t.TempDir() + "/out.csv"
	handled, err := FormatColumnsRowsArray(cmdctx.FormatFlags{Format: "csv", OutFile: out}, false, sampleColumnsRows(), map[string]any{})
	if !handled || err != nil {
		t.Fatalf("handled=%v err=%v", handled, err)
	}
}
