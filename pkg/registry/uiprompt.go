// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package registry

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/harness/cli/v3/pkg/spec"
	"github.com/harness/cli/v3/pkg/tui"
)

const (
	uiPromptHeight = 6
	// uiPromptChrome is the width taken by the box border and padding.
	uiPromptChrome = 6
)

// uiPrompt is the text box opened by a "prompt" ui_commands entry.
type uiPrompt struct {
	uc    spec.UICommand
	cs    *spec.CommandSpec
	id    string
	input textarea.Model
	busy  bool
	err   string
}

// uiPromptDoneMsg is sent when the target command of a submitted prompt returns.
type uiPromptDoneMsg struct{ err error }

func (m uiTableModel) openPrompt(uc spec.UICommand) (uiTableModel, tea.Cmd) {
	var cs *spec.CommandSpec
	if m.ctx.Resolver != nil {
		cs = m.ctx.Resolver.GetSpec(uc.Verb, uc.Noun)
	}
	if cs == nil || cs.Endpoint == nil {
		m.detail.err = fmt.Sprintf("%s %s is not an endpoint command; cannot prompt", uc.Verb, uc.Noun)
		return m, nil
	}
	in := textarea.New()
	in.ShowLineNumbers = false
	in.Prompt = ""
	in.SetWidth(max(m.width-uiPromptChrome, 20))
	in.SetHeight(uiPromptHeight)
	in.Focus()
	m.prompt = &uiPrompt{uc: uc, cs: cs, id: m.detail.id, input: in}
	return m, nil
}

func (m uiTableModel) handlePromptKey(msg tea.KeyPressMsg) (uiTableModel, tea.Cmd) {
	p := m.prompt
	key := msg.String()
	if key == "ctrl+c" {
		return m, tea.Quit
	}
	if p.busy {
		return m, nil
	}
	switch key {
	case "esc":
		m.prompt = nil
		return m, nil
	case "ctrl+s":
		text := strings.TrimSpace(p.input.Value())
		if text == "" {
			p.err = "text is required"
			return m, nil
		}
		p.busy = true
		p.err = ""
		return m, m.submitPrompt(text)
	}
	p.err = ""
	var cmd tea.Cmd
	p.input, cmd = p.input.Update(msg)
	return m, cmd
}

func (m uiTableModel) submitPrompt(text string) tea.Cmd {
	ctx, p := m.ctx, m.prompt
	return func() tea.Msg {
		pctx, err := buildDetailCtx(ctx, p.cs, p.id)
		if err != nil {
			return uiPromptDoneMsg{err: err}
		}
		if p.cs.RequiresParentId {
			pctx.ParentId, pctx.Id = p.id, ""
		}
		pctx.FlagValues[p.uc.InputFlag] = text
		pctx.ProvidedFlags[p.uc.InputFlag] = true
		_, err = CallEndpoint(pctx, p.cs.Endpoint)
		return uiPromptDoneMsg{err: err}
	}
}

// finishPrompt closes the box and refreshes the detail on success; on failure it
// keeps the box open with the typed text.
func (m uiTableModel) finishPrompt(msg uiPromptDoneMsg) (uiTableModel, tea.Cmd) {
	if m.prompt == nil {
		return m, nil
	}
	if msg.err != nil {
		m.prompt.busy = false
		m.prompt.err = msg.err.Error()
		return m, nil
	}
	m.prompt = nil
	m.detail.loading = true
	m.detail.err = ""
	return m, m.fetchDetail(m.detail.id)
}

func (m uiTableModel) promptView() string {
	p := m.prompt
	accent := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(tui.CLIAccent))
	subtle := lipgloss.NewStyle().Foreground(lipgloss.Color(tui.CLITextMuted))

	noun := strings.ReplaceAll(m.activeDetailCs().Noun, "_", " ")
	var b strings.Builder
	b.WriteString(accent.Render(tui.TruncateANSI(fmt.Sprintf("get %s  %s", noun, p.id), m.width-1)) + "\n\n")

	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(tui.CLIAccent)).
		Padding(0, 1).
		Render(accent.Render(uiCommandHintLabel(p.uc)) + "\n" + p.input.View())
	b.WriteString(box + "\n\n")

	switch {
	case p.busy:
		b.WriteString(subtle.Render("  Submitting…") + "\n")
	case p.err != "":
		b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color(tui.CLIError)).Render("  error: "+p.err) + "\n")
		b.WriteString(subtle.Render("  ctrl+s retry  esc cancel") + "\n")
	default:
		b.WriteString(subtle.Render("  ctrl+s submit  esc cancel") + "\n")
	}
	return b.String()
}
