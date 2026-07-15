// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package pipeline

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/harness/cli/pkg/console"
	"github.com/harness/cli/pkg/execgraph"
	"github.com/harness/cli/pkg/tui"
)

// hasSaveableContent reports whether the active tab has content available to save
// for the given node.
func (m *logViewModel) hasSaveableContent(node *execgraph.GraphNode) bool {
	switch m.activeTab {
	case tabLogs:
		_, ok := m.logCache[node.UUID]
		return ok
	case tabInputs:
		return len(node.StepParameters) > 0
	case tabOutputs:
		return len(node.Outcomes) > 0
	default:
		return false
	}
}

// saveContent returns the raw text to write for the tab that initiated the save
// (m.saveTab), for the currently selected node.
func (m *logViewModel) saveContent() (string, error) {
	node := m.selectedNode()
	nodeUUID := ""
	if node != nil {
		nodeUUID = node.UUID
	}
	switch m.saveTab {
	case tabInputs:
		if node == nil {
			return "", nil
		}
		var buf bytes.Buffer
		if err := json.Indent(&buf, node.StepParameters, "", "  "); err != nil {
			return "", err
		}
		return buf.String(), nil
	case tabOutputs:
		if node == nil {
			return "", nil
		}
		b, err := json.MarshalIndent(node.Outcomes, "", "  ")
		if err != nil {
			return "", err
		}
		return string(b), nil
	default: // tabLogs
		lc := m.logCache[nodeUUID]
		var raw string
		if lc != nil {
			raw = lc.rendered()
		}
		return console.StripANSI(raw), nil
	}
}

// doSave writes the active tab's content (log, inputs, or outputs) to m.saveInput.
// append=true opens the file in append mode; false truncates/creates. Append is
// only meaningful for logs; callers must not pass true for inputs/outputs.
func (m *logViewModel) doSave(appendMode bool) {
	content, err := m.saveContent()
	if err != nil {
		m.saveStatus = "error: " + err.Error()
		return
	}
	flag := os.O_WRONLY | os.O_CREATE
	if appendMode {
		flag |= os.O_APPEND
	} else {
		flag |= os.O_TRUNC
	}
	f, err := os.OpenFile(m.saveInput, flag, 0o644)
	if err != nil {
		m.saveStatus = "error: " + err.Error()
		return
	}
	_, err = f.WriteString(content)
	f.Close()
	if err != nil {
		m.saveStatus = "error: " + err.Error()
		return
	}
	m.saveStatus = "saved to " + m.saveInput
	m.saveDone = true
}

// overlayCenter composites the modal box over the background by replacing lines
// at the vertical/horizontal center without blanking the rest of the screen.
func overlayCenter(background, box string, w, h int) string {
	bgLines := strings.Split(background, "\n")
	boxLines := strings.Split(box, "\n")

	boxH := len(boxLines)
	boxW := 0
	for _, l := range boxLines {
		if lw := lipgloss.Width(l); lw > boxW {
			boxW = lw
		}
	}

	startRow := (h - boxH) / 2
	startCol := (w - boxW) / 2
	if startCol < 0 {
		startCol = 0
	}

	out := make([]string, len(bgLines))
	copy(out, bgLines)

	for i, boxLine := range boxLines {
		row := startRow + i
		if row < 0 || row >= len(out) {
			continue
		}
		bg := out[row]
		runes := []rune(bg)
		prefix := ""
		if startCol <= len(runes) {
			prefix = string(runes[:startCol])
		} else {
			prefix = bg + strings.Repeat(" ", startCol-len(runes))
		}
		out[row] = prefix + boxLine
	}

	return strings.Join(out, "\n")
}

func (m logViewModel) renderSaveModal() string {
	st := m.st
	var body string

	if m.saveConfirm {
		prompt := st.normal.Render("File already exists:") + " " + st.header.Render(m.saveInput) + "\n\n" +
			st.normal.Render("Overwrite?") + "\n\n" +
			lipgloss.NewStyle().Bold(true).Render("y") + st.dim.Render(" yes  ")
		if m.saveTab == tabLogs {
			prompt += lipgloss.NewStyle().Bold(true).Render("a") + st.dim.Render(" append  ")
		}
		prompt += lipgloss.NewStyle().Bold(true).Render("n") + st.dim.Render("/") +
			lipgloss.NewStyle().Bold(true).Render("esc") + st.dim.Render(" cancel")
		body = prompt
	} else {
		title := "Save log to file"
		switch m.saveTab {
		case tabInputs:
			title = "Save inputs to file"
		case tabOutputs:
			title = "Save outputs to file"
		}
		inputLine := "> " + m.saveInput + "█"
		var statusLine string
		if m.saveStatus != "" {
			if m.saveDone {
				statusLine = "\n" + lipgloss.NewStyle().Foreground(lipgloss.Color(tui.CLISuccess)).Render(m.saveStatus) +
					st.dim.Render("  (enter/esc to close)")
			} else {
				statusLine = "\n" + st.errStyle.Render(m.saveStatus)
			}
		}
		hint := ""
		if !m.saveDone {
			hint = "\n" + st.dim.Render("enter to save · esc to cancel")
		}
		body = st.normal.Render(title) + "\n\n" + inputLine + statusLine + hint
	}

	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(tui.CLIAccent)).
		Padding(1, 3).
		Width(50).
		Render(body)
}
