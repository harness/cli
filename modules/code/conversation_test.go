// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package code

import (
	"strings"
	"testing"
)

func TestNeedsBlankLine(t *testing.T) {
	cases := []struct {
		prev, next string
		want       bool
	}{
		{"", "block", false},
		{"", "line", false},
		{"block", "block", true},
		{"block", "line", true},
		{"line", "line", false},
		{"line", "block", true},
	}
	for _, c := range cases {
		if got := needsBlankLine(c.prev, c.next); got != c.want {
			t.Errorf("needsBlankLine(%q, %q) = %v, want %v", c.prev, c.next, got, c.want)
		}
	}
}

func TestThreadGroups(t *testing.T) {
	// Deliberately out of order: order 2's root arrives before order 1's, and
	// order 1's replies arrive out of sub_order sequence.
	activities := []activity{
		{order: 2, subOrder: 0, author: "carol"},
		{order: 1, subOrder: 2, author: "bob-second"},
		{order: 1, subOrder: 0, author: "alice"},
		{order: 1, subOrder: 1, author: "bob-first"},
	}

	groups := threadGroups(activities)
	if len(groups) != 2 {
		t.Fatalf("expected 2 thread groups, got %d", len(groups))
	}
	if groups[0].root.author != "alice" {
		t.Fatalf("expected order-1 group first with alice as root, got %+v", groups[0].root)
	}
	if len(groups[0].replies) != 2 || groups[0].replies[0].author != "bob-first" || groups[0].replies[1].author != "bob-second" {
		t.Fatalf("expected replies sorted by sub_order, got %+v", groups[0].replies)
	}
	if groups[1].root.author != "carol" || len(groups[1].replies) != 0 {
		t.Fatalf("expected order-2 group second with carol as root and no replies, got %+v", groups[1])
	}
}

func TestBuildRenderItems(t *testing.T) {
	comment := activityGroup{root: activity{kind: "comment", author: "alice"}}
	mergeEvent := activityGroup{root: activity{kind: "system", typ: "merge", author: "bob", createdMs: 1000,
		payload: map[string]any{"merge_method": "squash", "merge_sha": "336a01baf91ee"}}}
	deleteEvent := activityGroup{root: activity{kind: "system", typ: "branch-delete", author: "bob", createdMs: 1400,
		payload: map[string]any{"sha": "4c946ae2bf7f9"}}}

	items := buildRenderItems([]activityGroup{comment, mergeEvent, deleteEvent})

	if len(items) != 3 {
		t.Fatalf("expected one item per activity — no consolidation — got %d items: %+v", len(items), items)
	}
	if items[0].kind != "block" {
		t.Fatalf("expected first item to stay a multi-line comment block, got %+v", items[0])
	}
	if items[1].kind != "line" || items[1].sysCategory != "merge" || items[1].sysMuted ||
		items[1].sysAuthor != "bob" || items[1].sysText != "merged via squash → 336a01ba" {
		t.Fatalf("expected a non-muted merge line for bob, got %+v", items[1])
	}
	if items[2].kind != "line" || items[2].sysCategory != "default" || !items[2].sysMuted ||
		items[2].sysText != "deleted branch 4c946ae2" {
		t.Fatalf("expected a separate, muted branch-delete line — same author as the merge, but no longer combined onto it — got %+v", items[2])
	}
}

func TestCommentedHeader_AICodeReviewGetsSparkles(t *testing.T) {
	if got := commentedHeader(activity{author: "AI Code Review", createdMs: 1000}, nil); !strings.Contains(got, "✨") {
		t.Errorf("expected the AI Code Review bot's header to use the sparkles icon, got %q", got)
	}
	if got := commentedHeader(activity{author: "Zhenyu Zhang", createdMs: 1000}, nil); strings.Contains(got, "✨") {
		t.Errorf("expected a regular commenter's header to keep the plain bullet, got %q", got)
	}
}

