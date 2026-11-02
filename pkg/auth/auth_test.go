// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"net/http"
	"strings"
	"testing"

	"github.com/harness/cli/v3/pkg/config"
	"github.com/harness/cli/v3/pkg/hbase"
)

func TestResolveWithOverridesScope(t *testing.T) {
	cases := []struct {
		name                                  string
		org, project                          string
		orgOverride, projectOverride          string
		wantOrg, wantProject, wantErrContains string
	}{
		{name: "both supplied by flags", orgOverride: "flag-org", projectOverride: "flag-project", wantOrg: "flag-org", wantProject: "flag-project"},
		{name: "missing org supplied by flag", project: "base-project", orgOverride: "flag-org", wantOrg: "flag-org", wantProject: "base-project"},
		{name: "missing project supplied by flag", org: "base-org", projectOverride: "flag-project", wantOrg: "base-org", wantProject: "flag-project"},
		{name: "flags override existing scope", org: "base-org", project: "base-project", orgOverride: "flag-org", projectOverride: "flag-project", wantOrg: "flag-org", wantProject: "flag-project"},
		{name: "empty overrides preserve scope", org: "base-org", project: "base-project", wantOrg: "base-org", wantProject: "base-project"},
		{name: "org still required", projectOverride: "flag-project", wantErrContains: "org"},
		{name: "project still required", orgOverride: "flag-org", wantErrContains: "project"},
	}
	for _, mode := range []string{"implicit env", "env flag", "env selector", "named profile"} {
		t.Run(mode, func(t *testing.T) {
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					t.Setenv(hbase.EnvCLIHome, t.TempDir())
					t.Setenv(hbase.EnvProfile, "")
					t.Setenv(hbase.EnvAPIKey, "pat.test-account.test-token.not-a-secret")
					t.Setenv(hbase.EnvAccount, "test-account")
					t.Setenv(hbase.EnvOrg, tc.org)
					t.Setenv(hbase.EnvProject, tc.project)
					profileFlag, wantSource := "", SourceEnv
					switch mode {
					case "env flag":
						profileFlag = ProfileSentinelEnv
					case "env selector":
						t.Setenv(hbase.EnvProfile, ProfileSentinelEnv)
					case "named profile":
						profileFlag, wantSource = "test-profile", "profile:test-profile"
						cfg := &config.Config{Profiles: map[string]*config.Profile{
							profileFlag: {AccountID: "test-account", OrgID: tc.org, ProjectID: tc.project},
						}}
						if err := config.SaveConfig(cfg); err != nil {
							t.Fatal(err)
						}
						if err := SetCredential(profileFlag, "pat.test-account.test-token.not-a-secret"); err != nil {
							t.Fatal(err)
						}
						// Env scope must not fill gaps in a named profile.
						t.Setenv(hbase.EnvOrg, "ignored-env-org")
						t.Setenv(hbase.EnvProject, "ignored-env-project")
					}

					r, err := ResolveWithOverrides(profileFlag, tc.orgOverride, tc.projectOverride)
					if tc.wantErrContains != "" {
						if err == nil || !strings.Contains(err.Error(), tc.wantErrContains) {
							t.Fatalf("error = %v, want error containing %q", err, tc.wantErrContains)
						}
						return
					}
					if err != nil {
						t.Fatal(err)
					}
					if r.Source != wantSource || r.OrgID != tc.wantOrg || r.ProjectID != tc.wantProject {
						t.Errorf("source/scope = %q %q/%q, want %q %q/%q", r.Source, r.OrgID, r.ProjectID, wantSource, tc.wantOrg, tc.wantProject)
					}
				})
			}
		})
	}
}

func TestResolveWithOverridesValidatesCredentials(t *testing.T) {
	for _, tc := range []struct {
		name, key, account, wantErrContains string
	}{
		{"missing key", "", "test-account", "HARNESS_API_KEY is required"},
		{"invalid key", "not-a-token", "test-account", "HARNESS_API_KEY is invalid"},
		{"account mismatch", "pat.test-account.test-token.not-a-secret", "other-account", "does not match account in token"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(hbase.EnvCLIHome, t.TempDir())
			t.Setenv(hbase.EnvAPIKey, tc.key)
			t.Setenv(hbase.EnvAccount, tc.account)
			t.Setenv(hbase.EnvOrg, "")
			t.Setenv(hbase.EnvProject, "")
			_, err := ResolveWithOverrides(ProfileSentinelEnv, "flag-org", "flag-project")
			if err == nil || !strings.Contains(err.Error(), tc.wantErrContains) {
				t.Fatalf("error = %v, want error containing %q", err, tc.wantErrContains)
			}
		})
	}
}

