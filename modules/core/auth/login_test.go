// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"strings"
	"testing"

	"github.com/harness/cli/pkg/auth"
	"github.com/harness/cli/pkg/cmdctx"
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

func TestLoginHandler_validation(t *testing.T) {
	tests := []struct {
		name          string
		flags         map[string]any
		wantSubstr    string
		wantNoProfile bool
	}{
		{
			name: "mutually exclusive overwrite flags",
			flags: map[string]any{
				"overwrite":     true,
				"no-overwrite": true,
			},
			wantSubstr: "mutually exclusive",
		},
		{
			name: "invalid profile name",
			flags: map[string]any{
				"profile": "bad name!",
			},
			wantSubstr: "invalid profile name",
		},
		{
			name: "valid profile name passes regex check",
			flags: map[string]any{
				"profile": "my-profile_1",
			},
			wantNoProfile: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := LoginHandler(testCtx(tt.flags))
			if tt.wantNoProfile {
				if err == nil {
					t.Fatal("LoginHandler() error = nil, want error after config load")
				}
				if strings.Contains(err.Error(), "invalid profile name") {
					t.Fatalf("LoginHandler() error = %q, profile name validation should pass", err.Error())
				}
				return
			}
			if err == nil {
				t.Fatal("LoginHandler() error = nil, want error")
			}
			if !strings.Contains(err.Error(), tt.wantSubstr) {
				t.Fatalf("LoginHandler() error = %q, want substring %q", err.Error(), tt.wantSubstr)
			}
		})
	}
}
