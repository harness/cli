// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package rt

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/harness/cli/v3/pkg/cmdctx"
)

func TestIsTerminalStatus(t *testing.T) {
	for _, status := range []string{runStatusStopped, runStatusFinished, runStatusFailed} {
		if !isTerminalStatus(status) {
			t.Errorf("%q should end a watch", status)
		}
	}
	for _, status := range []string{"Queued", "Initializing", "Running", ""} {
		if isTerminalStatus(status) {
			t.Errorf("%q should keep a watch going", status)
		}
	}
}

func TestPollInterval(t *testing.T) {
	cases := []struct {
		name    string
		raw     any
		want    time.Duration
		wantErr string
	}{
		{name: "absent", want: defaultPollInterval},
		{name: "empty", raw: "", want: defaultPollInterval},
		{name: "seconds", raw: "5s", want: 5 * time.Second},
		{name: "minutes", raw: "2m", want: 2 * time.Minute},
		{name: "not a duration", raw: "5", wantErr: "not a duration"},
		{name: "words", raw: "quick", wantErr: "not a duration"},
		{name: "zero", raw: "0s", wantErr: "greater than zero"},
		{name: "negative", raw: "-5s", wantErr: "greater than zero"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fv := map[string]any{}
			if tc.raw != nil {
				fv["interval"] = tc.raw
			}
			got, err := pollInterval(&cmdctx.Ctx{FlagValues: fv})

			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("expected an error containing %q, got %s", tc.wantErr, got)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("error %q does not contain %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("got %s, want %s", got, tc.want)
			}
		})
	}
}

func TestRunToWatch(t *testing.T) {
	ctx := &cmdctx.Ctx{Id: "from-argv"}
	if got := runToWatch(ctx, map[string]any{"identity": "from-response"}); got != "from-response" {
		t.Errorf("got %q, want the identity in the response", got)
	}
	for _, result := range []any{nil, map[string]any{}, map[string]any{"identity": ""}, "ok"} {
		if got := runToWatch(ctx, result); got != "from-argv" {
			t.Errorf("result %v: got %q, want the id from the command line", result, got)
		}
	}
	if got := runToWatch(&cmdctx.Ctx{}, nil); got != "" {
		t.Errorf("with nothing to watch, got %q, want empty", got)
	}
}

func TestTerminalError(t *testing.T) {
	failed := map[string]any{"status": runStatusFailed, "errorMessage": "target unreachable"}
	err := terminalError(failed, "run-1")
	if err == nil || !strings.Contains(err.Error(), "target unreachable") {
		t.Fatalf("expected the server's reason, got %v", err)
	}
	if !strings.Contains(err.Error(), "run-1") {
		t.Errorf("expected the run id in %q", err)
	}

	err = terminalError(map[string]any{"status": runStatusFailed}, "run-2")
	if err == nil || !strings.Contains(err.Error(), "run-2 failed") {
		t.Fatalf("expected a bare failure, got %v", err)
	}

	for _, status := range []string{runStatusFinished, runStatusStopped} {
		if err := terminalError(map[string]any{"status": status}, "run-3"); err != nil {
			t.Errorf("%q should exit clean, got %v", status, err)
		}
	}
}

func TestProgressLineWithoutMetrics(t *testing.T) {
	got := progressLine(map[string]any{"status": "Queued", "targetUsers": float64(50)})
	if !strings.Contains(got, "Queued") || !strings.Contains(got, "users=50") {
		t.Fatalf("got %q, want the status and the target", got)
	}
	// Nothing has been measured yet, so no measurements should be claimed.
	if strings.Contains(got, "rps=") {
		t.Errorf("got %q, want no metrics before any arrive", got)
	}
}

