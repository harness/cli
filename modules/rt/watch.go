// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package rt

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/harness/cli/pkg/client"
	"github.com/harness/cli/pkg/cmdctx"
)

const watchFollowFnID = "watch"

// Runs report metrics about this often; polling faster mostly repeats a line.
const defaultPollInterval = 10 * time.Second

const (
	runStatusStopped  = "Stopped"
	runStatusFinished = "Finished"
	runStatusFailed   = "Failed"
)

func isTerminalStatus(status string) bool {
	return status == runStatusStopped || status == runStatusFinished || status == runStatusFailed
}

// Polls until the run is terminal, exiting non-zero on failure so a gating pipeline step fails too.
// The timeline goes to stderr, so "--follow --format json > run.json" still yields one document.
func watchFollowFn(ctx *cmdctx.Ctx, result any) error {
	identity := runToWatch(ctx, result)
	if identity == "" {
		return errors.New("--follow: the response did not name a run to watch")
	}
	interval, err := pollInterval(ctx)
	if err != nil {
		return err
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	var previous, status string
	for {
		run, err := readRun(ctx, identity)
		if err != nil {
			// A deadline can land mid-request; without this it surfaces as a raw transport error.
			if stopped := stoppedWatching(ctx, identity, status); stopped != nil {
				return stopped
			}
			return err
		}
		status = stringField(run, "status")

		if line := progressLine(run); line != previous {
			fmt.Fprintln(os.Stderr, line)
			previous = line
		}
		if isTerminalStatus(status) {
			return terminalError(run, identity)
		}

		select {
		case <-ctx.Context.Done():
			return stoppedWatching(ctx, identity, status)
		case <-ticker.C:
		}
	}
}

// Starting or reading a run names it in the response; stopping one answers with a bare ack.
func runToWatch(ctx *cmdctx.Ctx, result any) string {
	if id := stringField(asMap(result), "identity"); id != "" {
		return id
	}
	return ctx.Id
}

func pollInterval(ctx *cmdctx.Ctx) (time.Duration, error) {
	raw := cmdctx.GetString(ctx.FlagValues, "interval")
	if raw == "" {
		return defaultPollInterval, nil
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("--interval %q is not a duration such as 5s or 2m", raw)
	}
	if d <= 0 {
		return 0, fmt.Errorf("--interval must be greater than zero, got %s", d)
	}
	return d, nil
}

func readRun(ctx *cmdctx.Ctx, identity string) (map[string]any, error) {
	resp, _, err := client.New(ctx).Get(basePath+"/runs/"+url.PathEscape(identity), scopeParams(ctx))
	if err != nil {
		return nil, err
	}
	return asMap(resp), nil
}

// Returns nil while the context is live. Watching is a read, so the wording says the run was left alone.
func stoppedWatching(ctx *cmdctx.Ctx, identity, status string) error {
	cause := context.Cause(ctx.Context)
	if cause == nil {
		return nil
	}
	still := ""
	if status != "" {
		still = ", it was still " + status
	}
	reason := "watching was interrupted"
	if cmdctx.IsTimeout(cause) {
		reason = cause.Error()
	}
	return fmt.Errorf("stopped watching run %s%s: %s. The run is unaffected and can be followed again with: harness get loadtest_run %s --follow",
		identity, still, reason, identity)
}

// One line per poll, so a watch reads as a timeline. Unmeasured fields are omitted, not zeroed.
func progressLine(run map[string]any) string {
	status := stringField(run, "status")
	targetUsers := floatField(run, "targetUsers")
	metrics := asMap(run["lastMetrics"])
	if len(metrics) == 0 {
		return fmt.Sprintf("%-9s users=%.0f", status, targetUsers)
	}

	var line strings.Builder
	fmt.Fprintf(&line, "%-9s users=", status)
	// The ramp-up gap is worth watching, but not every tool reports the current count.
	if current := floatField(metrics, "currentUsers"); current > 0 {
		fmt.Fprintf(&line, "%.0f/%.0f", current, targetUsers)
	} else {
		fmt.Fprintf(&line, "%.0f", targetUsers)
	}

	fmt.Fprintf(&line, " rps=%.1f requests=%.0f failures=%.0f errors=%.2f%%",
		floatField(metrics, "totalRps"), floatField(metrics, "totalRequests"),
		floatField(metrics, "totalFailures"), floatField(metrics, "errorRate"))
	fmt.Fprintf(&line, " avg=%.0fms p50=%.0fms p95=%.0fms p99=%.0fms",
		floatField(metrics, "avgResponseMs"), floatField(metrics, "p50ResponseMs"),
		floatField(metrics, "p95ResponseMs"), floatField(metrics, "p99ResponseMs"))

	// Latency is time to first byte, which only JMeter measures; "lat-p99=0ms" would claim an instant reply.
	if hasLatency(metrics) {
		fmt.Fprintf(&line, " lat-avg=%.0fms lat-p50=%.0fms lat-p95=%.0fms lat-p99=%.0fms",
			floatField(metrics, "avgLatencyMs"), floatField(metrics, "p50LatencyMs"),
			floatField(metrics, "p95LatencyMs"), floatField(metrics, "p99LatencyMs"))
	}

	return line.String()
}

func hasLatency(metrics map[string]any) bool {
	return floatField(metrics, "avgLatencyMs") > 0 || floatField(metrics, "p50LatencyMs") > 0 ||
		floatField(metrics, "p95LatencyMs") > 0 || floatField(metrics, "p99LatencyMs") > 0
}

// Stopped and finished are both outcomes the caller asked for, so only Failed exits non-zero.
func terminalError(run map[string]any, identity string) error {
	if stringField(run, "status") != runStatusFailed {
		return nil
	}
	if msg := stringField(run, "errorMessage"); msg != "" {
		return fmt.Errorf("run %s failed: %s", identity, msg)
	}
	return fmt.Errorf("run %s failed", identity)
}
