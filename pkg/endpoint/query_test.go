// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package endpoint

import (
	"reflect"
	"testing"
)

func TestEvalQueryParams(t *testing.T) {
	env := map[string]any{"flags": map[string]any{"ids": []string{"a", "b"}, "none": []string{}, "q": "x"}}
	tests := []struct {
		name       string
		exprs      map[string]string
		wantSingle map[string]string
		wantMulti  map[string][]string
	}{
		{
			name:       "list literal becomes repeated key",
			exprs:      map[string]string{"kind": `["comment", "change-comment"]`},
			wantSingle: map[string]string{},
			wantMulti:  map[string][]string{"kind": {"comment", "change-comment"}},
		},
		{
			name:       "string slice flag becomes repeated key",
			exprs:      map[string]string{"id": `flags.ids`},
			wantSingle: map[string]string{},
			wantMulti:  map[string][]string{"id": {"a", "b"}},
		},
		{
			name:       "empty and blank lists omit the key",
			exprs:      map[string]string{"a": `flags.none`, "b": `["", ""]`},
			wantSingle: map[string]string{},
		},
		{
			name:       "joined list stays a single value",
			exprs:      map[string]string{"id": `join(flags.ids, ",")`, "q": `flags.q`, "nil": `nil`},
			wantSingle: map[string]string{"id": "a,b", "q": "x"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			single, multi := EvalQueryParams(env, tc.exprs)
			if !reflect.DeepEqual(single, tc.wantSingle) {
				t.Errorf("single = %v, want %v", single, tc.wantSingle)
			}
			if !reflect.DeepEqual(multi, tc.wantMulti) {
				t.Errorf("multi = %v, want %v", multi, tc.wantMulti)
			}
		})
	}
}
