// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package registry

import (
	"reflect"
	"strings"
	"testing"

	"github.com/harness/cli/v3/pkg/cmdctx"
	"github.com/harness/cli/v3/pkg/spec"
)

func TestFieldTypeRegistration(t *testing.T) {
	r := New()
	for _, name := range []string{"", "scalar", "tags", "set", "yaml", "ts", "multiline_text"} {
		h, ok := r.ResolveFieldType(name)
		if !ok || h.Mutate == nil {
			t.Fatalf("builtin field type %q has no mutator", name)
		}
	}
	handler := cmdctx.FieldTypeHandler{Mutate: mutateScalar}
	r.Module("example").RegisterFieldType("owners", handler)
	if h, ok := r.ResolveFieldType("example:owners"); !ok || h.Mutate == nil {
		t.Fatal("module field type was not qualified and registered")
	}
	defer func() {
		if p := recover(); p == nil || !strings.Contains(p.(string), "duplicate field type") {
			t.Fatalf("duplicate registration panic = %v", p)
		}
	}()
	r.Module("example").RegisterFieldType("owners", handler)
}

func TestCheckFunctions_FieldTypes(t *testing.T) {
	for _, tc := range []struct {
		name, kind, want string
		register         bool
	}{
		{name: "unknown", kind: "example:missing", want: "not registered"},
		{name: "missing mutator", kind: "example:empty", want: "has no mutator", register: true},
		{name: "display-only", kind: "example:missing"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := New()
			if tc.register {
				r.Module("example").RegisterFieldType("empty", cmdctx.FieldTypeHandler{})
			}
			field := spec.FieldDef{ID: "items", Expr: "it.items", FieldType: tc.kind}
			if tc.name != "display-only" {
				field.MutablePath = "items"
			}
			if err := r.RegisterNoun(spec.NounDef{Noun: "widget", Fields: []spec.FieldDef{field}}); err != nil {
				t.Fatal(err)
			}
			err := r.CheckFunctions()
			if tc.want == "" && err != nil || tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)) {
				t.Fatalf("CheckFunctions() = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestApplyMutations_CustomDispatch(t *testing.T) {
	r := New()
	field := spec.FieldDef{ID: "items", Expr: "it.items", MutablePath: "nested.items", FieldType: "example:items"}
	wantOp := cmdctx.FieldMutation{Kind: cmdctx.MutationSet, Raw: "items.a.b=c=d", Key: "items.a.b", Value: "c=d", HasValue: true}
	r.Module("example").RegisterFieldType("items", cmdctx.FieldTypeHandler{
		Normalize: func(_ spec.FieldDef, current any) (any, error) { return current.(string) + ":read", nil },
		Mutate: func(gotField spec.FieldDef, current any, op cmdctx.FieldMutation) (any, bool, error) {
			if gotField != field || current != "before:read" || op != wantOp {
				t.Fatalf("handler got field=%+v current=%v op=%+v", gotField, current, op)
			}
			return op.Value, true, nil
		},
		Encode: func(_ spec.FieldDef, current any) (any, error) { return current.(string) + ":write", nil },
	})
	m := map[string]any{"nested": map[string]any{"items": "before", "other": "keep"}}
	ctx := &cmdctx.Ctx{Resolver: r, SetArgs: map[string]string{wantOp.Key: wantOp.Value}, MutationFlags: []cmdctx.FieldMutation{wantOp}}
	if err := applyMutations(m, ctx, map[string]spec.FieldDef{"items": field}); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"nested": map[string]any{"items": "c=d:write", "other": "keep"}}
	if !reflect.DeepEqual(m, want) {
		t.Fatalf("body = %#v, want %#v", m, want)
	}
}

func TestApplyMutations_ExistingNoWriteCases(t *testing.T) {
	fields := map[string]spec.FieldDef{"tags": {ID: "tags", Expr: "it.tags", MutablePath: "tags", FieldType: "tags"}}
	m := map[string]any{"name": "keep"}
	if err := applyMutationsTest(m, nil, []string{"tags.env"}, fields); err != nil {
		t.Fatal(err)
	}
	if _, exists := m["tags"]; exists {
		t.Fatalf("deleting from absent tags introduced a field: %v", m)
	}
	m["tags"] = map[string]any{"a=b": "keep", "a": "other"}
	if err := applyMutationsTest(m, nil, []string{"tags.a=b"}, fields); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(m["tags"], map[string]any{"a": "other"}) {
		t.Fatalf("legacy '=' delete key changed: %v", m["tags"])
	}
}

func TestApplyMutations_StillSetsBeforeDeleting(t *testing.T) {
	r := New()
	field := spec.FieldDef{ID: "name", Expr: "it.name", MutablePath: "name"}
	m := map[string]any{"name": "old"}
	ctx := &cmdctx.Ctx{Resolver: r, SetArgs: map[string]string{"name": "new"}, DelArgs: []string{"name"},
		MutationFlags: []cmdctx.FieldMutation{
			{Kind: cmdctx.MutationDelete, Raw: "name", Key: "name"},
			{Kind: cmdctx.MutationSet, Raw: "name=new", Key: "name", Value: "new", HasValue: true},
		}}
	if err := applyMutations(m, ctx, map[string]spec.FieldDef{"name": field}); err != nil {
		t.Fatal(err)
	}
	if m["name"] != nil {
		t.Fatalf("old delete-after-set precedence changed: %v", m)
	}
}
