// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"net/http"
	"strings"
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
	t.Run("HARNESS_PIPELINEID unset never triggers, even with a cfg", func(t *testing.T) {
		clearAuthEnv(t)
		cfg := &PipelineAuthConfig{TokenEnvVar: "PIPELINE_TOKEN"}
		r, triggered, err := ResolvePipelineAuth(cfg, noopEvalHeaders)
		if r != nil || triggered || err != nil {
			t.Fatalf("ResolvePipelineAuth = (%v, %v, %v), want (nil, false, nil)", r, triggered, err)
		}
	})

	t.Run("nil cfg never triggers, even inside a pipeline — command just isn't pipeline-auth-aware", func(t *testing.T) {
		clearAuthEnv(t)
		t.Setenv(hbase.EnvPipelineID, "pipe1")
		r, triggered, err := ResolvePipelineAuth(nil, noopEvalHeaders)
		if r != nil || triggered || err != nil {
			t.Fatalf("ResolvePipelineAuth(nil) = (%v, %v, %v), want (nil, false, nil)", r, triggered, err)
		}
	})

	t.Run("missing a scope var errors naming it", func(t *testing.T) {
		clearAuthEnv(t)
		t.Setenv(hbase.EnvPipelineID, "pipe1")
		t.Setenv(hbase.EnvAccountID, "acct1")
		// HARNESS_ORG_ID deliberately left unset.
		cfg := &PipelineAuthConfig{TokenEnvVar: "PIPELINE_TOKEN"}
		r, triggered, err := ResolvePipelineAuth(cfg, noopEvalHeaders)
		if r != nil || !triggered || err == nil || !strings.Contains(err.Error(), hbase.EnvOrgID) {
			t.Fatalf("ResolvePipelineAuth = (%v, %v, %v), want error naming %s", r, triggered, err, hbase.EnvOrgID)
		}
	})

	t.Run("non-VM infra errors, no fallthrough", func(t *testing.T) {
		clearAuthEnv(t)
		t.Setenv(hbase.EnvPipelineID, "pipe1")
		t.Setenv(hbase.EnvAccountID, "acct1")
		t.Setenv(hbase.EnvOrgID, "org1")
		t.Setenv(hbase.EnvProjectID, "proj1")
		t.Setenv(hbase.EnvInfra, "KUBERNETES")
		cfg := &PipelineAuthConfig{TokenEnvVar: "PIPELINE_TOKEN"}
		r, triggered, err := ResolvePipelineAuth(cfg, noopEvalHeaders)
		if r != nil || !triggered || err == nil {
			t.Fatalf("ResolvePipelineAuth = (%v, %v, %v), want (nil, true, err)", r, triggered, err)
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
		r, triggered, err := ResolvePipelineAuth(cfg, noopEvalHeaders)
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
		r, triggered, err := ResolvePipelineAuth(cfg, noopEvalHeaders)
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