func TestSystemEventCategoryAndGlyph(t *testing.T) {
	cases := []struct {
		name string
		a    activity
		want string
	}{
		{"approved", activity{typ: "review-submit", payload: map[string]any{"decision": "approved"}}, "approve"},
		{"changereq", activity{typ: "review-submit", payload: map[string]any{"decision": "changereq"}}, "changereq"},
		{"branch-update", activity{typ: "branch-update"}, "commit"},
		{"title-change", activity{typ: "title-change"}, "title-change"},
		{"merge", activity{typ: "merge"}, "merge"},
		{"branch-delete", activity{typ: "branch-delete"}, "default"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := systemEventCategory(c.a); got != c.want {
				t.Errorf("systemEventCategory() = %q, want %q", got, c.want)
			}
		})
	}

	if glyph, muted := systemEventGlyph("approve"); glyph == "" || muted {
		t.Errorf("systemEventGlyph(approve) = (%q, %v), want a non-empty glyph and muted=false", glyph, muted)
	}
	if glyph, muted := systemEventGlyph("changereq"); glyph == "" || muted {
		t.Errorf("systemEventGlyph(changereq) = (%q, %v), want a non-empty glyph and muted=false", glyph, muted)
	}
	if glyph, muted := systemEventGlyph("commit"); glyph == "" || muted {
		t.Errorf("systemEventGlyph(commit) = (%q, %v), want a non-empty glyph and muted=false", glyph, muted)
	}
	if glyph, muted := systemEventGlyph("merge"); glyph == "" || muted {
		t.Errorf("systemEventGlyph(merge) = (%q, %v), want a non-empty glyph and muted=false", glyph, muted)
	}
	defaultGlyph, defaultMuted := systemEventGlyph("default")
	if defaultGlyph == "" || !defaultMuted {
		t.Errorf("systemEventGlyph(default) = (%q, %v), want a non-empty glyph and muted=true", defaultGlyph, defaultMuted)
	}
	commitGlyph, _ := systemEventGlyph("commit")
	approveGlyph, _ := systemEventGlyph("approve")
	mergeGlyph, _ := systemEventGlyph("merge")
	if commitGlyph == defaultGlyph || commitGlyph == approveGlyph || mergeGlyph == defaultGlyph || mergeGlyph == approveGlyph || mergeGlyph == commitGlyph {
		t.Errorf("expected commit %q and merge %q glyphs to each be distinct from default %q and approve %q", commitGlyph, mergeGlyph, defaultGlyph, approveGlyph)
	}
}

