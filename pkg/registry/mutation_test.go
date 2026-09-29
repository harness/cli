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

func TestEffectiveMutations(t *testing.T) {
	captured := []cmdctx.FieldMutation{
		{Kind: cmdctx.MutationSet, Raw: "modules.CD=", Key: "modules.CD", HasValue: true},
		{Kind: cmdctx.MutationSet, Raw: "modules.CD", Key: "modules.CD"},
		{Kind: cmdctx.MutationSet, Raw: "tags.env=old", Key: "tags.env", Value: "old", HasValue: true},
		{Kind: cmdctx.MutationSet, Raw: "tags.env=new", Key: "tags.env", Value: "new", HasValue: true},
	}
	got := effectiveMutations(map[string]string{"modules.CD": "", "tags.env": "new"}, []string{"tags.a=b", "modules.CD"}, captured)
	if len(got) != 4 {
		t.Fatalf("operations = %#v, want 4", got)
	}
	wantSets := map[string]cmdctx.FieldMutation{"modules.CD": captured[1], "tags.env": captured[3]}
	for _, op := range got[:2] {
		if op != wantSets[op.Key] {
			t.Fatalf("set operation = %+v, want %+v", op, wantSets[op.Key])
		}
	}
	wantDels := []cmdctx.FieldMutation{
		{Kind: cmdctx.MutationDelete, Raw: "tags.a=b", Key: "tags.a=b"},
		{Kind: cmdctx.MutationDelete, Raw: "modules.CD", Key: "modules.CD"},
	}
	if !reflect.DeepEqual(got[2:], wantDels) {
		t.Fatalf("deletes = %+v, want %+v", got[2:], wantDels)
	}
}

func TestBuildMutationBody(t *testing.T) {
	fields := map[string]spec.FieldDef{
		"name":    {ID: "name", MutablePath: "name"},
		"tags":    {ID: "tags", MutablePath: "tags", FieldType: "tags"},
		"modules": {ID: "modules", MutablePath: "modules", FieldType: "set"},
		"nested":  {ID: "nested", MutablePath: "config.tags", FieldType: "tags"},
	}
	set := func(key, value string) cmdctx.FieldMutation {
		return cmdctx.FieldMutation{Kind: cmdctx.MutationSet, Raw: key + "=" + value, Key: key, Value: value, HasValue: true}
	}
	del := func(key string) cmdctx.FieldMutation {
		return cmdctx.FieldMutation{Kind: cmdctx.MutationDelete, Raw: key, Key: key}
	}
	base := map[string]any{"name": "keep", "tags": map[string]any{"env": "prod", "team": "ops"}, "modules": []any{"CI", "CE"}}
	tests := []struct {
		name, wrap, wantError string
		base, extra           map[string]any
		ops                   []cmdctx.FieldMutation
		want                  map[string]any
	}{
		{name: "create", base: map[string]any{"name": "widget-id"}, wrap: "widget", ops: []cmdctx.FieldMutation{set("tags.env", "prod"), set("modules.CD", "")},
			want: map[string]any{"widget": map[string]any{"name": "widget-id", "tags": map[string]any{"env": "prod"}, "modules": []any{"CD"}}}},
		{name: "update", base: base, wrap: "widget", ops: []cmdctx.FieldMutation{set("tags.env", "staging"), set("modules.CD", ""), del("tags.team"), del("modules.CI")},
			extra: map[string]any{"audit.reason": "changed", "audit.omitted": nil},
			want:  map[string]any{"widget": map[string]any{"name": "keep", "tags": map[string]any{"env": "staging"}, "modules": []any{"CE", "CD"}}, "audit": map[string]any{"reason": "changed"}}},
		{name: "set_existing_is_idempotent", base: base, ops: []cmdctx.FieldMutation{set("modules.CI", "")}, want: base},
		{name: "delete_last_set_member", base: map[string]any{"modules": []any{"CI"}}, ops: []cmdctx.FieldMutation{del("modules.CI")}, want: map[string]any{"modules": []any{}}},
		{name: "delete_last_tag", base: map[string]any{"tags": map[string]any{"team": "ops"}}, ops: []cmdctx.FieldMutation{del("tags.team")}, want: map[string]any{"tags": map[string]any{}}},
		{name: "delete_from_absent_tags", base: map[string]any{"name": "keep"}, ops: []cmdctx.FieldMutation{del("tags.team")}, want: map[string]any{"name": "keep"}},
		{name: "scalar_set", ops: []cmdctx.FieldMutation{set("name", "new")}, want: map[string]any{"name": "new"}},
		{name: "scalar_delete", base: map[string]any{"name": "old"}, ops: []cmdctx.FieldMutation{del("name")}, want: map[string]any{"name": nil}},
		{name: "nested_path", base: map[string]any{"config": map[string]any{"other": "keep", "tags": map[string]any{"env": "prod"}}},
			ops: []cmdctx.FieldMutation{set("nested.env", "staging")}, want: map[string]any{"config": map[string]any{"other": "keep", "tags": map[string]any{"env": "staging"}}}},
		{name: "set_then_delete", base: map[string]any{"modules": []any{"CI"}}, ops: []cmdctx.FieldMutation{set("modules.CD", ""), del("modules.CD")}, want: map[string]any{"modules": []any{"CI"}}},
		{name: "tag_value_with_equals", ops: []cmdctx.FieldMutation{set("tags.env", "a=b")}, want: map[string]any{"tags": map[string]any{"env": "a=b"}}},
		{name: "unknown_field", ops: []cmdctx.FieldMutation{set("bad", "x")}, wantError: "unknown or read-only"},
		{name: "invalid_tag_set", base: base, ops: []cmdctx.FieldMutation{set("tags", "x")}, wantError: "tag fields require a key"},
		{name: "invalid_tag_delete", base: base, ops: []cmdctx.FieldMutation{del("tags")}, wantError: "tag fields require a key"},
		{name: "invalid_set", base: base, ops: []cmdctx.FieldMutation{set("modules.", "")}, wantError: "set fields require a member"},
		{name: "invalid_set_delete", base: base, ops: []cmdctx.FieldMutation{del("modules")}, wantError: "set fields require a member"},
		{name: "error_after_mutation", base: base, ops: []cmdctx.FieldMutation{set("tags.env", "new"), set("modules", "")}, wantError: "set fields require a member"},
	}
	handlers := New().fieldTypes
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var original, originalExtra map[string]any
			if tc.base != nil {
				original = cloneMutationMap(tc.base)
			}
			if tc.extra != nil {
				originalExtra = cloneMutationMap(tc.extra)
			}
			got, err := buildMutationBody(tc.base, fields, handlers, tc.ops, tc.wrap, tc.extra)
			if tc.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantError) || got != nil {
					t.Fatalf("body = %#v, error = %v, want %q and no body", got, err, tc.wantError)
				}
			} else if err != nil || !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("body = %#v, error = %v, want %#v", got, err, tc.want)
			}
			if !reflect.DeepEqual(tc.base, original) || !reflect.DeepEqual(tc.extra, originalExtra) {
				t.Fatalf("inputs modified: base = %#v, extra = %#v", tc.base, tc.extra)
			}
		})
	}
}

