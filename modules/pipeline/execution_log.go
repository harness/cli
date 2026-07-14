// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package pipeline

import (
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/spf13/pflag"

	"github.com/harness/cli/pkg/cmdctx"
	"github.com/harness/cli/pkg/console"
	"github.com/harness/cli/pkg/format"
	"github.com/harness/cli/pkg/logstream"
	"github.com/harness/cli/pkg/spec"
)

const listExecutionLogsFetchFnID = "list_execution_logs_fetch"
const getPipelineLogHandlerID = "get_pipeline_log"
const pipelineLogStageCompletionID = "pipeline_log_stage_completion"
const pipelineLogStepCompletionID = "pipeline_log_step_completion"

func getPipelineLogHandler(ctx *cmdctx.Ctx) error {
	if ctx.Id == "" {
		return fmt.Errorf("missing required argument <[pipeline/]execId>")
	}

	execId := logstream.ExecIdFromArg([]string{ctx.Id})
	if execId == "" {
		return fmt.Errorf("could not parse execId from %q", ctx.Id)
	}

	if cmdctx.GetBool(ctx.FlagValues, "ui") {
		if !console.IsBothTTY() {
			return fmt.Errorf("--ui requires an interactive terminal (both stdin and stdout must be a TTY)")
		}
		stg := cmdctx.GetString(ctx.FlagValues, "stage")
		stp := cmdctx.GetString(ctx.FlagValues, "step")
		if stg != "" || stp != "" {
			return fmt.Errorf("--ui is not compatible with --stage or --step")
		}
		return RunLogViewer(execLabelFromID(ctx.Id), ctx)
	}

	stageFlag := cmdctx.GetString(ctx.FlagValues, "stage")
	stepFlag := cmdctx.GetString(ctx.FlagValues, "step")
	if stepFlag != "" && stageFlag == "" {
		return fmt.Errorf("--step requires --stage")
	}

	if cmdctx.GetBool(ctx.FlagValues, "follow") {
		style := logstream.ParseMultiStyle(cmdctx.GetString(ctx.FlagValues, "format-multi"))
		return logstream.FollowMulti(ctx, execId, stageFlag, stepFlag, style, nil)
	}

	a := ctx.Auth
	hc := &http.Client{Timeout: 30 * time.Second}
	fmtFlag := ctx.FormatFlags.Format
	prefixFlag := cmdctx.GetString(ctx.FlagValues, "logkey-prefix")
	logkeyFlag := cmdctx.GetString(ctx.FlagValues, "logkey")

	w, closeW, err := format.OpenWriter(ctx.FormatFlags.OutFile)
	if err != nil {
		return err
	}
	defer closeW()

	if logkeyFlag != "" {
		hasContent, fetchErr := logstream.FetchAndPrintLog(hc, a, logkeyFlag, fmtFlag, ctx.IsPty, w)
		if fetchErr != nil {
			return fetchErr
		}
		if !hasContent {
			fmt.Fprintf(os.Stderr, "no log content for key %q\n", logkeyFlag)
		}
		return nil
	}

	entries, _, err := logstream.FetchLogKeys(ctx, execId)
	if err != nil {
		return err
	}

	type result struct {
		name string
		body string
		err  error
	}
	var results []result
	for _, e := range entries {
		if prefixFlag != "" && !strings.HasPrefix(e.LogKey, prefixFlag) {
			continue
		}
		if stageFlag != "" && !strings.EqualFold(e.ParentName, stageFlag) && !strings.EqualFold(e.Name, stageFlag) {
			continue
		}
		if stepFlag != "" && !strings.EqualFold(e.Name, stepFlag) {
			continue
		}
		label := e.FQN
		if label == "" {
			label = e.LogKey
		}
		var buf strings.Builder
		hasContent, fetchErr := logstream.FetchAndPrintLog(hc, a, e.LogKey, fmtFlag, ctx.IsPty, &buf)
		if fetchErr != nil {
			results = append(results, result{name: label, err: fetchErr})
			continue
		}
		if !hasContent {
			continue
		}
		results = append(results, result{name: label, body: buf.String()})
	}

	if len(results) == 0 {
		if prefixFlag != "" {
			return fmt.Errorf("no log keys matched prefix %q", prefixFlag)
		}
		if stageFlag != "" {
			return fmt.Errorf("no logs found for stage %q", stageFlag)
		}
		fmt.Fprintf(os.Stderr, "no log content for execution %q\n", execId)
		return nil
	}

	multiLog := len(results) > 1
	for _, r := range results {
		if multiLog {
			fmt.Fprintf(w, "\n== %s ==\n", r.name)
		}
		if r.err != nil {
			fmt.Fprintf(w, "(error: %v)\n", r.err)
			continue
		}
		fmt.Fprint(w, r.body)
	}
	return nil
}

func pipelineLogStageCompletion(ctx *cmdctx.Ctx, args []string, flags *pflag.FlagSet) ([]string, error) {
	execId := logstream.ExecIdFromArg(args)
	if execId == "" {
		return nil, nil
	}
	entries, _, err := logstream.FetchLogKeys(ctx, execId)
	if err != nil {
		return nil, err
	}
	seen := make(map[string]bool)
	var stages []string
	for _, e := range entries {
		if e.Depth == 1 && !seen[e.Name] {
			seen[e.Name] = true
			stages = append(stages, e.Name)
		}
	}
	return stages, nil
}

func pipelineLogStepCompletion(ctx *cmdctx.Ctx, args []string, flags *pflag.FlagSet) ([]string, error) {
	stage, _ := flags.GetString("stage")
	if stage == "" {
		return nil, nil
	}
	execId := logstream.ExecIdFromArg(args)
	if execId == "" {
		return nil, nil
	}
	entries, _, err := logstream.FetchLogKeys(ctx, execId)
	if err != nil {
		return nil, err
	}
	seen := make(map[string]bool)
	var steps []string
	for _, e := range entries {
		if strings.EqualFold(e.ParentName, stage) && !seen[e.Name] {
			seen[e.Name] = true
			steps = append(steps, e.Name)
		}
	}
	return steps, nil
}

func listExecutionLogsFetchFn(ctx *cmdctx.Ctx, _ *spec.EndpointSpec, _, _ int, _ any) (*cmdctx.PageResult, error) {
	if ctx.ParentId == "" {
		return nil, fmt.Errorf("missing required argument <execution-id>")
	}
	execId := ctx.ParentId
	if i := strings.LastIndex(execId, "/"); i >= 0 {
		execId = execId[i+1:]
	}

	entries, _, err := logstream.FetchLogKeys(ctx, execId)
	if err != nil {
		return nil, err
	}

	rows := make([]any, len(entries))
	for i, e := range entries {
		rows[i] = map[string]any{"log_key": e.LogKey, "name": e.Name, "fqn": e.FQN, "status": e.Status}
	}
	return &cmdctx.PageResult{
		Items:       rows,
		StartOffset: 0,
		Last:        true,
		HasTotal:    true,
		Total:       int64(len(rows)),
	}, nil
}
