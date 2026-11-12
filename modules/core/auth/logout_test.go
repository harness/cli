// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"testing"

	"github.com/harness/cli/v3/pkg/auth"
	"github.com/harness/cli/v3/pkg/config"
)

func TestLogoutHandler_CredentialsOnly(t *testing.T) {
	setup := func(t *testing.T) {
		t.Helper()
		isolatedCtx(t, nil)
		cfg := &config.Config{Profiles: map[string]*config.Profile{
			"a": {AccountID: "acct"},
			"b": {AccountID: "acct"},
		}}
		if err := config.SaveConfig(cfg); err != nil {
			t.Fatal(err)
		}
		for _, n := range []string{"a", "b"} {
			if err := auth.SetCredential(n, "pat.acct.tok-"+n+".not-a-secret"); err != nil {
				t.Fatal(err)
			}
		}
	}

	t.Run("keeps profile and other credentials", func(t *testing.T) {
		setup(t)
		ctx := testCtx(map[string]any{"profile": "a", "credentials-only": true})
		if err := LogoutHandler(ctx); err != nil {
			t.Fatal(err)
		}
		cfg, _ := config.LoadConfig()
		if len(cfg.Profiles) != 2 {
			t.Errorf("profiles = %d, want 2", len(cfg.Profiles))
		}
		creds, _ := auth.LoadCredentials()
		if creds["a"] != nil {
			t.Error("credentials for a should be removed")
		}
		if creds["b"] == nil {
			t.Error("credentials for b must be preserved")
		}
	})

	t.Run("without the flag removes the whole profile", func(t *testing.T) {
		setup(t)
		ctx := testCtx(map[string]any{"profile": "a"})
		if err := LogoutHandler(ctx); err != nil {
			t.Fatal(err)
		}
		cfg, _ := config.LoadConfig()
		if _, ok := cfg.Profiles["a"]; ok {
			t.Error("profile a should be removed")
		}
	})
}
