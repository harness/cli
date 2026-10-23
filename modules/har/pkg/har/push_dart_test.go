// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package har

import "testing"

func TestParsePubspecNameVersion(t *testing.T) {
	tests := []struct {
		name        string
		pubspec     string
		wantName    string
		wantVersion string
		wantErr     bool
	}{
		{"trailing comments", "name: foo # the name\nversion: 1.0.0 # bump me\n", "foo", "1.0.0", false},
		{"quoted with trailing comment", "name: \"foo\" # c\nversion: '1.2.3+4' # c\n", "foo", "1.2.3+4", false},
		{"nested keys ignored", "dependencies:\n  name: x\n  version: 9.9.9\nname: foo\nversion: 1.0.0\n", "foo", "1.0.0", false},
		{"missing version", "name: foo\n", "foo", "", false},
		{"malformed yaml", "name: [foo\nversion: 1.0.0\n", "", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			name, version, err := parsePubspecNameVersion([]byte(tt.pubspec))
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if name != tt.wantName || version != tt.wantVersion {
				t.Errorf("got (%q, %q), want (%q, %q)", name, version, tt.wantName, tt.wantVersion)
			}
		})
	}
}
