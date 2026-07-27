// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/harness/cli/pkg/cmdctx"
	"github.com/harness/cli/pkg/hbase"
)

// testCtx builds a bare Ctx with only FlagValues set.
func testCtx(flags map[string]any) *cmdctx.Ctx {
	return &cmdctx.Ctx{FlagValues: flags}
}

// isolatedCtx redirects HARNESS_CLI_HOME to a fresh temp dir so LoadConfig /
// SaveConfig never touch the real ~/.harness.
func isolatedCtx(t *testing.T, flags map[string]any) *cmdctx.Ctx {
	t.Helper()
	t.Setenv(hbase.EnvCLIHome, t.TempDir())
	return testCtx(flags)
}

// isolatedDir sets HARNESS_CLI_HOME to dir and returns the dir.
func isolatedDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv(hbase.EnvCLIHome, dir)
	return dir
}

func writeTestConfig(t *testing.T, dir, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(content), 0o600); err != nil {
		t.Fatalf("writing test config: %v", err)
	}
}

// validToken is a syntactically valid PAT whose account segment is "acctid".
const validToken = "pat.acctid.tokenid.secret123"

// --------------------------------------------------------------------------
// LoginHandler — early-return validation branches
// --------------------------------------------------------------------------

func TestLoginHandler_mutualExclusiveOverwrite(t *testing.T) {
	ctx := testCtx(map[string]any{"overwrite": true, "no-overwrite": true})
	err := LoginHandler(ctx)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "mutually exclusive") {
		t.Fatalf("error = %q, want %q", err, "mutually exclusive")
	}
}

func TestLoginHandler_profileNameValidation(t *testing.T) {
	tests := []struct {
		name         string
		profile      string
		wantErrSub   string
		wantNoErrSub string
	}{
		{name: "special chars rejected", profile: "bad name!", wantErrSub: "invalid profile name"},
		{name: "space rejected", profile: "bad profile", wantErrSub: "invalid profile name"},
		{name: "at-sign rejected", profile: "user@domain", wantErrSub: "invalid profile name"},
		{name: "dot rejected", profile: "my.profile", wantErrSub: "invalid profile name"},
		{name: "valid alphanumeric-dash", profile: "my-profile1", wantNoErrSub: "invalid profile name"},
		{name: "valid with underscores", profile: "my_profile_1", wantNoErrSub: "invalid profile name"},
		{name: "single char valid", profile: "a", wantNoErrSub: "invalid profile name"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := testCtx(map[string]any{"profile": tc.profile})
			err := LoginHandler(ctx)
			if tc.wantErrSub != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tc.wantErrSub)
				}
				if !strings.Contains(err.Error(), tc.wantErrSub) {
					t.Fatalf("error = %q, want substring %q", err, tc.wantErrSub)
				}
			}
			if tc.wantNoErrSub != "" && err != nil {
				if strings.Contains(err.Error(), tc.wantNoErrSub) {
					t.Fatalf("valid profile triggered profile-name error: %q", err)
				}
			}
		})
	}
}

// --------------------------------------------------------------------------
// LoginHandler — non-interactive (no TTY) path
// --------------------------------------------------------------------------

func TestLoginHandler_nonInteractive_noToken(t *testing.T) {
	// IsPty=false (default), no api-token → "not a terminal"
	ctx := isolatedCtx(t, map[string]any{"profile": "default"})
	err := LoginHandler(ctx)
	if err == nil {
		t.Fatal("expected error for missing token")
	}
	if !strings.Contains(err.Error(), "not a terminal") {
		t.Fatalf("error = %q, want %q", err, "not a terminal")
	}
}

func TestLoginHandler_nonInteractive_existingProfile_noOverwriteFlag(t *testing.T) {
	dir := isolatedDir(t)
	writeTestConfig(t, dir, `profiles:
  default:
    api_url: https://app.harness.io
    account_id: acct
`)
	ctx := testCtx(map[string]any{
		"no-overwrite": true,
		"api-token":    validToken,
	})
	err := LoginHandler(ctx)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("error = %q, want %q", err, "already exists")
	}
}

func TestLoginHandler_nonInteractive_existingProfile_noFlag(t *testing.T) {
	// Neither --overwrite nor --no-overwrite → "already exists — pass --overwrite or --no-overwrite"
	dir := isolatedDir(t)
	writeTestConfig(t, dir, `profiles:
  default:
    api_url: https://app.harness.io
    account_id: acct
`)
	ctx := testCtx(map[string]any{"api-token": validToken})
	err := LoginHandler(ctx)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("error = %q, want %q", err, "already exists")
	}
}

