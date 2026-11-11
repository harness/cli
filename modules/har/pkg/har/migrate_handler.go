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
	routeMigrationLogsToHlog()
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

	if err := resolveMigrateDestEndpoint(ctx, cfg, pkgURLFlag); err != nil {
		return err
	}

	bgCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sigs := make(chan os.Signal, 2)
	signal.Notify(sigs, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigs
		fmt.Println("\nInterrupted. Shutting down gracefully... (press Ctrl+C again to force quit)")
		cancel()
		<-sigs
		fmt.Println("Force quitting.")
		os.Exit(130)
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

// resolveMigrateDestEndpoint picks the destination package host: --pkg-url, then
// destination.endpoint, then auth. Only the auth fallback looks up the package
// host, so an explicit override works where /system/info is unavailable.
func resolveMigrateDestEndpoint(ctx *cmdctx.Ctx, cfg *types.Config, pkgURLFlag string) error {
	switch {
	case pkgURLFlag != "":
		cfg.Dest.Endpoint = pkgURLFlag
	case cfg.Dest.Endpoint != "":
	default:
		if err := resolvePackageHost(ctx); err != nil {
			return err
		}
		cfg.Dest.Endpoint = ctx.Auth.RegistryURL
	}
	if cfg.Dest.Endpoint == "" {
		return fmt.Errorf("pkg-url must be set: no destination package registry URL configured — " +
			"pass --pkg-url, set destination.endpoint in the config, or run the login flow so it can be resolved from auth")
	}
	return nil
}
