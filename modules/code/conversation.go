// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package code

import (
	"fmt"
	"io"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/harness/cli/v3/pkg/cmdctx"
	"github.com/harness/cli/v3/pkg/console"
)

// prConversationTextFormatter renders a pull request's activity timeline the way
// a person reads it: comment threads as prose, code comments with their diff
// hunk, and system events (reviewer added, branch updated, merged, ...) as dim
// one-liners. The endpoint hands the formatter the whole activities array via
// item_expr: it (see docs/code-pr-conversation.md), so d.GetSlice("it") is the array.
//
// Deliberately no wrapping and no terminal-width layout: comment bodies and system
// events often carry raw URLs (pipeline execution links, PR links), and wrapping
// those to a column width breaks them across lines and makes them unclickable in a
// terminal. Lines are printed as authored; the terminal soft-wraps if it must.
func prConversationTextFormatter(w io.Writer, d cmdctx.DataAccessor) error {
	opts := renderOptions{
		showResolved: d.GetBool(`flags["show-resolved"]`),
		showOutdated: d.GetBool(`flags["show-outdated"]`),
		showDiffs:    !d.GetBool(`flags["no-diffs"]`),
	}
	if err := renderConversation(w, d.GetSlice("it"), opts); err != nil {
		return err
	}
	if u := d.GetString("url(it)"); u != "" {
		fmt.Fprintf(w, "\n%s\n", u)
	}
	return nil
}

// activity is a parsed pull request activity entry (comment, code comment, or
// system event). Fields are read straight off the raw JSON map: the API's
// `payload` shape varies per `type`, so per-type renderers dig into a.payload
// themselves rather than a shared typed struct.
type activity struct {
	kind        string
	typ         string
	order       int64
	subOrder    int64
	author      string
	text        string
	createdMs   int64
	editedMs    int64
	resolved    bool
	payload     map[string]any
	codeComment map[string]any
	mentions    map[string]any
}

func parseActivity(raw any) (activity, bool) {
	m, ok := raw.(map[string]any)
	if !ok {
		return activity{}, false
	}
	a := activity{
		kind:        asString(m["kind"]),
		typ:         asString(m["type"]),
		order:       asInt64(m["order"]),
		subOrder:    asInt64(m["sub_order"]),
		text:        asString(m["text"]),
		createdMs:   asInt64(m["created"]),
		editedMs:    asInt64(m["edited"]),
		payload:     asMap(m["payload"]),
		codeComment: asMap(m["code_comment"]),
		mentions:    asMap(m["mentions"]),
	}
	if author := asMap(m["author"]); author != nil {
		a.author = asString(author["display_name"])
	}
	if r, ok := m["resolved"]; ok && r != nil {
		a.resolved = asInt64(r) != 0
	}
	return a, true
}

// activityGroup is one thread: a root activity plus its replies, sorted by
// sub_order. Every reply's parent_id points at the thread root, so grouping by
// order and sorting by sub_order is sufficient — no tree walk needed.
type activityGroup struct {
	root    activity
	replies []activity
}

// threadGroups partitions activities into threads by `order`, ascending, per the
// flat threading model in docs/code-pr-activity-analysis.md.
func threadGroups(activities []activity) []activityGroup {
	byOrder := map[int64][]activity{}
	var orders []int64
	for _, a := range activities {
		if _, exists := byOrder[a.order]; !exists {
			orders = append(orders, a.order)
		}
		byOrder[a.order] = append(byOrder[a.order], a)
	}
	sort.Slice(orders, func(i, j int) bool { return orders[i] < orders[j] })

	groups := make([]activityGroup, 0, len(orders))
	for _, ord := range orders {
		g := byOrder[ord]
		sort.Slice(g, func(i, j int) bool { return g[i].subOrder < g[j].subOrder })
		groups = append(groups, activityGroup{root: g[0], replies: g[1:]})
	}
	return groups
}

// renderItem is one render unit: a multi-line "block" (a comment or code-comment
// thread, which can run to many lines of body/diff/replies) or a single-line
// "line" (a system event). This — not color — is what decides blank-line
// placement in needsBlankLine: blocks get padding around them since they're long
// and vary in length; lines stack tightly regardless of what color or icon a
// given system event renders with.
type renderItem struct {
	kind        string // "block" or "line"
	group       activityGroup
	sysCategory string
	sysGlyph    string
	sysMuted    bool
	sysAuthor   string
	sysText     string
	sysCreated  int64
}

