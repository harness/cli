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

func TestMutateOwners(t *testing.T) {
	current := []any{
		map[string]any{"id": "u1", "type": "USER", "name": "Alice", "email": "alice@example.com"},
		map[string]any{"id": "platform", "type": "GROUP", "name": "platform"},
	}
	add := func(key string) cmdctx.FieldMutation { return cmdctx.FieldMutation{Kind: cmdctx.MutationAdd, Key: key} }
	del := func(key string) cmdctx.FieldMutation {
		return cmdctx.FieldMutation{Kind: cmdctx.MutationDelete, Key: key}
	}

	tests := []struct {
		name    string
		op      cmdctx.FieldMutation
		wantLen int
		check   func(t *testing.T, got []any)
		wantErr string
	}{
		{"add user by email", add("owners.user:bob@example.com"), 3, func(t *testing.T, got []any) {
			want := map[string]any{"type": "USER", "email": "bob@example.com"}
			if !reflect.DeepEqual(got[2], want) {
				t.Fatalf("added = %v, want %v", got[2], want)
			}
		}, ""},
		{"add user by id", add("owners.user:u9"), 3, func(t *testing.T, got []any) {
			want := map[string]any{"type": "USER", "id": "u9"}
			if !reflect.DeepEqual(got[2], want) {
				t.Fatalf("added = %v, want %v", got[2], want)
			}
		}, ""},
		{"add group", add("owners.group:ops"), 3, func(t *testing.T, got []any) {
			want := map[string]any{"type": "GROUP", "identifier": "ops"}
			if !reflect.DeepEqual(got[2], want) {
				t.Fatalf("added = %v, want %v", got[2], want)
			}
		}, ""},
		{"add existing user by email is a no-op", add("owners.user:alice@example.com"), 2, nil, ""},
		{"add existing group matched by read-side id is a no-op", add("owners.group:platform"), 2, nil, ""},
		{"delete user by id", del("owners.user:u1"), 1, nil, ""},
		{"delete user by email", del("owners.user:alice@example.com"), 1, nil, ""},
		{"delete group", del("owners.group:platform"), 1, nil, ""},
		{"scheme is case-insensitive", del("owners.USER:u1"), 1, nil, ""},
		{"user scheme does not match a group", del("owners.user:platform"), 2, nil, ""},
		{"set rejected", cmdctx.FieldMutation{Kind: cmdctx.MutationSet, Key: "owners"}, 0, nil, "use --add/--del"},
		{"missing member", add("owners"), 0, nil, "require a member"},
		{"missing scheme", add("owners.bob@example.com"), 0, nil, "must be user:"},
		{"unknown scheme", add("owners.team:x"), 0, nil, "unrecognized owner scheme"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, write, err := mutateOwners(spec.FieldDef{}, current, tt.op)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil || !write {
				t.Fatalf("write=%v err=%v", write, err)
			}
			items := got.([]any)
			if len(items) != tt.wantLen {
				t.Fatalf("len = %d, want %d: %v", len(items), tt.wantLen, items)
			}
			if tt.check != nil {
				tt.check(t, items)
			}
		})
	}
}

// A GROUP owner read back from the API carries its identifier under "id";
// it must be re-encoded as {type: GROUP, identifier} when preserved across
// an unrelated owners edit.
func TestEncodeOwners(t *testing.T) {
	got, err := encodeOwners(spec.FieldDef{}, []any{
		map[string]any{"type": "GROUP", "id": "platform-team", "name": "platform-team"},
		map[string]any{"type": "GROUP", "identifier": "ops", "id": "ignored"},
		map[string]any{"type": "USER", "id": "u1", "email": "alice@example.com"},
		map[string]any{"type": "USER", "email": "bob@example.com"},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []any{
		map[string]any{"type": "GROUP", "identifier": "platform-team"},
		map[string]any{"type": "GROUP", "identifier": "ops"},
		map[string]any{"type": "USER", "id": "u1"},
		map[string]any{"type": "USER", "email": "bob@example.com"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("encoded = %v, want %v", got, want)
	}
}

func TestEncodeOwners_Errors(t *testing.T) {
	for name, in := range map[string]map[string]any{
		"user without id or email": {"type": "USER", "name": "x"},
		"group without identifier": {"type": "GROUP", "name": "x"},
		"unknown type":             {"type": "BOT"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := encodeOwners(spec.FieldDef{}, []any{in}); err == nil {
				t.Fatal("want error")
			}
		})
	}
}

func TestNormalizeOwners(t *testing.T) {
	got, err := normalizeOwners(spec.FieldDef{}, []any{
		map[string]any{"id": "u1", "type": "USER", "name": "Alice", "extra": "dropped"},
		"not-a-map",
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []any{map[string]any{"type": "USER", "id": "u1", "name": "Alice", "email": "", "identifier": ""}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("normalized = %v, want %v", got, want)
	}
}
