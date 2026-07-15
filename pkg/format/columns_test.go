// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package format

import (
	"testing"

	"github.com/harness/cli/pkg/spec"
)

var testFields = []spec.FieldDef{
	{ID: "identifier", Expr: "it.identifier"},
	{ID: "name", Label: "Name", Expr: "it.name"},
	{ID: "type", Label: "Type", Expr: "it.type"},
	{ID: "status", Label: "Status", Expr: "it.status"},
	{ID: "last_run", Label: "Last Run", Expr: "it.lastRun", FieldType: "epoch_ms"},
}

var testDefault = []spec.TableColumn{
	{Header: "Identifier", Expr: "it.identifier"},
	{Header: "Name", Expr: "it.name"},
	{Header: "Type", Expr: "it.type"},
}

func TestApplyColumns(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    []spec.TableColumn
		wantErr bool
	}{
		{
			name:  "empty returns default",
			input: "",
			want:  testDefault,
		},
		// --- replace mode: plain field IDs ---
		{
			name:  "single field ID",
			input: "identifier",
			want:  []spec.TableColumn{{Header: "Identifier", Expr: "it.identifier"}},
		},
		{
			name:  "multiple field IDs",
			input: "identifier,name,status",
			want: []spec.TableColumn{
				{Header: "Identifier", Expr: "it.identifier"},
				{Header: "Name", Expr: "it.name"},
				{Header: "Status", Expr: "it.status"},
			},
		},
		{
			name:  "field with explicit label uses label",
			input: "name",
			want:  []spec.TableColumn{{Header: "Name", Expr: "it.name"}},
		},
		{
			name:  "field without explicit label auto-derives from ID",
			input: "identifier",
			want:  []spec.TableColumn{{Header: "Identifier", Expr: "it.identifier"}},
		},
		{
			name:  "last_run auto-derives to title case",
			input: "last_run",
			want:  []spec.TableColumn{{Header: "Last Run", Expr: "it.lastRun", FieldType: "epoch_ms"}},
		},
		{
			name:    "unknown field ID returns error",
			input:   "bogus",
			wantErr: true,
		},
		// --- replace mode: ad-hoc id:expr ---
		{
			name:  "ad-hoc id:expr",
			input: "my_col:it.foo.bar",
			want:  []spec.TableColumn{{Header: "My Col", Expr: "it.foo.bar"}},
		},
		{
			name:  "mix of field ID and ad-hoc",
			input: "name,extra:it.extra",
			want: []spec.TableColumn{
				{Header: "Name", Expr: "it.name"},
				{Header: "Extra", Expr: "it.extra"},
			},
		},
		{
			name:    "ad-hoc with empty expr",
			input:   "col:",
			wantErr: true,
		},
		// --- modify mode: sigils ---
		{
			name:  "add field by ID",
			input: "+status",
			want: []spec.TableColumn{
				{Header: "Identifier", Expr: "it.identifier"},
				{Header: "Name", Expr: "it.name"},
				{Header: "Type", Expr: "it.type"},
				{Header: "Status", Expr: "it.status"},
			},
		},
		{
			name:  "remove field by header (case-insensitive)",
			input: "-Type",
			want: []spec.TableColumn{
				{Header: "Identifier", Expr: "it.identifier"},
				{Header: "Name", Expr: "it.name"},
			},
		},
		{
			name:  "add and remove",
			input: "+status,-Type",
			want: []spec.TableColumn{
				{Header: "Identifier", Expr: "it.identifier"},
				{Header: "Name", Expr: "it.name"},
				{Header: "Status", Expr: "it.status"},
			},
		},
		{
			name:  "add ad-hoc column in modify mode",
			input: "+extra:it.extra",
			want: []spec.TableColumn{
				{Header: "Identifier", Expr: "it.identifier"},
				{Header: "Name", Expr: "it.name"},
				{Header: "Type", Expr: "it.type"},
				{Header: "Extra", Expr: "it.extra"},
			},
		},
		{
			name:  "remove unknown header is no-op",
			input: "-nope",
			want:  testDefault,
		},
		{
			name:    "add unknown field ID errors",
			input:   "+bogus",
			wantErr: true,
		},
		// --- mixed mode: any plain token means replace mode ---
		{
			name:  "mixed: plain wins, + stripped, - dropped",
			input: "+status,name,-Type",
			want: []spec.TableColumn{
				{Header: "Status", Expr: "it.status"},
				{Header: "Name", Expr: "it.name"},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ApplyColumns(testFields, testDefault, tc.input)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("len mismatch: got %d cols, want %d\ngot:  %+v\nwant: %+v", len(got), len(tc.want), got, tc.want)
			}
			for i, col := range got {
				if col.Header != tc.want[i].Header || col.Expr != tc.want[i].Expr || col.FieldType != tc.want[i].FieldType {
					t.Errorf("col[%d]: got {%q, %q, %q}, want {%q, %q, %q}",
						i, col.Header, col.Expr, col.FieldType,
						tc.want[i].Header, tc.want[i].Expr, tc.want[i].FieldType)
				}
			}
		})
	}
}

func TestLabelFromID(t *testing.T) {
	tests := []struct {
		id   string
		want string
	}{
		{"identifier", "Identifier"},
		{"last_run", "Last Run"},
		{"last_run_by", "Last Run By"},
		{"run_seq", "Run Seq"},
		{"id", "Id"},
		{"status", "Status"},
	}
	for _, tc := range tests {
		t.Run(tc.id, func(t *testing.T) {
			got := labelFromID(tc.id)
			if got != tc.want {
				t.Errorf("labelFromID(%q) = %q, want %q", tc.id, got, tc.want)
			}
		})
	}
}
