// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package pipeline

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/harness/cli/pkg/client"
	"github.com/harness/cli/pkg/cmdctx"
	"github.com/harness/cli/pkg/endpoint"
	"github.com/harness/cli/pkg/spec"
)

const listApprovalInstancesFetchFnID = "list_approval_instances_fetch"
const approveBodyFnID = "approval_approve_body"

// approvalApproveBody builds the Harness approval activity body for an APPROVE
// action: {action, comments, approverInputs?}. approverInputs is parsed from
// repeatable --approver-input key=value flags (the array shape can't be built
// by the spec body_params expr, which type-checks against a typed env).
func approvalApproveBody(ctx *cmdctx.Ctx) (any, error) {
	body := map[string]any{
		"action":   "APPROVE",
		"comments": cmdctx.GetString(ctx.FlagValues, "comment"),
	}
	pairs := cmdctx.GetStringSlice(ctx.FlagValues, "approver-input")
	if len(pairs) > 0 {
		inputs := make([]map[string]any, 0, len(pairs))
		for _, p := range pairs {
			name, value, _ := strings.Cut(p, "=")
			inputs = append(inputs, map[string]any{"name": name, "value": value})
		}
		body["approverInputs"] = inputs
	}
	return body, nil
}

// listApprovalInstancesFetchFn runs the normal approval-list fetch, then stamps
// each item with pipelineIdentifier (which the approvals API does not return).
// The pipeline id comes from the "<pipeline>/<execution>" arg prefix when
// present; otherwise a single execution-summary lookup resolves it. Resolution
// fails soft — a blank pipeline column is preferable to failing the whole list.
func listApprovalInstancesFetchFn(ctx *cmdctx.Ctx, ep *spec.EndpointSpec, wantStart, wantCount int, cursor any) (*cmdctx.PageResult, error) {
	parent := ctx.ParentId
	pipelineID, execID := "", parent
	if i := strings.LastIndex(parent, "/"); i >= 0 {
		pipelineID, execID = parent[:i], parent[i+1:]
	}

	result, err := endpoint.HTTPFetchFn(ctx, ep, wantStart, wantCount, cursor)
	if err != nil {
		return nil, err
	}

	if pipelineID == "" {
		pipelineID = resolvePipelineID(ctx, execID)
	}
	if pipelineID != "" {
		for i, raw := range result.Items {
			if m, ok := raw.(map[string]any); ok {
				m["pipelineIdentifier"] = pipelineID
				result.Items[i] = m
			}
		}
	}
	return result, nil
}

// resolvePipelineID returns the pipeline identifier for an execution via the
// execution-summary endpoint, or "" on any error.
func resolvePipelineID(ctx *cmdctx.Ctx, execID string) string {
	if execID == "" {
		return ""
	}
	resp, err := client.New(ctx).DoRaw(client.Request{
		Method: "GET",
		Path:   fmt.Sprintf("/pipeline/api/pipelines/execution/v2/%s", execID),
		QueryParams: map[string]string{
			"orgIdentifier":     ctx.Auth.OrgID,
			"projectIdentifier": ctx.Auth.ProjectID,
		},
	})
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return ""
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return ""
	}
	var env struct {
		Data struct {
			PipelineExecutionSummary struct {
				PipelineIdentifier string `json:"pipelineIdentifier"`
			} `json:"pipelineExecutionSummary"`
		} `json:"data"`
	}
	if json.Unmarshal(body, &env) != nil {
		return ""
	}
	return env.Data.PipelineExecutionSummary.PipelineIdentifier
}
