// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"fmt"

	"github.com/harness/cli/pkg/auth"
	"github.com/harness/cli/pkg/cmdctx"
	"github.com/harness/cli/pkg/config"
)

func LogoutHandler(ctx *cmdctx.Ctx) error {
	profileName := cmdctx.GetString(ctx.FlagValues, "profile")
	if profileName == "" {
		profileName = "default"
	}

	cfg, err := config.LoadConfig()
	if err != nil {
		return err
	}
	if _, exists := cfg.Profiles[profileName]; !exists {
		return fmt.Errorf("profile %q not found", profileName)
	}

	delete(cfg.Profiles, profileName)
	if err := config.SaveConfig(cfg); err != nil {
		return fmt.Errorf("saving config: %w", err)
	}
	if err := auth.DeleteCredential(profileName); err != nil {
		return fmt.Errorf("removing credentials: %w", err)
	}

	fmt.Printf("Profile %q removed.\n", profileName)
	return nil
}
