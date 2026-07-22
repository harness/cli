// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package pipeline

import (
	"strings"
	"testing"

	"github.com/harness/cli/pkg/auth"
	"github.com/harness/cli/pkg/cmdctx"
	"github.com/harness/cli/pkg/console"
)

func testCtx(flags map[string]any) *cmdctx.Ctx {
	if flags == nil {
		flags = map[string]any{}
	}
	return &cmdctx.Ctx{
		FlagValues: flags,
		Auth:       &auth.ResolvedAuth{AccountID: "acct", OrgID: "org", ProjectID: "proj"},
	}
}

func TestGetPipelineLogHandler_validation(t *testing.T) {
	tests := []struct {
		name       string
		ctx        *cmdctx.Ctx
		wantSubstr string
		skipNoTTY  bool
	}{
		{
			name:       "missing exec id",
			ctx:        testCtx(nil),
			wantSubstr: "missing required argument",
		},
		{
			name: "unparseable exec id",
			ctx: func() *cmdctx.Ctx {
				c := testCtx(nil)
				c.Id = "/"
				return c
			}(),
			wantSubstr: "could not parse execId",
		},
		{
			name: "step without stage",
			ctx: func() *cmdctx.Ctx {
				c := testCtx(map[string]any{"step": "Build"})
				c.Id = "pipeline-id/exec-id"
				return c
			}(),
			wantSubstr: "--step requires --stage",
		},
		{
			name: "ui with stage",
			ctx: func() *cmdctx.Ctx {
				c := testCtx(map[string]any{"ui": true, "stage": "Build"})
				c.Id = "pipeline-id/exec-id"
				return c
			}(),
			wantSubstr: "--ui is not compatible",
			skipNoTTY:  true,
		},
		{
			name: "ui with step",
			ctx: func() *cmdctx.Ctx {
				c := testCtx(map[string]any{"ui": true, "step": "Compile"})
				c.Id = "pipeline-id/exec-id"
				return c
			}(),
			wantSubstr: "--ui is not compatible",
			skipNoTTY:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.skipNoTTY && !console.IsBothTTY() {
				t.Skip("requires interactive terminal to test --ui flag compatibility")
			}
			err := getPipelineLogHandler(tt.ctx)
			if err == nil {
				t.Fatal("getPipelineLogHandler() error = nil, want error")
			}
			if !strings.Contains(err.Error(), tt.wantSubstr) {
				t.Fatalf("getPipelineLogHandler() error = %q, want substring %q", err.Error(), tt.wantSubstr)
			}
		})
	}
}
