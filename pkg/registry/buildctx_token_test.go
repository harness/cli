// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package registry

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/spf13/cobra"

	"github.com/harness/cli/v3/pkg/auth"
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
