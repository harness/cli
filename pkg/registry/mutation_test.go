// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package registry

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/harness/cli/v3/pkg/cmdctx"
	"github.com/harness/cli/v3/pkg/spec"
)

func TestJSONCopyMap(t *testing.T) {
	input := map[string]any{"nested": map[string]any{"items": []any{"CI"}}}
	copy, err := jsonCopyMap(input)
	if err != nil || !reflect.DeepEqual(copy, input) {
		t.Fatalf("copy = %#v, error = %v, want %#v", copy, err, input)
	}
	copy["nested"].(map[string]any)["items"].([]any)[0] = "CD"
	if input["nested"].(map[string]any)["items"].([]any)[0] != "CI" {
		t.Fatalf("copy aliases input: %#v", input)
	}
	if _, err := jsonCopyMap([]string{"not", "an", "object"}); err == nil {
		t.Fatal("expected error for non-object JSON")
	}
}

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
	add := func(key, value string, hasValue bool) cmdctx.FieldMutation {
		return cmdctx.FieldMutation{Kind: cmdctx.MutationAdd, Key: key, Value: value, HasValue: hasValue}
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
		{name: "add_tags_and_set_members", base: base, ops: []cmdctx.FieldMutation{add("tags.region", "us", true), add("modules.CD", "", false), add("modules.CE", "", false)},
			want: map[string]any{"name": "keep", "tags": map[string]any{"env": "prod", "team": "ops", "region": "us"}, "modules": []any{"CI", "CE", "CD"}}},
		{name: "add_existing_same_value", base: base, ops: []cmdctx.FieldMutation{add("tags.env", "prod", true), add("modules.CI", "", false)}, want: base},
		{name: "add_empty_map_value", ops: []cmdctx.FieldMutation{add("tags.note", "", true)}, want: map[string]any{"tags": map[string]any{"note": ""}}},
		{name: "add_map_conflict", base: base, ops: []cmdctx.FieldMutation{add("tags.env", "staging", true)}, wantError: "key already exists"},
		{name: "add_map_missing_value", ops: []cmdctx.FieldMutation{add("tags.env", "", false)}, wantError: "key=value"},
		{name: "add_set_with_value", ops: []cmdctx.FieldMutation{add("modules.CD", "x", true)}, wantError: "do not take a value"},
		{name: "add_set_missing_member", ops: []cmdctx.FieldMutation{add("modules", "", false)}, wantError: "require a member"},
		{name: "add_scalar", ops: []cmdctx.FieldMutation{add("name", "new", true)}, wantError: "do not support addition"},
		{name: "delete_then_add", base: base, ops: []cmdctx.FieldMutation{del("tags.env"), add("tags.env", "staging", true)},
			want: map[string]any{"name": "keep", "tags": map[string]any{"env": "staging", "team": "ops"}, "modules": []any{"CI", "CE"}}},
		{name: "add_then_delete", base: base, ops: []cmdctx.FieldMutation{add("modules.CD", "", false), del("modules.CD")}, want: base},
		{name: "intermediate_add_conflict_aborts", base: base, ops: []cmdctx.FieldMutation{add("tags.env", "wrong", true), del("tags.env")}, wantError: "key already exists"},
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

func TestBuildMutationBodyNormalizesAndEncodesOnce(t *testing.T) {
	field := spec.FieldDef{ID: "items", MutablePath: "items", FieldType: "example:items"}
	base := map[string]any{"items": []any{map[string]any{"name": "seed", "id": "read-only"}}}
	normalizes, encodes := 0, 0
	handlers := map[string]cmdctx.FieldTypeHandler{"example:items": {
		Normalize: func(_ spec.FieldDef, current any) (any, error) {
			normalizes++
			if normalizes > 1 {
				t.Fatal("normalized an already-mutated field")
			}
			return []any{current.([]any)[0].(map[string]any)["name"]}, nil
		},
		Mutate: func(_ spec.FieldDef, current any, op cmdctx.FieldMutation) (any, bool, error) {
			return append(current.([]any), op.Value), true, nil
		},
		Encode: func(_ spec.FieldDef, current any) (any, error) {
			encodes++
			out := []any{}
			for _, name := range current.([]any) {
				out = append(out, map[string]any{"name": name})
			}
			return out, nil
		},
	}}
	ops := []cmdctx.FieldMutation{{Kind: cmdctx.MutationAdd, Key: "items", Value: "one"}, {Kind: cmdctx.MutationAdd, Key: "items", Value: "two"}}
	got, err := buildMutationBody(base, map[string]spec.FieldDef{"items": field}, handlers, ops, "", nil)
	want := map[string]any{"items": []any{map[string]any{"name": "seed"}, map[string]any{"name": "one"}, map[string]any{"name": "two"}}}
	if err != nil || !reflect.DeepEqual(got, want) || normalizes != 1 || encodes != 1 {
		t.Fatalf("body = %#v, error = %v, normalizes = %d, encodes = %d", got, err, normalizes, encodes)
	}
	if !reflect.DeepEqual(base, map[string]any{"items": []any{map[string]any{"name": "seed", "id": "read-only"}}}) {
		t.Fatalf("input mutated: %#v", base)
	}
}

func TestBuildMutationBodyWithPickNormalizesPresentFieldsOnce(t *testing.T) {
	field := spec.FieldDef{ID: "items", MutablePath: "nested.items", FieldType: "example:items"}
	base := map[string]any{"nested": map[string]any{"items": "read", "other": "keep"}, "unknown": true}
	count := 0
	handlers := map[string]cmdctx.FieldTypeHandler{"example:items": {
		Normalize: func(_ spec.FieldDef, current any) (any, error) {
			count++
			return current.(string) + ":normalized", nil
		},
		Mutate: func(_ spec.FieldDef, current any, _ cmdctx.FieldMutation) (any, bool, error) {
			if current != "read:normalized" && current != "changed" {
				t.Fatalf("mutation current = %v", current)
			}
			return "changed", true, nil
		},
	}}
	fields := map[string]spec.FieldDef{field.ID: field}
	ops := []cmdctx.FieldMutation{{Kind: cmdctx.MutationSet, Key: "items"}, {Kind: cmdctx.MutationSet, Key: "items"}}
	got, err := buildMutationBodyWithPick(base, []spec.FieldDef{field}, fields, handlers, ops, "", nil, false)
	want := map[string]any{"nested": map[string]any{"items": "changed", "other": "keep"}, "unknown": true}
	if err != nil || count != 1 || !reflect.DeepEqual(got, want) {
		t.Fatalf("body = %#v, normalizes = %d, error = %v, want %#v", got, count, err, want)
	}
	if base["nested"].(map[string]any)["items"] != "read" {
		t.Fatalf("base mutated: %#v", base)
	}
}

func TestBuildMutationBodyWithPickSkipsAbsentAndFailsOnInvalidValue(t *testing.T) {
	field := spec.FieldDef{ID: "items", MutablePath: "nested.items", FieldType: "example:items"}
	count := 0
	handlers := map[string]cmdctx.FieldTypeHandler{"example:items": {
		Normalize: func(_ spec.FieldDef, _ any) (any, error) {
			count++
			return nil, errors.New("invalid items")
		},
	}}
	fields := map[string]spec.FieldDef{field.ID: field}
	got, err := buildMutationBodyWithPick(map[string]any{"nested": map[string]any{"other": "keep"}}, []spec.FieldDef{field}, fields, handlers, nil, "", nil, false)
	if err != nil || count != 0 || !reflect.DeepEqual(got, map[string]any{"nested": map[string]any{"other": "keep"}}) {
		t.Fatalf("absent body = %#v, count = %d, error = %v", got, count, err)
	}
	got, err = buildMutationBodyWithPick(map[string]any{"nested": map[string]any{"items": nil}}, []spec.FieldDef{field}, fields, handlers, nil, "", nil, false)
	if got != nil || err == nil || !strings.Contains(err.Error(), "field \"items\": normalizing picked value: invalid items") || count != 1 {
		t.Fatalf("invalid body = %#v, count = %d, error = %v", got, count, err)
	}
}

func TestBuildMutationBodyWithPickSparsePatch(t *testing.T) {
	fields := map[string]spec.FieldDef{
		"owner":   {ID: "owner", MutablePath: "config.owner"},
		"modules": {ID: "modules", MutablePath: "modules", FieldType: "set"},
		"tags":    {ID: "tags", MutablePath: "tags", FieldType: "tags"},
		"other":   {ID: "other", MutablePath: "other", FieldType: "example:other"},
	}
	normalizes := 0
	handlers := New().fieldTypes
	handlers["example:other"] = cmdctx.FieldTypeHandler{
		Normalize: func(_ spec.FieldDef, current any) (any, error) {
			normalizes++
			return current, nil
		},
	}
	base := map[string]any{"config": map[string]any{"owner": "current", "untouched": true},
		"modules": []any{"CI"}, "tags": map[string]any{"env": "prod"}, "other": "keep", "unknown": "keep"}
	ops := []cmdctx.FieldMutation{
		{Kind: cmdctx.MutationDelete, Raw: "owner", Key: "owner"},
		{Kind: cmdctx.MutationDelete, Raw: "modules.CI", Key: "modules.CI"},
		{Kind: cmdctx.MutationAdd, Raw: "tags.env=prod", Key: "tags.env", Value: "prod", HasValue: true},
	}
	got, err := buildMutationBodyWithPick(base, []spec.FieldDef{fields["other"]}, fields, handlers, ops, "widget", map[string]any{"audit.reason": "review", "audit.omitted": nil}, true)
	want := map[string]any{"widget": map[string]any{"config": map[string]any{"owner": nil}, "modules": []any{}}, "audit": map[string]any{"reason": "review"}}
	if err != nil || normalizes != 1 || !reflect.DeepEqual(got, want) {
		t.Fatalf("PATCH body = %#v, normalizes = %d, error = %v, want %#v", got, normalizes, err, want)
	}
	if base["config"].(map[string]any)["owner"] != "current" {
		t.Fatalf("base mutated: %#v", base)
	}
	got, err = buildMutationBodyWithPick(base, nil, fields, handlers, ops[2:], "", nil, true)
	if err != nil || !reflect.DeepEqual(got, map[string]any{}) {
		t.Fatalf("no-op PATCH body = %#v, error = %v", got, err)
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
	got, err := mutationBodyForCtx(ctx, map[string]any{}, "widget", nil, false, false)
	want := map[string]any{"widget": map[string]any{"tags": map[string]any{"env": "prod"}, "modules": []any{"CD"}}}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("body = %#v, error = %v, want %#v", got, err, want)
	}
}

func TestMutationBodyForCtxNormalizesPickedFieldsWithoutMutations(t *testing.T) {
	r := New()
	r.Module("example").RegisterFieldType("items", cmdctx.FieldTypeHandler{
		Normalize: func(_ spec.FieldDef, current any) (any, error) {
			return current.(string) + ":normalized", nil
		},
		Mutate: mutateScalar,
	})
	if err := r.RegisterNoun(spec.NounDef{Noun: "widget", Fields: []spec.FieldDef{
		{ID: "items", Expr: "it.nested.items", MutablePath: "nested.items", FieldType: "example:items"},
	}}); err != nil {
		t.Fatal(err)
	}
	ctx := &cmdctx.Ctx{Resolver: r, Noun: "widget"}
	base := map[string]any{"nested": map[string]any{"items": "read"}}
	for _, tc := range []struct {
		picked bool
		want   string
	}{{picked: true, want: "read:normalized"}, {picked: false, want: "read"}} {
		got, err := mutationBodyForCtx(ctx, base, "", nil, tc.picked, false)
		if err != nil || got["nested"].(map[string]any)["items"] != tc.want {
			t.Fatalf("picked = %t, body = %#v, error = %v, want %q", tc.picked, got, err, tc.want)
		}
	}
}
