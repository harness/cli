// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package exprfuncs

import (
	"strconv"
	"testing"
	"time"
)

// Every expectation is built from time.Local, so these pass in any zone rather than
// pinning TZ — the point of the local-time behaviour is that it follows the user.
func parsedMs(t *testing.T, in string) int64 {
	t.Helper()
	out := ParseDateMs(in)
	if out == "" {
		t.Fatalf("ParseDateMs(%q) = \"\", want millis", in)
	}
	ms, err := strconv.ParseInt(out, 10, 64)
	if err != nil {
		t.Fatalf("ParseDateMs(%q) = %q, not millis: %v", in, out, err)
	}
	return ms
}

func TestParseDateMsBareDateIsLocalMidnight(t *testing.T) {
	got := parsedMs(t, "2026-06-01")
	want := time.Date(2026, 6, 1, 0, 0, 0, 0, time.Local).UnixMilli()
	if got != want {
		t.Errorf("2026-06-01 = %s, want local midnight %s",
			time.UnixMilli(got), time.UnixMilli(want))
	}
}

// The regression that motivated local parsing: in a negative-offset zone, UTC parsing
// moved a bare date onto the previous day.
func TestParseDateMsBareDateKeepsItsCalendarDay(t *testing.T) {
	got := time.UnixMilli(parsedMs(t, "2026-01-01"))
	if got.Year() != 2026 || got.Month() != time.January || got.Day() != 1 {
		t.Errorf("2026-01-01 landed on %s, want the same calendar day locally", got)
	}
}

func TestParseDateMsDateTimeIsLocal(t *testing.T) {
	got := parsedMs(t, "2026-06-01T09:30:00")
	want := time.Date(2026, 6, 1, 9, 30, 0, 0, time.Local).UnixMilli()
	if got != want {
		t.Errorf("2026-06-01T09:30:00 = %s, want local 9:30 %s",
			time.UnixMilli(got), time.UnixMilli(want))
	}
}

// Millis are already absolute, so they must survive untouched by any zone shift.
func TestParseDateMsPassesThroughMillis(t *testing.T) {
	want := time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC).UnixMilli()
	in := strconv.FormatInt(want, 10)
	if got := ParseDateMs(in); got != in {
		t.Errorf("ParseDateMs(%q) = %q, want it unchanged", in, got)
	}
}

func TestParseDateMsRelativeSpans(t *testing.T) {
	for _, tc := range []struct {
		in   string
		back time.Duration
	}{
		{"12h", 12 * time.Hour},
		{"30d", 30 * 24 * time.Hour},
		{"2w", 2 * 7 * 24 * time.Hour},
		{"1m", 30 * 24 * time.Hour},
	} {
		before := time.Now().Add(-tc.back)
		got := time.UnixMilli(parsedMs(t, tc.in))
		after := time.Now().Add(-tc.back)
		if got.Before(before.Add(-time.Minute)) || got.After(after.Add(time.Minute)) {
			t.Errorf("%s = %s, want roughly %s ago", tc.in, got, tc.back)
		}
	}
}

func TestParseDateMsRejectsUnparseable(t *testing.T) {
	// "2026-06-01Z" and RFC3339 are deliberately unsupported for now; see the
	// extended-syntax follow-up.
	for _, in := range []any{
		"", "nonsense", "30 days ago", "2026-13-01", "2026-06-01Z",
		"2026-06-01T00:00:00Z", "0d", "d", 42, nil,
	} {
		if got := ParseDateMs(in); got != "" {
			t.Errorf("ParseDateMs(%#v) = %q, want \"\"", in, got)
		}
	}
}
