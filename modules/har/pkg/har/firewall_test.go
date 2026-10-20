// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package har

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParsePackageLockNestedParents(t *testing.T) {
	lock := `{
  "lockfileVersion": 3,
  "packages": {
    "": {"name": "app", "version": "1.0.0"},
    "node_modules/a": {"version": "1.0.0"},
    "node_modules/a/node_modules/b": {"version": "2.0.0"},
    "node_modules/@s/c": {"version": "3.0.0"},
    "node_modules/@s/c/node_modules/@s/d": {"version": "4.0.0"},
    "node_modules/local": {"version": "0.1.0", "link": true},
    "packages/ws": {"version": "0.2.0"},
    "packages/ws/node_modules/e": {"version": "5.0.0"}
  }
}`
	deps, err := parsePackageLock([]byte(lock))
	require.NoError(t, err)
	require.ElementsMatch(t, []dependency{
		{Name: "a", Version: "1.0.0"},
		{Name: "b", Version: "2.0.0", Parent: "a@1.0.0"},
		{Name: "@s/c", Version: "3.0.0"},
		{Name: "@s/d", Version: "4.0.0", Parent: "@s/c@3.0.0"},
		{Name: "e", Version: "5.0.0"},
	}, deps)
}

func TestValidateFileForPackageType(t *testing.T) {
	tests := []struct {
		file, packageType string
		wantErr           bool
	}{
		{"package-lock.json", "NPM", false},
		{"requirements.txt", "NPM", true},
		{"package-lock.json", "PYTHON", true},
		{"poetry.lock", "PYTHON", false},
		{"build.gradle.kts", "MAVEN", false},
		{"packages.lock.json", "NUGET", false},
		{"packages.lock.json", "NPM", true},
		{"package-lock.json", "GENERIC", true},
	}
	for _, tt := range tests {
		err := validateFileForPackageType(tt.file, tt.packageType)
		require.Equal(t, tt.wantErr, err != nil, "%s on %s: %v", tt.file, tt.packageType, err)
	}
}
