// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package iacm

import (
	"testing"

	"github.com/harness/cli/v3/pkg/auth"
)

func TestIacmExecutionURL(t *testing.T) {
	tests := []struct {
		name string
		a    *auth.ResolvedAuth
		org  string
		proj string
		pipe string
		exec string
		want string
	}{
		{
			name: "builds module-scoped link from UIUrl",
			a:    &auth.ResolvedAuth{AccountID: "acc1", UIUrl: "https://app.harness.io/", APIUrl: "https://api.harness.io"},
			org:  "default",
			proj: "Testim",
			pipe: "Testim_default_pipeline_plan",
			exec: "M-DJhbL4TnmESj_e_31v8w",
			want: "https://app.harness.io/ng/account/acc1/module/iacm/orgs/default/projects/Testim/pipelines/Testim_default_pipeline_plan/deployments/M-DJhbL4TnmESj_e_31v8w/pipeline",
		},
		{
			name: "falls back to APIUrl when UIUrl is unset",
			a:    &auth.ResolvedAuth{AccountID: "acc1", APIUrl: "https://qa0.harness.io"},
			org:  "org1",
			proj: "proj1",
			pipe: "pipe1",
			exec: "exec1",
			want: "https://qa0.harness.io/ng/account/acc1/module/iacm/orgs/org1/projects/proj1/pipelines/pipe1/deployments/exec1/pipeline",
		},
		{
			name: "empty when account id missing",
			a:    &auth.ResolvedAuth{APIUrl: "https://qa0.harness.io"},
			org:  "org1",
			proj: "proj1",
			pipe: "pipe1",
			exec: "exec1",
			want: "",
		},
		{
			name: "empty when execution id missing",
			a:    &auth.ResolvedAuth{AccountID: "acc1", APIUrl: "https://qa0.harness.io"},
			org:  "org1",
			proj: "proj1",
			pipe: "pipe1",
			exec: "",
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := iacmExecutionURL(tt.a, tt.org, tt.proj, tt.pipe, tt.exec)
			if got != tt.want {
				t.Errorf("iacmExecutionURL() = %q, want %q", got, tt.want)
			}
		})
	}
}
