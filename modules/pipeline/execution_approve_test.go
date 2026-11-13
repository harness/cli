// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package pipeline

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestParseWaitingApprovals(t *testing.T) {
	body := []byte(`[
		{"id":"a1","type":"HarnessApproval","status":"WAITING","details":{"approvalMessage":"ship it?"}},
		{"id":"a2","type":"JiraApproval","status":"WAITING"},
		{"id":"a3","type":"HarnessApproval","status":"APPROVED"}
	]`)
	got, err := parseWaitingApprovals(body)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "a1" || got[0].Message != "ship it?" {
		t.Fatalf("got %+v", got)
	}
}

func TestApprovalModelFlow(t *testing.T) {
	m := newApprovalModel(nil, []waitingApproval{{ID: "a1"}, {ID: "a2"}})
	key := func(s string) tea.KeyPressMsg { return tea.KeyPressMsg{Text: s} }
	step := func(m approvalModel, msg tea.Msg) approvalModel {
		nm, _ := m.Update(msg)
		return nm.(approvalModel)
	}

	m = step(m, key("r"))
	if m.phase != phaseComment || m.action != "REJECT" {
		t.Fatalf("r should start a reject comment, got phase=%v action=%q", m.phase, m.action)
	}
	m = step(m, key("n"))
	m = step(m, tea.KeyPressMsg{Code: tea.KeySpace, Text: " "})
	m = step(m, key("o"))
	m = step(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.phase != phaseConfirm || m.comment != "n o" {
		t.Fatalf("enter should confirm with comment, got phase=%v comment=%q", m.phase, m.comment)
	}
	m = step(m, key("n"))
	if m.phase != phaseSelect {
		t.Fatalf("n should cancel back to select, got %v", m.phase)
	}
}
