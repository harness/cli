// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/harness/cli/v3/pkg/config"
	"github.com/harness/cli/v3/pkg/hbase"
)

func TestApplyCredentialFlags(t *testing.T) {
	const (
		token   = "pat.test-account.test-token.not-a-secret"
		derived = "https://pkg.derived.example.com"
		saved   = "https://pkg.saved.example.com"
	)
	var gotAccount string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAccount = r.URL.Query().Get("account_identifier")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"data":{"registryUrl":"` + derived + `"}}`))
	}))
	defer srv.Close()

	base := func() *ResolvedAuth {
		return &ResolvedAuth{
			AuthType:    AuthTypePAT,
			PATToken:    token,
			APIUrl:      "https://app.harness.io",
			AccountID:   "saved-account",
			RegistryURL: saved,
		}
	}

	t.Run("token and api-url replace the credential and registry URL", func(t *testing.T) {
		r := base()
		err := ApplyCredentialFlags(r, CredentialFlags{
			APIURL: srv.URL, APIURLSet: true,
			Token: "pat.flag-account.test-token.not-a-secret", TokenSet: true,
			ResolveRegistryURL: true,
		})
		if err != nil {
			t.Fatal(err)
		}
		if r.RegistryURL != derived {
			t.Errorf("RegistryURL = %q, want %q", r.RegistryURL, derived)
		}
		if r.AccountID != "flag-account" {
			t.Errorf("AccountID = %q, want the account segment of --token", r.AccountID)
		}
		if r.PATToken != "pat.flag-account.test-token.not-a-secret" {
			t.Errorf("PATToken = %q, want the --token value", r.PATToken)
		}
		if gotAccount != "flag-account" {
			t.Errorf("lookup account = %q, want flag-account", gotAccount)
		}
	})

	t.Run("invalid token is rejected", func(t *testing.T) {
		err := ApplyCredentialFlags(base(), CredentialFlags{Token: "not-a-token", TokenSet: true})
		if err == nil {
			t.Fatal("expected an invalid --token error")
		}
	})

	t.Run("no flags keep the auth registry URL", func(t *testing.T) {
		gotAccount = ""
		r := base()
		if err := ApplyCredentialFlags(r, CredentialFlags{}); err != nil {
			t.Fatal(err)
		}
		if r.RegistryURL != saved || r.AccountID != "saved-account" {
			t.Errorf("auth values changed: registry %q account %q", r.RegistryURL, r.AccountID)
		}
		if gotAccount != "" {
			t.Errorf("system/info was called for account %q", gotAccount)
		}
	})
}

func TestLoadDoesNotResolveRegistryURL(t *testing.T) {
	const (
		token      = "pat.test-account.test-token.not-a-secret"
		explicit   = "https://pkg.explicit.example.com"
		profileArg = "test-profile"
	)
	for _, mode := range []string{"env", "profile"} {
		t.Run(mode, func(t *testing.T) {
			var lookups atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				lookups.Add(1)
				w.WriteHeader(http.StatusOK)
			}))
			defer srv.Close()

			t.Setenv(hbase.EnvCLIHome, t.TempDir())
			t.Setenv(hbase.EnvProfile, "")
			t.Setenv(hbase.EnvAccount, "other-account")
			profileFlag := ""
			if mode == "env" {
				t.Setenv(hbase.EnvAPIKey, token)
				t.Setenv(hbase.EnvAPIURL, srv.URL)
				t.Setenv(hbase.EnvRegistryURL, explicit)
			} else {
				t.Setenv(hbase.EnvAPIKey, "")
				profileFlag = profileArg
				cfg := &config.Config{Profiles: map[string]*config.Profile{
					profileArg: {AccountID: "stale-account", APIUrl: srv.URL, RegistryURL: explicit},
				}}
				if err := config.SaveConfig(cfg); err != nil {
					t.Fatal(err)
				}
				if err := SetCredential(profileArg, token); err != nil {
					t.Fatal(err)
				}
			}

			r, err := Load(profileFlag)
			if err != nil {
				t.Fatal(err)
			}
			if r.RegistryURL != "" {
				t.Errorf("RegistryURL = %q, want empty outside an Artifact Registry command", r.RegistryURL)
			}
			if r.AccountID != "test-account" {
				t.Errorf("AccountID = %q, want the account segment of the token", r.AccountID)
			}
			if got := lookups.Load(); got != 0 {
				t.Errorf("system/info lookups = %d, want 0", got)
			}
		})
	}
}
