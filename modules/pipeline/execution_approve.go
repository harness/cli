// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package pipeline

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"unicode"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/harness/cli/v3/pkg/client"
	"github.com/harness/cli/v3/pkg/cmdctx"
	"github.com/harness/cli/v3/pkg/console"
	"github.com/harness/cli/v3/pkg/logstream"
	"github.com/harness/cli/v3/pkg/tui"
)

const approveExecutionHandlerID = "approve_execution"

type waitingApproval struct {
	ID      string
	Message string
}

// parseWaitingApprovals returns the WAITING HarnessApproval instances from an approvals list body.
func parseWaitingApprovals(body []byte) ([]waitingApproval, error) {
	var items []struct {
		ID      string `json:"id"`
		Type    string `json:"type"`
		Status  string `json:"status"`
		Details struct {
			ApprovalMessage string `json:"approvalMessage"`
		} `json:"details"`
	}
	if err := json.Unmarshal(body, &items); err != nil {
		return nil, fmt.Errorf("parsing approvals: %w", err)
	}
	var out []waitingApproval
	for _, it := range items {
		if it.Status == "WAITING" && it.Type == "HarnessApproval" {
			out = append(out, waitingApproval{ID: it.ID, Message: it.Details.ApprovalMessage})
		}
	}
	return out, nil
}

func listWaitingApprovals(ctx *cmdctx.Ctx, execID string) ([]waitingApproval, error) {
	resp, err := client.New(ctx).DoRaw(client.Request{
		Method:      "GET",
		Path:        fmt.Sprintf("/pipeline/api/v1/orgs/%s/projects/%s/approvals/execution/%s", ctx.Auth.OrgID, ctx.Auth.ProjectID, execID),
		QueryParams: map[string]string{"approval_status": "WAITING", "approval_type": "HarnessApproval"},
	})
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("listing approvals failed (%d): %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return parseWaitingApprovals(body)
}

func submitApproval(ctx *cmdctx.Ctx, id, action, comment string) error {
	resp, err := client.New(ctx).DoRaw(client.Request{
		Method: "POST",
		Path:   fmt.Sprintf("/pipeline/api/approvals/%s/harness/activity", id),
		Body:   map[string]any{"action": action, "comments": comment},
	})
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("%s failed (%d): %s", strings.ToLower(action), resp.StatusCode, strings.TrimSpace(string(out)))
	}
	return nil
}

func approveExecutionHandler(ctx *cmdctx.Ctx) error {
	ctx.UIWantBack = true
	if !console.IsBothTTY() {
		return fmt.Errorf("approve requires an interactive terminal (TTY)")
	}
	execID := logstream.ExecIdFromArg([]string{ctx.Id})
	if execID == "" {
		return fmt.Errorf("could not parse execution id from %q", ctx.Id)
	}
	waiting, err := listWaitingApprovals(ctx, execID)
	if err != nil {
		return err
	}
	if len(waiting) == 0 {
		fmt.Printf("No pending approval for execution %s\n", ctx.Id)
		return nil
	}
	_, err = tea.NewProgram(newApprovalModel(ctx, waiting)).Run()
	return err
}

type approvalPhase int

const (
	phaseSelect approvalPhase = iota
	phaseComment
	phaseConfirm
	phaseSubmitting
	phaseDone
)

type approvalDoneMsg struct{ err error }

type approvalModel struct {
	ctx     *cmdctx.Ctx
	items   []waitingApproval
	cursor  int
	phase   approvalPhase
	action  string // APPROVE or REJECT
	comment string
	result  string
	failed  bool
}

func newApprovalModel(ctx *cmdctx.Ctx, items []waitingApproval) approvalModel {
	return approvalModel{ctx: ctx, items: items}
}

func (m approvalModel) Init() tea.Cmd { return nil }

func (m approvalModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case approvalDoneMsg:
		m.phase = phaseDone
		if msg.err != nil {
			m.failed = true
			m.result = msg.err.Error()
		} else {
			m.result = fmt.Sprintf("%s submitted for %s", strings.Title(strings.ToLower(m.action)), m.items[m.cursor].ID) //nolint:staticcheck
		}
		return m, nil

	case tea.KeyPressMsg:
		key := msg.String()
		if key == "ctrl+c" {
			return m, tea.Quit
		}
		switch m.phase {
		case phaseSelect:
			switch key {
			case "q", "esc":
				return m, tea.Quit
			case "up", "k":
				if m.cursor > 0 {
					m.cursor--
				}
			case "down", "j":
				if m.cursor < len(m.items)-1 {
					m.cursor++
				}
			case "a":
				m.action, m.phase = "APPROVE", phaseComment
			case "r":
				m.action, m.phase = "REJECT", phaseComment
			}
		case phaseComment:
			switch key {
			case "esc":
				m.phase, m.comment = phaseSelect, ""
			case "enter":
				m.phase = phaseConfirm
			case "backspace", "ctrl+h":
				if r := []rune(m.comment); len(r) > 0 {
					m.comment = string(r[:len(r)-1])
				}
			default:
				if r := []rune(key); len(r) == 1 && unicode.IsPrint(r[0]) {
					m.comment += key
				}
			}
		case phaseConfirm:
			switch key {
			case "y":
				m.phase = phaseSubmitting
				ctx, id, action, comment := m.ctx, m.items[m.cursor].ID, m.action, m.comment
				return m, func() tea.Msg { return approvalDoneMsg{submitApproval(ctx, id, action, comment)} }
			case "n", "esc":
				m.phase = phaseSelect
			}
		case phaseDone:
			return m, tea.Quit
		}
	}
	return m, nil
}

func (m approvalModel) View() tea.View {
	title := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(tui.CLIAccent))
	muted := lipgloss.NewStyle().Foreground(lipgloss.Color(tui.CLITextMuted))
	accent := lipgloss.NewStyle().Foreground(lipgloss.Color(tui.CLIAccent)).Bold(true)

	var b strings.Builder
	b.WriteString(title.Render("approvals  "+m.ctx.Id) + "\n\n")
	for i, it := range m.items {
		line := fmt.Sprintf("  %s  %s", it.ID, it.Message)
		if i == m.cursor {
			line = accent.Render("> " + strings.TrimPrefix(line, "  "))
		}
		b.WriteString(line + "\n")
	}
	b.WriteString("\n")

	verb := strings.ToLower(m.action)
	switch m.phase {
	case phaseSelect:
		b.WriteString(muted.Render("  ↑↓ select  a approve  r reject  esc back") + "\n")
	case phaseComment:
		b.WriteString(fmt.Sprintf("  Comment for %s (optional): %s%s\n", verb, m.comment, accent.Render("█")))
		b.WriteString(muted.Render("  enter continue  esc cancel") + "\n")
	case phaseConfirm:
		b.WriteString(fmt.Sprintf("  %s %s? ", strings.ToUpper(verb[:1])+verb[1:], m.items[m.cursor].ID))
		b.WriteString(muted.Render("y confirm  n cancel") + "\n")
	case phaseSubmitting:
		b.WriteString(muted.Render("  Submitting…") + "\n")
	case phaseDone:
		style := lipgloss.NewStyle().Foreground(lipgloss.Color(tui.CLISuccess))
		if m.failed {
			style = lipgloss.NewStyle().Foreground(lipgloss.Color(tui.CLIError))
		}
		b.WriteString("  " + style.Render(m.result) + "\n")
		b.WriteString(muted.Render("  any key to go back") + "\n")
	}
	v := tea.NewView(b.String())
	v.AltScreen = true
	return v
}
