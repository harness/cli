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

func TestMutateFlagSets(t *testing.T) {
	current := []any{map[string]any{"id": "fs-1"}, map[string]any{"id": "fs-2"}}
	tests := []struct {
		name    string
		op      cmdctx.FieldMutation
		want    []any
		wantErr string
	}{
		{"add new", cmdctx.FieldMutation{Kind: cmdctx.MutationAdd, Key: "flag_sets.fs-3"}, append(append([]any{}, current...), map[string]any{"id": "fs-3"}), ""},
		{"add existing is a no-op", cmdctx.FieldMutation{Kind: cmdctx.MutationAdd, Key: "flag_sets.fs-1"}, current, ""},
		{"delete", cmdctx.FieldMutation{Kind: cmdctx.MutationDelete, Key: "flag_sets.fs-1"}, current[1:], ""},
		{"set rejected", cmdctx.FieldMutation{Kind: cmdctx.MutationSet, Key: "flag_sets"}, nil, "use --add/--del"},
		{"missing member", cmdctx.FieldMutation{Kind: cmdctx.MutationAdd, Key: "flag_sets"}, nil, "require a member"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, write, err := mutateFlagSets(spec.FieldDef{}, current, tt.op)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil || !write || !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("got=%v write=%v err=%v, want %v", got, write, err, tt.want)
			}
		})
	}
}

func TestEncodeFlagSets(t *testing.T) {
	got, err := encodeFlagSets(spec.FieldDef{}, []any{
		map[string]any{"id": "fs-1", "name": "ignored", "type": "ignored"},
		map[string]any{"name": "no id"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if want := []any{map[string]any{"id": "fs-1"}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("encoded = %v, want %v", got, want)
	}
}
