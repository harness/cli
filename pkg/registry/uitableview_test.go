// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package registry

import (
	"context"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/harness/cli/v3/pkg/cmdctx"
	"github.com/harness/cli/v3/pkg/spec"
	"github.com/harness/cli/v3/pkg/tui"
)

func TestUpdate_BKey_WithHistory_SetsWantBack(t *testing.T) {
	m := uiTableModel{
		ctx: &cmdctx.Ctx{UIHistory: []cmdctx.UILink{{Verb: VerbList, Noun: "thing"}}},
	}
	newModel, cmd := m.Update(tea.KeyPressMsg{Text: "b"})
	nm := newModel.(uiTableModel)
	if !nm.wantBack {
		t.Fatal("wantBack = false, want true")
	}
	if cmd == nil {
		t.Fatal("cmd = nil, want tea.Quit")
	}
}

func TestUpdate_BKey_EmptyHistory_NoOp(t *testing.T) {
	m := uiTableModel{ctx: &cmdctx.Ctx{}}
	newModel, _ := m.Update(tea.KeyPressMsg{Text: "b"})
	nm := newModel.(uiTableModel)
	if nm.wantBack {
		t.Fatal("wantBack = true, want false (empty UIHistory)")
	}
}

func TestUpdate_BKey_DetailOnlyMode_WithHistory_SetsWantBack(t *testing.T) {
	m := uiTableModel{
		ctx:        &cmdctx.Ctx{UIHistory: []cmdctx.UILink{{Verb: VerbGet, Noun: "thing"}}},
		detailMode: true,
		detailOnly: true,
	}
	newModel, cmd := m.Update(tea.KeyPressMsg{Text: "b"})
	nm := newModel.(uiTableModel)
	if !nm.wantBack {
		t.Fatal("wantBack = false, want true")
	}
	if cmd == nil {
		t.Fatal("cmd = nil, want tea.Quit")
	}
}

func TestUpdate_BKey_InPlaceDetailFlip_CollapsesLikeEsc(t *testing.T) {
	m := uiTableModel{
		ctx:        &cmdctx.Ctx{UIHistory: []cmdctx.UILink{{Verb: VerbGet, Noun: "thing"}}},
		detailMode: true,
		detailOnly: false,
	}
	newModel, cmd := m.Update(tea.KeyPressMsg{Text: "b"})
	nm := newModel.(uiTableModel)
	if nm.detailMode {
		t.Fatal("detailMode = true, want false (b should collapse to the list, like esc)")
	}
	if nm.wantBack {
		t.Fatal("wantBack = true, want false (should not touch History when collapsing an in-place flip)")
	}
	if cmd != nil {
		t.Fatal("cmd != nil, want nil (should not quit)")
	}
}

func TestFinishUIExit_PushesLinkOnLinkHop(t *testing.T) {
	ctx := &cmdctx.Ctx{
		Verb:     VerbList,
		Noun:     "thing",
		ParentId: "parent-1",
		Level:    "org",
		Resolver: New(),
	}
	fm := uiTableModel{
		linkTarget: &cmdctx.UILink{Verb: VerbGet, Noun: "other"},
	}
	// The link target doesn't resolve against an empty Registry; the resulting
	// error is expected and irrelevant here — only the push is under test.
	_ = finishUIExit(ctx, fm)

	if len(ctx.UIHistory) != 1 {
		t.Fatalf("UIHistory len = %d, want 1", len(ctx.UIHistory))
	}
	got := ctx.UIHistory[0]
	if got.Verb != VerbList || got.Noun != "thing" || got.Id != "parent-1" || got.Level != "org" || got.Screen != cmdctx.ScreenTable {
		t.Fatalf("pushed link = %+v, want Verb=%s Noun=thing Id=parent-1 Level=org Screen=ScreenTable", got, VerbList)
	}
}

func TestFinishUIExit_PushesLinkOnViewHop(t *testing.T) {
	ctx := &cmdctx.Ctx{
		Verb:     VerbGet,
		Noun:     "thing",
		Id:       "child-1",
		Resolver: New(),
	}
	fm := uiTableModel{
		detailOnly:        true,
		launchUIId:        "child-1",
		launchUIHandlerFn: "missing_handler",
	}
	// missing_handler isn't registered on an empty Registry; the resulting error
	// is expected and irrelevant here — only the push is under test.
	_ = finishUIExit(ctx, fm)

	if len(ctx.UIHistory) != 1 {
		t.Fatalf("UIHistory len = %d, want 1", len(ctx.UIHistory))
	}
	got := ctx.UIHistory[0]
	if got.Verb != VerbGet || got.Noun != "thing" || got.Id != "child-1" || got.Screen != cmdctx.ScreenDetailForGet {
		t.Fatalf("pushed link = %+v, want Verb=%s Noun=thing Id=child-1 Screen=ScreenDetailForGet", got, VerbGet)
	}
}