func TestProgressLineWithMetrics(t *testing.T) {
	run := map[string]any{
		"status":      "Running",
		"targetUsers": float64(100),
		"lastMetrics": map[string]any{
			"currentUsers":  float64(40),
			"totalRps":      12.34,
			"totalRequests": float64(5000),
			"totalFailures": float64(7),
			"errorRate":     0.14,
			"avgResponseMs": float64(120),
			"p50ResponseMs": float64(100),
			"p95ResponseMs": float64(300),
			"p99ResponseMs": float64(800),
		},
	}
	got := progressLine(run)
	// During ramp-up the gap between current and target is the thing worth watching.
	for _, want := range []string{"Running", "users=40/100", "rps=12.3", "requests=5000", "failures=7", "errors=0.14%", "avg=120ms", "p99=800ms"} {
		if !strings.Contains(got, want) {
			t.Errorf("got %q, want it to contain %q", got, want)
		}
	}
	// Latency is time to first byte, which only JMeter measures.
	if strings.Contains(got, "lat-") {
		t.Errorf("got %q, want no latency block when the tool does not measure it", got)
	}
}

func TestProgressLineWithoutCurrentUsers(t *testing.T) {
	run := map[string]any{
		"status":      "Running",
		"targetUsers": float64(100),
		"lastMetrics": map[string]any{"totalRps": 5.0},
	}
	got := progressLine(run)
	if strings.Contains(got, "0/100") {
		t.Fatalf("got %q, want the target alone rather than a zero current count", got)
	}
	if !strings.Contains(got, "users=100") {
		t.Fatalf("got %q, want users=100", got)
	}
}

func TestProgressLineWithLatency(t *testing.T) {
	run := map[string]any{
		"status":      "Running",
		"targetUsers": float64(10),
		"lastMetrics": map[string]any{
			"totalRps":      1.0,
			"avgLatencyMs":  float64(15),
			"p99LatencyMs":  float64(90),
			"p50LatencyMs":  float64(10),
			"p95LatencyMs":  float64(60),
			"avgResponseMs": float64(120),
		},
	}
	got := progressLine(run)
	for _, want := range []string{"lat-avg=15ms", "lat-p50=10ms", "lat-p95=60ms", "lat-p99=90ms"} {
		if !strings.Contains(got, want) {
			t.Errorf("got %q, want it to contain %q", got, want)
		}
	}
}

func TestHasLatency(t *testing.T) {
	if hasLatency(map[string]any{"totalRps": 5.0}) {
		t.Error("a tool that reports no latency should not get a latency block")
	}
	if hasLatency(map[string]any{"avgLatencyMs": float64(0), "p99LatencyMs": float64(0)}) {
		t.Error("all-zero latency is the server omitting it, not an instant reply")
	}
	if !hasLatency(map[string]any{"p95LatencyMs": float64(60)}) {
		t.Error("one measured percentile is enough to show the block")
	}
}

func TestStoppedWatchingSaysTheRunIsUnaffected(t *testing.T) {
	ctx, _ := apiCtx(t, nil)
	inner, cancel := context.WithCancelCause(ctx.Context)
	cancel(errors.New("interrupted"))
	ctx.Context = inner

	err := stoppedWatching(ctx, "checkout-aaa", "Running")
	if err == nil {
		t.Fatal("expected giving up to be reported")
	}
	for _, want := range []string{"checkout-aaa", "still Running", "unaffected", "--follow"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("message %q should mention %q", err, want)
		}
	}
}

func TestStoppedWatchingUsesTheTimeoutWording(t *testing.T) {
	ctx, _ := apiCtx(t, nil)
	inner, cancel := context.WithCancelCause(ctx.Context)
	cancel(&cmdctx.TimeoutError{Secs: 30})
	ctx.Context = inner

	err := stoppedWatching(ctx, "checkout-aaa", "Running")
	if err == nil || !strings.Contains(err.Error(), "timed out after 30s") {
		t.Fatalf("expected the timeout named, got %v", err)
	}
}

func TestStoppedWatchingOmitsAnUnknownStatus(t *testing.T) {
	ctx, _ := apiCtx(t, nil)
	inner, cancel := context.WithCancelCause(ctx.Context)
	cancel(errors.New("interrupted"))
	ctx.Context = inner

	err := stoppedWatching(ctx, "checkout-aaa", "")
	if err == nil || strings.Contains(err.Error(), "still") {
		t.Fatalf("expected no claim about the status, got %v", err)
	}
}

