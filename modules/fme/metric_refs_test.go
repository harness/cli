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

// The read shape is [{id, name}]; the write shape is plain id strings.
func TestNormalizeMetricRefs(t *testing.T) {
	got, err := normalizeMetricRefs(spec.FieldDef{}, []any{
		map[string]any{"id": "m1", "name": "Metric 1"},
		map[string]any{"name": "no id"},
		"m2",
	})
	if err != nil {
		t.Fatal(err)
	}
	if want := []any{"m1", "m2"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("normalized = %v, want %v", got, want)
	}
}

func TestMutateMetricRefs(t *testing.T) {
	current := []any{"m1", "m2"}
	tests := []struct {
		name    string
		op      cmdctx.FieldMutation
		want    []any
		wantErr string
	}{
		{"add", cmdctx.FieldMutation{Kind: cmdctx.MutationAdd, Key: "key_metrics.m3"}, []any{"m1", "m2", "m3"}, ""},
		{"add existing is a no-op", cmdctx.FieldMutation{Kind: cmdctx.MutationAdd, Key: "key_metrics.m1"}, current, ""},
		{"set adds a member", cmdctx.FieldMutation{Kind: cmdctx.MutationSet, Key: "key_metrics.m3"}, []any{"m1", "m2", "m3"}, ""},
		{"delete", cmdctx.FieldMutation{Kind: cmdctx.MutationDelete, Key: "key_metrics.m1"}, []any{"m2"}, ""},
		{"missing member", cmdctx.FieldMutation{Kind: cmdctx.MutationAdd, Key: "key_metrics"}, nil, "requires a metric id"},
		{"add with value rejected", cmdctx.FieldMutation{Kind: cmdctx.MutationAdd, Key: "key_metrics.m3", HasValue: true}, nil, "do not take a value"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, write, err := mutateMetricRefs(spec.FieldDef{}, current, tt.op)
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