func TestCurrentScreenLink_CapturesOffsetOnTable(t *testing.T) {
	ctx := &cmdctx.Ctx{Verb: VerbList, Noun: "thing", ParentId: "parent-1"}
	table := tui.NewTable(nil, 5, 40)
	rows := make([]tui.Row, 10)
	for i := range rows {
		rows[i] = tui.Row{"x"}
	}
	table.SetRows(rows)
	table.SetCursor(4)
	fm := uiTableModel{t: table}

	link := currentScreenLink(ctx, fm)
	if link.Offset != 4 {
		t.Fatalf("Offset = %d, want 4", link.Offset)
	}
}

func TestCurrentScreenLink_CapturesOffsetEvenMidDetailFlip(t *testing.T) {
	// "b" always resumes the underlying list, never the detail overlay, so an
	// in-place detail flip (detailMode true, detailOnly false) over a table must
	// still capture that table's live cursor.
	ctx := &cmdctx.Ctx{Verb: VerbList, Noun: "thing", ParentId: "parent-1"}
	table := tui.NewTable(nil, 5, 40)
	rows := make([]tui.Row, 10)
	for i := range rows {
		rows[i] = tui.Row{"x"}
	}
	table.SetRows(rows)
	table.SetCursor(4)
	fm := uiTableModel{t: table, detailMode: true, detailOnly: false}

	link := currentScreenLink(ctx, fm)
	if link.Offset != 4 {
		t.Fatalf("Offset = %d, want 4 (mid-flip should still capture the list cursor)", link.Offset)
	}
}

func TestCurrentScreenLink_DetailOnlyScreenHasZeroOffset(t *testing.T) {
	// Case 4 detail-only Hops never populate fm.t, so its Cursor() is naturally 0.
	ctx := &cmdctx.Ctx{Verb: VerbGet, Noun: "thing", Id: "child-1"}
	fm := uiTableModel{detailMode: true, detailOnly: true}

	link := currentScreenLink(ctx, fm)
	if link.Offset != 0 {
		t.Fatalf("Offset = %d, want 0 (detail-only screens have no underlying table)", link.Offset)
	}
}

func TestCurrentScreenLink_CapturesOffsetAcrossPages(t *testing.T) {
	ctx := &cmdctx.Ctx{Verb: VerbList, Noun: "thing", ParentId: "parent-1"}
	table := tui.NewTable(nil, 5, 40)
	rows := make([]tui.Row, 10)
	for i := range rows {
		rows[i] = tui.Row{"x"}
	}
	table.SetRows(rows)
	table.SetCursor(3)
	fm := uiTableModel{t: table, page: 2, pageSize: 20}

	link := currentScreenLink(ctx, fm)
	if link.Offset != 43 {
		t.Fatalf("Offset = %d, want 43 (page 2 * pageSize 20 + cursor 3)", link.Offset)
	}
}

func TestCurrentScreenLink_CapturesSearchTermIntoFlagValues(t *testing.T) {
	ctx := &cmdctx.Ctx{Verb: VerbList, Noun: "thing", ParentId: "parent-1", FlagValues: map[string]any{"other": "x"}}
	table := tui.NewTable(nil, 5, 40)
	fm := uiTableModel{t: table, hasSearch: true, searchTerm: "foo"}

	link := currentScreenLink(ctx, fm)
	if got := link.FlagValues["search"]; got != "foo" {
		t.Fatalf("FlagValues[search] = %v, want %q", got, "foo")
	}
	if got := link.FlagValues["other"]; got != "x" {
		t.Fatalf("FlagValues[other] = %v, want %q (should carry other flags through)", got, "x")
	}
	if ctx.FlagValues["search"] != nil {
		t.Fatalf("ctx.FlagValues mutated, want the copy left untouched")
	}
}

