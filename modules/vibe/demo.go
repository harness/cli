// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package vibe

import (
	"fmt"
	"slices"

	"github.com/harness/cli/pkg/cmdctx"
)

var demoPaths = []string{
	"success",
	"fail_source_import",
	"fail_app_discovery",
	"fail_app_build",
	"fail_infra",
	"fail_preview",
	"fail_security",
	"held_approval",
	"ready_publish",
	"live",
}

func vibeDemoStateHandler(ctx *cmdctx.Ctx) error {
	appID := cmdctx.GetString(ctx.FlagValues, "app")
	path := cmdctx.GetString(ctx.FlagValues, "path")
	if appID == "" {
		return fmt.Errorf("required: --app <appId>")
	}
	if path == "" {
		return fmt.Errorf("required: --path <demo-path>")
	}
	if !slices.Contains(demoPaths, path) {
		return fmt.Errorf("invalid --path %q: must be one of %v", path, demoPaths)
	}

	api := newAPI(ctx)
	var dep map[string]any
	if err := api.putJSON("/api/apps/"+appID+"/demo-state", map[string]any{"path": path}, &dep); err != nil {
		return err
	}
	return writeGet(ctx, dep)
}
