// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package har

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"syscall"

	"github.com/harness/cli/modules/har/pkg/har/migrate"
	"github.com/harness/cli/modules/har/pkg/har/migrate/types"

	"github.com/harness/cli/v3/pkg/cmdctx"
)

const executeRegistryMigrateHandlerID = "execute_registry_migrate"

func executeRegistryMigrateHandler(ctx *cmdctx.Ctx) error {
	a := ctx.Auth
	filePath := cmdctx.GetString(ctx.FlagValues, "config")
	concurrencyStr := cmdctx.GetString(ctx.FlagValues, "concurrency")
	overwrite := cmdctx.GetBool(ctx.FlagValues, "overwrite")
	dryRun := cmdctx.GetBool(ctx.FlagValues, "dry-run")
	pkgURLFlag := cmdctx.GetString(ctx.FlagValues, "pkg-url")
	summary := cmdctx.GetBool(ctx.FlagValues, "summary")
	resultFile := cmdctx.GetString(ctx.FlagValues, "result-file")

	cfg, err := types.LoadConfig(filePath)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	if concurrencyStr != "" {
		if n, err := strconv.Atoi(concurrencyStr); err == nil && n > 0 {
			cfg.Concurrency = n
		}
	}
	if overwrite {
		cfg.Overwrite = true
	}
	if dryRun {
		cfg.DryRun = true
	}
	if summary {
		cfg.Summary = true
	}
	if resultFile != "" {
		cfg.ResultFile = resultFile
	}

	// Thread auth context into the destination (HAR) registry config.
	cfg.Dest.AccountID = a.AccountID
	cfg.Dest.APIBaseURL = a.APIUrl

	// Resolve the destination package registry endpoint: explicit --pkg-url
	// override, then whatever the config file already specifies, then the
	// resolved auth registry URL. Fail fast rather than streaming migrated
	// artifacts into a 404 from an empty package host.
	switch {
	case pkgURLFlag != "":
		cfg.Dest.Endpoint = pkgURLFlag
	case cfg.Dest.Endpoint != "":
		// keep the value from the config file
	default:
		cfg.Dest.Endpoint = a.RegistryURL
	}
	if cfg.Dest.Endpoint == "" {
		return fmt.Errorf("pkg-url must be set: no destination package registry URL configured — " +
			"pass --pkg-url, set destination.endpoint in the config, or run the login flow so it can be resolved from auth")
	}

	bgCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigs
		fmt.Println("\nInterrupted. Shutting down gracefully...")
		cancel()
	}()

	svc, err := migrate.NewMigrationService(bgCtx, cfg)
	if err != nil {
		return fmt.Errorf("creating migration service: %w", err)
	}

	if err := svc.Run(bgCtx); err != nil {
		return fmt.Errorf("migration failed: %w", err)
	}

	fmt.Println("Migration completed successfully.")
	return nil
}