func TestCurrentScreenLink_NoSearchLeavesFlagValuesUnchanged(t *testing.T) {
	ctx := &cmdctx.Ctx{Verb: VerbList, Noun: "thing", ParentId: "parent-1", FlagValues: map[string]any{"other": "x"}}
	table := tui.NewTable(nil, 5, 40)
	fm := uiTableModel{t: table, hasSearch: false}

	link := currentScreenLink(ctx, fm)
	if _, ok := link.FlagValues["search"]; ok {
		t.Fatal("FlagValues[search] present, want no injected key when hasSearch is false")
	}
}

func TestFinishUIExit_WantBack_PopsAndReplays(t *testing.T) {
	ctx := &cmdctx.Ctx{
		Resolver:  New(),
		UIHistory: []cmdctx.UILink{{Verb: VerbGet, Noun: "thing", Id: "prev-id"}},
	}
	fm := uiTableModel{wantBack: true}
	// "get thing" doesn't resolve against an empty Registry; the resulting error
	// is expected and irrelevant here — only the pop is under test.
	_ = finishUIExit(ctx, fm)

	if len(ctx.UIHistory) != 0 {
		t.Fatalf("UIHistory len = %d, want 0 (popped by wantBack)", len(ctx.UIHistory))
	}
}

func TestFinishUIExit_WantBack_EmptyHistoryNoOp(t *testing.T) {
	ctx := &cmdctx.Ctx{Resolver: New()}
	fm := uiTableModel{wantBack: true}
	if err := finishUIExit(ctx, fm); err != nil {
		t.Fatalf("finishUIExit: %v", err)
	}
}

func TestFinishUIExit_ViewHop_ResumesLeftScreen(t *testing.T) {
	r := New()
	r.RegisterWorkflow("noop_handler", func(*cmdctx.Ctx) error { return nil })
	ctx := &cmdctx.Ctx{
		Verb:      VerbGet,
		Noun:      "thing",
		Id:        "child-1",
		Resolver:  r,
		UIHistory: []cmdctx.UILink{{Verb: VerbGet, Noun: "thing", Id: "prev-id"}},
	}
	fm := uiTableModel{
		detailOnly:        true,
		launchUIId:        "child-1",
		launchUIHandlerFn: "noop_handler",
	}
	// The handler pushes the screen it's leaving, runs "noop_handler" (returns nil), then sets
	// ctx.UIWantBack (as a view handler's own "b" key would) so finishUIExit pops that same
	// entry back off to resume it via dispatchLink — which doesn't resolve against an empty
	// Registry; the resulting error is expected and irrelevant here. Only the net stack effect
	// (resume, not leak or exit) is under test.
	ctx.UIWantBack = true
	_ = finishUIExit(ctx, fm)

	if len(ctx.UIHistory) != 1 || ctx.UIHistory[0].Id != "prev-id" {
		t.Fatalf("UIHistory = %+v, want just the pre-existing prev-id entry (view-hop's own push+pop should net to zero)", ctx.UIHistory)
	}
}

func TestFinishUIExit_ViewHop_QuitOnlyByDefault(t *testing.T) {
	r := New()
	r.RegisterWorkflow("noop_handler", func(*cmdctx.Ctx) error { return nil })
	ctx := &cmdctx.Ctx{
		Verb:      VerbGet,
		Noun:      "thing",
		Id:        "child-1",
		Resolver:  r,
		UIHistory: []cmdctx.UILink{{Verb: VerbGet, Noun: "thing", Id: "prev-id"}},
	}
	fm := uiTableModel{
		detailOnly:        true,
		launchUIId:        "child-1",
		launchUIHandlerFn: "noop_handler",
	}
	// The handler returns nil without setting ctx.UIWantBack (the default), so the view
	// hop's own push is left in place and there is no pop/resume — quitting the handler's
	// screen quits outright rather than resuming the caller.
	if err := finishUIExit(ctx, fm); err != nil {
		t.Fatalf("finishUIExit: %v", err)
	}

	if len(ctx.UIHistory) != 2 {
		t.Fatalf("UIHistory len = %d, want 2 (view hop pushed, no pop without UIWantBack)", len(ctx.UIHistory))
	}
}

func TestFinishUIExit_NoHopNoPush(t *testing.T) {
	ctx := &cmdctx.Ctx{Verb: VerbList, Noun: "thing", Resolver: New()}
	fm := uiTableModel{}
	if err := finishUIExit(ctx, fm); err != nil {
		t.Fatalf("finishUIExit: %v", err)
	}
	if len(ctx.UIHistory) != 0 {
		t.Fatalf("UIHistory len = %d, want 0 (no hop fired)", len(ctx.UIHistory))
	}
}

