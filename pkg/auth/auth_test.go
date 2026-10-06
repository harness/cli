// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"net/http"
	"testing"

	"github.com/harness/cli/v3/pkg/hbase"
)

func TestNormalizeAPIURL(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		// Original *.harness.io shorthands — must keep working unchanged.
		{"harness0", "https://harness0.harness.io"},
		{"qa", "https://qa.harness.io"},
		{"app.harness.io", "https://app.harness.io"},
		{"qa.harness.io", "https://qa.harness.io"},
		{"https://app.harness.io", "https://app.harness.io"},
		// New: bare vanity/on-prem host gets a scheme prepended.
		{"harness.onefiserv.net", "https://harness.onefiserv.net"},
		// Already has a scheme — left alone either way.
		{"https://harness.onefiserv.net", "https://harness.onefiserv.net"},
		{"http://harness.onefiserv.net", "http://harness.onefiserv.net"},
	}
	for _, c := range cases {
		if got := NormalizeAPIURL(c.in); got != c.want {
			t.Errorf("NormalizeAPIURL(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestValidateAPIURL(t *testing.T) {
	valid := []string{
		// Original standard forms.
		"https://app.harness.io",
		"https://qa.harness.io",
		"https://harness0.harness.io",
		// Vanity/on-prem domains, now also accepted.
		"https://harness.onefiserv.net",
		// SSO/MCP gateway URLs carry a "/cli" path segment — must still validate.
		"https://mcp.harness.io/cli",
	}
	for _, u := range valid {
		if err := ValidateAPIURL(u); err != nil {
			t.Errorf("ValidateAPIURL(%q) = %v, want nil", u, err)
		}
	}

	invalid := []string{
		"ftp://bad.url",
		"http://not-https.example.com",
		"not a url",
		"",
	}
	for _, u := range invalid {
		if err := ValidateAPIURL(u); err == nil {
			t.Errorf("ValidateAPIURL(%q) = nil, want error", u)
		}
	}
}

// TestNormalizeThenValidateAPIURL exercises the exact pipeline login.go and the
// wizard use: raw user input → NormalizeAPIURL → ValidateAPIURL. It confirms
// every shorthand a user could type for the standard Harness SaaS host still
// resolves to a URL that passes validation, alongside the new vanity/on-prem case.
func TestSetAuthHeaderRoutesTokenType(t *testing.T) {
	const rawJWT = "eyJhbGciOiJIUzI1NiJ9.payload.sig"

	tests := []struct {
		name       string
		auth       ResolvedAuth
		wantAuth   string
		wantAPIKey string
	}{
		{
			name:       "pat uses x-api-key only",
			auth:       ResolvedAuth{AuthType: AuthTypePAT, PATToken: "pat.acct.id.secret"},
			wantAPIKey: "pat.acct.id.secret",
		},
		{
			name:     "raw CI jwt uses CIManager authorization only",
			auth:     ResolvedAuth{AuthType: AuthTypeCI, CIToken: rawJWT},
			wantAuth: "CIManager " + rawJWT,
		},
		{
			name:     "already prefixed CI token is not prefixed twice",
			auth:     ResolvedAuth{AuthType: AuthTypeCI, CIToken: "CIManager " + rawJWT},
			wantAuth: "CIManager " + rawJWT,
		},
		{
			name:     "sso uses bearer",
			auth:     ResolvedAuth{AuthType: AuthTypeSSO, SSOToken: rawJWT},
			wantAuth: "Bearer " + rawJWT,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req, err := http.NewRequest(http.MethodGet, "https://pkg.example.test/upload", nil)
			if err != nil {
				t.Fatal(err)
			}
			tc.auth.SetAuthHeader(req)
			if got := req.Header.Get("Authorization"); got != tc.wantAuth {
				t.Errorf("Authorization = %q, want %q", got, tc.wantAuth)
			}
			if got := req.Header.Get("x-api-key"); got != tc.wantAPIKey {
				t.Errorf("x-api-key = %q, want %q", got, tc.wantAPIKey)
			}
		})
	}
}

func TestLoadCITokenEnv(t *testing.T) {
	const rawJWT = "eyJhbGciOiJIUzI1NiJ9.payload.sig"
	t.Setenv(hbase.EnvCIToken, rawJWT)
	t.Setenv(hbase.EnvAccount, "acct")
	t.Setenv(hbase.EnvOrg, "org")
	t.Setenv(hbase.EnvProject, "proj")
	t.Setenv(hbase.EnvAPIKey, "")

	got, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if got.AuthType != AuthTypeCI || got.CIToken != rawJWT || got.PATToken != "" {
		t.Fatalf("Load() = %+v, want CI auth with the raw token", got)
	}
	if err := Validate(got); err != nil {
		t.Fatal(err)
	}
}

func TestLoadAPIKeyWinsOverCIToken(t *testing.T) {
	t.Setenv(hbase.EnvAPIKey, "pat.acct.id.secret")
	t.Setenv(hbase.EnvCIToken, "eyJhbGciOiJIUzI1NiJ9.payload.sig")
	t.Setenv(hbase.EnvAccount, "acct")

	got, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if got.AuthType != AuthTypePAT || got.CIToken != "" {
		t.Fatalf("Load() = %+v, want PAT auth when both token env vars are set", got)
	}
}

func TestValidateCITokenRequiresAccount(t *testing.T) {
	err := Validate(&ResolvedAuth{
		Source:    SourceEnv,
		AuthType:  AuthTypeCI,
		CIToken:   "eyJhbGciOiJIUzI1NiJ9.payload.sig",
		OrgID:     "org",
		ProjectID: "proj",
	})
	if err == nil {
		t.Fatal("expected missing account to fail validation")
	}
}

func TestNormalizeThenValidateAPIURL(t *testing.T) {
	inputs := []string{
		"harness0",
		"app.harness.io",
		"https://app.harness.io",
		"harness.onefiserv.net",
		"https://harness.onefiserv.net",
	}
	for _, in := range inputs {
		normalized := NormalizeAPIURL(in)
		if err := ValidateAPIURL(normalized); err != nil {
			t.Errorf("NormalizeAPIURL(%q) = %q, which ValidateAPIURL rejected: %v", in, normalized, err)
		}
	}
}
