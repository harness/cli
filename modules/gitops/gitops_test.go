// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package gitops

import (
	"strings"
	"testing"

	"github.com/harness/cli/pkg/auth"
	"github.com/harness/cli/pkg/cmdctx"
	"github.com/harness/cli/pkg/registry"
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

func TestExecuteAgentInstall_validation(t *testing.T) {
	tests := []struct {
		name       string
		ctx        *cmdctx.Ctx
		wantSubstr string
		wantNoErr  bool
	}{
		{
			name:       "missing agent id",
			ctx:        testCtx(nil),
			wantSubstr: "requires a positional",
		},
		{
			name: "invalid method",
			ctx: func() *cmdctx.Ctx {
				c := testCtx(map[string]any{"method": "docker"})
				c.Id = "my-agent"
				return c
			}(),
			wantSubstr: `must be "helm" or "yaml"`,
		},
		{
			name: "default method passes validation",
			ctx: func() *cmdctx.Ctx {
				c := testCtx(nil)
				c.Id = "my-agent"
				c.Resolver = registry.New()
				return c
			}(),
			wantSubstr: "get gitops_agent command spec not found",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := executeAgentInstall(tt.ctx)
			if tt.wantNoErr {
				if err != nil {
					t.Fatalf("executeAgentInstall() error = %v, want nil", err)
				}
				return
			}
			if err == nil {
				t.Fatal("executeAgentInstall() error = nil, want error")
			}
			if tt.wantSubstr != "" && !strings.Contains(err.Error(), tt.wantSubstr) {
				t.Fatalf("executeAgentInstall() error = %q, want substring %q", err.Error(), tt.wantSubstr)
			}
		})
	}
}
