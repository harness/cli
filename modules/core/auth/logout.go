// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"fmt"

	"github.com/harness/cli/v3/pkg/auth"
	"github.com/harness/cli/v3/pkg/cmdctx"
	"github.com/harness/cli/v3/pkg/config"
)

func LogoutHandler(ctx *cmdctx.Ctx) error {
	profileName := cmdctx.GetString(ctx.FlagValues, "profile")
	if profileName == "" {
		profileName = "default"
	}
	if auth.IsReservedProfileName(profileName) {
		return fmt.Errorf("%q is a reserved profile name and cannot be logged out", profileName)
	}

	cfg, err := config.LoadConfig()
	if err != nil {
		return err
	}
	if _, exists := cfg.Profiles[profileName]; !exists {
		return fmt.Errorf("profile %q not found", profileName)
	}

	credentialsOnly := cmdctx.GetBool(ctx.FlagValues, "credentials-only")
	if credentialsOnly {
		if err := auth.DeleteCredential(profileName); err != nil {
			return fmt.Errorf("removing credentials: %w", err)
		}
		fmt.Printf("Credentials for profile %q removed; the profile itself was kept.\n", profileName)
		return nil
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