// systemEventCategory buckets a system activity for icon selection and
// same-category consolidation (see consolidateSystemLines). A review decision
// or a pushed commit is substantive signal, not bookkeeping, so each gets its
// own category; everything else (reviewer-add, branch-delete, merge,
// label-modify, ...) shares the generic "default" bucket.
func systemEventCategory(a activity) string {
	switch {
	case a.typ == "review-submit" && asString(a.payload["decision"]) == "approved":
		return "approve"
	case a.typ == "review-submit" && asString(a.payload["decision"]) == "changereq":
		return "changereq"
	case a.typ == "branch-update":
		return "commit"
	case a.typ == "title-change":
		return "title-change"
	case a.typ == "merge":
		return "merge"
	default:
		return "default"
	}
}

// systemEventGlyph returns the pre-colored left-hand icon for a system activity's
// category, and whether the rest of the line should render dim. Approvals,
// change requests, and pushed commits get a distinctly colored, non-dim line;
// everything else stays a plain dim bullet.
func systemEventGlyph(category string) (glyph string, muted bool) {
	switch category {
	case "approve":
		return console.GreenCheck(), false
	case "changereq":
		return console.RedX(), false
	case "commit":
		return console.WithColor(console.ColorMagenta, "◆"), false
	case "title-change":
		return "→", false
	case "merge":
		return console.WithColor(console.ColorGreen, "✦"), false
	default:
		return console.WithColor(console.ColorBrightBlack, "▸"), true
	}
}

// buildRenderItems turns each thread group into one render item: a "block"
// comment or code-comment thread, or a "line" system-event line. Every system
// event gets its own line — an earlier version consolidated consecutive
// same-author events (e.g. `merge` immediately followed by `branch-delete`)
// onto one comma-joined line, but that reads as confusing rather than tidy,
// especially once each event category has its own icon and color. A code
// thread that collapses to its one-line tagged header (see
// shouldCollapseCodeThread) is a "line" too, for the same reason: once it's
// not printing a body, it shouldn't get a block's blank-line padding either.
func buildRenderItems(groups []activityGroup, opts renderOptions) []renderItem {
	items := make([]renderItem, 0, len(groups))
	for _, g := range groups {
		if g.root.kind != "system" {
			kind := "block"
			if isCodeThread(g.root) && shouldCollapseCodeThread(asBool(g.root.codeComment["outdated"]), g.root.resolved, opts) {
				kind = "line"
			}
			items = append(items, renderItem{kind: kind, group: g})
			continue
		}
		category := systemEventCategory(g.root)
		glyph, muted := systemEventGlyph(category)
		items = append(items, renderItem{
			kind: "line", sysCategory: category, sysGlyph: glyph, sysMuted: muted,
			sysAuthor: g.root.author, sysText: systemEventText(g.root), sysCreated: g.root.createdMs,
		})
	}
	return items
}

// needsBlankLine reports whether a blank line separates an item of kind `next`
// from the previous item of kind `prev` (`""` before anything has been written,
// otherwise "block" or "line"). block<->block and block<->line always get a
// blank line, since a comment/code thread can run to many lines and needs
// visual room; line<->line (consecutive single-line system events) never does,
// regardless of what color or icon each one renders with.
func needsBlankLine(prev, next string) bool {
	if prev == "" {
		return false
	}
	return prev != "line" || next != "line"
}

// renderOptions carries every flag-driven rendering choice, so adding one
// doesn't mean threading a new parameter through every render function.
type renderOptions struct {
	showResolved bool // --show-resolved: expand resolved code-comment threads instead of collapsing them
	showOutdated bool // --show-outdated: expand outdated code-comment threads instead of collapsing them
	showDiffs    bool // !--no-diffs: include the diff hunk on code-comment threads
}

// renderConversation is the pure rendering core behind prConversationTextFormatter.
func renderConversation(w io.Writer, raw []any, opts renderOptions) error {
	activities := make([]activity, 0, len(raw))
	for _, r := range raw {
		if a, ok := parseActivity(r); ok {
			activities = append(activities, a)
		}
	}
	fmt.Fprintln(w)

	if len(activities) == 0 {
		fmt.Fprintln(w, "No activity.")
		return nil
	}

	lastKind := ""
	for _, item := range buildRenderItems(threadGroups(activities), opts) {
		if needsBlankLine(lastKind, item.kind) {
			fmt.Fprintln(w)
		}
		lastKind = item.kind

		if isCodeThread(item.group.root) {
			writeCodeThread(w, item.group.root, item.group.replies, opts)
			continue
		}
		if item.kind == "line" {
			writeSystemLine(w, item.sysGlyph, item.sysMuted, item.sysAuthor, item.sysText, item.sysCreated)
			continue
		}
		writeCommentThread(w, item.group.root, item.group.replies)
	}
	return nil
}