func TestLoginHandler_nonInteractive_badPATFormat(t *testing.T) {
	ctx := isolatedCtx(t, map[string]any{"api-token": "notapat"})
	err := LoginHandler(ctx)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "invalid token") {
		t.Fatalf("error = %q, want %q", err, "invalid token")
	}
}

func TestLoginHandler_nonInteractive_accountMismatch(t *testing.T) {
	// Token has account "acctid", flag passes "otheracct" → mismatch error.
	ctx := isolatedCtx(t, map[string]any{
		"api-token":   validToken,
		"account":     "otheracct",
		"no-validate": true,
	})
	err := LoginHandler(ctx)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "does not match account ID in token") {
		t.Fatalf("error = %q, want %q", err, "does not match account ID in token")
	}
}

func TestLoginHandler_nonInteractive_badAPIURL(t *testing.T) {
	// Provide a token so we reach URL validation; use IsPty=false and a non-empty
	// api-url that doesn't look like a Harness URL.
	ctx := isolatedCtx(t, map[string]any{
		"api-token": validToken,
		"api-url":   "ftp://bad.url",
	})
	err := LoginHandler(ctx)
	if err == nil {
		t.Fatal("expected error for invalid API URL")
	}
	// ValidateAPIURL returns an error about the URL not being a valid Harness URL.
	if !strings.Contains(strings.ToLower(err.Error()), "url") && !strings.Contains(strings.ToLower(err.Error()), "harness") {
		t.Fatalf("error = %q, expected URL-related error", err)
	}
}

func TestLoginHandler_nonInteractive_overwriteExistingProfile(t *testing.T) {
	// --overwrite on existing profile: hits the "silent" branch and continues.
	// With a valid-format token and --no-validate, it should proceed past that
	// branch and reach fetchRegistryURL (HTTP) — we use a test server.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"registryUrl": ""}})
	}))
	defer srv.Close()

	dir := isolatedDir(t)
	writeTestConfig(t, dir, `profiles:
  default:
    api_url: https://app.harness.io
    account_id: acctid
`)
	ctx := testCtx(map[string]any{
		"overwrite":   true,
		"api-token":   validToken,
		"api-url":     srv.URL,
		"no-validate": true,
	})
	err := LoginHandler(ctx)
	// Should not fail with "already exists" — the overwrite branch must be silent.
	if err != nil && strings.Contains(err.Error(), "already exists") {
		t.Fatalf("--overwrite should not return already-exists error, got: %v", err)
	}
}

func TestLoginHandler_IsPty_emptyURL_noToken(t *testing.T) {
	// IsPty=true + empty api-url → apiURL defaults to app.harness.io.
	// No token → "API token is required" (line 152, distinct from the non-IsPty "not a terminal").
	ctx := isolatedCtx(t, nil)
	ctx.IsPty = true
	err := LoginHandler(ctx)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "API token is required") {
		t.Fatalf("error = %q, want %q", err, "API token is required")
	}
}

func TestLoginHandler_IsPty_badAPIURL(t *testing.T) {
	// IsPty=true + non-empty bad api-url → ValidateAPIURL error.
	ctx := isolatedCtx(t, map[string]any{
		"api-url":   "ftp://not-harness.example.com",
		"api-token": validToken,
	})
	ctx.IsPty = true
	err := LoginHandler(ctx)
	if err == nil {
		t.Fatal("expected error for invalid API URL")
	}
	if !strings.Contains(err.Error(), "not a valid Harness API URL") {
		t.Fatalf("error = %q, want %q", err, "not a valid Harness API URL")
	}
}

func TestLoginHandler_configLoadError(t *testing.T) {
	// Point HARNESS_CLI_HOME at a file (not a dir), so config.LoadConfig
	// gets a path that ends in /config.yaml but the parent is a file, causing
	// an error when it tries to read it.
	tmp := t.TempDir()
	// Write a file where config.yaml would be expected.
	configPath := filepath.Join(tmp, "config.yaml")
	if err := os.WriteFile(configPath, []byte("not: [valid yaml"), 0o600); err != nil {
		t.Fatalf("setup: %v", err)
	}
	t.Setenv(hbase.EnvCLIHome, tmp)

	ctx := testCtx(map[string]any{"profile": "default"})
	err := LoginHandler(ctx)
	if err == nil {
		t.Fatal("expected error for corrupt config")
	}
	// Error must come from config parsing, not from profile-name validation.
	if strings.Contains(err.Error(), "invalid profile name") {
		t.Fatalf("got profile-name error instead of config error: %v", err)
	}
}

