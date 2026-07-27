// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package registry

import "testing"

func TestParseScopePrefix(t *testing.T) {
	tests := []struct {
		name      string
		raw       string
		isList    bool
		wantID    string
		wantLevel string
	}{
		{
			name:      "account dot prefix",
			raw:       "account.foo",
			wantID:    "foo",
			wantLevel: "account",
		},
		{
			name:      "org dot prefix",
			raw:       "org.bar",
			wantLevel: "org",
			wantID:    "bar",
		},
		{
			name:      "list bare account",
			raw:       "account",
			isList:    true,
			wantID:    "",
			wantLevel: "account",
		},
		{
			name:      "non-list bare account",
			raw:       "account",
			wantID:    "account",
			wantLevel: "project",
		},
		{
			name:      "list bare org",
			raw:       "org",
			isList:    true,
			wantID:    "",
			wantLevel: "org",
		},
		{
			name:      "non-list bare org treated as literal id",
			raw:       "org",
			wantID:    "org",
			wantLevel: "project",
		},
		{
			name:      "plain id has project level",
			raw:       "my-resource",
			wantID:    "my-resource",
			wantLevel: "project",
		},
		{
			name:      "empty string has project level",
			raw:       "",
			wantID:    "",
			wantLevel: "project",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotID, gotLevel := parseScopePrefix(tt.raw, tt.isList)
			if gotID != tt.wantID || gotLevel != tt.wantLevel {
				t.Fatalf("parseScopePrefix(%q, %v) = (%q, %q), want (%q, %q)",
					tt.raw, tt.isList, gotID, gotLevel, tt.wantID, tt.wantLevel)
			}
		})
	}
}
