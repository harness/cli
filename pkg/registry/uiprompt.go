// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package registry

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/harness/cli/v3/pkg/cmdctx"
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
}

// uiPromptRun is a submitted prompt, run by finishUIExit once the TUI has quit.
type uiPromptRun struct {
	uc   spec.UICommand
	cs   *spec.CommandSpec
	id   string
	text string
}

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
	switch msg.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "esc":
		m.prompt = nil
		return m, nil
	case "ctrl+s":
		m.promptRun = &uiPromptRun{uc: p.uc, cs: p.cs, id: p.id, text: p.input.Value()}
		m.prompt = nil
		return m, tea.Quit
	}
	var cmd tea.Cmd
	p.input, cmd = p.input.Update(msg)
	return m, cmd
}

// promptFlagValues builds the target's flag values: spec defaults first, then
// the typed text under input_flag.
func promptFlagValues(cs *spec.CommandSpec, inputFlag, text string) (map[string]any, map[string]bool) {
	fv := make(map[string]any, len(cs.Flags))
	provided := make(map[string]bool, len(cs.Flags))
	for _, f := range cs.Flags {
		switch {
		case f.IsBool:
			fv[f.Name] = false
		case f.IsMulti:
			fv[f.Name] = []string{}
		case f.IsArray:
			fv[f.Name] = parseArrayFlag(f.Default)
		default:
			fv[f.Name] = f.Default
		}
		provided[f.Name] = false
	}
	for _, f := range cs.Flags {
		if f.Name != inputFlag {
			continue
		}
		switch {
		case f.IsMulti:
			fv[f.Name] = []string{text}
		case f.IsArray:
			fv[f.Name] = parseArrayFlag(text)
		default:
			fv[f.Name] = text
		}
		provided[f.Name] = true
	}
	if cs.ConfirmMode == spec.ConfirmPrompt {
		fv["force"] = true
	}
	return fv, provided
}

// runPromptCommand runs the prompt's target as a link-style hop with the typed
// text, through the same confirm gate and error reporting as the CLI.
func runPromptCommand(ctx *cmdctx.Ctx, run *uiPromptRun) error {
	cs := run.cs
	fv, provided := promptFlagValues(cs, run.uc.InputFlag, run.text)
	profile, org, project := scopeFromCtx(ctx)
	link := &cmdctx.UILink{
		Verb: cs.Verb, Noun: cs.Noun, Id: run.id, Level: ctx.Level,
		Profile: profile, Org: org, Project: project,
		FlagValues: fv, ProvidedFlags: provided,
		Screen: cmdctx.ScreenDetailForGet,
	}
	if cs.RequiresParentId {
		link.Screen = cmdctx.ScreenTable
	}
	pctx, err := buildLinkCtx(ctx, link, cs)
	if err != nil {
		return err
	}
	if err := resolveFlagValues(pctx, cs); err != nil {
		return err
	}
	for _, f := range cs.Flags {
		if !f.Required || f.Name == run.uc.InputFlag {
			continue
		}
		if v, _ := pctx.FlagValues[f.Name].(string); v == "" && !f.IsBool && !f.IsMulti && !f.IsArray {
			return fmt.Errorf("flag --%s is required", f.Name)
		}
	}
	runner, ok := ctx.Resolver.(interface {
		RunCommandCtx(*cmdctx.Ctx, *spec.CommandSpec) error
	})
	if !ok {
		return fmt.Errorf("resolver cannot run commands")
	}
	return runner.RunCommandCtx(pctx, cs)
}

// finishPromptRun runs a submitted prompt and goes back a screen on success. On
// failure it shows an error page whose "b" returns to the screen the prompt was
// opened from.
func finishPromptRun(ctx *cmdctx.Ctx, run *uiPromptRun) error {
	if err := runPromptCommand(ctx, run); err != nil {
		back, perr := showPromptError(run, err)
		if perr != nil || !back {
			return perr
		}
	}
	if link, ok := ctx.PopUILink(); ok {
		return dispatchLink(ctx, &link)
	}
	return nil
}

type uiErrorModel struct {
	title, msg string
	back       bool
}

func (m uiErrorModel) Init() tea.Cmd { return nil }

func (m uiErrorModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if k, ok := msg.(tea.KeyPressMsg); ok {
		switch k.String() {
		case "b":
			m.back = true
			return m, tea.Quit
		case "q", "esc", "ctrl+c":
			return m, tea.Quit
		}
	}
	return m, nil
}

func (m uiErrorModel) View() tea.View {
	accent := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(tui.CLIAccent))
	subtle := lipgloss.NewStyle().Foreground(lipgloss.Color(tui.CLITextMuted))
	var b strings.Builder
	b.WriteString(accent.Render(m.title) + "\n\n")
	b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color(tui.CLIError)).Render("error: "+m.msg) + "\n\n")
	b.WriteString(subtle.Render("  b back  q quit") + "\n")
	v := tea.NewView(b.String())
	v.AltScreen = true
	return v
}

func showPromptError(run *uiPromptRun, err error) (back bool, _ error) {
	m := uiErrorModel{title: fmt.Sprintf("%s %s  %s", run.uc.Verb, strings.ReplaceAll(run.uc.Noun, "_", " "), run.id), msg: err.Error()}
	final, rerr := tea.NewProgram(m).Run()
	if rerr != nil {
		return false, rerr
	}
	fm, ok := final.(uiErrorModel)
	return ok && fm.back, nil
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
	submit := "submit"
	if p.cs.ConfirmMode == spec.ConfirmPrompt {
		submit = p.uc.Label
	}
	b.WriteString(subtle.Render("  ctrl+s "+submit+"  esc cancel") + "\n")
	return b.String()
}
