// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package fme

import (
	"reflect"
	"strings"
	"testing"

	"github.com/harness/cli/v3/pkg/cmdctx"
	"github.com/harness/cli/v3/pkg/spec"
)

func tagList(names ...string) []any {
	out := make([]any, 0, len(names))
	for _, n := range names {
		out = append(out, map[string]any{"id": "id-" + n, "name": n})
	}
	return out
}

func TestMutateTags(t *testing.T) {
	tests := []struct {
		name    string
		current any
		op      cmdctx.FieldMutation
		want    []string
		wantErr string
	}{
		{"add new", tagList("a"), cmdctx.FieldMutation{Kind: cmdctx.MutationAdd, Key: "tags.b"}, []string{"a", "b"}, ""},
		{"add existing is a no-op", tagList("a"), cmdctx.FieldMutation{Kind: cmdctx.MutationAdd, Key: "tags.a"}, []string{"a"}, ""},
		{"add to nil", nil, cmdctx.FieldMutation{Kind: cmdctx.MutationAdd, Key: "tags.a"}, []string{"a"}, ""},
		{"delete", tagList("a", "b"), cmdctx.FieldMutation{Kind: cmdctx.MutationDelete, Key: "tags.a"}, []string{"b"}, ""},
		{"delete missing is a no-op", tagList("a"), cmdctx.FieldMutation{Kind: cmdctx.MutationDelete, Key: "tags.z"}, []string{"a"}, ""},
		{"delete with value uses raw", tagList("a", "b"), cmdctx.FieldMutation{Kind: cmdctx.MutationDelete, Key: "tags", Raw: "tags.a", HasValue: true}, []string{"b"}, ""},
		{"set rejected", nil, cmdctx.FieldMutation{Kind: cmdctx.MutationSet, Key: "tags"}, nil, "use --add/--del"},
		{"missing member", nil, cmdctx.FieldMutation{Kind: cmdctx.MutationAdd, Key: "tags"}, nil, "require a member"},
		{"add with value rejected", nil, cmdctx.FieldMutation{Kind: cmdctx.MutationAdd, Key: "tags.a", HasValue: true}, nil, "do not take a value"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, write, err := mutateTags(spec.FieldDef{}, tt.current, tt.op)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil || !write {
				t.Fatalf("write=%v err=%v", write, err)
			}
			var names []string
			for _, item := range got.([]any) {
				names = append(names, tagName(item))
			}
			if !reflect.DeepEqual(names, tt.want) {
				t.Fatalf("tags = %v, want %v", names, tt.want)
			}
		})
	}
}

// The read shape carries a read-only id; the write shape must be {name} only.
func TestEncodeTags(t *testing.T) {
	got, err := encodeTags(spec.FieldDef{}, append(tagList("a", "b"), map[string]any{"id": "x"}))
	if err != nil {
		t.Fatal(err)
	}
	want := []any{map[string]any{"name": "a"}, map[string]any{"name": "b"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("encoded = %v, want %v", got, want)
	}
}
