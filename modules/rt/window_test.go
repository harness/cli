// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package rt

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/harness/cli/v3/pkg/cmdctx"
)

// Returns both bounds parsed, since every assertion here is about the instants.
func windowFor(t *testing.T, flags map[string]any) (time.Time, time.Time) {
	t.Helper()
	qp, err := usageWindowParams(&cmdctx.Ctx{FlagValues: flags})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	start, err := strconv.ParseInt(qp["startTime"], 10, 64)
	if err != nil {
		t.Fatalf("startTime %q is not millis: %v", qp["startTime"], err)
	}
	end, err := strconv.ParseInt(qp["endTime"], 10, 64)
	if err != nil {
		t.Fatalf("endTime %q is not millis: %v", qp["endTime"], err)
	}
	return time.UnixMilli(start), time.UnixMilli(end)
}

func TestUsageWindowSendsBothBounds(t *testing.T) {
	qp, err := usageWindowParams(&cmdctx.Ctx{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, key := range []string{"startTime", "endTime"} {
		if qp[key] == "" {
			t.Errorf("%s is missing; the route rejects a half-open window", key)
		}
	}
	if len(qp) != 2 {
		t.Errorf("got %v, want only the two bounds", qp)
	}
}

func TestUsageWindowDefaultsToThirtyDays(t *testing.T) {
	before := time.Now()
	start, end := windowFor(t, nil)
	after := time.Now()

	if end.Before(before.Add(-time.Second)) || end.After(after.Add(time.Second)) {
		t.Errorf("end = %s, want roughly now", end)
	}
	if span := end.Sub(start); span < defaultUsageWindow-time.Minute || span > defaultUsageWindow+time.Minute {
		t.Errorf("window spans %s, want %s", span, defaultUsageWindow)
	}
}

func TestUsageWindowFromOnlyEndsNow(t *testing.T) {
	before := time.Now()
	start, end := windowFor(t, map[string]any{"from": "7d"})
	after := time.Now()

	if span := end.Sub(start); span < 7*24*time.Hour-time.Minute || span > 7*24*time.Hour+time.Minute {
		t.Errorf("window spans %s, want 7 days", span)
	}
	if end.Before(before.Add(-time.Second)) || end.After(after.Add(time.Second)) {
		t.Errorf("end = %s, want roughly now", end)
	}
}

func TestUsageWindowToOnlyEndsAtTheGivenDate(t *testing.T) {
	start, end := windowFor(t, map[string]any{"to": "2026-06-01"})

	// A bare date is midnight where the user is, so --to 2026-06-01 means what it reads as.
	wantEnd := time.Date(2026, 6, 1, 0, 0, 0, 0, time.Local)
	if !end.Equal(wantEnd) {
		t.Errorf("end = %s, want the given %s", end.UTC(), wantEnd)
	}
	if span := end.Sub(start); span != defaultUsageWindow {
		t.Errorf("window spans %s, want the default %s ending at --to", span, defaultUsageWindow)
	}
	if start.After(end) {
		t.Errorf("start %s is after end %s", start, end)
	}
}

func TestUsageWindowExplicitDates(t *testing.T) {
	start, end := windowFor(t, map[string]any{"from": "2026-01-01", "to": "2026-02-01"})
	if start.Year() != 2026 || start.Month() != time.January {
		t.Errorf("start = %s, want 2026-01-01", start)
	}
	if end.Month() != time.February {
		t.Errorf("end = %s, want 2026-02-01", end)
	}
}

func TestUsageWindowAcceptsUnixMillis(t *testing.T) {
	want := time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)
	start, _ := windowFor(t, map[string]any{"from": strconv.FormatInt(want.UnixMilli(), 10)})
	if !start.Equal(want) {
		t.Errorf("start = %s, want %s", start, want)
	}
}

func TestUsageWindowRejectsInverted(t *testing.T) {
	_, err := usageWindowParams(&cmdctx.Ctx{FlagValues: map[string]any{
		"from": "2026-06-01", "to": "2026-01-01",
	}})
	if err == nil {
		t.Fatal("expected an inverted window to be refused")
	}
	// The message has to name both ends, since the flags are easy to mix up.
	for _, want := range []string{"--to", "--from", "2026-06-01", "2026-01-01"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q should mention %q", err, want)
		}
	}
}

func TestUsageWindowAllowsAnInstant(t *testing.T) {
	start, end := windowFor(t, map[string]any{"from": "2026-01-01", "to": "2026-01-01"})
	if !start.Equal(end) {
		t.Errorf("start %s and end %s should be the same instant", start, end)
	}
}

func TestUsageWindowRejectsUnparseableBounds(t *testing.T) {
	for _, tc := range []struct{ flag, value string }{
		{"from", "nonsense"},
		{"to", "nonsense"},
		{"from", "30 days ago"},
	} {
		_, err := usageWindowParams(&cmdctx.Ctx{FlagValues: map[string]any{tc.flag: tc.value}})
		if err == nil {
			t.Errorf("--%s %q: expected an error", tc.flag, tc.value)
			continue
		}
		// The flag description is the only other place these formats appear.
		if !strings.Contains(err.Error(), "--"+tc.flag) || !strings.Contains(err.Error(), "30d") {
			t.Errorf("--%s %q: error %q should name the flag and the accepted formats", tc.flag, tc.value, err)
		}
	}
}
