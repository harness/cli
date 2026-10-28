// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package registry

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/harness/cli/v3/pkg/auth"
	"github.com/harness/cli/v3/pkg/config"
	"github.com/harness/cli/v3/pkg/hbase"
)

func TestResolveCommandAuthTokenWithoutLogin(t *testing.T) {
	clearPipelineAuthTestEnv(t)
	const token = "pat.flag-account.test-token.not-a-secret"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("account_identifier") != "flag-account" || r.Header.Get("x-api-key") != token {
			t.Errorf("unexpected lookup %s with x-api-key %q", r.URL, r.Header.Get("x-api-key"))
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"data":{"registryUrl":"https://pkg.derived.example.com"}}`))
	}))
	defer srv.Close()

	resolved, err := runResolveCommandAuth(t, []string{"--api-url", srv.URL, "--token", token})
	if err != nil {
		t.Fatal(err)
	}
	if resolved.AccountID != "flag-account" || resolved.PATToken != token {
		t.Errorf("auth = account %q token %q", resolved.AccountID, resolved.PATToken)
	}
	if resolved.RegistryURL != "https://pkg.derived.example.com" {
		t.Errorf("RegistryURL = %q", resolved.RegistryURL)
	}
}

func TestResolveCommandAuthTokenKeepsSourceSettings(t *testing.T) {
	const token = "pat.flag-account.test-token.not-a-secret"
	cases := []struct {
		name    string
		setup   func(t *testing.T, apiURL string)
		profile string
		wantErr string
	}{
		{
			name: "malformed HARNESS_API_KEY",
			setup: func(t *testing.T, apiURL string) {
				t.Setenv(hbase.EnvAPIKey, "not-a-token")
				t.Setenv(hbase.EnvAPIURL, apiURL)
				t.Setenv(hbase.EnvOrg, "src-org")
				t.Setenv(hbase.EnvProject, "src-project")
			},
		},
		{
			name: "not logged in uses HARNESS_API_URL and scope",
			setup: func(t *testing.T, apiURL string) {
				t.Setenv(hbase.EnvAPIURL, apiURL)
				t.Setenv(hbase.EnvOrg, "src-org")
				t.Setenv(hbase.EnvProject, "src-project")
			},
		},
		{
			name:    "profile without a stored token",
			profile: "tokenless",
			setup: func(t *testing.T, apiURL string) {
				cfg := &config.Config{Profiles: map[string]*config.Profile{
					"tokenless": {APIUrl: apiURL, OrgID: "src-org", ProjectID: "src-project"},
				}}
				if err := config.SaveConfig(cfg); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name:    "unknown explicit profile is still an error",
			profile: "missing",
			setup:   func(*testing.T, string) {},
			wantErr: `profile "missing" not found`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clearPipelineAuthTestEnv(t)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(`{"data":{"registryUrl":"https://pkg.derived.example.com"}}`))
			}))
			defer srv.Close()
			tc.setup(t, srv.URL)

			args := []string{"--token", token}
			if tc.profile != "" {
				args = append(args, "--profile", tc.profile)
			}
			resolved, err := runResolveCommandAuth(t, args)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("error = %v, want %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if resolved.APIUrl != srv.URL || resolved.OrgID != "src-org" || resolved.ProjectID != "src-project" {
				t.Errorf("settings = api %q org %q project %q, want the source's", resolved.APIUrl, resolved.OrgID, resolved.ProjectID)
			}
			if resolved.PATToken != token || resolved.AccountID != "flag-account" {
				t.Errorf("credential = token %q account %q, want --token's", resolved.PATToken, resolved.AccountID)
			}
		})
	}
}

func TestResolveCommandAuthStillRequiresLoginWithoutToken(t *testing.T) {
	clearPipelineAuthTestEnv(t)
	_, err := runResolveCommandAuth(t, nil)
	if !errors.Is(err, auth.ErrNotLoggedIn) {
		t.Fatalf("error = %v, want ErrNotLoggedIn", err)
	}
}

func TestResolveCommandAuthDoesNotResolveRegistryURLForOtherModules(t *testing.T) {
	clearPipelineAuthTestEnv(t)
	t.Setenv(hbase.EnvAPIKey, "pat.test-account.test-token.not-a-secret")
	t.Setenv(hbase.EnvOrg, "test-org")
	t.Setenv(hbase.EnvProject, "test-project")
	resolved, err := runResolveCommandAuthForModule(t, nil, "pipeline")
	if err != nil {
		t.Fatal(err)
	}
	if resolved.RegistryURL != "" {
		t.Errorf("RegistryURL = %q, want empty outside the har module", resolved.RegistryURL)
	}
}

func runResolveCommandAuth(t *testing.T, args []string) (*auth.ResolvedAuth, error) {
	return runResolveCommandAuthForModule(t, args, "har")
}

func runResolveCommandAuthForModule(t *testing.T, args []string, module string) (*auth.ResolvedAuth, error) {
	t.Helper()
	var resolved *auth.ResolvedAuth
	var resolveErr error
	cmd := &cobra.Command{
		Use: "push",
		RunE: func(cmd *cobra.Command, _ []string) error {
			resolved, resolveErr = resolveCommandAuth(cmd, nil, module)
			return resolveErr
		},
	}
	cmd.Flags().String("profile", "", "")
	cmd.Flags().String("org", "", "")
	cmd.Flags().String("project", "", "")
	cmd.Flags().String("api-url", "", "")
	cmd.Flags().String("token", "", "")
	cmd.SetArgs(args)
	if err := cmd.Execute(); err != nil {
		return resolved, err
	}
	return resolved, nil
}
