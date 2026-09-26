// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package exprenv

import (
	"reflect"
	"testing"

	"github.com/harness/cli/v3/pkg/cmdctx"
)

func baseEnv() map[string]any {
	return map[string]any{
		"ctx": map[string]any{
			"id":       "my-id",
			"parentId": "parent-id",
		},
		"flags": map[string]any{
			"search":  "hello",
			"timeout": "",
		},
		"it": map[string]any{
			"name":        "test-item",
			"description": "a description",
			"nested": map[string]any{
				"value": "deep",
			},
		},
	}
}

func TestEvalExpr(t *testing.T) {
	env := baseEnv()
	tests := []struct {
		expr string
		want string
	}{
		{"it.name", "test-item"},
		{"it.nested.value", "deep"},
		{"ctx.id", "my-id"},
		{"flags.search", "hello"},
		// nil/missing → empty string
		{"it.missing", ""},
		// oneof null-coalescing
		{"it.absent ?? it.name", "test-item"},
		// string literal
		{`"literal"`, "literal"},
	}
	for _, tc := range tests {
		t.Run(tc.expr, func(t *testing.T) {
			got := EvalExpr(env, tc.expr)
			if got != tc.want {
				t.Errorf("EvalExpr(%q) = %q, want %q", tc.expr, got, tc.want)
			}
		})
	}
}

func TestEvalExprAny_Nil(t *testing.T) {
	env := baseEnv()
	// nil return from conditional — used for optional body params
	_, ok := EvalExprAny(env, `flags.timeout != "" ? flags.timeout : nil`)
	if ok {
		t.Error("expected ok=false for nil expression result")
	}
}

func TestEvalExprAny_Value(t *testing.T) {
	env := baseEnv()
	env["flags"] = map[string]any{"timeout": "5000"}
	result, ok := EvalExprAny(env, `flags.timeout != "" ? flags.timeout : nil`)
	if !ok {
		t.Fatal("expected ok=true")
	}
	if result != "5000" {
		t.Errorf("got %v, want %q", result, "5000")
	}
}

func TestFlagPresenceFunctions(t *testing.T) {
	tests := []struct {
		name     string
		flags    map[string]any
		provided map[string]bool
		expr     string
		want     any
		wantOK   bool
	}{
		{"absent empty", map[string]any{"comment": ""}, nil, `flagIfProvided("comment")`, nil, false},
		{"explicit empty", map[string]any{"comment": ""}, map[string]bool{"comment": true}, `flagIfProvided("comment")`, "", true},
		{"explicit value", map[string]any{"comment": "hello"}, map[string]bool{"comment": true}, `flagIfProvided("comment")`, "hello", true},
		{"absent nonempty default", map[string]any{"mode": "auto"}, nil, `flagIfProvided("mode")`, nil, false},
		{"explicit false", map[string]any{"enabled": false}, map[string]bool{"enabled": true}, `flagIfProvided("enabled")`, false, true},
		{"explicit zero", map[string]any{"count": 0}, map[string]bool{"count": true}, `flagIfProvided("count")`, 0, true},
		{"absent predicate", map[string]any{"comment": ""}, nil, `flagProvided("comment")`, false, true},
		{"present predicate", map[string]any{"comment": ""}, map[string]bool{"comment": true}, `flagProvided("comment")`, true, true},
		{"absent presence value", map[string]any{"comment": ""}, nil, `providedFlags.comment`, false, true},
		{"present empty presence value", map[string]any{"comment": ""}, map[string]bool{"comment": true}, `providedFlags.comment`, true, true},
		{"nonempty default presence value", map[string]any{"mode": "auto"}, nil, `providedFlags.mode`, false, true},
		{"explicit false presence value", map[string]any{"enabled": false}, map[string]bool{"enabled": true}, `providedFlags.enabled`, true, true},
		{"hyphenated presence value", map[string]any{"no-cascade": false}, map[string]bool{"no-cascade": true}, `providedFlags["no-cascade"]`, true, true},
		{"transformed absent", map[string]any{"action": ""}, nil, `flagProvided("action") ? [flags.action] : nil`, nil, false},
		{"transformed present", map[string]any{"action": ""}, map[string]bool{"action": true}, `flagProvided("action") ? [flags.action] : nil`, []any{""}, true},
		{"transformed with presence value", map[string]any{"action": ""}, map[string]bool{"action": true}, `providedFlags.action ? [flags.action] : nil`, []any{""}, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := &cmdctx.Ctx{FlagValues: tc.flags, ProvidedFlags: tc.provided}
			got, ok := EvalExprAny(Make(ctx), tc.expr)
			if ok != tc.wantOK || !reflect.DeepEqual(got, tc.want) {
				t.Errorf("EvalExprAny(%q) = (%v, %t), want (%v, %t)", tc.expr, got, ok, tc.want, tc.wantOK)
			}
		})
	}
}

