// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package har

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/harness/cli/v3/pkg/auth"
	"github.com/harness/cli/v3/pkg/cmdctx"
	"github.com/harness/cli/v3/pkg/hbase"
)

func TestWithPackageHost(t *testing.T) {
	const derived = "https://pkg.derived.example.com"
	cases := []struct {
		name        string
		apiURL      string // "" means the fake server
		registryURL string
		source      string
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
			name: "pipeline auth is left alone", registryURL: hbase.DefaultRegistryURL, source: auth.SourcePipeline,
			wantURL: hbase.DefaultRegistryURL,
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
			name: "response without registryUrl stops the command", registryURL: hbase.DefaultRegistryURL,
			status: http.StatusOK, body: `{"data":{}}`, wantLookup: true, wantErr: "no registryUrl",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
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
			ctx := &cmdctx.Ctx{
				Context: context.Background(),
				Auth: &auth.ResolvedAuth{
					Source: tc.source, AuthType: auth.AuthTypePAT, APIUrl: apiURL,
					AccountID: "acct", PATToken: "test-token", RegistryURL: tc.registryURL,
				},
			}
			ran := false
			err := withPackageHost(func(*cmdctx.Ctx) error { ran = true; return nil })(ctx)

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
