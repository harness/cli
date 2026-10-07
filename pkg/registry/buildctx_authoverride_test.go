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

// clearAuthOverrideTestEnv wipes every env var the normal auth-resolution
// chain reads, and points config loading at an empty temp dir, so each test
// case is hermetic regardless of the real machine's logged-in profile.
func clearAuthOverrideTestEnv(t *testing.T) {
	t.Helper()
	for _, v := range []string{
		hbase.EnvAPIKey, hbase.EnvProfile, hbase.EnvAccount,
		hbase.EnvOrg, hbase.EnvProject, hbase.EnvAPIURL,
	} {
		t.Setenv(v, "")
	}
	t.Setenv(hbase.EnvCLIHome, t.TempDir())
}

func registerAuthOverrideTestList(t *testing.T, r *Registry, noun string, override *spec.AuthOverrideSpec) *spec.CommandSpec {
	t.Helper()
	registerWorkflowNoun(t, r, noun)
	wfID := "test:authoverride:" + noun
	r.RegisterWorkflow(wfID, func(*cmdctx.Ctx) error { return nil })
	cs := &spec.CommandSpec{
		Command:     "list " + noun,
		Verb:        VerbList,
		VerbHandler: VerbList,
		Noun:        noun,
		Module:      "test",
		HandlerType: spec.HandlerWorkflow,
		WorkflowID:  wfID,
		Endpoint:    &spec.EndpointSpec{AuthOverride: override},
	}
	if err := r.Register(cs); err != nil {
		t.Fatalf("Register list %s: %v", noun, err)
	}
	return cs
}

func authOverrideTestCmd(t *testing.T, r *Registry, cs *spec.CommandSpec) *cobra.Command {
	t.Helper()
	cmd := &cobra.Command{Use: cs.Command}
	r.bindWorkflowCmd(cmd, cs, func(*cmdctx.Ctx) error { return nil })
	cmd.Flags().Float64("timeout", 0, "timeout")
	cmd.Flags().String("profile", "", "profile")
	cmd.Flags().String("org", "", "org")
	cmd.Flags().String("project", "", "project")
	return cmd
}

func TestBuildCtx_AuthOverrideTriggersFromEnv(t *testing.T) {
	clearAuthOverrideTestEnv(t)
	t.Setenv("TEST_PIPELINE_TOKEN", "tok123")
	t.Setenv(hbase.EnvAccount, "acct1")

	r := New()
	cs := registerAuthOverrideTestList(t, r, "aothing1", &spec.AuthOverrideSpec{
		TokenEnvVar: "TEST_PIPELINE_TOKEN",
		Header:      "Authorization",
		Prefix:      "Bearer ",
	})
	cmd := authOverrideTestCmd(t, r, cs)

	ctx, err := buildCtx(cmd, cs, nil, r)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ctx.Auth.OverrideHeader != "Authorization" || ctx.Auth.OverrideValue != "Bearer tok123" {
		t.Errorf("Auth override = (%q, %q), want (%q, %q)", ctx.Auth.OverrideHeader, ctx.Auth.OverrideValue, "Authorization", "Bearer tok123")
	}
	if ctx.Auth.AccountID != "acct1" {
		t.Errorf("AccountID = %q, want %q", ctx.Auth.AccountID, "acct1")
	}
}

func TestBuildCtx_AuthOverrideSkippedWhenProfileFlagSet(t *testing.T) {
	clearAuthOverrideTestEnv(t)
	t.Setenv("TEST_PIPELINE_TOKEN", "tok123")
	t.Setenv(hbase.EnvAccount, "acct1")

	r := New()
	cs := registerAuthOverrideTestList(t, r, "aothing2", &spec.AuthOverrideSpec{
		TokenEnvVar: "TEST_PIPELINE_TOKEN",
		Header:      "Authorization",
	})
	cmd := authOverrideTestCmd(t, r, cs)
	if err := cmd.Flags().Set("profile", "nonexistent-test-profile"); err != nil {
		t.Fatalf("Set profile: %v", err)
	}

	_, err := buildCtx(cmd, cs, nil, r)
	if err == nil {
		t.Fatal("expected an error from the explicit --profile falling through to normal resolution, got nil")
	}
	if !strings.Contains(err.Error(), `"nonexistent-test-profile" not found`) {
		t.Errorf("error = %q, want it to reference the explicit profile (override must be skipped, not fired)", err.Error())
	}
}

func TestBuildCtx_AuthOverrideFallsThroughWhenTokenUnset(t *testing.T) {
	clearAuthOverrideTestEnv(t)
	// TEST_PIPELINE_TOKEN deliberately left unset.

	r := New()
	cs := registerAuthOverrideTestList(t, r, "aothing3", &spec.AuthOverrideSpec{
		TokenEnvVar: "TEST_PIPELINE_TOKEN",
		Header:      "Authorization",
	})
	cmd := authOverrideTestCmd(t, r, cs)

	_, err := buildCtx(cmd, cs, nil, r)
	if err == nil {
		t.Fatal("expected an error falling through to normal resolution with no profile configured, got nil")
	}
	if !strings.Contains(err.Error(), "not logged in") {
		t.Errorf("error = %q, want the normal 'not logged in' fallback error", err.Error())
	}
}

func TestBuildCtx_AuthOverrideOrgProjectFlagsApplyOnTop(t *testing.T) {
	clearAuthOverrideTestEnv(t)
	t.Setenv("TEST_PIPELINE_TOKEN", "tok123")
	t.Setenv(hbase.EnvAccount, "acct1")

	r := New()
	cs := registerAuthOverrideTestList(t, r, "aothing4", &spec.AuthOverrideSpec{
		TokenEnvVar: "TEST_PIPELINE_TOKEN",
		Header:      "Authorization",
	})
	cmd := authOverrideTestCmd(t, r, cs)
	if err := cmd.Flags().Set("org", "flag-org"); err != nil {
		t.Fatalf("Set org: %v", err)
	}
	if err := cmd.Flags().Set("project", "flag-project"); err != nil {
		t.Fatalf("Set project: %v", err)
	}

	ctx, err := buildCtx(cmd, cs, nil, r)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ctx.Auth.OrgID != "flag-org" || ctx.Auth.ProjectID != "flag-project" {
		t.Errorf("OrgID/ProjectID = %q/%q, want %q/%q (flags should apply on top of a triggered override)", ctx.Auth.OrgID, ctx.Auth.ProjectID, "flag-org", "flag-project")
	}
}
