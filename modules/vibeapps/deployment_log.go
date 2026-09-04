// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package vibeapps

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/harness/cli/v3/pkg/client"
	"github.com/harness/cli/v3/pkg/cmdctx"
)

const getVibeappDeploymentLogWorkflowID = "get_vibeapp_deployment_log"
const vibeappRunFollowFnID = "vibeapp_run_follow"

const deploymentLogPollInterval = 3 * time.Second

type deploymentEvent struct {
	StageKey  string `json:"stageKey"`
	Level     string `json:"level"`
	Message   string `json:"message"`
	CreatedAt string `json:"createdAt"`
}

type deploymentError struct {
	StageKey     string `json:"stageKey"`
	WorkloadName string `json:"workloadName"`
	Message      string `json:"message"`
	Remediation  string `json:"remediation"`
}

type deploymentSecurityFinding struct {
	Title       string `json:"title"`
	Severity    string `json:"severity"`
	Remediation string `json:"remediation"`
	Blocking    bool   `json:"blocking"`
}

type deploymentLogView struct {
	ID       string            `json:"id"`
	Status   string            `json:"status"`
	Events   []deploymentEvent `json:"events"`
	Errors   []deploymentError `json:"errors"`
	Security struct {
		Status        string                      `json:"status"`
		BlockingCount int                         `json:"blockingCount"`
		Findings      []deploymentSecurityFinding `json:"findings"`
	} `json:"security"`
	ErrorMessage string `json:"errorMessage"`
}

func lastPart(s string) string {
	if i := strings.LastIndex(s, "/"); i >= 0 {
		return s[i+1:]
	}
	return s
}

func isTerminalDeploymentStatus(status string) bool {
	switch status {
	case "completed", "failed", "canceled":
		return true
	}
	return false
}

func fetchDeploymentLogView(ctx *cmdctx.Ctx, deploymentID string) (*deploymentLogView, error) {
	raw, _, err := client.New(ctx).Get(apiPrefix+"/api/v1/deployments/"+deploymentID, nil)
	if err != nil {
		return nil, fmt.Errorf("fetching deployment %s: %w", deploymentID, err)
	}
	var view deploymentLogView
	if err := decodeInto(raw, &view); err != nil {
		return nil, fmt.Errorf("parsing deployment response: %w", err)
	}
	return &view, nil
}

func printNewEvents(events []deploymentEvent, from int) int {
	for _, e := range events[from:] {
		fmt.Printf("[%s] %-8s %s\n", e.StageKey, strings.ToUpper(e.Level), e.Message)
	}
	return len(events)
}

func printDeploymentSummary(view *deploymentLogView) {
	if len(view.Errors) > 0 {
		fmt.Println("\nErrors:")
		for _, e := range view.Errors {
			fmt.Printf("  [%s/%s] %s\n", e.StageKey, e.WorkloadName, e.Message)
			if e.Remediation != "" {
				fmt.Printf("    remediation: %s\n", e.Remediation)
			}
		}
	}
	if len(view.Security.Findings) > 0 {
		fmt.Printf("\nSecurity (%s, %d blocking):\n", view.Security.Status, view.Security.BlockingCount)
		for _, f := range view.Security.Findings {
			marker := ""
			if f.Blocking {
				marker = " [BLOCKING]"
			}
			fmt.Printf("  [%s]%s %s\n", f.Severity, marker, f.Title)
			if f.Remediation != "" {
				fmt.Printf("    remediation: %s\n", f.Remediation)
			}
		}
	}
	if view.ErrorMessage != "" {
		fmt.Printf("\nerror: %s\n", view.ErrorMessage)
	}
	fmt.Printf("\nstatus: %s\n", view.Status)
}

// getVibeappDeploymentLogWorkflow implements "get vibeapp_deployment:log <app-id>/<deployment-id>
// [--follow]": fetches (or, with --follow, polls and streams) the deployment's events step
// log, errors, and security findings — the single payload with everything needed to debug a
// failed or running deployment.
func getVibeappDeploymentLogWorkflow(ctx *cmdctx.Ctx) error {
	deploymentID := lastPart(ctx.Id)
	if deploymentID == "" {
		return fmt.Errorf("expected <app-id>/<deployment-id> or <deployment-id>")
	}

	follow := cmdctx.GetBool(ctx.FlagValues, "follow")
	_, err := streamDeploymentLog(ctx, deploymentID, follow)
	return err
}

// streamDeploymentLog prints a deployment's log once, or (with follow) polls and streams
// new events until the deployment reaches a terminal state, then prints the summary and
// returns the final view. Callers decide what a terminal "failed" status means for their
// own exit behavior; this never returns a non-nil error for a failed deployment itself,
// only for transport/cancellation failures.
func streamDeploymentLog(ctx *cmdctx.Ctx, deploymentID string, follow bool) (*deploymentLogView, error) {
	view, err := fetchDeploymentLogView(ctx, deploymentID)
	if err != nil {
		return nil, err
	}
	printed := printNewEvents(view.Events, 0)

	if !follow || isTerminalDeploymentStatus(view.Status) {
		printDeploymentSummary(view)
		return view, nil
	}

	ticker := time.NewTicker(deploymentLogPollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Context.Done():
			return nil, fmt.Errorf("canceled while following deployment %s (last status: %s)", deploymentID, view.Status)
		case <-ticker.C:
		}

		view, err = fetchDeploymentLogView(ctx, deploymentID)
		if err != nil {
			return nil, err
		}
		printed = printNewEvents(view.Events, printed)
		if isTerminalDeploymentStatus(view.Status) {
			printDeploymentSummary(view)
			return view, nil
		}
	}
}

// vibeappRunFollowFn is the follow_fn for "execute vibeapp:run --follow": it extracts
// the newly-triggered deployment's id from the response and streams its log to completion.
func vibeappRunFollowFn(ctx *cmdctx.Ctx, result any) error {
	m := asMap(result)
	deploymentID, _ := m["id"].(string)
	if deploymentID == "" {
		return fmt.Errorf("--follow: could not extract deployment ID from response")
	}
	fmt.Fprintln(os.Stderr, "\nFollowing deployment log ...")

	followCtx := *ctx
	fv := make(map[string]any, len(ctx.FlagValues)+1)
	for k, v := range ctx.FlagValues {
		fv[k] = v
	}
	fv["follow"] = true
	followCtx.FlagValues = fv
	followCtx.Id = deploymentID
	return getVibeappDeploymentLogWorkflow(&followCtx)
}
