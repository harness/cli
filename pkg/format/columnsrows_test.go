// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package format

import (
	"bytes"
	"os"
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
			map[string]any{"name": "x_2"},
			map[string]any{"name": "x"},
		},
		"rows": []any{
			map[string]any{"values": []any{"a", "b", "c"}},
		},
	}
	rows, fields, ok := ExpandColumnsRows(data)
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
	rows, fields, ok := ExpandColumnsRows(data)
	if !ok {
		t.Fatal("expected empty columns/rows result to be recognized")
	}
	if len(rows) != 0 || len(fields) != 0 {
		t.Fatalf("rows=%v fields=%v", rows, fields)
	}
	out := t.TempDir() + "/out.txt"
	handled, err := FormatColumnsRowsArray(cmdctx.FormatFlags{Format: "table", OutFile: out}, false, data, map[string]any{})
	if err != nil || !handled {
		t.Fatalf("handled=%v err=%v", handled, err)
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
	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "alpha") {
		t.Fatalf("table output did not use extracted item_expr payload: %q", got)
	}

	err = FormatSingleOutput(cmdctx.FormatFlags{Format: "table"}, false, map[string]any{"x": 1}, "it", "", nil, nil, map[string]any{})
	if err == nil || !strings.Contains(err.Error(), "not supported here") {
		t.Fatalf("err = %v, want not supported", err)
	}
}

func TestFormatSingleOutput_RawTableRejected(t *testing.T) {
	data := map[string]any{"result": sampleColumnsRows()}
	err := FormatSingleOutput(cmdctx.FormatFlags{Format: "table", Raw: true}, false, data, "it.result", "", nil, nil, map[string]any{})
	if err == nil || err.Error() != "--raw is only supported with --format json" {
		t.Fatalf("err = %v", err)
	}
}

func TestFormatSingleOutput_RawJSONKeepsEnvelope(t *testing.T) {
	out := t.TempDir() + "/out.json"
	data := map[string]any{"result": sampleColumnsRows(), "metadata": "kept"}
	err := FormatSingleOutput(cmdctx.FormatFlags{Format: "json", Raw: true, OutFile: out}, false, data, "it.result", "", nil, nil, map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), `"metadata": "kept"`) || !strings.Contains(string(got), `"result"`) {
		t.Fatalf("raw JSON did not preserve envelope: %q", got)
	}
}

func TestFormatSingleOutput_TruncatedTableNotice(t *testing.T) {
	out := t.TempDir() + "/out.txt"
	result := sampleColumnsRows()
	result["truncated"] = true
	data := map[string]any{"result": result}
	err := FormatSingleOutput(cmdctx.FormatFlags{Format: "table", OutFile: out}, false, data, "it.result", "", nil, nil, map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "(truncated)") {
		t.Fatalf("table output = %q", got)
	}
}

func TestFormatColumnsRowsArray_CSV(t *testing.T) {
	out := t.TempDir() + "/out.csv"
	handled, err := FormatColumnsRowsArray(cmdctx.FormatFlags{Format: "csv", OutFile: out}, false, sampleColumnsRows(), map[string]any{})
	if !handled || err != nil {
		t.Fatalf("handled=%v err=%v", handled, err)
	}
}
