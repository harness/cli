// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package registry

import (
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
		if cmd == nil || m.promptRun == nil || m.promptRun.text != text || m.prompt != nil {
			t.Fatalf("ctrl+s with %q: cmd=%v run=%+v", text, cmd != nil, m.promptRun)
		}
	}
	m, _ := promptTestModel("ship it").handlePromptKey(tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.prompt != nil || m.promptRun != nil {
		t.Fatal("esc must close the box without submitting")
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

	cs.Flags[0] = spec.Flag{Name: "comment", IsMulti: true}
	fv, _ = promptFlagValues(cs, "comment", "hi, there")
	if got := fv["comment"]; !reflect.DeepEqual(got, []string{"hi, there"}) {
		t.Fatalf("multi input flag: %v", got)
	}
}

func TestErrorScreenKeys(t *testing.T) {
	for key, wantBack := range map[string]bool{"b": true, "q": false} {
		m, cmd := uiErrorModel{}.Update(tea.KeyPressMsg{Code: rune(key[0]), Text: key})
		if cmd == nil || m.(uiErrorModel).back != wantBack {
			t.Fatalf("%s: back=%v want %v", key, m.(uiErrorModel).back, wantBack)
		}
	}
}