func TestSystemEventText(t *testing.T) {
	cases := []struct {
		name string
		a    activity
		want string
	}{
		{
			name: "reviewer-add plural with code owners",
			a: activity{typ: "reviewer-add",
				payload:  map[string]any{"principal_ids": []any{int64(10), int64(20)}, "code_owners": true},
				mentions: map[string]any{"10": map[string]any{"display_name": "carol"}, "20": map[string]any{"display_name": "dave"}}},
			want: "requested review from carol, dave as code owners",
		},
		{
			name: "reviewer-add singular",
			a: activity{typ: "reviewer-add",
				payload:  map[string]any{"principal_id": int64(30)},
				mentions: map[string]any{"30": map[string]any{"display_name": "erin"}}},
			want: "requested review from erin",
		},
		{
			name: "reviewer-add plural over the +N more threshold",
			a: activity{typ: "reviewer-add",
				payload: map[string]any{"principal_ids": []any{int64(1), int64(2), int64(3), int64(4)}},
				mentions: map[string]any{
					"1": map[string]any{"display_name": "a"}, "2": map[string]any{"display_name": "b"},
					"3": map[string]any{"display_name": "c"}, "4": map[string]any{"display_name": "d"},
				}},
			want: "requested review from a, b, c +1 more",
		},
		{name: "review-submit approved", a: activity{typ: "review-submit", payload: map[string]any{"decision": "approved"}}, want: "approved these changes"},
		{name: "review-submit changereq", a: activity{typ: "review-submit", payload: map[string]any{"decision": "changereq"}}, want: "requested changes"},
		{name: "state-change closed", a: activity{typ: "state-change", payload: map[string]any{"new": "closed"}}, want: "closed"},
		{name: "state-change reopened", a: activity{typ: "state-change", payload: map[string]any{"new": "open"}}, want: "reopened"},
		{
			name: "state-change draft flip to draft",
			a:    activity{typ: "state-change", payload: map[string]any{"old_draft": false, "new_draft": true}},
			want: "converted to draft",
		},
		{
			name: "state-change draft flip to ready",
			a:    activity{typ: "state-change", payload: map[string]any{"old_draft": true, "new_draft": false}},
			want: "marked ready for review",
		},
		{
			name: "branch-update plain",
			a:    activity{typ: "branch-update", payload: map[string]any{"old": "8bd89d6aaaaaa", "new": "255f7f1bbbbbb"}},
			want: "pushed a new commit",
		},
		{
			name: "branch-update plain with commit title",
			a:    activity{typ: "branch-update", payload: map[string]any{"old": "8bd89d6aaaaaa", "new": "255f7f1bbbbbb", "commit_title": "fix: thing"}},
			want: `pushed a new commit · "fix: thing"`,
		},
		{
			name: "branch-update forced with commit title",
			a: activity{typ: "branch-update",
				payload: map[string]any{"old": "8bd89d6aaaaaa", "new": "255f7f1bbbbbb", "forced": true, "commit_title": "fix: thing"}},
			want: `force-pushed 8bd89d6a → 255f7f1b · "fix: thing"`,
		},
		{name: "branch-delete", a: activity{typ: "branch-delete", payload: map[string]any{"sha": "4c946ae2bf7f9"}}, want: "deleted branch 4c946ae2"},
		{
			name: "merge",
			a:    activity{typ: "merge", payload: map[string]any{"merge_method": "squash", "merge_sha": "336a01baf91ee"}},
			want: "merged via squash → 336a01ba",
		},
		{name: "label-modify added", a: activity{typ: "label-modify", payload: map[string]any{"type": "added", "label": "bug"}}, want: "added label bug"},
		{name: "label-modify removed", a: activity{typ: "label-modify", payload: map[string]any{"type": "removed", "label": "bug"}}, want: "removed label bug"},
		{
			name: "title-change",
			a:    activity{typ: "title-change", payload: map[string]any{"old": "feat: old title", "new": "feat: new title"}},
			want: `renamed to "feat: new title"`,
		},
		{name: "unknown type with text", a: activity{typ: "target-branch-change", text: "develop → main"}, want: "target branch change develop → main"},
		{name: "unknown type without text", a: activity{typ: "merge-queue-added"}, want: "merge queue added"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := systemEventText(c.a); got != c.want {
				t.Errorf("systemEventText() = %q, want %q", got, c.want)
			}
		})
	}
}

func TestRenderMarkdownLines(t *testing.T) {
	cases := []struct {
		name string
		text string
		want []string
	}{
		{name: "bold and inline code", text: "**Configured** account IDs use `resolveAccountIds()`.",
			want: []string{"Configured account IDs use `resolveAccountIds()`."}},
		{name: "bullet list", text: "- item one\n- item two", want: []string{"• item one", "• item two"}},
		{name: "fenced code block not touched by inline rendering", text: "```\n**not bold**\n```",
			want: []string{"**not bold**"}},
		{name: "raw url left on one line, unwrapped", text: "see https://example.com/a/very/long/path?x=1&y=2",
			want: []string{"see https://example.com/a/very/long/path?x=1&y=2"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := renderMarkdownLines(c.text)
			if len(got) != len(c.want) {
				t.Fatalf("renderMarkdownLines(%q) = %v, want %v", c.text, got, c.want)
			}
			for i := range got {
				if got[i] != c.want[i] {
					t.Errorf("renderMarkdownLines(%q)[%d] = %q, want %q", c.text, i, got[i], c.want[i])
				}
			}
		})
	}
}
