// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package registry

import (
	"fmt"
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

func TestBuildCtx_WorkflowIdSlashAllowed(t *testing.T) {
	// IdAllowSlash=true: slashes in id are permitted, validateIdParts returns nil.
	r := New()
	registerWorkflowExecute(t, r, "slashok", &spec.CommandSpec{IdAllowSlash: true})
	cs := r.GetSpec(VerbExecute, "slashok")
	cmd := buildWorkflowTestCmd(t, r, cs)
	ctx, err := buildCtx(cmd, cs, []string{"foo/bar/baz"}, r)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ctx.Id != "foo/bar/baz" {
		t.Fatalf("ctx.Id = %q, want %q", ctx.Id, "foo/bar/baz")
	}
}

func TestBuildCtx_WorkflowFieldsAndFormatMutuallyExclusive(t *testing.T) {
	r := New()
	registerWorkflowExecute(t, r, "fmtfields2", &spec.CommandSpec{})
	cs := r.GetSpec(VerbExecute, "fmtfields2")
	cmd := buildWorkflowTestCmd(t, r, cs)
	addFlag(cmd.Flags(), specFields)
	if err := cmd.ParseFlags([]string{"--fields", "name", "--format", "table"}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}
	_, err := buildCtx(cmd, cs, nil, r)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "--fields and --format are mutually exclusive") {
		t.Fatalf("error = %q, want substring", err)
	}
}

func TestBuildCtx_WorkflowYamlSetsFormat(t *testing.T) {
	// --yaml with no conflicting flags should succeed and set FormatFlags.Format="yaml".
	r := New()
	registerWorkflowExecute(t, r, "yamlfmt", &spec.CommandSpec{})
	cs := r.GetSpec(VerbExecute, "yamlfmt")
	cmd := buildWorkflowTestCmd(t, r, cs)
	if err := cmd.ParseFlags([]string{"--yaml"}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}
	ctx, err := buildCtx(cmd, cs, []string{"myid"}, r)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ctx.FormatFlags.Format != "yaml" {
		t.Fatalf("FormatFlags.Format = %q, want %q", ctx.FormatFlags.Format, "yaml")
	}
}

func TestBuildCtx_WorkflowJsonSetsFormat(t *testing.T) {
	r := New()
	registerWorkflowExecute(t, r, "jsonfmt", &spec.CommandSpec{})
	cs := r.GetSpec(VerbExecute, "jsonfmt")
	cmd := buildWorkflowTestCmd(t, r, cs)
	if err := cmd.ParseFlags([]string{"--json"}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}
	ctx, err := buildCtx(cmd, cs, []string{"myid"}, r)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ctx.FormatFlags.Format != "json" {
		t.Fatalf("FormatFlags.Format = %q, want %q", ctx.FormatFlags.Format, "json")
	}
}

func TestBuildCtx_WorkflowRequiredFlagWithCompletionValues(t *testing.T) {
	// Required flag with CompletionValues produces "(val1, val2)" hint in the error.
	r := New()
	registerWorkflowExecute(t, r, "reqcomp", &spec.CommandSpec{
		Flags: []spec.Flag{
			{Name: "env", Required: true, Description: "target env", CompletionValues: []string{"dev", "prod"}},
		},
	})
	cs := r.GetSpec(VerbExecute, "reqcomp")
	cmd := buildWorkflowTestCmd(t, r, cs)
	_, err := buildCtx(cmd, cs, []string{"my-id"}, r)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "dev") || !strings.Contains(err.Error(), "prod") {
		t.Fatalf("error = %q, want completion values in message", err)
	}
}

// ---- List verb: AllowsParentId ----

