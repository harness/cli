// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package pipeline

import (
	"fmt"
	"io"

	"github.com/harness/harness-cli/pkg/cmdctx"
	"github.com/harness/harness-cli/pkg/execgraph"
	"github.com/harness/harness-cli/pkg/format"
	"github.com/harness/harness-cli/pkg/registry"
)

// ModuleInit registers pipeline workflows and formatters. Commands are declared in pipeline.spec.yaml.
func ModuleInit(reg registry.ModuleRegistrar) {
	reg.RegisterFetchFn(listExecutionLogsFetchFnID, listExecutionLogsFetchFn)
	reg.RegisterWorkflow(getPipelineLogHandlerID, getPipelineLogHandler)
	reg.RegisterBodyFn(executePipelineBodyFnID, executePipelineBody)
	reg.RegisterBodyFn(executeInputSetBodyFnID, executeInputSetBody)
	reg.RegisterBodyFn(executeDynamicBodyFnID, executeDynamicBody)
	reg.RegisterFollowFn(executeFollowFnID, executeFollowFn)
	reg.RegisterTextFormatter("execution_detail", formatGetExecution)
	reg.RegisterFetchFn(listExecutionStepsFetchFnID, listExecutionStepsFetchFn)
	reg.RegisterFetchFn(listApprovalInstancesFetchFnID, listApprovalInstancesFetchFn)
	reg.RegisterBodyFn(approveBodyFnID, approvalApproveBody)
	reg.RegisterFlagCompletionFn(pipelineLogStageCompletionID, pipelineLogStageCompletion)
	reg.RegisterFlagCompletionFn(pipelineLogStepCompletionID, pipelineLogStepCompletion)
	reg.RegisterEndpointValidatorFn(validatePipelineCreateID, validatePipelineCreate)
}

func formatGetExecution(w io.Writer, d cmdctx.DataAccessor) error {
	s := "it.pipelineExecutionSummary"
	status := d.GetString(s + ".status")
	failureMsg := d.GetString(s + ".failureInfo.message")

	format.WriteLabeledValues(w, []format.LabeledValue{
		{Label: "ExecutionId", Value: d.GetString(s + ".planExecutionId")},
		{Label: "Pipeline", Value: d.GetString(s + ".pipelineIdentifier")},
		{Label: "Name", Value: d.GetString(s + ".name")},
		{Label: "Run #", Value: fmt.Sprintf("%d", d.GetInt64(s+".runSequence"))},
		{Label: "Status", Value: status},
		{Label: "Trigger", Value: d.GetString(s + ".executionTriggerInfo.triggerType")},
		{Label: "TriggeredBy", Value: d.GetString(s + ".executionTriggerInfo.triggeredBy.identifier")},
		{Label: "Started", Value: d.GetTs(s + ".startTs")},
		{Label: "Ended", Value: d.GetTs(s + ".endTs")},
		{Label: "Stages", Value: fmt.Sprintf("%d total, %d success, %d failed",
			d.GetInt64(s+".totalStagesCount"),
			d.GetInt64(s+".successfulStagesCount"),
			d.GetInt64(s+".failedStagesCount"),
		)},
	})
	if failureMsg != "" {
		fmt.Fprintf(w, "\nError:  %s\n", failureMsg)
	}

	execID := d.GetString(s + ".planExecutionId")
	pipelineID := d.GetString(s + ".pipelineIdentifier")
	ref := execID
	if pipelineID != "" && execID != "" {
		ref = pipelineID + "/" + execID
	}

	if g, err := reUnmarshal[executionGraphEnvelope](d.GetData()); err == nil && g.ExecutionGraph.RootNodeID != "" {
		fmt.Fprintf(w, "\n")
		printExecutionTree(w, g.ExecutionGraph)
	}

	if ref != "" {
		fmt.Fprintf(w, "\nDig deeper:\n")
		fmt.Fprintf(w, "  harness list execution_step %s\n", ref)
		fmt.Fprintf(w, "  harness get execution_log %s\n", ref)
	}

	if u := d.GetString("url(it)"); u != "" {
		fmt.Fprintf(w, "\n%s\n", u)
	}
	fmt.Fprintf(w, "\n")
	return nil
}

type executionGraphEnvelope struct {
	ExecutionGraph execgraph.ExecutionGraph `json:"executionGraph"`
}

