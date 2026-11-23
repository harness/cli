// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package har

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/harness/cli/modules/har/pkg/har/migrate/types"

	"github.com/harness/cli/v3/pkg/auth"
	"github.com/harness/cli/v3/pkg/cmdctx"
	"github.com/harness/cli/v3/pkg/config"
	"github.com/harness/cli/v3/pkg/hbase"
)

func TestWithPackageHost(t *testing.T) {
	const derived = "https://pkg.derived.example.com"
	cases := []struct {
		name        string
		apiURL      string // "" means the fake server
		registryURL string
		source      string
		sso         bool
		envRegURL   string // HARNESS_REGISTRY_URL
		profRegURL  string // registry_url saved in profile "default"
		status      int
		body        string
		wantLookup  bool
		wantURL     string
		wantErr     string
	}{
		{
			name: "defaulted host off production is looked up", registryURL: hbase.DefaultRegistryURL,
			status: http.StatusOK, body: `{"data":{"registryUrl":"` + derived + `"}}`, wantLookup: true, wantURL: derived,
		},
		{
			name: "empty host off production is looked up", registryURL: "",
			status: http.StatusOK, body: `{"data":{"registryUrl":"` + derived + `"}}`, wantLookup: true, wantURL: derived,
		},
		{
			name: "explicit host is kept", registryURL: "https://pkg.custom.example.com",
			wantURL: "https://pkg.custom.example.com",
		},
		{
			name: "production API keeps the production host", apiURL: hbase.DefaultAPIURL,
			registryURL: hbase.DefaultRegistryURL, wantURL: hbase.DefaultRegistryURL,
		},
		{
			name: "pipeline host is kept", registryURL: hbase.DefaultRegistryURL, source: auth.SourcePipeline,
			wantURL: hbase.DefaultRegistryURL,
		},
		{
			name: "pipeline host without a scheme gets https", registryURL: "pkg.custom.example.com",
			source: auth.SourcePipeline, wantURL: "https://pkg.custom.example.com",
		},
		{
			name: "empty pipeline host is looked up", registryURL: "", source: auth.SourcePipeline,
			status: http.StatusOK, body: `{"data":{"registryUrl":"` + derived + `"}}`, wantLookup: true, wantURL: derived,
		},
		{
			name: "failed lookup in pipeline mode says the step gave no host", registryURL: "",
			source: auth.SourcePipeline, status: http.StatusInternalServerError, body: `{}`, wantLookup: true,
			wantErr: "pipeline step did not provide",
		},
		{
			name: "failed lookup in env mode points at the env var", registryURL: hbase.DefaultRegistryURL,
			source: auth.SourceEnv, status: http.StatusInternalServerError, body: `{}`, wantLookup: true,
			wantErr: "set " + hbase.EnvRegistryURL,
		},
		{
			name: "failed lookup in profile mode points at login", registryURL: hbase.DefaultRegistryURL,
			source: "profile:default", status: http.StatusInternalServerError, body: `{}`, wantLookup: true,
			wantErr: "harness auth login --overwrite",
		},
		{
			name: "production host set on purpose via env is kept", registryURL: hbase.DefaultRegistryURL,
			source: auth.SourceEnv, envRegURL: hbase.DefaultRegistryURL, wantURL: hbase.DefaultRegistryURL,
		},
		{
			name: "production host saved in the profile is kept", registryURL: hbase.DefaultRegistryURL,
			source: "profile:default", profRegURL: hbase.DefaultRegistryURL, wantURL: hbase.DefaultRegistryURL,
		},
		{
			name: "profile without a saved host is looked up", registryURL: hbase.DefaultRegistryURL,
			source: "profile:default", status: http.StatusOK, body: `{"data":{"registryUrl":"` + derived + `"}}`,
			wantLookup: true, wantURL: derived,
		},
		{
			name: "failed lookup for an SSO profile has no login hint", registryURL: hbase.DefaultRegistryURL,
			source: "profile:default", sso: true, status: http.StatusInternalServerError, body: `{}`, wantLookup: true,
			wantErr: "could not get the package registry URL from http://127.0.0.1",
		},
		{
			name: "response without registryUrl stops the command", registryURL: hbase.DefaultRegistryURL,
			status: http.StatusOK, body: `{"data":{}}`, wantLookup: true, wantErr: "no registryUrl",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(hbase.EnvCLIHome, t.TempDir())
			t.Setenv(hbase.EnvRegistryURL, tc.envRegURL)
			if err := config.SaveConfig(&config.Config{Profiles: map[string]*config.Profile{
				"default": {RegistryURL: tc.profRegURL},
			}}); err != nil {
				t.Fatal(err)
			}
			var lookups int
			var gotAccount string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				lookups++
				if r.URL.Path != "/gateway/har/api/v3/system/info" {
					t.Errorf("lookup path = %q", r.URL.Path)
				}
				gotAccount = r.URL.Query().Get("account_identifier")
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			t.Cleanup(srv.Close)

			apiURL := tc.apiURL
			if apiURL == "" {
				apiURL = srv.URL
			}
			a := &auth.ResolvedAuth{
				Source: tc.source, AuthType: auth.AuthTypePAT, APIUrl: apiURL,
				AccountID: "acct", PATToken: "test-token", RegistryURL: tc.registryURL,
			}
			if tc.sso {
				claims := base64.RawURLEncoding.EncodeToString([]byte(`{"exp":4102444800}`))
				a.AuthType, a.PATToken, a.SSOToken = auth.AuthTypeSSO, "", "e30."+claims+".sig"
			}
			ctx := &cmdctx.Ctx{Context: context.Background(), Auth: a}
			ran := false
			err := withPackageHost(func(*cmdctx.Ctx) error { ran = true; return nil })(ctx)
			if tc.sso && err != nil && strings.Contains(err.Error(), "auth login") {
				t.Errorf("SSO error points at a login command: %v", err)
			}

			if (lookups > 0) != tc.wantLookup {
				t.Fatalf("lookups = %d, want lookup %v", lookups, tc.wantLookup)
			}
			if tc.wantLookup && gotAccount != "acct" {
				t.Errorf("account_identifier = %q, want acct", gotAccount)
			}
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) || ran {
					t.Fatalf("error = %v, handler ran = %v; want %q and the handler skipped", err, ran, tc.wantErr)
				}
				return
			}
			if err != nil || !ran || ctx.Auth.RegistryURL != tc.wantURL {
				t.Fatalf("error = %v, handler ran = %v, RegistryURL = %q; want %q", err, ran, ctx.Auth.RegistryURL, tc.wantURL)
			}
		})
	}
}

