// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package auth

import "testing"

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