func TestLoadWithOverrides(t *testing.T) {
	setup := func(t *testing.T, org, project string) {
		t.Helper()
		t.Setenv(hbase.EnvCLIHome, t.TempDir())
		t.Setenv(hbase.EnvAPIKey, "pat.test-account.test-token.not-a-secret")
		t.Setenv(hbase.EnvAccount, "test-account")
		t.Setenv(hbase.EnvOrg, org)
		t.Setenv(hbase.EnvProject, project)
	}
	t.Run("flags override env scope", func(t *testing.T) {
		setup(t, "env-org", "env-project")
		r, err := LoadWithOverrides(ProfileSentinelEnv, "flag-org", "flag-project")
		if err != nil {
			t.Fatal(err)
		}
		if r.OrgID != "flag-org" || r.ProjectID != "flag-project" {
			t.Errorf("scope = %q/%q, want flag-org/flag-project", r.OrgID, r.ProjectID)
		}
	})
	t.Run("empty overrides preserve scope", func(t *testing.T) {
		setup(t, "env-org", "env-project")
		r, err := LoadWithOverrides(ProfileSentinelEnv, "", "")
		if err != nil {
			t.Fatal(err)
		}
		if r.OrgID != "env-org" || r.ProjectID != "env-project" {
			t.Errorf("scope = %q/%q, want env-org/env-project", r.OrgID, r.ProjectID)
		}
	})
	t.Run("does not validate", func(t *testing.T) {
		setup(t, "", "")
		if _, err := LoadWithOverrides(ProfileSentinelEnv, "", ""); err != nil {
			t.Fatalf("LoadWithOverrides must not require scope: %v", err)
		}
	})
}

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

// clearAuthEnv wipes every env var ResolvePipelineAuth might read, so each
// test case starts from a clean slate regardless of what the real environment
// (or a previous subtest via t.Setenv) has set.
func clearAuthEnv(t *testing.T) {
	for _, v := range []string{
		"PIPELINE_TOKEN", "PIPELINE_API_URL", "PIPELINE_REGISTRY_URL",
		hbase.EnvPipelineID, hbase.EnvAccountID, hbase.EnvOrgID, hbase.EnvProjectID, hbase.EnvInfra,
	} {
		t.Setenv(v, "")
	}
}

func noopEvalHeaders(headers map[string]string, token string) map[string]string {
	if headers == nil {
		return nil
	}
	out := make(map[string]string, len(headers))
	for k := range headers {
		out[k] = "Bearer " + token
	}
	return out
}