// registerWorkflowList registers a list-verb workflow spec for the given noun.
func registerWorkflowList(t *testing.T, r *Registry, noun string, cs *spec.CommandSpec) {
	t.Helper()
	registerWorkflowNoun(t, r, noun)
	wfID := "test:list:" + noun
	r.RegisterWorkflow(wfID, func(*cmdctx.Ctx) error { return nil })
	cs.Command = "list " + noun
	cs.Verb = VerbList
	cs.VerbHandler = VerbList
	cs.Noun = noun
	cs.Module = "test"
	cs.HandlerType = spec.HandlerWorkflow
	cs.WorkflowID = wfID
	cs.NoAuth = true
	if err := r.Register(cs); err != nil {
		t.Fatalf("Register list %s: %v", noun, err)
	}
}

func TestBuildCtx_ListAllowsParentId(t *testing.T) {
	// VerbList has AllowsParentId=true. An optional parent arg sets ctx.ParentId.
	r := New()
	registerWorkflowList(t, r, "listable", &spec.CommandSpec{})
	cs := r.GetSpec(VerbList, "listable")
	cmd := &cobra.Command{Use: cs.Command}
	r.bindWorkflowCmd(cmd, cs, func(*cmdctx.Ctx) error { return nil })
	cmd.Flags().Float64("timeout", 0, "timeout")

	ctx, err := buildCtx(cmd, cs, []string{"parent-123"}, r)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ctx.ParentId != "parent-123" {
		t.Fatalf("ctx.ParentId = %q, want %q", ctx.ParentId, "parent-123")
	}
}

func TestBuildCtx_ListNoParentIdOK(t *testing.T) {
	// VerbList with no args and no RequiresParentId should succeed with empty ParentId.
	r := New()
	registerWorkflowList(t, r, "listnoparent", &spec.CommandSpec{})
	cs := r.GetSpec(VerbList, "listnoparent")
	cmd := &cobra.Command{Use: cs.Command}
	r.bindWorkflowCmd(cmd, cs, func(*cmdctx.Ctx) error { return nil })
	cmd.Flags().Float64("timeout", 0, "timeout")

	ctx, err := buildCtx(cmd, cs, nil, r)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ctx.ParentId != "" {
		t.Fatalf("ctx.ParentId = %q, want empty", ctx.ParentId)
	}
}

func TestBuildCtx_ListRequiresParentId(t *testing.T) {
	// RequiresParentId=true with no args must error.
	r := New()
	registerWorkflowList(t, r, "listrequired", &spec.CommandSpec{
		RequiresParentId: true,
		ParentIdLabel:    "pipeline-id",
	})
	cs := r.GetSpec(VerbList, "listrequired")
	cmd := &cobra.Command{Use: cs.Command}
	r.bindWorkflowCmd(cmd, cs, func(*cmdctx.Ctx) error { return nil })
	cmd.Flags().Float64("timeout", 0, "timeout")

	_, err := buildCtx(cmd, cs, nil, r)
	if err == nil {
		t.Fatal("expected error for missing required parent id")
	}
	if !strings.Contains(err.Error(), "requires a positional") {
		t.Fatalf("error = %q, want %q", err, "requires a positional")
	}
}

func TestBuildCtx_ListTooManyArgs(t *testing.T) {
	// VerbGet/VerbList reject more than 1 positional arg.
	r := New()
	registerWorkflowList(t, r, "listargs", &spec.CommandSpec{})
	cs := r.GetSpec(VerbList, "listargs")
	cmd := &cobra.Command{Use: cs.Command}
	r.bindWorkflowCmd(cmd, cs, func(*cmdctx.Ctx) error { return nil })
	cmd.Flags().Float64("timeout", 0, "timeout")

	_, err := buildCtx(cmd, cs, []string{"p1", "p2"}, r)
	if err == nil {
		t.Fatal("expected error for extra argument")
	}
	if !strings.Contains(err.Error(), "unexpected argument") {
		t.Fatalf("error = %q, want %q", err, "unexpected argument")
	}
}

// ---- Create verb: AllowsId ----

