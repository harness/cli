// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package annotations

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/harness/cli/v3/pkg/cmdctx"
	"github.com/harness/cli/v3/pkg/hbase"
)

const HandlerID = "execute_annotation"

// Handler is the `execute annotation` workflow handler. It is
// a silent no-op unless CI_ENABLE_HARNESS_ANNOTATIONS or PIPE_HARNESS_ANNOTATIONS
// is set to "true" — the real caller is a pipeline step script that should never
// fail just because this feature isn't enabled on its account.
func Handler(ctx *cmdctx.Ctx) error {
	if !isAnnotationsEnabled() {
		fmt.Fprintln(os.Stderr, "Annotations feature flag is disabled. Please reach out to Harness Team to get it enabled")
		return nil
	}

	if len(ctx.Args) > 0 {
		return fmt.Errorf("unexpected arguments: %s", strings.Join(ctx.Args, " "))
	}

	style := strings.ToLower(strings.TrimSpace(cmdctx.GetString(ctx.FlagValues, "style")))
	switch style {
	case "info", "success", "warning", "error":
	default:
		return fmt.Errorf("--style must be one of: info, success, warning, error")
	}

	mode := strings.ToLower(strings.TrimSpace(cmdctx.GetString(ctx.FlagValues, "mode")))
	switch mode {
	case "append", "replace", "delete":
	default:
		return fmt.Errorf("--mode must be one of: append, replace, delete")
	}

	priority := cmdctx.GetInt(ctx.FlagValues, "priority")
	if priority <= 0 || priority > 10 {
		return fmt.Errorf("--priority must be between 1 and 10")
	}

	contextID := strings.TrimSpace(cmdctx.GetString(ctx.FlagValues, "context"))
	if r := []rune(contextID); len(r) > 256 {
		contextID = string(r[:256])
	}
	if contextID == "" {
		return fmt.Errorf("--context is required")
	}

	var summary string
	if summaryFile := strings.TrimSpace(cmdctx.GetString(ctx.FlagValues, "summary-file")); summaryFile != "" {
		sc, err := readSummaryFile(summaryFile)
		if err != nil {
			return err
		}
		summary = truncateUTF8ByBytes(sc, maxSummaryFileBytes)
	} else {
		summary = truncateUTF8ByBytes(cmdctx.GetString(ctx.FlagValues, "summary"), maxSummaryFileBytes)
	}

	path := os.Getenv(envAnnotFile)
	if path == "" {
		path = "annotations.json"
	}

	env, err := loadEnvelope(path)
	if err != nil {
		return fmt.Errorf("annotation failed: %w", err)
	}

	env, result := annotate(env, annotationInput{
		ContextID:        contextID,
		Style:            style,
		Summary:          summary,
		Mode:             mode,
		Priority:         priority,
		StepID:           os.Getenv(envStepID),
		AccountID:        os.Getenv(hbase.EnvAccountID),
		OrgID:            os.Getenv(hbase.EnvOrgID),
		ProjectID:        os.Getenv(hbase.EnvProjectID),
		PipelineID:       os.Getenv(hbase.EnvPipelineID),
		PlanExecutionID:  os.Getenv(envExecutionID),
		StageExecutionID: os.Getenv(envStageID),
	})

	if err := saveEnvelope(path, env); err != nil {
		return fmt.Errorf("annotation failed: %w", err)
	}

	resultJSON, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(resultJSON))
	return nil
}
