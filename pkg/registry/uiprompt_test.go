// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package registry

import (
	"errors"
	"reflect"
	"strings"
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
		prompt:     &uiPrompt{uc: spec.UICommand{InputFlag: "comment"}, cs: &spec.CommandSpec{Verb: VerbExecute, Noun: "thing"}, id: "x", input: in},
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

func TestRunPromptCommandRejectsNonScalarInputFlag(t *testing.T) {
	for name, f := range map[string]spec.Flag{
		"multi": {Name: "comment", IsMulti: true},
		"array": {Name: "comment", IsArray: true},
	} {
		cs := &spec.CommandSpec{Verb: VerbExecute, Noun: "thing", Flags: []spec.Flag{f}}
		err := runPromptCommand(nil, &uiPromptRun{uc: spec.UICommand{InputFlag: "comment"}, cs: cs})
		if err == nil || !strings.Contains(err.Error(), "single-value flag") {
			t.Fatalf("%s input flag: got %v", name, err)
		}
	}
}

func TestPromptConfirmModal(t *testing.T) {
	press := func(m uiTableModel, msgs ...tea.KeyPressMsg) (uiTableModel, tea.Cmd) {
		var cmd tea.Cmd
		for _, msg := range msgs {
			m, cmd = m.handlePromptKey(msg)
		}
		return m, cmd
	}
	enter := tea.KeyPressMsg{Code: tea.KeyEnter}
	esc := tea.KeyPressMsg{Code: tea.KeyEscape}
	key := func(r rune) tea.KeyPressMsg { return tea.KeyPressMsg{Code: r, Text: string(r)} }

	confirmID := func() uiTableModel {
		m := promptTestModel("ship it")
		m.prompt.cs.ConfirmMode = spec.ConfirmID
		return m
	}
	m, cmd := press(confirmID(), ctrlS())
	if cmd != nil || m.prompt.busy || m.prompt.confirm == nil {
		t.Fatalf("ctrl+s must open the modal without running: cmd=%v busy=%v", cmd != nil, m.prompt.busy)
	}
	m, cmd = press(m, key('n'), enter)
	if cmd != nil || m.prompt.busy || m.prompt.confirm == nil || m.prompt.confirm.err == "" {
		t.Fatalf("wrong id must keep the modal open: cmd=%v busy=%v", cmd != nil, m.prompt.busy)
	}
	m, cmd = press(m, esc)
	if cmd != nil || m.prompt == nil || m.prompt.confirm != nil || m.prompt.input.Value() != "ship it" {
		t.Fatal("esc must return to the text box with the text kept")
	}
	m, cmd = press(m, ctrlS(), key('x'), enter)
	if cmd == nil || !m.prompt.busy || m.prompt.confirm != nil {
		t.Fatalf("matching id must run: cmd=%v busy=%v", cmd != nil, m.prompt.busy)
	}

	confirmYN := promptTestModel("ship it")
	confirmYN.prompt.cs.ConfirmMode = spec.ConfirmPrompt
	m, cmd = press(confirmYN, ctrlS(), enter)
	if cmd != nil || m.prompt.busy || m.prompt.confirm != nil {
		t.Fatal("enter must cancel the y/N modal")
	}
	m, cmd = press(m, ctrlS(), key('y'))
	if cmd == nil || !m.prompt.busy {
		t.Fatal("y must run")
	}
}

func TestPromptConfirmModalIgnoresPaste(t *testing.T) {
	m := promptTestModel("ship it")
	m.prompt.input.Focus()
	m.prompt.cs.ConfirmMode = spec.ConfirmID
	m, _ = m.handlePromptKey(ctrlS())
	for range 2 {
		newModel, _ := m.Update(tea.PasteMsg{Content: "x"})
		m = newModel.(uiTableModel)
	}
	if m.prompt.input.Value() != "ship it" || m.prompt.confirm.typed != "" {
		t.Fatalf("paste must be ignored: text=%q typed=%q", m.prompt.input.Value(), m.prompt.confirm.typed)
	}
	m, _ = m.handlePromptKey(tea.KeyPressMsg{Code: tea.KeyEscape})
	newModel, _ := m.Update(tea.PasteMsg{Content: "!"})
	if got := newModel.(uiTableModel).prompt.input.Value(); got != "ship it!" {
		t.Fatalf("paste into the text box must still work once the modal is closed: %q", got)
	}
}
