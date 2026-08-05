// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package plugin

import (
	"path/filepath"
	"testing"
)

func TestNameFromBinary(t *testing.T) {
	tests := []struct {
		path     string
		wantName string
		wantOK   bool
	}{
		{"harness-har", "har", true},
		{"harness-har.exe", "har", true},
		{filepath.Join("some", "dir", "harness-har.exe"), "har", true},
		{"harness-my-plugin", "my-plugin", true},
		// Not conforming: no prefix, empty name, or an illegal name charset.
		{"harness", "", false},
		{"har", "", false},
		{"harness-", "", false},
		{"harness-.exe", "", false},
		{"harness-Har", "", false},
		{"harness-my_plugin", "", false},
		{"harness--har", "", false},
		{"harness-har-", "", false},
		{"harness-../evil", "", false},
	}
	for _, tt := range tests {
		name, ok := NameFromBinary(tt.path)
		if ok != tt.wantOK || name != tt.wantName {
			t.Errorf("NameFromBinary(%q) = (%q, %v), want (%q, %v)", tt.path, name, ok, tt.wantName, tt.wantOK)
		}
	}
}

func TestBinaryNameRoundTrip(t *testing.T) {
	for _, name := range []string{"har", "my-plugin", "abc123"} {
		got, ok := NameFromBinary(BinaryName(name))
		if !ok || got != name {
			t.Errorf("round trip of %q = (%q, %v)", name, got, ok)
		}
	}
}

func TestValidateName(t *testing.T) {
	valid := []string{"har", "my-plugin", "a1", "abc-123-xyz"}
	for _, name := range valid {
		if err := ValidateName(name); err != nil {
			t.Errorf("ValidateName(%q) = %v, want nil", name, err)
		}
	}
	// A name becomes a filename, a command namespace, and a registry key, so
	// path separators and traversal must be rejected before touching the FS.
	invalid := []string{"", "Har", "my_plugin", "-har", "har-", "my--plugin", "../evil", "a/b", "har.spec"}
	for _, name := range invalid {
		if err := ValidateName(name); err == nil {
			t.Errorf("ValidateName(%q) = nil, want an error", name)
		}
	}
}