func isCodeThread(root activity) bool {
	return root.codeComment != nil || root.typ == "code-comment"
}

// ---------------------------------------------------------------------------
// system events
// ---------------------------------------------------------------------------

// writeSystemLine prints one system-event line with its category glyph. For a
// muted (default-category) line, everything stays dim, matching plain
// bookkeeping. For a non-muted line (approve, changereq, commit, title-change),
// the author name renders in the same cyan used for comment authors — one
// consistent "who did this" color across the whole timeline — while the rest of
// the line uses the terminal's default color so the event doesn't read as dim.
func writeSystemLine(w io.Writer, glyph string, muted bool, author, text string, createdMs int64) {
	if muted {
		fmt.Fprintln(w, glyph+" "+console.WithColor(console.ColorBrightBlack, author+" "+text+" · "+relativeTime(createdMs)))
		return
	}
	fmt.Fprintln(w, glyph+" "+console.WithColor(console.ColorCyan, author)+" "+text+" · "+relativeTime(createdMs))
}

// systemEventText renders one system activity's payload per the phrasing table in
// docs/code-pr-conversation.md. Unrecognized types (10 of the 19 enum values are
// unconfirmed against real data) fall back to a generic, honestly-vague line rather
// than guessing at a payload shape.
func systemEventText(a activity) string {
	switch a.typ {
	case "reviewer-add":
		return reviewerAddText(a)
	case "review-submit":
		return reviewSubmitText(a)
	case "state-change":
		return stateChangeText(a)
	case "branch-update":
		return branchUpdateText(a)
	case "branch-delete":
		return "deleted branch " + sha8(asString(a.payload["sha"]))
	case "merge":
		return mergeText(a)
	case "label-modify":
		return labelModifyText(a)
	case "title-change":
		return titleChangeText(a)
	default:
		return genericFallback(a)
	}
}

func genericFallback(a activity) string {
	label := strings.ReplaceAll(a.typ, "-", " ")
	if a.text != "" {
		return label + " " + a.text
	}
	return label
}

func sha8(sha string) string {
	if len(sha) > 8 {
		return sha[:8]
	}
	return sha
}

func reviewerAddText(a activity) string {
	p := a.payload
	suffix := ""
	if asBool(p["code_owners"]) {
		suffix = " as code owners"
	}
	if ids := asSlice(p["principal_ids"]); len(ids) > 0 {
		names := make([]string, 0, len(ids))
		for _, idv := range ids {
			if name := resolveMention(a.mentions, asInt64(idv)); name != "" {
				names = append(names, name)
			}
		}
		return "requested review from " + joinWithMore(names, 3) + suffix
	}
	if idv, ok := p["principal_id"]; ok {
		return "requested review from " + resolveMention(a.mentions, asInt64(idv)) + suffix
	}
	return genericFallback(a)
}

// resolveMention looks up a principal's display name from the activity's own
// `mentions` map, keyed by principal ID as a string. No principal lookup call needed.
func resolveMention(mentions map[string]any, id int64) string {
	if mentions == nil || id == 0 {
		return ""
	}
	m := asMap(mentions[strconv.FormatInt(id, 10)])
	return asString(m["display_name"])
}

func joinWithMore(names []string, max int) string {
	if len(names) <= max {
		return strings.Join(names, ", ")
	}
	return strings.Join(names[:max], ", ") + fmt.Sprintf(" +%d more", len(names)-max)
}

func reviewSubmitText(a activity) string {
	switch decision := asString(a.payload["decision"]); decision {
	case "approved":
		return "approved these changes"
	case "changereq":
		return "requested changes"
	default:
		return decision
	}
}

func stateChangeText(a activity) string {
	p := a.payload
	oldDraft, newDraft := asBool(p["old_draft"]), asBool(p["new_draft"])
	if oldDraft != newDraft {
		if newDraft {
			return "converted to draft"
		}
		return "marked ready for review"
	}
	switch asString(p["new"]) {
	case "closed":
		return "closed"
	case "open":
		return "reopened"
	default:
		return genericFallback(a)
	}
}

