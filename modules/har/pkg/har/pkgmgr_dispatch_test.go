// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package har

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPkgmgrPlan(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		usage   string
		args    []string
		wrapped map[string]bool
		want    pkgmgrPlanResult
		wantErr bool
	}{
		{
			name:    "npm test is native",
			usage:   "npm",
			args:    []string{"test", "--coverage"},
			wrapped: npmFirewallCommands,
			want:    pkgmgrPlanResult{args: []string{"test", "--coverage"}},
		},
		{
			name:    "npm install is firewall wrapped",
			usage:   "npm",
			args:    []string{"install", "--legacy-peer-deps"},
			wrapped: npmFirewallCommands,
			want:    pkgmgrPlanResult{wrapped: true, command: "install", nativeArgs: []string{"--legacy-peer-deps"}},
		},
		{
			name:    "npm ci keeps registry and drops it from native args",
			usage:   "npm",
			args:    []string{"--registry", "npm-proxy", "ci", "--ignore-scripts"},
			wrapped: npmFirewallCommands,
			want:    pkgmgrPlanResult{wrapped: true, command: "ci", nativeArgs: []string{"--ignore-scripts"}, registry: "npm-proxy"},
		},
		{
			name:    "npm publish keeps registry token for the native binary",
			usage:   "npm",
			args:    []string{"publish", "--registry", "https://registry.npmjs.org"},
			wrapped: npmFirewallCommands,
			want:    pkgmgrPlanResult{args: []string{"publish", "--registry", "https://registry.npmjs.org"}},
		},
		{
			name:    "pip freeze is native",
			usage:   "pip",
			args:    []string{"freeze"},
			wrapped: pipFirewallCommands,
			want:    pkgmgrPlanResult{args: []string{"freeze"}},
		},
		{
			name:    "pip install is firewall wrapped",
			usage:   "pip",
			args:    []string{"install", "-r", "requirements.txt"},
			wrapped: pipFirewallCommands,
			want:    pkgmgrPlanResult{wrapped: true, command: "install", nativeArgs: []string{"-r", "requirements.txt"}},
		},
		{
			name:    "mvn test is native",
			usage:   "mvn",
			args:    []string{"test"},
			wrapped: mvnFirewallCommands,
			want:    pkgmgrPlanResult{args: []string{"test"}},
		},
		{
			name:    "mvn install is firewall wrapped",
			usage:   "mvn",
			args:    []string{"--registry=maven-proxy", "install"},
			wrapped: mvnFirewallCommands,
			want:    pkgmgrPlanResult{wrapped: true, command: "install", nativeArgs: []string{}, registry: "maven-proxy"},
		},
		{
			name:    "dotnet build is native",
			usage:   "dotnet",
			args:    []string{"build", "--no-restore"},
			wrapped: dotnetFirewallCommands,
			want:    pkgmgrPlanResult{args: []string{"build", "--no-restore"}},
		},
		{
			name:    "dotnet restore is firewall wrapped",
			usage:   "dotnet",
			args:    []string{"restore", "App.sln"},
			wrapped: dotnetFirewallCommands,
			want:    pkgmgrPlanResult{wrapped: true, command: "restore", nativeArgs: []string{"App.sln"}},
		},
		{
			name:    "missing command",
			usage:   "npm",
			wrapped: npmFirewallCommands,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := pkgmgrPlan(tt.usage, tt.args, tt.wrapped)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestPkgmgrRunNativePreservesExitCode(t *testing.T) {
	var code int
	orig := pkgmgrExit
	pkgmgrExit = func(c int) { code = c }
	t.Cleanup(func() { pkgmgrExit = orig })

	err := pkgmgrRunNative("sh", []string{"-c", "exit 7"})
	require.NoError(t, err)
	require.Equal(t, 7, code)
}

func TestPkgmgrRunNativeMissingBinary(t *testing.T) {
	err := pkgmgrRunNative("definitely-missing-binary-xyz", nil)
	require.Error(t, err)
}
