// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package vibe

import (
	"fmt"

	"github.com/harness/cli/pkg/cmdctx"
)

func vibeAppListHandler(ctx *cmdctx.Ctx) error {
	api := newAPI(ctx)
	apps, err := api.listJSON("/api/apps")
	if err != nil {
		return err
	}
	return writeList(ctx, apps, vibeAppFields, vibeAppListColumns)
}

func vibeAppGetHandler(ctx *cmdctx.Ctx) error {
	appID := ctx.Id
	if appID == "" {
		return fmt.Errorf("get vibe_app requires <appId>")
	}
	api := newAPI(ctx)
	var app map[string]any
	if err := api.getJSON("/api/apps/"+appID, &app); err != nil {
		return err
	}
	return writeGet(ctx, app)
}