func registerWorkflowCreate(t *testing.T, r *Registry, noun string, cs *spec.CommandSpec) {
	t.Helper()
	registerWorkflowNoun(t, r, noun)
	wfID := "test:create:" + noun
	r.RegisterWorkflow(wfID, func(*cmdctx.Ctx) error { return nil })
	cs.Command = "create " + noun
	cs.Verb = VerbCreate
	cs.VerbHandler = VerbCreate
	cs.Noun = noun
	cs.Module = "test"
	cs.HandlerType = spec.HandlerWorkflow
	cs.WorkflowID = wfID
	cs.NoAuth = true
	if err := r.Register(cs); err != nil {
		t.Fatalf("Register create %s: %v", noun, err)
	}
}

func TestBuildCtx_CreateAllowsIdOptional(t *testing.T) {
	// VerbCreate AllowsId=true; omitting the id is fine.
	r := New()
	registerWorkflowCreate(t, r, "created", &spec.CommandSpec{})
	cs := r.GetSpec(VerbCreate, "created")
	cmd := &cobra.Command{Use: cs.Command}
	r.bindWorkflowCmd(cmd, cs, func(*cmdctx.Ctx) error { return nil })
	cmd.Flags().Float64("timeout", 0, "timeout")

	ctx, err := buildCtx(cmd, cs, nil, r)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ctx.Id != "" {
		t.Fatalf("ctx.Id = %q, want empty", ctx.Id)
	}
}

func TestBuildCtx_CreateRequiresIdError(t *testing.T) {
	// VerbCreate + RequiresId=true: omitting id should error.
	r := New()
	registerWorkflowCreate(t, r, "reqcreate", &spec.CommandSpec{RequiresId: true})
	cs := r.GetSpec(VerbCreate, "reqcreate")
	cmd := &cobra.Command{Use: cs.Command}
	r.bindWorkflowCmd(cmd, cs, func(*cmdctx.Ctx) error { return nil })
	cmd.Flags().Float64("timeout", 0, "timeout")

	_, err := buildCtx(cmd, cs, nil, r)
	if err == nil {
		t.Fatal("expected error for missing required id")
	}
	if !strings.Contains(err.Error(), "requires a positional") {
		t.Fatalf("error = %q, want %q", err, "requires a positional")
	}
}

// ---- BuiltinFlags.Set and .Del ----

func TestBuildCtx_SetArgs(t *testing.T) {
	r := New()
	registerWorkflowExecute(t, r, "setargs", &spec.CommandSpec{
		BuiltinFlags: spec.BuiltinFlags{Set: true},
	})
	cs := r.GetSpec(VerbExecute, "setargs")
	cmd := buildWorkflowTestCmd(t, r, cs)
	if err := cmd.ParseFlags([]string{"--set", "key=value", "--set", "other=val2"}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}
	ctx, err := buildCtx(cmd, cs, []string{"my-id"}, r)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ctx.SetArgs["key"] != "value" || ctx.SetArgs["other"] != "val2" {
		t.Fatalf("SetArgs = %v, unexpected", ctx.SetArgs)
	}
}

func TestBuildCtx_SetArgsBadFormat(t *testing.T) {
	r := New()
	registerWorkflowExecute(t, r, "setbad", &spec.CommandSpec{
		BuiltinFlags: spec.BuiltinFlags{Set: true},
	})
	cs := r.GetSpec(VerbExecute, "setbad")
	cmd := buildWorkflowTestCmd(t, r, cs)
	if err := cmd.ParseFlags([]string{"--set", "noequals"}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}
	_, err := buildCtx(cmd, cs, []string{"my-id"}, r)
	if err == nil {
		t.Fatal("expected error for bad key=value")
	}
	if !strings.Contains(err.Error(), "key=value") {
		t.Fatalf("error = %q, want key=value format hint", err)
	}
}

