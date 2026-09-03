// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package rt

import (
	"fmt"
	"strconv"
	"time"

	"github.com/harness/cli/v3/pkg/cmdctx"
	"github.com/harness/cli/v3/pkg/exprenv/exprfuncs"
)

const usageWindowParamsID = "usage_window"

// The console opens on the last 30 days, so the bare command agrees with what people see.
const defaultUsageWindow = 30 * 24 * time.Hour

// Both bounds are required — the route parses them unchecked and answers 400 on an empty
// string rather than defaulting. The spec has no way to say "now", so the window is built here.
func usageWindowParams(ctx *cmdctx.Ctx) (map[string]string, error) {
	// End first, so the default span hangs off it: otherwise --to alone would imply a start
	// after its own end, and "usage up to June" would be refused as inverted.
	end, err := usageBound(ctx, "to", time.Now())
	if err != nil {
		return nil, err
	}
	start, err := usageBound(ctx, "from", time.UnixMilli(end).Add(-defaultUsageWindow))
	if err != nil {
		return nil, err
	}
	// The server's own inverted-window error does not say which flag was wrong.
	if end < start {
		return nil, fmt.Errorf("--to is earlier than --from, so the window is empty: %s to %s",
			time.UnixMilli(start).Format(time.RFC3339), time.UnixMilli(end).Format(time.RFC3339))
	}
	return map[string]string{
		"startTime": strconv.FormatInt(start, 10),
		"endTime":   strconv.FormatInt(end, 10),
	}, nil
}

// Parses through the spec's own parseDateMs, so the accepted formats match the flag descriptions.
func usageBound(ctx *cmdctx.Ctx, flag string, fallback time.Time) (int64, error) {
	raw := cmdctx.GetString(ctx.FlagValues, flag)
	if raw == "" {
		return fallback.UnixMilli(), nil
	}
	parsed := exprfuncs.ParseDateMs(raw)
	if parsed == "" {
		return 0, fmt.Errorf("--%s %q is not a date: use a span such as 30d or 2w, a date such as 2026-01-01, or unix millis", flag, raw)
	}
	ms, err := strconv.ParseInt(parsed, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("--%s %q is not a date: %w", flag, raw, err)
	}
	return ms, nil
}
