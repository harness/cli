// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package code

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/harness/cli/v3/pkg/console"
)

// activityItem is a normalized view of one raw activity map, as returned by
// /code/api/v1/repos/{repo}/pullreq/{pr}/activities.
type activityItem struct {
	Kind       string
	Type       string
	AuthorName string
	Text       string
	Decision   string
	Order      int64
	SubOrder   int64
	ParentID   int64
	Created    int64
}

func toActivityItem(raw any) (activityItem, bool) {
	m, ok := raw.(map[string]any)
	if !ok {
		return activityItem{}, false
	}
	item := activityItem{
		Kind:     asString(m["kind"]),
		Type:     asString(m["type"]),
		Text:     strings.TrimSpace(asString(m["text"])),
		Order:    asInt64(m["order"]),
		SubOrder: asInt64(m["sub_order"]),
		ParentID: asInt64(m["parent_id"]),
		Created:  asInt64(m["created"]),
	}
	if author, ok := m["author"].(map[string]any); ok {
		item.AuthorName = asString(author["display_name"])
	}
	if payload, ok := m["payload"].(map[string]any); ok {
		item.Decision = asString(payload["decision"])
	}
	if item.Decision == "" {
		item.Decision = asString(m["decision"])
	}
	return item, true
}

// isComment reports whether the activity is part of the comment/reply thread,
// as opposed to a system/review timeline event.
func isComment(kind string) bool {
	return kind == "comment"
}

// renderCommentsSummary prints a gh-pr-view-style collapsed comments section:
// a "Not showing N comments" divider, the newest comment's author/time/text, and
// a hint pointing at hintCmd to see the full conversation. Prints nothing when
// activities contains no comments.
func renderCommentsSummary(w io.Writer, activities []any, now time.Time, hintCmd string) {
	var comments []activityItem
	for _, raw := range activities {
		item, ok := toActivityItem(raw)
		if !ok || !isComment(item.Kind) {
			continue
		}
		comments = append(comments, item)
	}
	if len(comments) == 0 {
		return
	}

	sort.SliceStable(comments, func(i, j int) bool {
		if comments[i].Order != comments[j].Order {
			return comments[i].Order < comments[j].Order
		}
		return comments[i].SubOrder < comments[j].SubOrder
	})
	newest := comments[len(comments)-1]

	printDivider(w, "Comments")
	fmt.Fprintln(w)

	fmt.Fprintf(w, "%s • %s • %s\n\n", console.WithBold(newest.AuthorName), relativeTimeSince(time.UnixMilli(newest.Created), now), console.WithColor(console.ColorBrightBlue, "Newest comment"))
	text := newest.Text
	if text == "" {
		text = "(no comment text)"
	}
	for _, line := range strings.Split(text, "\n") {
		fmt.Fprintf(w, "  %s\n", line)
	}
	fmt.Fprintln(w)

	fmt.Fprintf(w, "Use `%s` to view the full conversation\n", hintCmd)
}

// relativeTimeSince formats then relative to now, e.g. "3h ago". Timestamps
// older than a week fall back to an absolute date.
func relativeTimeSince(then, now time.Time) string {
	if then.UnixMilli() == 0 {
		return ""
	}
	d := now.Sub(then)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	case d < 7*24*time.Hour:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	default:
		return then.Format("Jan 2, 2006")
	}
}
