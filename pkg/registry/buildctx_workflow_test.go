// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package registry

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/harness/cli/pkg/cmdctx"
	"github.com/harness/cli/pkg/spec"
)

func buildWorkflowTestCmd(t *testing.T, r *Registry, cs *spec.CommandSpec) *cobra.Command {
	t.Helper()
	cmd := &cobra.Command{Use: cs.Command}
	r.bindWorkflowCmd(cmd, cs, func(*cmdctx.Ctx) error { return nil })
	cmd.Flags().Float64("timeout", 0, "Command timeout in seconds")
	return cmd
}

func registerWorkflowNoun(t *testing.T, r *Registry, noun string) {
	t.Helper()
	if err := r.RegisterNoun(spec.NounDef{Noun: noun, NounAliases: []string{noun + "s"}}); err != nil {
		t.Fatalf("RegisterNoun %q: %v", noun, err)
	}
}

func registerWorkflowExecute(t *testing.T, r *Registry, noun string, cs *spec.CommandSpec) string {
	t.Helper()
	registerWorkflowNoun(t, r, noun)
	wfID := "test:" + noun
	r.RegisterWorkflow(wfID, func(*cmdctx.Ctx) error { return nil })
	cs.Command = "execute " + noun
	cs.Verb = VerbExecute
	cs.VerbHandler = VerbExecute
	cs.Noun = noun
	cs.Module = "test"
	cs.HandlerType = spec.HandlerWorkflow
	cs.WorkflowID = wfID
	cs.NoAuth = true
	if err := r.Register(cs); err != nil {
		t.Fatalf("Register execute %s: %v", noun, err)
	}
	return wfID
}

func TestBuildCtx_WorkflowFormatFlags(t *testing.T) {
	r := New()
	registerWorkflowExecute(t, r, "fmtthing", &spec.CommandSpec{})
	cs := r.GetSpec(VerbExecute, "fmtthing")

	tests := []struct {
		name    string
		args    []string
		wantErr string
	}{
		{
			name:    "json and yaml mutually exclusive",
			args:    []string{"--json", "--yaml"},
			wantErr: "--json and --yaml are mutually exclusive",
		},
		{
			name:    "json and format mutually exclusive",
			args:    []string{"--json", "--format", "table"},
			wantErr: "--json/--yaml and --format are mutually exclusive",
		},
		{
			name:    "yaml and format mutually exclusive",
			args:    []string{"--yaml", "--format", "json"},
			wantErr: "--json/--yaml and --format are mutually exclusive",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := buildWorkflowTestCmd(t, r, cs)
			cmd.SetArgs(tt.args)
			if err := cmd.ParseFlags(tt.args); err != nil {
				t.Fatalf("ParseFlags: %v", err)
			}
			_, err := buildCtx(cmd, cs, nil, r)
			if err == nil {
				t.Fatal("buildCtx() = nil, want error")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("buildCtx() error %q does not contain %q", err, tt.wantErr)
			}
		})
	}

	t.Run("fields and json mutually exclusive", func(t *testing.T) {
		cmd := buildWorkflowTestCmd(t, r, cs)
		addFlag(cmd.Flags(), specFields)
		flagArgs := []string{"--fields", "name", "--json"}
		if err := cmd.ParseFlags(flagArgs); err != nil {
			t.Fatalf("ParseFlags: %v", err)
		}
		_, err := buildCtx(cmd, cs, nil, r)
		if err == nil {
			t.Fatal("buildCtx() = nil, want error")
		}
		if !strings.Contains(err.Error(), "--fields and --json/--yaml are mutually exclusive") {
			t.Fatalf("buildCtx() error %q missing expected substring", err)
		}
	})

}

func TestBuildCtx_WorkflowTimeout(t *testing.T) {
	r := New()
	registerWorkflowExecute(t, r, "timeoutthing", &spec.CommandSpec{})
	cs := r.GetSpec(VerbExecute, "timeoutthing")
	cmd := buildWorkflowTestCmd(t, r, cs)
	if err := cmd.ParseFlags([]string{"--timeout", "-1"}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}
	_, err := buildCtx(cmd, cs, []string{"id"}, r)
	if err == nil {
		t.Fatal("buildCtx() = nil, want error")
	}
	if !strings.Contains(err.Error(), "--timeout must be >= 0") {
		t.Fatalf("buildCtx() error %q missing expected substring", err)
	}
}

func TestBuildCtx_WorkflowMissingRequiredID(t *testing.T) {
	r := New()
	registerWorkflowExecute(t, r, "missingid", &spec.CommandSpec{})
	cs := r.GetSpec(VerbExecute, "missingid")
	cmd := buildWorkflowTestCmd(t, r, cs)
	_, err := buildCtx(cmd, cs, nil, r)
	if err == nil {
		t.Fatal("buildCtx() = nil, want error")
	}
	if !strings.Contains(err.Error(), "requires a positional") {
		t.Fatalf("buildCtx() error %q missing expected substring", err)
	}
}

func TestBuildCtx_WorkflowRequiredFlag(t *testing.T) {
	r := New()
	registerWorkflowExecute(t, r, "reqflag", &spec.CommandSpec{
		Flags: []spec.Flag{
			{Name: "method", Required: true, Description: "install method"},
		},
	})
	cs := r.GetSpec(VerbExecute, "reqflag")
	cmd := buildWorkflowTestCmd(t, r, cs)
	_, err := buildCtx(cmd, cs, []string{"my-id"}, r)
	if err == nil {
		t.Fatal("buildCtx() = nil, want error")
	}
	if !strings.Contains(err.Error(), "flag --method is required") {
		t.Fatalf("buildCtx() error %q missing expected substring", err)
	}
}

func TestBuildCtx_WorkflowIdPartsTooMany(t *testing.T) {
	r := New()
	registerWorkflowExecute(t, r, "cluster", &spec.CommandSpec{
		IdParts: 2,
		IdLabel: "<agent/cluster_id>",
	})
	cs := r.GetSpec(VerbExecute, "cluster")
	cmd := buildWorkflowTestCmd(t, r, cs)
	_, err := buildCtx(cmd, cs, []string{"agent/cluster/extra"}, r)
	if err == nil {
		t.Fatal("buildCtx() = nil, want error")
	}
	if !strings.Contains(err.Error(), "exactly 2 parts") {
		t.Fatalf("buildCtx() error %q missing expected substring", err)
	}
}

func TestBuildCtx_WorkflowIdSlashNotAllowed(t *testing.T) {
	r := New()
	registerWorkflowExecute(t, r, "noslash", &spec.CommandSpec{})
	cs := r.GetSpec(VerbExecute, "noslash")
	cmd := buildWorkflowTestCmd(t, r, cs)
	_, err := buildCtx(cmd, cs, []string{"foo/bar"}, r)
	if err == nil {
		t.Fatal("buildCtx() = nil, want error")
	}
	if !strings.Contains(err.Error(), "must not contain '/'") {
		t.Fatalf("buildCtx() error %q missing expected substring", err)
	}
}