func TestResolvePipelineAuth(t *testing.T) {
	t.Run("HARNESS_PIPELINE_ID unset never triggers, even with a cfg", func(t *testing.T) {
		clearAuthEnv(t)
		cfg := &PipelineAuthConfig{TokenEnvVar: "PIPELINE_TOKEN"}
		r, triggered, err := ResolvePipelineAuth(cfg, false, noopEvalHeaders)
		if r != nil || triggered || err != nil {
			t.Fatalf("ResolvePipelineAuth = (%v, %v, %v), want (nil, false, nil)", r, triggered, err)
		}
	})

	t.Run("nil cfg never triggers, even inside a pipeline — command just isn't pipeline-auth-aware", func(t *testing.T) {
		clearAuthEnv(t)
		t.Setenv(hbase.EnvPipelineID, "pipe1")
		r, triggered, err := ResolvePipelineAuth(nil, false, noopEvalHeaders)
		if r != nil || triggered || err != nil {
			t.Fatalf("ResolvePipelineAuth(nil) = (%v, %v, %v), want (nil, false, nil)", r, triggered, err)
		}
	})

	t.Run("forced with HARNESS_PIPELINE_ID unset errors instead of silently not triggering", func(t *testing.T) {
		clearAuthEnv(t)
		cfg := &PipelineAuthConfig{TokenEnvVar: "PIPELINE_TOKEN"}
		r, triggered, err := ResolvePipelineAuth(cfg, true, noopEvalHeaders)
		if r != nil || !triggered || err == nil || !strings.Contains(err.Error(), hbase.EnvPipelineID) {
			t.Fatalf("ResolvePipelineAuth(forced) = (%v, %v, %v), want error naming %s", r, triggered, err, hbase.EnvPipelineID)
		}
	})

	t.Run("forced with nil cfg errors instead of silently not triggering", func(t *testing.T) {
		clearAuthEnv(t)
		t.Setenv(hbase.EnvPipelineID, "pipe1")
		r, triggered, err := ResolvePipelineAuth(nil, true, noopEvalHeaders)
		if r != nil || !triggered || err == nil {
			t.Fatalf("ResolvePipelineAuth(nil, forced) = (%v, %v, %v), want (nil, true, err)", r, triggered, err)
		}
	})

	t.Run("missing a scope var errors naming it", func(t *testing.T) {
		clearAuthEnv(t)
		t.Setenv(hbase.EnvPipelineID, "pipe1")
		t.Setenv(hbase.EnvAccountID, "acct1")
		// HARNESS_ORG_ID deliberately left unset.
		cfg := &PipelineAuthConfig{TokenEnvVar: "PIPELINE_TOKEN"}
		r, triggered, err := ResolvePipelineAuth(cfg, false, noopEvalHeaders)
		if r != nil || !triggered || err == nil || !strings.Contains(err.Error(), hbase.EnvOrgID) {
			t.Fatalf("ResolvePipelineAuth = (%v, %v, %v), want error naming %s", r, triggered, err, hbase.EnvOrgID)
		}
	})

	t.Run("non-VM infra with token unset errors on the missing token, not the infra", func(t *testing.T) {
		clearAuthEnv(t)
		t.Setenv(hbase.EnvPipelineID, "pipe1")
		t.Setenv(hbase.EnvAccountID, "acct1")
		t.Setenv(hbase.EnvOrgID, "org1")
		t.Setenv(hbase.EnvProjectID, "proj1")
		t.Setenv(hbase.EnvInfra, "KUBERNETES")
		cfg := &PipelineAuthConfig{TokenEnvVar: "PIPELINE_TOKEN"}
		r, triggered, err := ResolvePipelineAuth(cfg, false, noopEvalHeaders)
		if r != nil || !triggered || err == nil || !strings.Contains(err.Error(), "PIPELINE_TOKEN") {
			t.Fatalf("ResolvePipelineAuth = (%v, %v, %v), want error naming PIPELINE_TOKEN", r, triggered, err)
		}
	})

	t.Run("non-VM infra with token set succeeds — pipeline auth is infra-agnostic", func(t *testing.T) {
		clearAuthEnv(t)
		t.Setenv(hbase.EnvPipelineID, "pipe1")
		t.Setenv(hbase.EnvAccountID, "acct1")
		t.Setenv(hbase.EnvOrgID, "org1")
		t.Setenv(hbase.EnvProjectID, "proj1")
		t.Setenv(hbase.EnvInfra, "KUBERNETES")
		t.Setenv("PIPELINE_TOKEN", "tok123")
		cfg := &PipelineAuthConfig{TokenEnvVar: "PIPELINE_TOKEN"}
		r, triggered, err := ResolvePipelineAuth(cfg, false, noopEvalHeaders)
		if err != nil || !triggered || r == nil {
			t.Fatalf("ResolvePipelineAuth = (%v, %v, %v), want a resolved pipeline auth on non-VM infra too", r, triggered, err)
		}
	})

	t.Run("VM infra with token unset errors", func(t *testing.T) {
		clearAuthEnv(t)
		t.Setenv(hbase.EnvPipelineID, "pipe1")
		t.Setenv(hbase.EnvAccountID, "acct1")
		t.Setenv(hbase.EnvOrgID, "org1")
		t.Setenv(hbase.EnvProjectID, "proj1")
		t.Setenv(hbase.EnvInfra, hbase.InfraVM)
		cfg := &PipelineAuthConfig{TokenEnvVar: "PIPELINE_TOKEN"}
		r, triggered, err := ResolvePipelineAuth(cfg, false, noopEvalHeaders)
		if r != nil || !triggered || err == nil {
			t.Fatalf("ResolvePipelineAuth = (%v, %v, %v), want (nil, true, err)", r, triggered, err)
		}
	})

	t.Run("happy path: VM infra, scope and token set, headers evaluated", func(t *testing.T) {
		clearAuthEnv(t)
		t.Setenv(hbase.EnvPipelineID, "pipe1")
		t.Setenv(hbase.EnvAccountID, "acct1")
		t.Setenv(hbase.EnvOrgID, "org1")
		t.Setenv(hbase.EnvProjectID, "proj1")
		t.Setenv(hbase.EnvInfra, hbase.InfraVM)
		t.Setenv("PIPELINE_TOKEN", "tok123")
		t.Setenv("PIPELINE_API_URL", "https://ti.example.com")
		cfg := &PipelineAuthConfig{
			TokenEnvVar:  "PIPELINE_TOKEN",
			APIURLEnvVar: "PIPELINE_API_URL",
			Headers:      map[string]string{"Authorization": "ignored by noopEvalHeaders"},
		}
		r, triggered, err := ResolvePipelineAuth(cfg, false, noopEvalHeaders)
		if err != nil || !triggered || r == nil {
			t.Fatalf("ResolvePipelineAuth = (%v, %v, %v), want a resolved pipeline auth", r, triggered, err)
		}
		want := &ResolvedAuth{
			Source:    SourcePipeline,
			AccountID: "acct1",
			OrgID:     "org1",
			ProjectID: "proj1",
			APIUrl:    "https://ti.example.com",
			Headers:   map[string]string{"Authorization": "Bearer tok123"},
		}
		if r.Source != want.Source || r.AccountID != want.AccountID || r.OrgID != want.OrgID ||
			r.ProjectID != want.ProjectID || r.APIUrl != want.APIUrl || r.Headers["Authorization"] != want.Headers["Authorization"] {
			t.Errorf("ResolvedAuth = %+v, want %+v", *r, *want)
		}
	})
}

