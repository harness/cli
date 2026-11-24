// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package registry

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/harness/cli/v3/pkg/spec"
)

func typeInto(c *uiConfirm, s string) {
	for _, r := range s {
		c.handleKey(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
}

func TestUIConfirmPromptDefaultsToNo(t *testing.T) {
	cs := &spec.CommandSpec{Verb: VerbDelete, Noun: "thing", ConfirmMode: spec.ConfirmPrompt}
	for key, want := range map[string]uiConfirmResult{
		"y": uiConfirmAccepted, "n": uiConfirmCancelled, "enter": uiConfirmCancelled,
		"esc": uiConfirmCancelled, "x": uiConfirmPending,
	} {
		msg := tea.KeyPressMsg{Text: key}
		switch key {
		case "enter":
			msg = tea.KeyPressMsg{Code: tea.KeyEnter}
		case "esc":
			msg = tea.KeyPressMsg{Code: tea.KeyEscape}
		}
		if got := newUIConfirm(cs, "a/b").handleKey(msg); got != want {
			t.Errorf("key %q: got %v want %v", key, got, want)
		}
	}
}

func TestUIConfirmIDMismatchKeepsModalOpen(t *testing.T) {
	cs := &spec.CommandSpec{Verb: VerbDelete, Noun: "thing", ConfirmMode: spec.ConfirmID}
	c := newUIConfirm(cs, "repo/7")
	typeInto(c, "repo/8")
	if got := c.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter}); got != uiConfirmPending || c.err == "" {
		t.Fatalf("mismatch: got %v err %q", got, c.err)
	}
	c.handleKey(tea.KeyPressMsg{Code: tea.KeyBackspace})
	if c.err != "" || c.typed != "repo/" {
		t.Fatalf("editing must clear the error and trim: typed %q err %q", c.typed, c.err)
	}
	typeInto(c, "7")
	if got := c.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter}); got != uiConfirmAccepted {
		t.Fatalf("matching id: got %v", got)
	}
}

func TestUIConfirmIDEscCancels(t *testing.T) {
	cs := &spec.CommandSpec{Verb: VerbDelete, Noun: "thing", ConfirmMode: spec.ConfirmID}
	c := newUIConfirm(cs, "x")
	typeInto(c, "x")
	if got := c.handleKey(tea.KeyPressMsg{Code: tea.KeyEscape}); got != uiConfirmCancelled {
		t.Fatalf("got %v", got)
	}
}
