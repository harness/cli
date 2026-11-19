// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package registry

import (
	"errors"
	"testing"

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"

	"github.com/harness/cli/v3/pkg/cmdctx"
	"github.com/harness/cli/v3/pkg/spec"
)

func promptTestModel(text string) uiTableModel {
	in := textarea.New()
	in.SetValue(text)
	return uiTableModel{
		width: 80, height: 24,
		getCs:      &spec.CommandSpec{Noun: "thing"},
		detailMode: true,
		prompt:     &uiPrompt{uc: spec.UICommand{InputFlag: "comment"}, id: "x", input: in},
	}
}

func ctrlS() tea.KeyPressMsg { return tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl} }

func TestPromptKeys(t *testing.T) {
	t.Run("ctrl+s on empty text still submits", func(t *testing.T) {
		m, cmd := promptTestModel("").handlePromptKey(ctrlS())
		if cmd == nil || !m.prompt.busy {
			t.Fatalf("empty submit: cmd=%v busy=%v", cmd != nil, m.prompt.busy)
		}
	})
	t.Run("ctrl+s on text submits", func(t *testing.T) {
		m, cmd := promptTestModel("ship it").handlePromptKey(ctrlS())
		if cmd == nil || !m.prompt.busy {
			t.Fatalf("submit: cmd=%v busy=%v", cmd != nil, m.prompt.busy)
		}
	})
	t.Run("keys are ignored while submitting", func(t *testing.T) {
		m := promptTestModel("ship it")
		m.prompt.busy = true
		m, cmd := m.handlePromptKey(tea.KeyPressMsg{Code: tea.KeyEscape})
		if cmd != nil || m.prompt == nil {
			t.Fatal("esc must not close the box while a submit is in flight")
		}
	})
	t.Run("esc discards the text", func(t *testing.T) {
		m, _ := promptTestModel("ship it").handlePromptKey(tea.KeyPressMsg{Code: tea.KeyEscape})
		if m.prompt != nil {
			t.Fatal("prompt still open after esc")
		}
	})
}

func TestPromptDone(t *testing.T) {
	t.Run("error keeps the box and the text", func(t *testing.T) {
		m, cmd := promptTestModel("ship it").finishPrompt(uiPromptDoneMsg{err: errors.New("boom")})
		if cmd != nil || m.prompt == nil || m.prompt.err != "boom" || m.prompt.input.Value() != "ship it" {
			t.Fatalf("after error: prompt=%+v", m.prompt)
		}
	})
	t.Run("success from a hopped detail pops the history", func(t *testing.T) {
		m := promptTestModel("ship it")
		m.detailOnly = true
		m.ctx = &cmdctx.Ctx{UIHistory: []cmdctx.UILink{{}}}
		m, cmd := m.finishPrompt(uiPromptDoneMsg{})
		if cmd == nil || m.prompt != nil || !m.wantBack {
			t.Fatalf("after success: cmd=%v prompt=%v wantBack=%v", cmd != nil, m.prompt != nil, m.wantBack)
		}
	})
	t.Run("success from an in-place detail returns to the list", func(t *testing.T) {
		m, cmd := promptTestModel("ship it").finishPrompt(uiPromptDoneMsg{})
		if cmd != nil || m.prompt != nil || m.detailMode {
			t.Fatalf("after success: cmd=%v prompt=%v detailMode=%v", cmd != nil, m.prompt != nil, m.detailMode)
		}
	})
}