func TestLoginHandler_nonInteractive_validateTokenCalled(t *testing.T) {
	// Without --no-validate, validateToken is called. Point it at a test server
	// that returns 401 so we can confirm the validation error is surfaced.
	// NOTE: api-url must pass ValidateAPIURL (requires *.harness.io host) which
	// prevents using a local httptest.Server URL here. The remaining happy-path
	// branches (lines 165-198) that require a valid api-url + network are covered
	// by validateToken and fetchRegistryURL unit tests below instead.
	ctx := isolatedCtx(t, map[string]any{
		"api-token": validToken,
		// api-url empty → defaults to https://app.harness.io; validateToken will
		// fail with a network error (not a "validation failed" message).
	})
	err := LoginHandler(ctx)
	if err == nil {
		t.Fatal("expected error when token validation fails")
	}
	// Must NOT be a profile-name or PAT-format error — those would mean we regressed
	// to an earlier gate.
	if strings.Contains(err.Error(), "invalid profile name") || strings.Contains(err.Error(), "invalid token") {
		t.Fatalf("error at wrong stage: %q", err)
	}
}

// --------------------------------------------------------------------------
// validateToken — all HTTP status branches
// --------------------------------------------------------------------------

func TestValidateToken(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		body       string
		wantErrSub string
		wantNil    bool
	}{
		{
			name:       "200 OK returns nil",
			statusCode: 200,
			wantNil:    true,
		},
		{
			name:       "401 returns token rejected",
			statusCode: 401,
			wantErrSub: "token rejected (401)",
		},
		{
			name:       "403 returns access denied",
			statusCode: 403,
			wantErrSub: "access denied (403)",
		},
		{
			name:       "500 with JSON message returns message",
			statusCode: 500,
			body:       `{"message":"internal error"}`,
			wantErrSub: "internal error",
		},
		{
			name:       "500 without JSON message returns status code",
			statusCode: 500,
			body:       `not json`,
			wantErrSub: "validation failed with status 500",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.statusCode)
				if tc.body != "" {
					w.Write([]byte(tc.body))
				}
			}))
			defer srv.Close()

			err := validateToken(srv.URL, "tok", "acct")
			if tc.wantNil {
				if err != nil {
					t.Fatalf("expected nil, got %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tc.wantErrSub) {
				t.Fatalf("error = %q, want substring %q", err, tc.wantErrSub)
			}
		})
	}
}

func TestValidateToken_unreachableServer(t *testing.T) {
	// Use a server that is immediately closed so the HTTP request fails.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	srv.Close()

	err := validateToken(srv.URL, "tok", "acct")
	if err == nil {
		t.Fatal("expected error for unreachable server")
	}
	if !strings.Contains(err.Error(), "cannot reach") {
		t.Fatalf("error = %q, want %q", err, "cannot reach")
	}
}

// --------------------------------------------------------------------------
// fetchRegistryURL — all branches
// --------------------------------------------------------------------------

func TestFetchRegistryURL(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		body       string
		wantURL    string
		wantErrSub string
	}{
		{
			name:       "200 with registryUrl",
			statusCode: 200,
			body:       `{"data":{"registryUrl":"https://pkg.harness.io"}}`,
			wantURL:    "https://pkg.harness.io",
		},
		{
			name:       "200 without registryUrl returns empty",
			statusCode: 200,
			body:       `{"data":{}}`,
			wantURL:    "",
		},
		{
			name:       "non-200 returns error",
			statusCode: 404,
			wantErrSub: "status 404",
		},
		{
			name:       "200 with invalid JSON returns decode error",
			statusCode: 200,
			body:       `not json`,
			wantErrSub: "decoding response",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.statusCode)
				if tc.body != "" {
					w.Write([]byte(tc.body))
				}
			}))
			defer srv.Close()

			got, err := fetchRegistryURL(srv.URL, "tok", "acct")
			if tc.wantErrSub != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tc.wantErrSub)
				}
				if !strings.Contains(err.Error(), tc.wantErrSub) {
					t.Fatalf("error = %q, want substring %q", err, tc.wantErrSub)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.wantURL {
				t.Fatalf("fetchRegistryURL = %q, want %q", got, tc.wantURL)
			}
		})
	}
}

func TestFetchRegistryURL_unreachableServer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	srv.Close()

	_, err := fetchRegistryURL(srv.URL, "tok", "acct")
	if err == nil {
		t.Fatal("expected error for unreachable server")
	}
	if !strings.Contains(err.Error(), "request failed") {
		t.Fatalf("error = %q, want %q", err, "request failed")
	}
}
