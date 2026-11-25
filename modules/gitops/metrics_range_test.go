// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package gitops

import (
	"strings"
	"testing"

	"github.com/harness/cli/v3/pkg/cmdctx"
)

func TestMetricsRangeParams(t *testing.T) {
	cases := []struct {
		name, start, end, wantErr string
	}{
		{"valid", "2026-11-18T00:00:00Z", "2026-11-18T06:00:00Z", ""},
		{"exactly 14 days", "2026-11-01T00:00:00Z", "2026-11-15T00:00:00Z", ""},
		{"start after end", "2026-11-18T06:00:00Z", "2026-11-18T00:00:00Z", "after"},
		{"over 14 days", "2026-11-01T00:00:00Z", "2026-11-15T00:00:01Z", "14 days"},
		{"bad format", "yesterday", "2026-11-18T00:00:00Z", "RFC3339"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ctx := &cmdctx.Ctx{FlagValues: map[string]any{"start-time": c.start, "end-time": c.end}}
			got, err := metricsRangeParams(ctx)
			if c.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), c.wantErr) {
					t.Fatalf("err = %v, want containing %q", err, c.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got["startTime"] != c.start || got["endTime"] != c.end {
				t.Fatalf("got %v", got)
			}
		})
	}
}
