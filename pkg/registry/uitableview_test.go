// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package registry

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/harness/cli/pkg/cmdctx"
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

func TestUpdate_BKey_DetailMode_WithHistory_SetsWantBack(t *testing.T) {
	m := uiTableModel{
		ctx:        &cmdctx.Ctx{UIHistory: []cmdctx.UILink{{Verb: VerbGet, Noun: "thing"}}},
		detailMode: true,
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
