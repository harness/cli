// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package pipeline

import (
	"strings"
	"testing"

	"github.com/harness/cli/pkg/auth"
	"github.com/harness/cli/pkg/cmdctx"
	"github.com/spf13/pflag"
)

func testCtx(flags map[string]any) *cmdctx.Ctx {
	return &cmdctx.Ctx{
		FlagValues: flags,
		Auth:       &auth.ResolvedAuth{AccountID: "acct", OrgID: "org", ProjectID: "proj"},
	}
}

func TestGetPipelineLogHandler_validation(t *testing.T) {
	tests := []struct {
		name       string
		id         string
		flags      map[string]any
		wantErrSub string
	}{
		{
			name:       "missing exec id",
			flags:      nil,
			wantErrSub: "missing required argument",
		},
		{
			name:       "unparseable exec id",
			id:         "/",
			flags:      nil,
			wantErrSub: "could not parse execId",
		},
		{
			name:       "step without stage",
			id:         "abc123",
			flags:      map[string]any{"step": "Build"},
			wantErrSub: "--step requires --stage",
		},
		{
			// --ui in a non-TTY environment (always the case in tests) returns
			// "requires an interactive terminal" — not a "not compatible" error.
			// This covers the ui+non-TTY branch of getPipelineLogHandler.
			name:       "ui flag requires TTY",
			id:         "abc123",
			flags:      map[string]any{"ui": true},
			wantErrSub: "--ui requires an interactive terminal",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := testCtx(tc.flags)
			ctx.Id = tc.id
			err := getPipelineLogHandler(ctx)
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), tc.wantErrSub) {
				t.Fatalf("error = %q, want substring %q", err, tc.wantErrSub)
			}
		})
	}
}

func TestListExecutionLogsFetchFn_missingParentId(t *testing.T) {
	ctx := testCtx(nil)
	_, err := listExecutionLogsFetchFn(ctx, nil, 0, 0, nil)
	if err == nil {
		t.Fatal("expected error for missing parent id")
	}
	if !strings.Contains(err.Error(), "missing required argument") {
		t.Fatalf("error = %q, want substring %q", err, "missing required argument")
	}
}

func TestPipelineLogStageCompletion_emptyOnNoExecId(t *testing.T) {
	ctx := testCtx(nil)
	stages, err := pipelineLogStageCompletion(ctx, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(stages) != 0 {
		t.Fatalf("expected no stages, got %v", stages)
	}
}

func TestPipelineLogStepCompletion_emptyWhenNoStage(t *testing.T) {
	ctx := testCtx(nil)
	fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
	fs.String("stage", "", "")
	steps, err := pipelineLogStepCompletion(ctx, nil, fs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(steps) != 0 {
		t.Fatalf("expected no steps, got %v", steps)
	}
}
