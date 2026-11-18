// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package gitops

import (
	"fmt"
	"time"

	"github.com/harness/cli/v3/pkg/cmdctx"
)

const (
	metricsRangeFnID = "gitops_agent_metrics_range"
	metricsMaxRange  = 14 * 24 * time.Hour
)

// The API accepts an inverted or over-long range and answers with an empty result, so reject it here.
func metricsRangeParams(ctx *cmdctx.Ctx) (map[string]string, error) {
	start, err := parseMetricsTime(ctx, "start-time")
	if err != nil {
		return nil, err
	}
	end, err := parseMetricsTime(ctx, "end-time")
	if err != nil {
		return nil, err
	}
	if start.After(end) {
		return nil, fmt.Errorf("--start-time is after --end-time")
	}
	if end.Sub(start) > metricsMaxRange {
		return nil, fmt.Errorf("range is over 14 days (the metrics retention window); use a shorter range")
	}
	return map[string]string{
		"startTime": start.UTC().Format(time.RFC3339),
		"endTime":   end.UTC().Format(time.RFC3339),
	}, nil
}

func parseMetricsTime(ctx *cmdctx.Ctx, flag string) (time.Time, error) {
	raw := cmdctx.GetString(ctx.FlagValues, flag)
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Time{}, fmt.Errorf("--%s %q is not an RFC3339 time, e.g. 2026-11-18T00:00:00Z", flag, raw)
	}
	return t, nil
}
