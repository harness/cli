// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package iacm

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"sort"
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

func TestZipSourceCodeSkipsHeavyAndStateFiles(t *testing.T) {
	root := t.TempDir()
	files := map[string]bool{
		"main.tf":                             true,
		".terraform.lock.hcl":                 true,
		"mod/vars.tf":                         true,
		".terraform/providers/big.bin":        false,
		"mod/.terraform/modules/modules.json": false,
		".git/HEAD":                           false,
		"terraform.tfstate":                   false,
		"terraform.tfstate.backup":            false,
	}
	for name := range files {
		p := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	data, err := zipSourceCode(root, operationPlan)
	if err != nil {
		t.Fatal(err)
	}
	gr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(gr)
	got := map[string]bool{}
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		got[hdr.Name] = true
	}

	for name, want := range files {
		if got[name] != want {
			t.Errorf("%s: included=%v, want %v", name, got[name], want)
		}
	}
}

func TestApplyWorkspaceScopeUpdatesAuth(t *testing.T) {
	tests := []struct {
		name    string
		cfg     *workspaceConfig
		wantOrg string
		wantPrj string
	}{
		{name: "both", cfg: &workspaceConfig{Org: "file-org", Project: "file-project"}, wantOrg: "file-org", wantPrj: "file-project"},
		{name: "neither", cfg: &workspaceConfig{}, wantOrg: "profile-org", wantPrj: "profile-project"},
		{name: "org only", cfg: &workspaceConfig{Org: "file-org"}, wantOrg: "file-org", wantPrj: "profile-project"},
		{name: "project only", cfg: &workspaceConfig{Project: "file-project"}, wantOrg: "profile-org", wantPrj: "file-project"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := &auth.ResolvedAuth{OrgID: "profile-org", ProjectID: "profile-project"}
			orgID, projectID := applyWorkspaceScope(a, tt.cfg)
			if orgID != tt.wantOrg || projectID != tt.wantPrj || a.OrgID != tt.wantOrg || a.ProjectID != tt.wantPrj {
				t.Fatalf("scope org=%q project=%q auth org=%q project=%q", orgID, projectID, a.OrgID, a.ProjectID)
			}
		})
	}
}

func TestRemoteExecutionExecutePath(t *testing.T) {
	const base = "/gateway/iacm/api/orgs/default/projects/Testim/workspaces/ws/remote-executions/exec-1/execute"
	if got := remoteExecutionExecutePath("default", "Testim", "ws", "exec-1", operationPlan); got != base {
		t.Errorf("plan path = %q, want no operation query", got)
	}
	if got := remoteExecutionExecutePath("default", "Testim", "ws", "exec-1", operationSynth); got != base+"?operation=synth" {
		t.Errorf("synth path = %q", got)
	}
	if got := remoteExecutionExecutePath("default", "Testim", "ws", "exec-1", operationDiff); got != base+"?operation=diff" {
		t.Errorf("diff path = %q", got)
	}
}

func strPtr(s string) *string { return &s }

func TestGetDefaultPipelineByOperation(t *testing.T) {
	pipelines := map[string]*defaultPipelineOverride{
		"synth": {ProjectPipeline: strPtr("projSynth")},
		"diff":  {WorkspacePipeline: strPtr("wsDiff"), ProjectPipeline: strPtr("projDiff")},
	}
	for op, want := range map[string]string{operationSynth: "projSynth", operationDiff: "wsDiff"} {
		got, err := getDefaultPipeline(pipelines, op)
		if err != nil || got != want {
			t.Errorf("%s: got %q, %v; want %q", op, got, err, want)
		}
	}
	if _, err := getDefaultPipeline(pipelines, operationPlan); err == nil ||
		err.Error() != "workspace has no default pipeline configured for 'plan'" {
		t.Errorf("plan: unexpected error %v", err)
	}
}

func tarNames(t *testing.T, data []byte) []string {
	t.Helper()
	gr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(gr)
	var names []string
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, h.Name)
	}
	sort.Strings(names)
	return names
}

func TestZipSourceCodeSkipsCDKBuildDirs(t *testing.T) {
	root := t.TempDir()
	for _, f := range []string{"cdk.json", "lib/stack.ts", "node_modules/x/index.js", "cdk.out/manifest.json", ".git/HEAD"} {
		p := filepath.Join(root, f)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	cdk, err := zipSourceCode(root, operationSynth)
	if err != nil {
		t.Fatal(err)
	}
	if got := tarNames(t, cdk); len(got) != 2 || got[0] != "cdk.json" || got[1] != "lib/stack.ts" {
		t.Errorf("synth archive = %v", got)
	}

	plan, err := zipSourceCode(root, operationPlan)
	if err != nil {
		t.Fatal(err)
	}
	if got := tarNames(t, plan); len(got) != 4 {
		t.Errorf("plan archive should keep node_modules and cdk.out, got %v", got)
	}
}
