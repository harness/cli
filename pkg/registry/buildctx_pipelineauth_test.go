// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package registry

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/harness/cli/v3/pkg/cmdctx"
	"github.com/harness/cli/v3/pkg/hbase"
	"github.com/harness/cli/v3/pkg/spec"
)

// clearPipelineAuthTestEnv wipes every env var the normal auth-resolution
// chain and pipeline_auth read, and points config loading at an empty temp
// dir, so each test case is hermetic regardless of the real machine's logged-in
// profile or environment.
func clearPipelineAuthTestEnv(t *testing.T) {
	t.Helper()
	for _, v := range []string{
		hbase.EnvAPIKey, hbase.EnvProfile, hbase.EnvAccount,
		hbase.EnvOrg, hbase.EnvProject, hbase.EnvAPIURL,
		hbase.EnvPipelineID, hbase.EnvAccountID, hbase.EnvOrgID, hbase.EnvProjectID, hbase.EnvInfra,
		"TEST_PIPELINE_TOKEN",
	} {
		t.Setenv(v, "")
	}
	t.Setenv(hbase.EnvCLIHome, t.TempDir())
}

func registerPipelineAuthTestList(t *testing.T, r *Registry, noun string, pa *spec.PipelineAuthSpec) *spec.CommandSpec {
	t.Helper()
	registerWorkflowNoun(t, r, noun)
	wfID := "test:pipelineauth:" + noun
	r.RegisterWorkflow(wfID, func(*cmdctx.Ctx) error { return nil })
	cs := &spec.CommandSpec{
		Command:      "list " + noun,
		Verb:         VerbList,
		VerbHandler:  VerbList,
		Noun:         noun,
		Module:       "test",
		HandlerType:  spec.HandlerWorkflow,
		WorkflowID:   wfID,
		PipelineAuth: pa,
	}
	if err := r.Register(cs); err != nil {
		t.Fatalf("Register list %s: %v", noun, err)
	}
	return cs
}

func pipelineAuthTestCmd(t *testing.T, r *Registry, cs *spec.CommandSpec) *cobra.Command {
	t.Helper()
	cmd := &cobra.Command{Use: cs.Command}
	r.bindWorkflowCmd(cmd, cs, func(*cmdctx.Ctx) error { return nil })
	cmd.Flags().Float64("timeout", 0, "timeout")
	cmd.Flags().String("profile", "", "profile")
	cmd.Flags().String("org", "", "org")
	cmd.Flags().String("project", "", "project")
	return cmd
}

func setPipelineScopeEnv(t *testing.T) {
	t.Helper()
	t.Setenv(hbase.EnvPipelineID, "pipe1")
	t.Setenv(hbase.EnvAccountID, "acct1")
	t.Setenv(hbase.EnvOrgID, "org1")
	t.Setenv(hbase.EnvProjectID, "proj1")
	t.Setenv(hbase.EnvInfra, hbase.InfraVM)
}

func TestBuildCtx_PipelineAuthTriggersFromPipelineID(t *testing.T) {
	clearPipelineAuthTestEnv(t)
	setPipelineScopeEnv(t)
	t.Setenv("TEST_PIPELINE_TOKEN", "tok123")

	r := New()
	cs := registerPipelineAuthTestList(t, r, "pathing1", &spec.PipelineAuthSpec{
		TokenEnvVar:  "TEST_PIPELINE_TOKEN",
		APIURLEnvVar: "TEST_PIPELINE_API_URL",
		Headers:      map[string]string{"Authorization": `"Bearer " + token`},
	})
	cmd := pipelineAuthTestCmd(t, r, cs)

	ctx, err := buildCtx(cmd, cs, nil, r)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ctx.Auth.Headers["Authorization"] != "Bearer tok123" {
		t.Errorf("Auth.Headers[Authorization] = %q, want %q", ctx.Auth.Headers["Authorization"], "Bearer tok123")
	}
	if ctx.Auth.AccountID != "acct1" || ctx.Auth.OrgID != "org1" || ctx.Auth.ProjectID != "proj1" {
		t.Errorf("scope = %q/%q/%q, want acct1/org1/proj1", ctx.Auth.AccountID, ctx.Auth.OrgID, ctx.Auth.ProjectID)
	}
}

