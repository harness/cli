// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package pipeline

import (
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/harness/cli/v3/pkg/client"
	"github.com/harness/cli/v3/pkg/cmdctx"
	"github.com/harness/cli/v3/pkg/console"
	"github.com/harness/cli/v3/pkg/logstream"
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

// pickApproval resolves a 1-based answer against n choices.
func pickApproval(answer string, n int) (int, error) {
	i, err := strconv.Atoi(strings.TrimSpace(answer))
	if err != nil || i < 1 || i > n {
		return 0, fmt.Errorf("invalid choice %q (expected 1-%d)", answer, n)
	}
	return i - 1, nil
}

func approveExecutionHandler(ctx *cmdctx.Ctx) error {
	ctx.UIWantBack = true

	execID := logstream.ExecIdFromArg([]string{ctx.Id})
	if execID == "" {
		return fmt.Errorf("could not parse execution id from %q", ctx.Id)
	}

	resp, err := client.New(ctx).DoRaw(client.Request{
		Method:      "GET",
		Path:        fmt.Sprintf("/pipeline/api/v1/orgs/%s/projects/%s/approvals/execution/%s", ctx.Auth.OrgID, ctx.Auth.ProjectID, execID),
		QueryParams: map[string]string{"approval_status": "WAITING", "approval_type": "HarnessApproval"},
	})
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("listing approvals failed (%d): %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	waiting, err := parseWaitingApprovals(body)
	if err != nil {
		return err
	}
	if len(waiting) == 0 {
		fmt.Printf("No pending approval for execution %s\n", ctx.Id)
		return nil
	}

	chosen := waiting[0]
	if len(waiting) > 1 {
		for i, w := range waiting {
			fmt.Printf("  %d) %s  %s\n", i+1, w.ID, w.Message)
		}
		answer, err := console.ReadPrompt("Approve which", "")
		if err != nil {
			return err
		}
		idx, err := pickApproval(answer, len(waiting))
		if err != nil {
			return err
		}
		chosen = waiting[idx]
	}

	fmt.Printf("Execution %s\nApproval  %s\n", ctx.Id, chosen.ID)
	if chosen.Message != "" {
		fmt.Printf("Message   %s\n", chosen.Message)
	}
	comment, err := console.ReadPrompt("Comment (optional)", "")
	if err != nil {
		return err
	}
	if !console.PromptYesNo("Approve?") {
		fmt.Println("Cancelled")
		return nil
	}

	ctx.SetFlag("comment", comment)
	reqBody, err := approvalApproveBody(ctx)
	if err != nil {
		return err
	}
	resp, err = client.New(ctx).DoRaw(client.Request{
		Method: "POST",
		Path:   fmt.Sprintf("/pipeline/api/approvals/%s/harness/activity", chosen.ID),
		Body:   reqBody,
	})
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("approve failed (%d): %s", resp.StatusCode, strings.TrimSpace(string(out)))
	}
	fmt.Printf("Approved %s\n", chosen.ID)
	return nil
}
