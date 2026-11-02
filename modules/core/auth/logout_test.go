// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"testing"

	"github.com/harness/cli/v3/pkg/auth"
	"github.com/harness/cli/v3/pkg/config"
)

// seedLogoutProfile writes a "prod" profile with a stored PAT into an isolated
// HARNESS_CLI_HOME and returns nothing — callers assert via LoadConfig /
// LoadCredentials afterwards.
func seedLogoutProfile(t *testing.T) {
	t.Helper()
	isolatedDir(t)
	cfg := &config.Config{Profiles: map[string]*config.Profile{
		"prod": {AccountID: "acctid", OrgID: "o", ProjectID: "p"},
	}}
	if err := config.SaveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	if err := auth.SetCredential("prod", validToken); err != nil {
		t.Fatal(err)
	}
}

func TestLogoutHandler_credentialsOnly(t *testing.T) {
	seedLogoutProfile(t)

	ctx := testCtx(map[string]any{"profile": "prod", "credentials-only": true})
	if err := LogoutHandler(ctx); err != nil {
		t.Fatalf("LogoutHandler: %v", err)
	}

	// The profile stays in config.yaml.
	cfg, err := config.LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := cfg.Profiles["prod"]; !ok {
		t.Error("--credentials-only removed the profile; it should be kept")
	}

	// The stored token is gone.
	creds, err := auth.LoadCredentials()
	if err != nil {
		t.Fatal(err)
	}
	if c := creds["prod"]; c != nil && c.Token != "" {
		t.Errorf("--credentials-only left a stored token %q", c.Token)
	}
}

func TestLogoutHandler_full(t *testing.T) {
	seedLogoutProfile(t)

	ctx := testCtx(map[string]any{"profile": "prod"})
	if err := LogoutHandler(ctx); err != nil {
		t.Fatalf("LogoutHandler: %v", err)
	}

	cfg, err := config.LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := cfg.Profiles["prod"]; ok {
		t.Error("full logout should remove the profile from config")
	}
	creds, err := auth.LoadCredentials()
	if err != nil {
		t.Fatal(err)
	}
	if c := creds["prod"]; c != nil && c.Token != "" {
		t.Error("full logout should remove the stored token")
	}
}

func TestLogoutHandler_credentialsOnlyMissingProfile(t *testing.T) {
	isolatedDir(t)
	ctx := testCtx(map[string]any{"profile": "ghost", "credentials-only": true})
	if err := LogoutHandler(ctx); err == nil {
		t.Fatal("expected an error logging out a profile that does not exist")
	}
}
