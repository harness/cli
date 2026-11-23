// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package upload

import (
	"fmt"
	"strings"

	"github.com/harness/cli/v3/pkg/cmdctx"
)

const HandlerID = "execute_coverage_upload"

// Handler is the `execute coverage:upload` workflow handler. It calls the
// Harness coverage service to ensure the repository is registered, resolve
// exclude settings, and upload the parsed/encoded report.
func Handler(ctx *cmdctx.Ctx) error {
	if len(ctx.Args) > 0 {
		return fmt.Errorf("unexpected arguments: %s", strings.Join(ctx.Args, " "))
	}

	f := uploadFlags{
		accountID:       cmdctx.GetString(ctx.FlagValues, "account"),
		provider:        cmdctx.GetString(ctx.FlagValues, "provider"),
		owner:           cmdctx.GetString(ctx.FlagValues, "owner"),
		identifier:      cmdctx.GetString(ctx.FlagValues, "identifier"),
		commitID:        cmdctx.GetString(ctx.FlagValues, "commit"),
		branch:          cmdctx.GetString(ctx.FlagValues, "branch"),
		buildID:         cmdctx.GetInt(ctx.FlagValues, "build"),
		pipelineID:      cmdctx.GetString(ctx.FlagValues, "pipeline"),
		executionID:     cmdctx.GetString(ctx.FlagValues, "execution"),
		pullReqID:       int64(cmdctx.GetInt(ctx.FlagValues, "pr")),
		targetScope:     cmdctx.GetString(ctx.FlagValues, "target-scope"),
		tags:            cmdctx.GetStringSlice(ctx.FlagValues, "tags"),
		files:           cmdctx.GetStringSlice(ctx.FlagValues, "file"),
		base:            cmdctx.GetString(ctx.FlagValues, "base"),
		patch:           cmdctx.GetBool(ctx.FlagValues, "patch"),
		excludePatterns: cmdctx.GetStringSlice(ctx.FlagValues, "exclude"),
	}

	return upload(ctx, f)
}
