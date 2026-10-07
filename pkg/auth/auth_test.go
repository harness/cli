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

// clearAuthEnv wipes every env var ResolveCommandOverride might read, so each
// test case starts from a clean slate regardless of what the real environment
// (or a previous subtest via t.Setenv) has set.
func clearAuthEnv(t *testing.T) {
	for _, v := range []string{
		"PIPELINE_TOKEN", "CUSTOM_ACCOUNT", "CUSTOM_ORG", "CUSTOM_PROJECT", "CUSTOM_API_URL",
		hbase.EnvAccount, hbase.EnvOrg, hbase.EnvProject, hbase.EnvAPIURL,
	} {
		t.Setenv(v, "")
	}
}

func TestResolveCommandOverride(t *testing.T) {
	t.Run("nil config never triggers", func(t *testing.T) {
		clearAuthEnv(t)
		r, triggered, err := ResolveCommandOverride(nil)
		if r != nil || triggered || err != nil {
			t.Fatalf("ResolveCommandOverride(nil) = (%v, %v, %v), want (nil, false, nil)", r, triggered, err)
		}
	})

	t.Run("token env var unset does not trigger", func(t *testing.T) {
		clearAuthEnv(t)
		cfg := &CommandOverrideConfig{TokenEnvVar: "PIPELINE_TOKEN", Header: "Authorization"}
		r, triggered, err := ResolveCommandOverride(cfg)
		if r != nil || triggered || err != nil {
			t.Fatalf("ResolveCommandOverride = (%v, %v, %v), want (nil, false, nil)", r, triggered, err)
		}
	})

	t.Run("triggered but no account anywhere errors", func(t *testing.T) {
		clearAuthEnv(t)
		t.Setenv("PIPELINE_TOKEN", "tok123")
		cfg := &CommandOverrideConfig{TokenEnvVar: "PIPELINE_TOKEN", Header: "Authorization"}
		r, triggered, err := ResolveCommandOverride(cfg)
		if r != nil || !triggered || err == nil {
			t.Fatalf("ResolveCommandOverride = (%v, %v, %v), want (nil, true, err)", r, triggered, err)
		}
	})

	t.Run("full override: custom account/org/project/api_url env vars, prefix applied", func(t *testing.T) {
		clearAuthEnv(t)
		t.Setenv("PIPELINE_TOKEN", "tok123")
		t.Setenv("CUSTOM_ACCOUNT", "acct1")
		t.Setenv("CUSTOM_ORG", "org1")
		t.Setenv("CUSTOM_PROJECT", "proj1")
		t.Setenv("CUSTOM_API_URL", "https://ti.example.com")
		cfg := &CommandOverrideConfig{
			TokenEnvVar:   "PIPELINE_TOKEN",
			Header:        "Authorization",
			Prefix:        "Bearer ",
			AccountEnvVar: "CUSTOM_ACCOUNT",
			OrgEnvVar:     "CUSTOM_ORG",
			ProjectEnvVar: "CUSTOM_PROJECT",
			APIURLEnvVar:  "CUSTOM_API_URL",
		}
		r, triggered, err := ResolveCommandOverride(cfg)
		if err != nil || !triggered || r == nil {
			t.Fatalf("ResolveCommandOverride = (%v, %v, %v), want a resolved override", r, triggered, err)
		}
		want := &ResolvedAuth{
			Source:         SourceEnv,
			AccountID:      "acct1",
			OrgID:          "org1",
			ProjectID:      "proj1",
			APIUrl:         "https://ti.example.com",
			OverrideHeader: "Authorization",
			OverrideValue:  "Bearer tok123",
		}
		if *r != *want {
			t.Errorf("ResolvedAuth = %+v, want %+v", *r, *want)
		}
	})

	t.Run("unset optional env vars fall back to standard HARNESS_* then built-in default URL", func(t *testing.T) {
		clearAuthEnv(t)
		t.Setenv("PIPELINE_TOKEN", "tok123")
		t.Setenv(hbase.EnvAccount, "standard-acct")
		cfg := &CommandOverrideConfig{TokenEnvVar: "PIPELINE_TOKEN", Header: "x-api-key"}
		r, triggered, err := ResolveCommandOverride(cfg)
		if err != nil || !triggered || r == nil {
			t.Fatalf("ResolveCommandOverride = (%v, %v, %v), want a resolved override", r, triggered, err)
		}
		if r.AccountID != "standard-acct" {
			t.Errorf("AccountID = %q, want fallback to %s", r.AccountID, hbase.EnvAccount)
		}
		if r.OrgID != "" || r.ProjectID != "" {
			t.Errorf("OrgID/ProjectID = %q/%q, want empty (optional, unset)", r.OrgID, r.ProjectID)
		}
		if r.APIUrl != hbase.DefaultAPIURL {
			t.Errorf("APIUrl = %q, want built-in default %q", r.APIUrl, hbase.DefaultAPIURL)
		}
		if r.OverrideValue != "tok123" {
			t.Errorf("OverrideValue = %q, want raw token (no prefix configured)", r.OverrideValue)
		}
	})
}

func TestSetAuthHeader_OverrideTakesPriority(t *testing.T) {
	r := &ResolvedAuth{
		AuthType:       AuthTypePAT,
		PATToken:       "pat.acct.id.secret",
		OverrideHeader: "x-api-key",
		OverrideValue:  "CIManager tok123",
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
		t.Errorf("Authorization header = %q, want empty — override must fully replace, not stack", got)
	}
}