// branchUpdateText renders a push. A plain push reads as "pushed a new commit" —
// matching the Harness Code UI, which doesn't show SHAs for this either — since
// the old→new SHA pair is exactly the kind of raw-ID noise a prose timeline
// shouldn't lead with. A force-push keeps its SHA arrow: it rewrites history, so
// which commit got replaced by which is the point, not incidental detail.
func branchUpdateText(a activity) string {
	p := a.payload
	s := "pushed a new commit"
	if asBool(p["forced"]) {
		s = fmt.Sprintf("force-pushed %s → %s", sha8(asString(p["old"])), sha8(asString(p["new"])))
	}
	if title := asString(p["commit_title"]); title != "" {
		s += fmt.Sprintf(" · %q", title)
	}
	return s
}

func mergeText(a activity) string {
	p := a.payload
	return fmt.Sprintf("merged via %s → %s", asString(p["merge_method"]), sha8(asString(p["merge_sha"])))
}

func titleChangeText(a activity) string {
	return fmt.Sprintf("renamed to %q", asString(a.payload["new"]))
}

func labelModifyText(a activity) string {
	p := a.payload
	verb := "added"
	if asString(p["type"]) == "removed" {
		verb = "removed"
	}
	return fmt.Sprintf("%s label %s", verb, asString(p["label"]))
}

// ---------------------------------------------------------------------------
// comment threads
// ---------------------------------------------------------------------------

func writeCommentThread(w io.Writer, root activity, replies []activity) {
	fmt.Fprintln(w, commentedHeader(root, nil, false))
	if root.text != "" {
		writeBody(w, root.text)
	}
	writeReplies(w, replies)
}

// writeReplies prints each reply with a blank line ahead of it — between the
// parent and the first reply, and between replies themselves — so a threaded
// reply chain doesn't read as one run-on block.
func writeReplies(w io.Writer, replies []activity) {
	for _, reply := range replies {
		fmt.Fprintln(w)
		writeReply(w, reply)
	}
}

// aiCodeReviewAuthor is the display name the AI Code Review service account
// posts under (see modules/code/insight.go's own "AI Code Review" heading) —
// exact-matched here so its comments get the sparkles icon instead of the
// plain "●" every other commenter gets.
const aiCodeReviewAuthor = "AI Code Review"

// commentedHeader renders the "● Author commented · 2h ago" line used for both
// plain and code comment threads, matching the phrasing the Harness Code UI itself
// uses for a comment activity (as opposed to a system event like "merged"). The
// AI Code Review bot is a special case: it gets a "✨" icon instead of "●". A
// muted header (a collapsed outdated/resolved code thread) drops both special
// icons for the same dim "▸" bullet every other collapsed line uses, and dims
// the whole line — it's bookkeeping the reader has already opted out of
// expanding, not a comment to read.
func commentedHeader(a activity, tags []string, muted bool) string {
	if muted {
		text := "▸ " + a.author + " commented"
		if len(tags) > 0 {
			text += " " + strings.Join(tags, " · ")
		}
		text += " · " + relativeTime(a.createdMs)
		if a.editedMs != 0 && a.editedMs != a.createdMs {
			text += " (edited)"
		}
		return console.WithColor(console.ColorBrightBlack, text)
	}
	bullet := "●"
	if a.author == aiCodeReviewAuthor {
		bullet = "✨"
	}
	line := console.WithColor(console.ColorCyan, bullet) + " " +
		console.WithColor(console.ColorCyan, a.author) + " commented"
	if len(tags) > 0 {
		line += " " + console.WithColor(console.ColorYellow, strings.Join(tags, " · "))
	}
	line += " · " + relativeTime(a.createdMs)
	if a.editedMs != 0 && a.editedMs != a.createdMs {
		line += " (edited)"
	}
	return line
}

func writeReply(w io.Writer, a activity) {
	fmt.Fprintln(w, "  "+commentedHeader(a, nil, false))
	for _, l := range renderMarkdownLines(a.text) {
		if l == "" {
			fmt.Fprintln(w)
		} else {
			fmt.Fprintln(w, "    "+l)
		}
	}
}