func TestBuildCtx_DelArgs(t *testing.T) {
	r := New()
	registerWorkflowExecute(t, r, "delargs", &spec.CommandSpec{
		BuiltinFlags: spec.BuiltinFlags{Del: true},
	})
	cs := r.GetSpec(VerbExecute, "delargs")
	cmd := buildWorkflowTestCmd(t, r, cs)
	if err := cmd.ParseFlags([]string{"--del", "field1", "--del", "field2"}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}
	ctx, err := buildCtx(cmd, cs, []string{"my-id"}, r)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(ctx.DelArgs) != 2 || ctx.DelArgs[0] != "field1" {
		t.Fatalf("DelArgs = %v, unexpected", ctx.DelArgs)
	}
}

// ---- HasArgs ----

func TestBuildCtx_HasArgs(t *testing.T) {
	// HasArgs=true: extra positional args beyond the id go into ctx.Args.
	r := New()
	registerWorkflowExecute(t, r, "hasargs", &spec.CommandSpec{HasArgs: true})
	cs := r.GetSpec(VerbExecute, "hasargs")
	cmd := buildWorkflowTestCmd(t, r, cs)
	ctx, err := buildCtx(cmd, cs, []string{"my-id", "extra1", "extra2"}, r)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(ctx.Args) != 2 || ctx.Args[0] != "extra1" {
		t.Fatalf("ctx.Args = %v, want [extra1 extra2]", ctx.Args)
	}
}

// ---- MultiLevel + --level flag ----

func TestBuildCtx_MultiLevel_LevelFlagValid(t *testing.T) {
	r := New()
	if err := r.RegisterNoun(spec.NounDef{Noun: "mlres", MultiLevel: true}); err != nil {
		t.Fatalf("RegisterNoun: %v", err)
	}
	wfID := "test:execute:mlres"
	r.RegisterWorkflow(wfID, func(*cmdctx.Ctx) error { return nil })
	cs := &spec.CommandSpec{
		Command: "execute mlres", Verb: VerbExecute, VerbHandler: VerbExecute,
		Noun: "mlres", Module: "test", HandlerType: spec.HandlerWorkflow,
		WorkflowID: wfID, NoAuth: true,
	}
	if err := r.Register(cs); err != nil {
		t.Fatalf("Register: %v", err)
	}
	cs = r.GetSpec(VerbExecute, "mlres")

	cmd := &cobra.Command{Use: cs.Command}
	r.bindWorkflowCmd(cmd, cs, func(*cmdctx.Ctx) error { return nil })
	cmd.Flags().Float64("timeout", 0, "timeout")
	if err := cmd.ParseFlags([]string{"--level", "org"}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}
	ctx, err := buildCtx(cmd, cs, []string{"my-id"}, r)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ctx.Level != "org" {
		t.Fatalf("ctx.Level = %q, want %q", ctx.Level, "org")
	}
}

func TestBuildCtx_MultiLevel_LevelFlagInvalid(t *testing.T) {
	r := New()
	if err := r.RegisterNoun(spec.NounDef{Noun: "mlres2", MultiLevel: true}); err != nil {
		t.Fatalf("RegisterNoun: %v", err)
	}
	wfID := "test:execute:mlres2"
	r.RegisterWorkflow(wfID, func(*cmdctx.Ctx) error { return nil })
	cs := &spec.CommandSpec{
		Command: "execute mlres2", Verb: VerbExecute, VerbHandler: VerbExecute,
		Noun: "mlres2", Module: "test", HandlerType: spec.HandlerWorkflow,
		WorkflowID: wfID, NoAuth: true,
	}
	if err := r.Register(cs); err != nil {
		t.Fatalf("Register: %v", err)
	}
	cs = r.GetSpec(VerbExecute, "mlres2")

	cmd := &cobra.Command{Use: cs.Command}
	r.bindWorkflowCmd(cmd, cs, func(*cmdctx.Ctx) error { return nil })
	cmd.Flags().Float64("timeout", 0, "timeout")
	if err := cmd.ParseFlags([]string{"--level", "galaxy"}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}
	_, err := buildCtx(cmd, cs, []string{"my-id"}, r)
	if err == nil {
		t.Fatal("expected error for invalid --level")
	}
	if !strings.Contains(err.Error(), "invalid --level") {
		t.Fatalf("error = %q, want invalid --level", err)
	}
}