func TestStoppedWatchingIsSilentWhileLive(t *testing.T) {
	ctx, _ := apiCtx(t, nil)
	if err := stoppedWatching(ctx, "checkout-aaa", "Running"); err != nil {
		t.Fatalf("got %v, want nothing while the watch is still live", err)
	}
}

func TestWatchFollowFnNeedsARunToWatch(t *testing.T) {
	ctx, _ := apiCtx(t, nil)
	err := watchFollowFn(ctx, map[string]any{"acknowledged": true})
	if err == nil || !strings.Contains(err.Error(), "did not name a run") {
		t.Fatalf("expected the missing run reported, got %v", err)
	}
}

func TestWatchFollowFnRejectsABadInterval(t *testing.T) {
	ctx, _ := apiCtx(t, nil)
	ctx.FlagValues["interval"] = "soon"
	err := watchFollowFn(ctx, map[string]any{"identity": "checkout-aaa"})
	if err == nil || !strings.Contains(err.Error(), "--interval") {
		t.Fatalf("expected the interval rejected before polling, got %v", err)
	}
}

func TestWatchFollowFnStopsAtATerminalStatus(t *testing.T) {
	ctx, calls := apiCtx(t, map[string]any{
		api("/runs/checkout-aaa"): map[string]any{
			"identity": "checkout-aaa", "status": runStatusFinished,
		},
	})
	ctx.FlagValues["interval"] = "1h" // never elapses; the watch must not wait for it

	if err := watchFollowFn(ctx, map[string]any{"identity": "checkout-aaa"}); err != nil {
		t.Fatalf("a finished run should exit clean, got %v", err)
	}
	if len(*calls) != 1 {
		t.Errorf("polled %d times, want the watch to end on the first read", len(*calls))
	}
}

func TestWatchFollowFnFailsOnAFailedRun(t *testing.T) {
	ctx, _ := apiCtx(t, map[string]any{
		api("/runs/checkout-aaa"): map[string]any{
			"identity": "checkout-aaa", "status": runStatusFailed,
			"errorMessage": "the host refused every connection",
		},
	})

	err := watchFollowFn(ctx, map[string]any{"identity": "checkout-aaa"})
	if err == nil {
		t.Fatal("expected a failed run to exit non-zero")
	}
	if !strings.Contains(err.Error(), "the host refused every connection") {
		t.Errorf("error %q should carry what the server said", err)
	}
}

func TestWatchFollowFnFallsBackToTheCommandLineID(t *testing.T) {
	ctx, calls := apiCtx(t, map[string]any{
		api("/runs/checkout-aaa"): map[string]any{"status": runStatusStopped},
	})
	ctx.Id = "checkout-aaa"

	if err := watchFollowFn(ctx, map[string]any{"acknowledged": true}); err != nil {
		t.Fatalf("a stopped run should exit clean, got %v", err)
	}
	if _, ok := findCall(calls, "GET", api("/runs/checkout-aaa")); !ok {
		t.Error("the watch never read the run named on the command line")
	}
}

func TestWatchFollowFnSurfacesAReadFailure(t *testing.T) {
	ctx, _ := apiCtx(t, nil) // every route 404s
	err := watchFollowFn(ctx, map[string]any{"identity": "checkout-aaa"})
	if err == nil {
		t.Fatal("expected an unreadable run to end the watch")
	}
	if strings.Contains(err.Error(), "unaffected") {
		t.Errorf("error %q reads as giving up, but the watch was never interrupted", err)
	}
}

func TestWatchFollowFnReportsATimeoutDuringARead(t *testing.T) {
	ctx, _ := apiCtx(t, nil)
	inner, cancel := context.WithCancelCause(ctx.Context)
	cancel(&cmdctx.TimeoutError{Secs: 5})
	ctx.Context = inner

	err := watchFollowFn(ctx, map[string]any{"identity": "checkout-aaa"})
	if err == nil {
		t.Fatal("expected the timeout to end the watch")
	}
	for _, want := range []string{"timed out after 5s", "checkout-aaa", "unaffected", "--follow"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("message %q should mention %q", err, want)
		}
	}
	if strings.Contains(err.Error(), "API request failed") {
		t.Errorf("message %q is the raw transport error, not the interrupted-watch one", err)
	}
}