func writeBody(w io.Writer, text string) {
	for _, l := range renderMarkdownLines(text) {
		if l == "" {
			fmt.Fprintln(w)
		} else {
			fmt.Fprintln(w, "  "+l)
		}
	}
}

// ---------------------------------------------------------------------------
// code comment threads
// ---------------------------------------------------------------------------

// shouldCollapseCodeThread reports whether a code-comment thread should render
// as its one-line header only, hiding the location, diff, body, and replies.
// Outdated and resolved threads collapse by default — they're the ones a
// reader has the least reason to dig into — unless the matching --show-outdated
// / --show-resolved flag opts back in. A thread that's both stays collapsed
// unless both flags are set.
func shouldCollapseCodeThread(outdated, resolved bool, opts renderOptions) bool {
	if outdated && !opts.showOutdated {
		return true
	}
	if resolved && !opts.showResolved {
		return true
	}
	return false
}

func writeCodeThread(w io.Writer, root activity, replies []activity, opts renderOptions) {
	outdated := asBool(root.codeComment["outdated"])
	resolved := root.resolved
	var tags []string
	if outdated {
		tags = append(tags, "outdated")
	}
	if resolved {
		tags = append(tags, "resolved")
	}
	collapsed := shouldCollapseCodeThread(outdated, resolved, opts)
	fmt.Fprintln(w, commentedHeader(root, tags, collapsed))

	if collapsed {
		return
	}

	path := asString(root.codeComment["path"])
	line := asInt64(root.codeComment["line_new"])
	if line == 0 {
		line = asInt64(root.codeComment["line_old"])
	}
	loc := path
	if line > 0 {
		loc = fmt.Sprintf("%s:%d", path, line)
	}
	if loc != "" {
		fmt.Fprintln(w, "  "+loc)
	}

	if opts.showDiffs {
		if title := asString(root.payload["title"]); title != "" {
			fmt.Fprintln(w, "  "+console.WithColor(console.ColorBrightBlack, title))
		}
		for _, l := range asSlice(root.payload["lines"]) {
			fmt.Fprintln(w, "  "+colorDiffLine(asString(l)))
		}
	}

	if root.text != "" {
		fmt.Fprintln(w)
		writeBody(w, root.text)
	}
	writeReplies(w, replies)
}

// colorDiffLine tints a raw diff line green/red by its +/- prefix, matching the
// convention readers already know from `git diff`.
func colorDiffLine(l string) string {
	switch {
	case strings.HasPrefix(l, "+"):
		return console.WithColor(console.ColorGreen, l)
	case strings.HasPrefix(l, "-"):
		return console.WithColor(console.ColorRed, l)
	default:
		return l
	}
}

// ---------------------------------------------------------------------------
// markdown (hand-rolled subset: bold, inline code, fenced code blocks, bullets)
// ---------------------------------------------------------------------------

var (
	mdBoldPattern = regexp.MustCompile(`\*\*(.+?)\*\*`)
	mdCodePattern = regexp.MustCompile("`([^`]+)`")
)

// renderMarkdownLines renders text's small markdown subset line by line, with no
// reflow: comment bodies and system events often carry raw URLs (pipeline links,
// PR links), and wrapping those to a column width breaks them across lines and
// makes them unclickable in a terminal. Lines are emitted as authored; the
// terminal soft-wraps if it must.
func renderMarkdownLines(text string) []string {
	var out []string
	inFence := false
	for _, raw := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(raw)
		if strings.HasPrefix(trimmed, "```") {
			inFence = !inFence
			continue
		}
		if inFence {
			out = append(out, console.WithColor(console.ColorBrightBlack, raw))
			continue
		}
		if strings.HasPrefix(trimmed, "- ") || strings.HasPrefix(trimmed, "* ") {
			out = append(out, "• "+renderMarkdownInline(strings.TrimSpace(trimmed[2:])))
		} else {
			out = append(out, renderMarkdownInline(raw))
		}
	}
	return out
}

func renderMarkdownInline(s string) string {
	s = mdCodePattern.ReplaceAllStringFunc(s, func(m string) string {
		return console.WithColor(console.ColorYellow, m)
	})
	s = mdBoldPattern.ReplaceAllStringFunc(s, func(m string) string {
		inner := strings.TrimSuffix(strings.TrimPrefix(m, "**"), "**")
		return console.WithBold(inner)
	})
	return s
}

// ---------------------------------------------------------------------------
// layout helpers
// ---------------------------------------------------------------------------
