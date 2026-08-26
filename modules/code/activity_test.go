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
// activityTextFormatter / renderActivityTimeline
// ---------------------------------------------------------------------------

func TestActivityTextFormatter_EmptyList(t *testing.T) {
	out := captureStdout(t, func() {
		if err := activityTextFormatter(os.Stdout, nil); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
	if !strings.Contains(out, "No activity.") {
		t.Fatalf("expected empty-state message, got:\n%s", out)
	}
}

func TestRenderActivityTimeline_CommentOnly(t *testing.T) {
	now := time.UnixMilli(1_700_000_000_000)
	activities := []any{
		map[string]any{
			"kind":    "comment",
			"author":  map[string]any{"display_name": "Alice"},
			"text":    "Looks good overall.",
			"order":   float64(1),
			"created": float64(now.Add(-2 * time.Hour).UnixMilli()),
		},
	}
	out := captureStdout(t, func() {
		if err := renderActivityTimeline(os.Stdout, activities, now); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
	if !strings.Contains(out, "Alice") {
		t.Fatalf("expected author name in output, got:\n%s", out)
	}
	if !strings.Contains(out, "2h ago") {
		t.Fatalf("expected relative timestamp \"2h ago\", got:\n%s", out)
	}
	if !strings.Contains(out, "Looks good overall.") {
		t.Fatalf("expected comment text, got:\n%s", out)
	}
}

func TestRenderActivityTimeline_MixedCommentAndSystem(t *testing.T) {
	now := time.UnixMilli(1_700_000_000_000)
	activities := []any{
		map[string]any{
			"kind":    "system",
			"type":    "merge",
			"author":  map[string]any{"display_name": "Bob"},
			"order":   float64(2),
			"created": float64(now.UnixMilli()),
		},
		map[string]any{
			"kind":    "comment",
			"author":  map[string]any{"display_name": "Alice"},
			"text":    "Can you tweak this?",
			"order":   float64(1),
			"created": float64(now.UnixMilli()),
		},
	}
	out := captureStdout(t, func() {
		if err := renderActivityTimeline(os.Stdout, activities, now); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
	commentIdx := strings.Index(out, "Can you tweak this?")
	mergeIdx := strings.Index(out, "merged this pull request")
	if commentIdx == -1 || mergeIdx == -1 {
		t.Fatalf("expected both comment and system event in output, got:\n%s", out)
	}
	if commentIdx > mergeIdx {
		t.Fatalf("expected comment (order=1) before system event (order=2), got:\n%s", out)
	}
	if !strings.Contains(out, "●") {
		t.Fatalf("expected bullet marker on system event line, got:\n%s", out)
	}
	if !strings.Contains(out, "Bob merged this pull request") {
		t.Fatalf("expected hand-rolled verb for merge type, got:\n%s", out)
	}
}

func TestRenderActivityTimeline_SystemEventUsesTextWhenPresent(t *testing.T) {
	now := time.UnixMilli(1_700_000_000_000)
	activities := []any{
		map[string]any{
			"kind":    "system",
			"type":    "title-change",
			"author":  map[string]any{"display_name": "Carol"},
			"text":    "Carol changed the title from \"foo\" to \"bar\"",
			"order":   float64(1),
			"created": float64(now.UnixMilli()),
		},
	}
	out := captureStdout(t, func() {
		if err := renderActivityTimeline(os.Stdout, activities, now); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
	if !strings.Contains(out, `Carol changed the title from "foo" to "bar"`) {
		t.Fatalf("expected raw text to be used verbatim, got:\n%s", out)
	}
	if strings.Contains(out, "changed the title") == false {
		t.Fatalf("sanity: expected substring not found, got:\n%s", out)
	}
}

func TestRenderActivityTimeline_ThreadedRepliesIndented(t *testing.T) {
	now := time.UnixMilli(1_700_000_000_000)
	activities := []any{
		map[string]any{
			"kind":      "comment",
			"author":    map[string]any{"display_name": "Alice"},
			"text":      "Top-level comment",
			"order":     float64(1),
			"parent_id": float64(0),
			"created":   float64(now.UnixMilli()),
		},
		map[string]any{
			"kind":      "comment",
			"author":    map[string]any{"display_name": "Bob"},
			"text":      "A reply",
			"order":     float64(1),
			"sub_order": float64(1),
			"parent_id": float64(1),
			"created":   float64(now.UnixMilli()),
		},
	}
	out := captureStdout(t, func() {
		if err := renderActivityTimeline(os.Stdout, activities, now); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
	lines := strings.Split(out, "\n")
	var replyLine string
	for _, l := range lines {
		if strings.Contains(l, "A reply") {
			replyLine = l
			break
		}
	}
	if replyLine == "" {
		t.Fatalf("expected reply text in output, got:\n%s", out)
	}
	if !strings.HasPrefix(replyLine, "    ") {
		t.Fatalf("expected reply to be indented under its parent, got line: %q", replyLine)
	}
}

func TestRenderActivityTimeline_SortsByOrderThenSubOrder(t *testing.T) {
	now := time.UnixMilli(1_700_000_000_000)
	activities := []any{
		map[string]any{
			"kind": "comment", "author": map[string]any{"display_name": "X"},
			"text": "second", "order": float64(1), "sub_order": float64(2),
			"created": float64(now.UnixMilli()),
		},
		map[string]any{
			"kind": "comment", "author": map[string]any{"display_name": "X"},
			"text": "first", "order": float64(1), "sub_order": float64(1),
			"created": float64(now.UnixMilli()),
		},
	}
	out := captureStdout(t, func() {
		if err := renderActivityTimeline(os.Stdout, activities, now); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
	firstIdx := strings.Index(out, "first")
	secondIdx := strings.Index(out, "second")
	if firstIdx == -1 || secondIdx == -1 || firstIdx > secondIdx {
		t.Fatalf("expected \"first\" (sub_order=1) before \"second\" (sub_order=2), got:\n%s", out)
	}
}

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
// systemEventColor
// ---------------------------------------------------------------------------

func TestSystemEventColor(t *testing.T) {
	cases := []struct {
		name         string
		activityType string
		decision     string
		wantColor    bool
	}{
		{"approved decision", "review-submit", "approved", true},
		{"changereq decision", "review-submit", "changereq", true},
		{"merge type", "merge", "", true},
		{"branch-delete type", "branch-delete", "", true},
		{"unknown type no decision", "reviewer-add", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := systemEventColor(tc.activityType, tc.decision)
			if (got != 0) != tc.wantColor {
				t.Fatalf("systemEventColor(%q, %q) = %v, want color set: %v", tc.activityType, tc.decision, got, tc.wantColor)
			}
		})
	}
}
