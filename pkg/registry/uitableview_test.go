// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package registry

import (
	"context"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/harness/cli/pkg/cmdctx"
	"github.com/harness/cli/pkg/spec"
	"github.com/harness/cli/pkg/tui"
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

func TestCurrentScreenLink_CapturesListPosOnTable(t *testing.T) {
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
	if link.ListPos != 4 {
		t.Fatalf("ListPos = %d, want 4", link.ListPos)
	}
}

func TestCurrentScreenLink_DetailModeDoesNotCaptureListPos(t *testing.T) {
	ctx := &cmdctx.Ctx{Verb: VerbGet, Noun: "thing", Id: "child-1"}
	table := tui.NewTable(nil, 5, 40)
	rows := make([]tui.Row, 10)
	for i := range rows {
		rows[i] = tui.Row{"x"}
	}
	table.SetRows(rows)
	table.SetCursor(4)
	fm := uiTableModel{t: table, detailMode: true, detailOnly: true}

	link := currentScreenLink(ctx, fm)
	if link.ListPos != 0 {
		t.Fatalf("ListPos = %d, want 0 (detail screens have no list cursor to capture)", link.ListPos)
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
	// The handler pushes the screen it's leaving, runs "noop_handler" (returns nil), then
	// pops that same entry back off to resume it via dispatchLink — which doesn't resolve
	// against an empty Registry; the resulting error is expected and irrelevant here. Only
	// the net stack effect (resume, not leak or exit) is under test.
	_ = finishUIExit(ctx, fm)

	if len(ctx.UIHistory) != 1 || ctx.UIHistory[0].Id != "prev-id" {
		t.Fatalf("UIHistory = %+v, want just the pre-existing prev-id entry (view-hop's own push+pop should net to zero)", ctx.UIHistory)
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

func TestBuildLinkCtx_TableScreen_CarriesListPosToRestoreListPos(t *testing.T) {
	ctx := &cmdctx.Ctx{Context: context.Background(), Resolver: New()}
	link := &cmdctx.UILink{Verb: VerbList, Noun: "thing", Id: "parent-1", Screen: cmdctx.ScreenTable, ListPos: 4}
	targetCs := &spec.CommandSpec{Verb: VerbList, Noun: "thing", NoAuth: true}

	newCtx, err := buildLinkCtx(ctx, link, targetCs)
	if err != nil {
		t.Fatalf("buildLinkCtx: %v", err)
	}
	if newCtx.RestoreListPos != 4 {
		t.Fatalf("RestoreListPos = %d, want 4", newCtx.RestoreListPos)
	}
	if newCtx.ParentId != "parent-1" {
		t.Fatalf("ParentId = %q, want parent-1", newCtx.ParentId)
	}
}

func TestBuildLinkCtx_DetailScreen_DoesNotSetRestoreListPos(t *testing.T) {
	ctx := &cmdctx.Ctx{Context: context.Background(), Resolver: New()}
	link := &cmdctx.UILink{Verb: VerbGet, Noun: "thing", Id: "child-1", Screen: cmdctx.ScreenDetailForGet, ListPos: 4}
	targetCs := &spec.CommandSpec{Verb: VerbGet, Noun: "thing", NoAuth: true}

	newCtx, err := buildLinkCtx(ctx, link, targetCs)
	if err != nil {
		t.Fatalf("buildLinkCtx: %v", err)
	}
	if newCtx.RestoreListPos != 0 {
		t.Fatalf("RestoreListPos = %d, want 0 (detail screens have no list cursor to restore)", newCtx.RestoreListPos)
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