func TestResolveMigrateDestEndpoint(t *testing.T) {
	cases := []struct {
		name         string
		pkgURLFlag   string
		cfgEndpoint  string
		wantLookup   bool
		wantEndpoint string
	}{
		{name: "--pkg-url skips the lookup", pkgURLFlag: "https://pkg.flag.example.com",
			cfgEndpoint: "https://pkg.cfg.example.com", wantEndpoint: "https://pkg.flag.example.com"},
		{name: "destination.endpoint skips the lookup", cfgEndpoint: "https://pkg.cfg.example.com",
			wantEndpoint: "https://pkg.cfg.example.com"},
		{name: "no override looks up the host", wantLookup: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var lookups int
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				lookups++
				w.WriteHeader(http.StatusForbidden)
			}))
			t.Cleanup(srv.Close)

			ctx := &cmdctx.Ctx{
				Context: context.Background(),
				Auth: &auth.ResolvedAuth{
					AuthType: auth.AuthTypePAT, APIUrl: srv.URL, AccountID: "acct",
					PATToken: "test-token", RegistryURL: hbase.DefaultRegistryURL,
				},
			}
			cfg := &types.Config{Dest: types.RegistryConfig{Endpoint: tc.cfgEndpoint}}
			err := resolveMigrateDestEndpoint(ctx, cfg, tc.pkgURLFlag)

			if (lookups > 0) != tc.wantLookup {
				t.Fatalf("lookups = %d, want lookup %v", lookups, tc.wantLookup)
			}
			if tc.wantLookup {
				if err == nil {
					t.Fatal("expected the failed lookup to stop the migration")
				}
				return
			}
			if err != nil || cfg.Dest.Endpoint != tc.wantEndpoint {
				t.Fatalf("error = %v, endpoint = %q; want %q", err, cfg.Dest.Endpoint, tc.wantEndpoint)
			}
		})
	}
}