func TestSetAuthHeader_PipelineHeadersTakePriority(t *testing.T) {
	r := &ResolvedAuth{
		AuthType: AuthTypePAT,
		PATToken: "pat.acct.id.secret",
		Headers:  map[string]string{"x-api-key": "CIManager tok123"},
	}
	req, err := http.NewRequest(http.MethodGet, "https://example.com", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	r.SetAuthHeader(req)
	if got := req.Header.Get("x-api-key"); got != "CIManager tok123" {
		t.Errorf("x-api-key header = %q, want %q", got, "CIManager tok123")
	}
	if got := req.Header.Get("Authorization"); got != "" {
		t.Errorf("Authorization header = %q, want empty — pipeline headers must fully replace, not stack", got)
	}
}

func TestSetAuthHeader_HarnessAccount(t *testing.T) {
	tests := []struct {
		name string
		auth ResolvedAuth
		pre  string
		want string
	}{
		{"PAT sets account", ResolvedAuth{AuthType: AuthTypePAT, PATToken: "pat", AccountID: "acct"}, "", "acct"},
		{"SSO sets account", ResolvedAuth{AuthType: AuthTypeSSO, SSOToken: "tok", AccountID: "acct"}, "", "acct"},
		{"pipeline headers still get account", ResolvedAuth{Headers: map[string]string{"x-api-key": "k"}, AccountID: "acct"}, "", "acct"},
		{"pipeline header wins", ResolvedAuth{Headers: map[string]string{"Harness-Account": "other"}, AccountID: "acct"}, "", "other"},
		{"existing header kept", ResolvedAuth{AuthType: AuthTypePAT, PATToken: "pat", AccountID: "acct"}, "preset", "preset"},
		{"empty account omitted", ResolvedAuth{AuthType: AuthTypePAT, PATToken: "pat"}, "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, _ := http.NewRequest("GET", "https://example.com", nil)
			if tt.pre != "" {
				req.Header.Set("Harness-Account", tt.pre)
			}
			tt.auth.SetAuthHeader(req)
			if got := req.Header.Get("Harness-Account"); got != tt.want {
				t.Errorf("Harness-Account = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestProfileAPIKeyEnvVar(t *testing.T) {
	cases := map[string]string{
		"default":   "HARNESS_PROFILE_DEFAULT_API_KEY",
		"my-prod":   "HARNESS_PROFILE_MY_PROD_API_KEY",
		"My.Prod":   "HARNESS_PROFILE_MY_PROD_API_KEY",
		"staging 2": "HARNESS_PROFILE_STAGING_2_API_KEY",
	}
	for in, want := range cases {
		if got := ProfileAPIKeyEnvVar(in); got != want {
			t.Errorf("ProfileAPIKeyEnvVar(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestResolveProfileTokenFromEnvVar(t *testing.T) {
	const (
		account = "acctx"
		fileTok = "pat.acctx.filetok.filesecret"
		envTok  = "pat.acctx.envtok.envsecret"
		envVar  = "HARNESS_PROFILE_PROD_API_KEY"
	)

	// seedProfile isolates HARNESS_CLI_HOME and writes a "prod" PAT profile,
	// optionally with a token in the credentials file.
	seedProfile := func(t *testing.T, withFileToken bool) {
		t.Helper()
		t.Setenv(hbase.EnvCLIHome, t.TempDir())
		t.Setenv(hbase.EnvProfile, "")
		t.Setenv(hbase.EnvAPIKey, "")
		cfg := &config.Config{Profiles: map[string]*config.Profile{
			"prod": {AccountID: account, OrgID: "o", ProjectID: "p"},
		}}
		if err := config.SaveConfig(cfg); err != nil {
			t.Fatal(err)
		}
		if withFileToken {
			if err := SetCredential("prod", fileTok); err != nil {
				t.Fatal(err)
			}
		}
	}

	t.Run("env var overrides credentials file", func(t *testing.T) {
		seedProfile(t, true)
		t.Setenv(envVar, envTok)
		r, err := Resolve("prod")
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		if r.PATToken != envTok {
			t.Errorf("PATToken = %q, want env token %q", r.PATToken, envTok)
		}
		if r.TokenEnvVar != envVar {
			t.Errorf("TokenEnvVar = %q, want %q", r.TokenEnvVar, envVar)
		}
		if r.Source != "profile:prod" {
			t.Errorf("Source = %q, want profile:prod (still a profile)", r.Source)
		}
	})

	t.Run("env var works with no credentials file entry", func(t *testing.T) {
		seedProfile(t, false)
		t.Setenv(envVar, envTok)
		r, err := Resolve("prod")
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		if r.PATToken != envTok || r.TokenEnvVar != envVar {
			t.Errorf("PATToken/TokenEnvVar = %q/%q, want %q/%q", r.PATToken, r.TokenEnvVar, envTok, envVar)
		}
	})

	t.Run("falls back to credentials file when env var unset", func(t *testing.T) {
		seedProfile(t, true)
		t.Setenv(envVar, "")
		r, err := Resolve("prod")
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		if r.PATToken != fileTok {
			t.Errorf("PATToken = %q, want file token %q", r.PATToken, fileTok)
		}
		if r.TokenEnvVar != "" {
			t.Errorf("TokenEnvVar = %q, want empty (file source)", r.TokenEnvVar)
		}
	})

	t.Run("errors when neither env var nor file has a token", func(t *testing.T) {
		seedProfile(t, false)
		t.Setenv(envVar, "")
		if _, err := Load("prod"); err == nil || !strings.Contains(err.Error(), "no token found") {
			t.Fatalf("Load error = %v, want 'no token found'", err)
		}
	})

	t.Run("account mismatch names the env var", func(t *testing.T) {
		seedProfile(t, false)
		t.Setenv(envVar, "pat.otheracct.envtok.envsecret")
		_, err := Resolve("prod")
		if err == nil || !strings.Contains(err.Error(), envVar) {
			t.Fatalf("Resolve error = %v, want error naming %q", err, envVar)
		}
	})
}

func TestResolveProfileSSOIgnoresEnvVar(t *testing.T) {
	t.Setenv(hbase.EnvCLIHome, t.TempDir())
	t.Setenv(hbase.EnvProfile, "")
	t.Setenv(hbase.EnvAPIKey, "")
	cfg := &config.Config{Profiles: map[string]*config.Profile{
		"prod": {AuthType: config.AuthTypeSSO, AccountID: "acctx", OrgID: "o", ProjectID: "p"},
	}}
	if err := config.SaveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	if err := SetSSOCredentials("prod", "jwt.header.payload", "refresh-token"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HARNESS_PROFILE_PROD_API_KEY", "pat.acctx.envtok.envsecret")

	r, err := Load("prod")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if r.AuthType != AuthTypeSSO {
		t.Fatalf("AuthType = %v, want SSO", r.AuthType)
	}
	if r.TokenEnvVar != "" {
		t.Errorf("TokenEnvVar = %q, want empty for SSO", r.TokenEnvVar)
	}
	if r.PATToken != "" {
		t.Errorf("PATToken = %q, want empty for SSO", r.PATToken)
	}
	if r.SSOToken == "" {
		t.Error("SSOToken should be loaded from the credentials file")
	}
}