func TestBuildCtx_PipelineAuthSkippedWhenProfileFlagSet(t *testing.T) {
	clearPipelineAuthTestEnv(t)
	setPipelineScopeEnv(t)
	t.Setenv("TEST_PIPELINE_TOKEN", "tok123")

	r := New()
	cs := registerPipelineAuthTestList(t, r, "pathing2", &spec.PipelineAuthSpec{
		TokenEnvVar: "TEST_PIPELINE_TOKEN", APIURLEnvVar: "TEST_PIPELINE_API_URL",
	})
	cmd := pipelineAuthTestCmd(t, r, cs)
	if err := cmd.Flags().Set("profile", "nonexistent-test-profile"); err != nil {
		t.Fatalf("Set profile: %v", err)
	}

	_, err := buildCtx(cmd, cs, nil, r)
	if err == nil {
		t.Fatal("expected an error from the explicit --profile falling through to normal resolution, got nil")
	}
	if !strings.Contains(err.Error(), `"nonexistent-test-profile" not found`) {
		t.Errorf("error = %q, want it to reference the explicit profile (pipeline auth must be skipped, not fired)", err.Error())
	}
}

func TestBuildCtx_PipelineAuthFallsThroughWhenPipelineIDUnset(t *testing.T) {
	clearPipelineAuthTestEnv(t)
	// HARNESS_PIPELINEID deliberately left unset — not running in a pipeline.
	t.Setenv("TEST_PIPELINE_TOKEN", "tok123")

	r := New()
	cs := registerPipelineAuthTestList(t, r, "pathing3", &spec.PipelineAuthSpec{
		TokenEnvVar: "TEST_PIPELINE_TOKEN", APIURLEnvVar: "TEST_PIPELINE_API_URL",
	})
	cmd := pipelineAuthTestCmd(t, r, cs)

	_, err := buildCtx(cmd, cs, nil, r)
	if err == nil {
		t.Fatal("expected an error falling through to normal resolution with no profile configured, got nil")
	}
	if !strings.Contains(err.Error(), "not logged in") {
		t.Errorf("error = %q, want the normal 'not logged in' fallback error", err.Error())
	}
}

// TestBuildCtx_PipelineAuthFallsThroughWhenCommandHasNoBlock covers a command
// with no pipeline_auth running inside a pipeline (HARNESS_PIPELINEID set):
// it must not error, just fall through to normal profile/env resolution —
// pipeline-auth-agnostic commands still work inside a pipeline.
func TestBuildCtx_PipelineAuthFallsThroughWhenCommandHasNoBlock(t *testing.T) {
	clearPipelineAuthTestEnv(t)
	setPipelineScopeEnv(t)

	r := New()
	cs := registerPipelineAuthTestList(t, r, "pathing4", nil)
	cmd := pipelineAuthTestCmd(t, r, cs)

	_, err := buildCtx(cmd, cs, nil, r)
	if err == nil || !strings.Contains(err.Error(), "not logged in") {
		t.Fatalf("error = %v, want the normal 'not logged in' fallback error (no pipeline_auth block means no pipeline auth, not an error)", err)
	}
}

func TestBuildCtx_PipelineAuthOrgProjectFlagsError(t *testing.T) {
	clearPipelineAuthTestEnv(t)
	setPipelineScopeEnv(t)
	t.Setenv("TEST_PIPELINE_TOKEN", "tok123")

	r := New()
	cs := registerPipelineAuthTestList(t, r, "pathing5", &spec.PipelineAuthSpec{
		TokenEnvVar: "TEST_PIPELINE_TOKEN", APIURLEnvVar: "TEST_PIPELINE_API_URL",
	})
	cmd := pipelineAuthTestCmd(t, r, cs)
	if err := cmd.Flags().Set("org", "flag-org"); err != nil {
		t.Fatalf("Set org: %v", err)
	}
	if err := cmd.Flags().Set("project", "flag-project"); err != nil {
		t.Fatalf("Set project: %v", err)
	}

	_, err := buildCtx(cmd, cs, nil, r)
	if err == nil || !strings.Contains(err.Error(), "not allowed in pipeline mode") {
		t.Fatalf("error = %v, want an --org/--project-not-allowed error (pipeline scope is fixed)", err)
	}
}
