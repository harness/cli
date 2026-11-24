// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package registry

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/harness/cli/v3/pkg/spec"
	"github.com/harness/cli/v3/pkg/tui"
)

const uiConfirmWidth = 50

// uiConfirm is the modal that stands in for runConfirmGate on a command run
// from the TUI: y/N for confirm_mode prompt, the typed id for confirm_id.
type uiConfirm struct {
	mode   string
	gerund string
	noun   string
	id     string
	typed  string
	err    string
}

type uiConfirmResult int

const (
	uiConfirmPending uiConfirmResult = iota
	uiConfirmAccepted
	uiConfirmCancelled
)

func newUIConfirm(cs *spec.CommandSpec, id string) *uiConfirm {
	return &uiConfirm{
		mode:   cs.ConfirmMode,
		gerund: verbGerund(cs.Verb),
		noun:   strings.ReplaceAll(cs.Noun, "_", " "),
		id:     id,
	}
}

func (c *uiConfirm) handleKey(msg tea.KeyPressMsg) uiConfirmResult {
	key := msg.String()
	if key == "esc" {
		return uiConfirmCancelled
	}
	if c.mode == spec.ConfirmPrompt {
		switch key {
		case "y", "Y":
			return uiConfirmAccepted
		case "n", "N", "enter":
			return uiConfirmCancelled
		}
		return uiConfirmPending
	}
	switch key {
	case "enter":
		if confirmIDMatches(c.typed, c.id) {
			return uiConfirmAccepted
		}
		c.err = "Identifier did not match"
	case "backspace":
		if r := []rune(c.typed); len(r) > 0 {
			c.typed = string(r[:len(r)-1])
		}
		c.err = ""
	default:
		if msg.Text != "" {
			c.typed += msg.Text
			c.err = ""
		}
	}
	return uiConfirmPending
}

func (c *uiConfirm) view() string {
	bold := lipgloss.NewStyle().Bold(true)
	subtle := lipgloss.NewStyle().Foreground(lipgloss.Color(tui.CLITextMuted))

	var body string
	if c.mode == spec.ConfirmPrompt {
		body = fmt.Sprintf("%s %q — are you sure?\n\n%s%s  %s%s%s",
			c.gerund, c.id,
			bold.Render("y"), subtle.Render(" yes"),
			bold.Render("n"), subtle.Render("/"), bold.Render("enter")+subtle.Render(" cancel"))
	} else {
		body = fmt.Sprintf("%s %s %q\nThis action is not recoverable.\n\nType %q to confirm:\n> %s█",
			c.gerund, c.noun, c.id, c.id, c.typed)
		if c.err != "" {
			body += "\n" + lipgloss.NewStyle().Foreground(lipgloss.Color(tui.CLIError)).Render(c.err)
		}
		body += "\n\n" + subtle.Render("enter confirm · esc cancel")
	}
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(tui.CLIAccent)).
		Padding(1, 3).
		Width(uiConfirmWidth).
		Render(body)
}