func TestBuildCtx_MultiLevel_PrefixConflictsWithLevelFlag(t *testing.T) {
	// id with "org." prefix + --level account should conflict.
	r := New()
	if err := r.RegisterNoun(spec.NounDef{Noun: "mlres3", MultiLevel: true}); err != nil {
		t.Fatalf("RegisterNoun: %v", err)
	}
	wfID := "test:execute:mlres3"
	r.RegisterWorkflow(wfID, func(*cmdctx.Ctx) error { return nil })
	cs := &spec.CommandSpec{
		Command: "execute mlres3", Verb: VerbExecute, VerbHandler: VerbExecute,
		Noun: "mlres3", Module: "test", HandlerType: spec.HandlerWorkflow,
		WorkflowID: wfID, NoAuth: true,
	}
	if err := r.Register(cs); err != nil {
		t.Fatalf("Register: %v", err)
	}
	cs = r.GetSpec(VerbExecute, "mlres3")

	cmd := &cobra.Command{Use: cs.Command}
	r.bindWorkflowCmd(cmd, cs, func(*cmdctx.Ctx) error { return nil })
	cmd.Flags().Float64("timeout", 0, "timeout")
	if err := cmd.ParseFlags([]string{"--level", "account"}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}
	// "org.myid" sets level=org from prefix; --level=account conflicts.
	_, err := buildCtx(cmd, cs, []string{"org.myid"}, r)
	if err == nil {
		t.Fatal("expected conflict error")
	}
	if !strings.Contains(err.Error(), "conflicts with") {
		t.Fatalf("error = %q, want conflicts with", err)
	}
}

// ---- resolveFlagValues ----

func TestBuildCtx_ResolveFlagValues_FnNotRegistered(t *testing.T) {
	r := New()
	registerWorkflowExecute(t, r, "resolvefn", &spec.CommandSpec{
		Flags: []spec.Flag{
			{Name: "myarg", FlagResolveFn: "unregistered_fn", Description: "test"},
		},
	})
	cs := r.GetSpec(VerbExecute, "resolvefn")
	cmd := buildWorkflowTestCmd(t, r, cs)
	if err := cmd.ParseFlags([]string{"--myarg", "somevalue"}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}
	_, err := buildCtx(cmd, cs, []string{"my-id"}, r)
	if err == nil {
		t.Fatal("expected error for unregistered flag_resolve_fn")
	}
	if !strings.Contains(err.Error(), "not registered") {
		t.Fatalf("error = %q, want 'not registered'", err)
	}
}

func TestBuildCtx_ResolveFlagValues_FnReturnsError(t *testing.T) {
	r := New()
	r.RegisterFlagResolveFn("fail_fn", func(_ *cmdctx.Ctx, raw string) (string, error) {
		return "", fmt.Errorf("resolve failed: %s", raw)
	})
	registerWorkflowExecute(t, r, "resolveerr", &spec.CommandSpec{
		Flags: []spec.Flag{
			{Name: "myarg", FlagResolveFn: "fail_fn", Description: "test"},
		},
	})
	cs := r.GetSpec(VerbExecute, "resolveerr")
	cmd := buildWorkflowTestCmd(t, r, cs)
	if err := cmd.ParseFlags([]string{"--myarg", "somevalue"}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}
	_, err := buildCtx(cmd, cs, []string{"my-id"}, r)
	if err == nil {
		t.Fatal("expected error from flag resolve fn")
	}
	if !strings.Contains(err.Error(), "resolve failed") {
		t.Fatalf("error = %q, want 'resolve failed'", err)
	}
}

