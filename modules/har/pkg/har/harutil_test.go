// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package har

import "testing"

func TestParseRegistry(t *testing.T) {
	tests := []struct {
		name    string
		id      string
		want    string
		wantErr bool
	}{
		{name: "registry only", id: "my-registry", want: "my-registry"},
		{name: "legacy registry and name", id: "my-registry/package", want: "my-registry"},
		{name: "empty", id: "", wantErr: true},
		{name: "missing registry", id: "/package", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseRegistry(tt.id)
			if tt.wantErr {
				if err == nil {
					t.Fatal("parseRegistry() error = nil, want error")
				}
				return
			}
			if err != nil {
				t.Fatalf("parseRegistry() error = %v", err)
			}
			if got != tt.want {
				t.Errorf("parseRegistry() = %q, want %q", got, tt.want)
			}
		})
	}
}
