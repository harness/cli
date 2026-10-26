// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package waituntil

import (
	"fmt"
	"strings"

	"github.com/harness/cli/v3/pkg/cmdctx"
)

const HandlerID = "execute_coverage_wait_until"

// Handler is the `execute coverage:wait-until` workflow handler. It polls the
// Harness coverage service until all reports for the execution are processed,
// then evaluates the result against the given quality-gate thresholds.
func Handler(ctx *cmdctx.Ctx) error {
	if len(ctx.Args) > 0 {
		return fmt.Errorf("unexpected arguments: %s", strings.Join(ctx.Args, " "))
	}

	f := waitUntilFlags{
		accountID:   cmdctx.GetString(ctx.FlagValues, "account"),
		executionID: cmdctx.GetString(ctx.FlagValues, "execution"),
		gt:          cmdctx.GetFloat64(ctx.FlagValues, "gt"),
		gte:         cmdctx.GetFloat64(ctx.FlagValues, "gte"),
		patchGt:     cmdctx.GetFloat64(ctx.FlagValues, "patch-gt"),
		patchGte:    cmdctx.GetFloat64(ctx.FlagValues, "patch-gte"),
		timeout:     cmdctx.GetString(ctx.FlagValues, "wait-timeout"),
		interval:    cmdctx.GetString(ctx.FlagValues, "interval"),
	}

	return waitUntil(ctx, f)
}
