// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package code

import (
	"os"
	"strings"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// relativeTimeSince
// ---------------------------------------------------------------------------

func TestRelativeTimeSince(t *testing.T) {
	now := time.UnixMilli(1_700_000_000_000)
	cases := []struct {
		name string
		then time.Time
		want string
	}{
		{"zero timestamp", time.UnixMilli(0), ""},
		{"just now", now.Add(-10 * time.Second), "just now"},
		{"minutes ago", now.Add(-5 * time.Minute), "5m ago"},
		{"hours ago", now.Add(-3 * time.Hour), "3h ago"},
		{"days ago", now.Add(-2 * 24 * time.Hour), "2d ago"},
		{"weeks ago falls back to date", now.Add(-14 * 24 * time.Hour), now.Add(-14 * 24 * time.Hour).Format("Jan 2, 2006")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := relativeTimeSince(tc.then, now)
			if got != tc.want {
				t.Fatalf("relativeTimeSince(%v, %v) = %q, want %q", tc.then, now, got, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// renderCommentsSummary
// ---------------------------------------------------------------------------

func TestRenderCommentsSummary_NoComments(t *testing.T) {
	now := time.UnixMilli(1_700_000_000_000)
	activities := []any{
		map[string]any{"kind": "system", "type": "merge", "order": float64(1)},
	}
	out := captureStdout(t, func() {
		renderCommentsSummary(os.Stdout, activities, now, "harness list pr_comment repo1/42")
	})
	if out != "" {
		t.Fatalf("expected no output when there are no comments, got:\n%s", out)
	}
}

func TestRenderCommentsSummary_ShowsNewestCommentOnly(t *testing.T) {
	now := time.UnixMilli(1_700_000_000_000)
	activities := []any{
		map[string]any{
			"kind": "comment", "author": map[string]any{"display_name": "Alice"},
			"text": "first comment", "order": float64(1),
			"created": float64(now.Add(-2 * time.Hour).UnixMilli()),
		},
		map[string]any{
			"kind": "comment", "author": map[string]any{"display_name": "Bob"},
			"text": "newest comment", "order": float64(2),
			"created": float64(now.UnixMilli()),
		},
		map[string]any{
			"kind": "change-comment", "author": map[string]any{"display_name": "Charlie"},
			"text": "change comment", "order": float64(3),
			"created": float64(now.Add(-3 * time.Hour).UnixMilli()),
		},
	}
	out := captureStdout(t, func() {
		renderCommentsSummary(os.Stdout, activities, now, "harness list pr_comment repo1/42")
	})
	if !strings.Contains(out, "Not showing 1 comment") {
		t.Fatalf("expected collapsed comments summary with count 1 comment, got:\n%s", out)
	}
	if !strings.Contains(out, "Bob") || !strings.Contains(out, "newest comment") {
		t.Fatalf("expected the newest comment (Bob's) to be shown, got:\n%s", out)
	}
	if strings.Contains(out, "first comment") {
		t.Fatalf("expected only the newest comment to render, not the older one, got:\n%s", out)
	}
	if !strings.Contains(out, "harness list pr_comment repo1/42") {
		t.Fatalf("expected hint pointing at \"list pr_comment\" with the PR id, got:\n%s", out)
	}
}