func TestMake_ProvidedFlagsIncludesExplicitCoreFlags(t *testing.T) {
	ctx := &cmdctx.Ctx{
		FlagValues:    map[string]any{"comment": ""},
		ProvidedFlags: map[string]bool{"timeout": true},
	}
	provided := Make(ctx)["providedFlags"].(map[string]bool)
	if value, ok := provided["comment"]; !ok || value {
		t.Errorf("providedFlags.comment = (%t, %t), want (false, true)", value, ok)
	}
	if !provided["timeout"] {
		t.Error("explicit core flag should be present")
	}
	if _, ok := ctx.ProvidedFlags["comment"]; ok {
		t.Error("Make should not mutate ctx.ProvidedFlags")
	}
}

func TestEvalItemsExpr(t *testing.T) {
	env := map[string]any{
		"it": map[string]any{
			"items": []any{"a", "b", "c"},
		},
	}
	items, err := EvalItemsExpr(env, "it.items")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(items) != 3 {
		t.Fatalf("got %d items, want 3", len(items))
	}
}

func TestEvalItemsExpr_Concat(t *testing.T) {
	env := map[string]any{
		"it": map[string]any{
			"entity_types": []any{"e1", "e2"},
			"event_types":  []any{"ev1"},
			"config_types": nil,
		},
		"concat": func(args ...[]any) []any {
			var out []any
			for _, a := range args {
				out = append(out, a...)
			}
			return out
		},
	}
	// mirrors the kg:type items_expr pattern
	items, err := EvalItemsExpr(env, `concat(it.entity_types ?? [], it.event_types ?? [], it.config_types ?? [])`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(items) != 3 {
		t.Fatalf("got %d items, want 3", len(items))
	}
}

func TestResolvePath(t *testing.T) {
	env := map[string]any{
		"ctx": map[string]any{"parentId": "my-repo", "id": "main"},
	}
	tests := []struct {
		path string
		want string
		err  bool
	}{
		{"/code/api/v1/repos/{{ctx.parentId}}/branches", "/code/api/v1/repos/my-repo/branches", false},
		{"/code/api/v1/repos/{{ctx.parentId}}/branches/{{ctx.id}}", "/code/api/v1/repos/my-repo/branches/main", false},
		{"/static/path", "/static/path", false},
		{"{{ctx.id}}", "main", false},
		{"{{unclosed", "", true},
	}
	for _, tc := range tests {
		t.Run(tc.path, func(t *testing.T) {
			got, err := ResolvePath(env, tc.path)
			if tc.err {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("ResolvePath(%q) = %q, want %q", tc.path, got, tc.want)
			}
		})
	}
}

func TestWithIt(t *testing.T) {
	base := map[string]any{"ctx": "original", "it": "old"}
	updated := WithIt(base, "new-it")
	if updated["it"] != "new-it" {
		t.Errorf("expected it=new-it, got %v", updated["it"])
	}
	// base must be unchanged
	if base["it"] != "old" {
		t.Error("WithIt mutated the original env")
	}
}
