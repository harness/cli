// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package analyze

import (
	"fmt"
	"strings"

	"github.com/harness/cli/v3/pkg/cmdctx"
)

const HandlerID = "execute_coverage_analyze"

// Handler is the `execute coverage:analyze` workflow handler. It is
// local-only: it never calls the Harness API and requires no authentication.
func Handler(ctx *cmdctx.Ctx) error {
	if len(ctx.Args) > 0 {
		return fmt.Errorf("unexpected arguments: %s", strings.Join(ctx.Args, " "))
	}

	files := cmdctx.GetStringSlice(ctx.FlagValues, "file")
	excludePatterns := cmdctx.GetStringSlice(ctx.FlagValues, "exclude")
	patch := cmdctx.GetBool(ctx.FlagValues, "patch")
	base := cmdctx.GetString(ctx.FlagValues, "base")
	head := cmdctx.GetString(ctx.FlagValues, "head")

	stats, err := analyze(files, excludePatterns, patch, base, head)
	if err != nil {
		return err
	}

	displayStats(stats)
	return nil
}