func TestBuildMutationBodyIsolatesHandlersAndResult(t *testing.T) {
	field := spec.FieldDef{ID: "items", MutablePath: "nested.items", FieldType: "example:items"}
	base := map[string]any{"nested": map[string]any{"items": map[string]any{"old": "keep"}}}
	op := cmdctx.FieldMutation{Kind: cmdctx.MutationSet, Key: "items.value", Value: "new"}
	handlers := map[string]cmdctx.FieldTypeHandler{"example:items": {
		Mutate: func(_ spec.FieldDef, current any, _ cmdctx.FieldMutation) (any, bool, error) {
			current.(map[string]any)["old"] = "changed"
			return current, false, nil
		},
	}}
	got, err := buildMutationBody(base, map[string]spec.FieldDef{"items": field}, handlers, []cmdctx.FieldMutation{op}, "widget", nil)
	want := map[string]any{"widget": base}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("body = %#v, error = %v, want %#v", got, err, want)
	}
	got["widget"].(map[string]any)["nested"].(map[string]any)["items"].(map[string]any)["old"] = "after"
	if base["nested"].(map[string]any)["items"].(map[string]any)["old"] != "keep" {
		t.Fatalf("result aliases input: %#v", base)
	}
}

func TestMutationBodyForCtxResolvesRegisteredTypes(t *testing.T) {
	r := New()
	if err := r.RegisterNoun(spec.NounDef{Noun: "widget", Fields: []spec.FieldDef{
		{ID: "tags", Expr: "it.tags", MutablePath: "tags", FieldType: "tags"},
		{ID: "modules", Expr: "it.modules", MutablePath: "modules", FieldType: "set"},
	}}); err != nil {
		t.Fatal(err)
	}
	ctx := &cmdctx.Ctx{Resolver: r, Noun: "widget", SetArgs: map[string]string{"tags.env": "prod", "modules.CD": ""},
		MutationFlags: []cmdctx.FieldMutation{{Kind: cmdctx.MutationSet, Raw: "modules.CD", Key: "modules.CD"}}}
	got, err := mutationBodyForCtx(ctx, map[string]any{}, "widget", nil)
	want := map[string]any{"widget": map[string]any{"tags": map[string]any{"env": "prod"}, "modules": []any{"CD"}}}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("body = %#v, error = %v, want %#v", got, err, want)
	}
}