func TestBuildCtx_ResolveFlagValues_FnTransforms(t *testing.T) {
	r := New()
	r.RegisterFlagResolveFn("upper_fn", func(_ *cmdctx.Ctx, raw string) (string, error) {
		return strings.ToUpper(raw), nil
	})
	registerWorkflowExecute(t, r, "resolveok", &spec.CommandSpec{
		Flags: []spec.Flag{
			{Name: "myarg", FlagResolveFn: "upper_fn", Description: "test"},
		},
	})
	cs := r.GetSpec(VerbExecute, "resolveok")
	cmd := buildWorkflowTestCmd(t, r, cs)
	if err := cmd.ParseFlags([]string{"--myarg", "hello"}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}
	ctx, err := buildCtx(cmd, cs, []string{"my-id"}, r)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cmdctx.GetString(ctx.FlagValues, "myarg") != "HELLO" {
		t.Fatalf("FlagValues[myarg] = %q, want %q", cmdctx.GetString(ctx.FlagValues, "myarg"), "HELLO")
	}
}

// ---- buildDetailCtx ----

func TestBuildDetailCtx(t *testing.T) {
	r := New()
	registerWorkflowExecute(t, r, "detailnoun", &spec.CommandSpec{})
	cs := r.GetSpec(VerbExecute, "detailnoun")
	cmd := buildWorkflowTestCmd(t, r, cs)
	parent, err := buildCtx(cmd, cs, []string{"parent-id"}, r)
	if err != nil {
		t.Fatalf("setup buildCtx: %v", err)
	}
	parent.Level = "org"

	detailCS := &spec.CommandSpec{
		Verb: VerbGet, VerbHandler: VerbGet, Noun: "detailnoun",
	}
	detail := buildDetailCtx(parent, detailCS, "child-id")

	if detail.Id != "child-id" {
		t.Fatalf("detail.Id = %q, want %q", detail.Id, "child-id")
	}
	if detail.Level != "org" {
		t.Fatalf("detail.Level = %q, want %q", detail.Level, "org")
	}
	if detail.Verb != VerbGet {
		t.Fatalf("detail.Verb = %q, want %q", detail.Verb, VerbGet)
	}
	if detail.FormatFlags.Format != "text" {
		t.Fatalf("detail.FormatFlags.Format = %q, want %q", detail.FormatFlags.Format, "text")
	}
	if detail.Context == nil {
		t.Fatal("detail.Context is nil")
	}
}

// ---- validateIdParts via AllowsParentId ----

func TestValidateIdParts_ParentIdSlashNotAllowed(t *testing.T) {
	r := New()
	registerWorkflowList(t, r, "parentslash", &spec.CommandSpec{})
	cs := r.GetSpec(VerbList, "parentslash")
	cmd := &cobra.Command{Use: cs.Command}
	r.bindWorkflowCmd(cmd, cs, func(*cmdctx.Ctx) error { return nil })
	cmd.Flags().Float64("timeout", 0, "timeout")

	_, err := buildCtx(cmd, cs, []string{"parent/bad"}, r)
	if err == nil {
		t.Fatal("expected error for slash in parent id")
	}
	if !strings.Contains(err.Error(), "must not contain '/'") {
		t.Fatalf("error = %q, want must not contain '/'", err)
	}
}

func TestValidateIdParts_IdPartsPopulated(t *testing.T) {
	// IdParts=2 and exactly 2 slash-separated parts: IdParts slice should be populated.
	r := New()
	registerWorkflowExecute(t, r, "twoparts", &spec.CommandSpec{
		IdParts: 2,
		IdLabel: "<a/b>",
	})
	cs := r.GetSpec(VerbExecute, "twoparts")
	cmd := buildWorkflowTestCmd(t, r, cs)
	ctx, err := buildCtx(cmd, cs, []string{"agentA/clusterB"}, r)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(ctx.IdParts) != 2 || ctx.IdParts[0] != "agentA" || ctx.IdParts[1] != "clusterB" {
		t.Fatalf("ctx.IdParts = %v, want [agentA clusterB]", ctx.IdParts)
	}
}