func TestBuildLinkCtx_TableScreen_CarriesOffsetToRestoreOffset(t *testing.T) {
	ctx := &cmdctx.Ctx{Context: context.Background(), Resolver: New()}
	link := &cmdctx.UILink{Verb: VerbList, Noun: "thing", Id: "parent-1", Screen: cmdctx.ScreenTable, Offset: 44}
	targetCs := &spec.CommandSpec{Verb: VerbList, Noun: "thing", NoAuth: true}

	newCtx, err := buildLinkCtx(ctx, link, targetCs)
	if err != nil {
		t.Fatalf("buildLinkCtx: %v", err)
	}
	if newCtx.RestoreOffset != 44 {
		t.Fatalf("RestoreOffset = %d, want 44", newCtx.RestoreOffset)
	}
	if newCtx.ParentId != "parent-1" {
		t.Fatalf("ParentId = %q, want parent-1", newCtx.ParentId)
	}
}

func TestBuildLinkCtx_DetailScreen_DoesNotSetRestoreOffset(t *testing.T) {
	ctx := &cmdctx.Ctx{Context: context.Background(), Resolver: New()}
	link := &cmdctx.UILink{Verb: VerbGet, Noun: "thing", Id: "child-1", Screen: cmdctx.ScreenDetailForGet, Offset: 4}
	targetCs := &spec.CommandSpec{Verb: VerbGet, Noun: "thing", NoAuth: true}

	newCtx, err := buildLinkCtx(ctx, link, targetCs)
	if err != nil {
		t.Fatalf("buildLinkCtx: %v", err)
	}
	if newCtx.RestoreOffset != 0 {
		t.Fatalf("RestoreOffset = %d, want 0 (detail screens have no list cursor to restore)", newCtx.RestoreOffset)
	}
}

func TestNewUITableModel_SeedsPageAndCursorFromCtxRestoreOffset(t *testing.T) {
	// termHeight 24 -> pageSize = tableHeight(24) = 24-uiOverheadLines-1. Derive
	// the expected pageSize the same way the model does, so this test doesn't
	// hardcode uiOverheadLines.
	pageSize := tableHeight(24)
	offset := 2*pageSize + 4
	ctx := &cmdctx.Ctx{RestoreOffset: offset}
	m := newUITableModel(ctx, nil, nil, nil, nil, "title", 80, 24, nil)
	if m.page != 2 {
		t.Fatalf("page = %d, want 2 (derived from ctx.RestoreOffset / pageSize)", m.page)
	}
	if m.restoreCursor != 4 {
		t.Fatalf("restoreCursor = %d, want 4 (derived from ctx.RestoreOffset %% pageSize)", m.restoreCursor)
	}
}

func TestNewUITableModel_RestoreOffsetSurvivesPageSizeChange(t *testing.T) {
	// Capture at one pageSize, restore at a different (e.g. post-resize)
	// pageSize, and confirm the absolute row is still targeted correctly.
	capturedPageSize := 10
	capturedPage, capturedCursor := 2, 3
	offset := capturedPage*capturedPageSize + capturedCursor // absolute row 23

	ctx := &cmdctx.Ctx{RestoreOffset: offset}
	m := newUITableModel(ctx, nil, nil, nil, nil, "title", 80, 24, nil)

	restoredPageSize := m.pageSize
	if got := m.page*restoredPageSize + m.restoreCursor; got != offset {
		t.Fatalf("restored absolute row = %d, want %d (offset must survive pageSize change)", got, offset)
	}
}

func TestApplyPage_FirstLoadRestoresCursor_SubsequentLoadsGotoTop(t *testing.T) {
	rows := make([]tui.Row, 10)
	for i := range rows {
		rows[i] = tui.Row{"x"}
	}
	m := &uiTableModel{
		tspec:         &spec.TableSpec{Columns: []spec.TableColumn{{Header: "ID", Expr: "it.id"}}},
		t:             tui.NewTable(nil, 5, 40),
		width:         40,
		restoreCursor: 4,
	}

	m.applyPage(rows, nil)
	if got := m.t.Cursor(); got != 4 {
		t.Fatalf("Cursor() after first load = %d, want 4 (restored)", got)
	}
	if !m.restoreApplied {
		t.Fatal("restoreApplied = false after first load, want true")
	}

	m.applyPage(rows, nil)
	if got := m.t.Cursor(); got != 0 {
		t.Fatalf("Cursor() after second load = %d, want 0 (GotoTop, not re-restored)", got)
	}
}
