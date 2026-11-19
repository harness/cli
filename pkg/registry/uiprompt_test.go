// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package registry

import (
	"errors"
	"reflect"
	"testing"

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"

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
	for _, text := range []string{"", "ship it"} {
		m, cmd := promptTestModel(text).handlePromptKey(ctrlS())
		if cmd == nil || !m.prompt.busy {
			t.Fatalf("ctrl+s with %q: cmd=%v busy=%v", text, cmd != nil, m.prompt.busy)
		}
	}
	busy := promptTestModel("ship it")
	busy.prompt.busy = true
	if m, cmd := busy.handlePromptKey(tea.KeyPressMsg{Code: tea.KeyEscape}); cmd != nil || m.prompt == nil {
		t.Fatal("esc must not close the box while the command runs")
	}
	m, _ := promptTestModel("ship it").handlePromptKey(tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.prompt != nil {
		t.Fatal("esc must close the box")
	}
}

func TestPromptDone(t *testing.T) {
	m, cmd := promptTestModel("ship it").finishPrompt(uiPromptDoneMsg{err: errors.New("boom")})
	if cmd != nil || m.prompt == nil || m.prompt.busy || m.prompt.err != "boom" || m.prompt.input.Value() != "ship it" || m.promptDone {
		t.Fatalf("after error: prompt=%+v done=%v", m.prompt, m.promptDone)
	}
	m, cmd = promptTestModel("ship it").finishPrompt(uiPromptDoneMsg{})
	if cmd == nil || m.prompt != nil || !m.promptDone {
		t.Fatalf("after success: cmd=%v prompt=%v done=%v", cmd != nil, m.prompt != nil, m.promptDone)
	}
}

func TestPromptFlagValues(t *testing.T) {
	cs := &spec.CommandSpec{Flags: []spec.Flag{
		{Name: "comment"},
		{Name: "kind", Default: "approve"},
		{Name: "force", IsBool: true},
		{Name: "tags", IsMulti: true},
	}}
	fv, provided := promptFlagValues(cs, "comment", "")
	want := map[string]any{"comment": "", "kind": "approve", "force": false, "tags": []string{}}
	if !reflect.DeepEqual(fv, want) {
		t.Fatalf("defaults: got %v want %v", fv, want)
	}
	if !provided["comment"] || provided["kind"] {
		t.Fatalf("provided: %v", provided)
	}
}

func TestPromptFlagValuesConfirmMode(t *testing.T) {
	for mode, wantForce := range map[string]bool{spec.ConfirmPrompt: true, spec.ConfirmID: false, spec.ConfirmNone: false} {
		fv, _ := promptFlagValues(&spec.CommandSpec{ConfirmMode: mode, Flags: []spec.Flag{{Name: "comment"}}}, "comment", "x")
		if got, _ := fv["force"].(bool); got != wantForce {
			t.Fatalf("confirm_mode %q: force=%v want %v", mode, got, wantForce)
		}
	}
}
