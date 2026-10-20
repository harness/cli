// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package ci

import (
	"fmt"
	"strings"

	"github.com/harness/cli/v3/pkg/cmdctx"
)

const executeCoverageAnalyzeHandlerID = "execute_coverage_analyze"

// executeCoverageAnalyzeHandler is the `execute coverage:analyze` workflow
// handler. It is local-only: it never calls the Harness API and requires no
// authentication.
func executeCoverageAnalyzeHandler(ctx *cmdctx.Ctx) error {
	if len(ctx.Args) > 0 {
		return fmt.Errorf("unexpected arguments: %s", strings.Join(ctx.Args, " "))
	}

	files := cmdctx.GetStringSlice(ctx.FlagValues, "file")
	excludePatterns := cmdctx.GetStringSlice(ctx.FlagValues, "exclude")
	patch := cmdctx.GetBool(ctx.FlagValues, "patch")
	base := cmdctx.GetString(ctx.FlagValues, "base")
	head := cmdctx.GetString(ctx.FlagValues, "head")

	stats, err := coverageAnalyze(files, excludePatterns, patch, base, head)
	if err != nil {
		return err
	}

	coverageDisplayStats(stats)
	return nil
}

const executeCoverageUploadHandlerID = "execute_coverage_upload"

// executeCoverageUploadHandler is the `execute coverage:upload` workflow
// handler. It calls the Harness coverage service to ensure the repository is
// registered, resolve exclude settings, and upload the parsed/encoded report.
func executeCoverageUploadHandler(ctx *cmdctx.Ctx) error {
	if len(ctx.Args) > 0 {
		return fmt.Errorf("unexpected arguments: %s", strings.Join(ctx.Args, " "))
	}

	f := coverageUploadFlags{
		accountID:       cmdctx.GetString(ctx.FlagValues, "account"),
		provider:        cmdctx.GetString(ctx.FlagValues, "provider"),
		owner:           cmdctx.GetString(ctx.FlagValues, "owner"),
		identifier:      cmdctx.GetString(ctx.FlagValues, "identifier"),
		commitID:        cmdctx.GetString(ctx.FlagValues, "commit"),
		branch:          cmdctx.GetString(ctx.FlagValues, "branch"),
		buildID:         cmdctx.GetInt(ctx.FlagValues, "build"),
		pipelineID:      cmdctx.GetString(ctx.FlagValues, "pipeline"),
		executionID:     cmdctx.GetString(ctx.FlagValues, "execution"),
		pullReqID:       int64(cmdctx.GetInt(ctx.FlagValues, "pr")),
		targetScope:     cmdctx.GetString(ctx.FlagValues, "target-scope"),
		tags:            cmdctx.GetStringSlice(ctx.FlagValues, "tags"),
		files:           cmdctx.GetStringSlice(ctx.FlagValues, "file"),
		base:            cmdctx.GetString(ctx.FlagValues, "base"),
		patch:           cmdctx.GetBool(ctx.FlagValues, "patch"),
		excludePatterns: cmdctx.GetStringSlice(ctx.FlagValues, "exclude"),
	}

	return coverageUpload(ctx, f)
}

const executeCoverageWaitUntilHandlerID = "execute_coverage_wait_until"

// executeCoverageWaitUntilHandler is the `execute coverage:wait-until`
// workflow handler. It polls the Harness coverage service until all reports
// for the execution are processed, then evaluates the result against the
// given quality-gate thresholds.
func executeCoverageWaitUntilHandler(ctx *cmdctx.Ctx) error {
	if len(ctx.Args) > 0 {
		return fmt.Errorf("unexpected arguments: %s", strings.Join(ctx.Args, " "))
	}

	f := coverageWaitUntilFlags{
		accountID:   cmdctx.GetString(ctx.FlagValues, "account"),
		executionID: cmdctx.GetString(ctx.FlagValues, "execution"),
		gt:          cmdctx.GetFloat64(ctx.FlagValues, "gt"),
		gte:         cmdctx.GetFloat64(ctx.FlagValues, "gte"),
		patchGt:     cmdctx.GetFloat64(ctx.FlagValues, "patch-gt"),
		patchGte:    cmdctx.GetFloat64(ctx.FlagValues, "patch-gte"),
		timeout:     cmdctx.GetString(ctx.FlagValues, "wait-timeout"),
		interval:    cmdctx.GetString(ctx.FlagValues, "interval"),
	}

	return coverageWaitUntil(ctx, f)
}
