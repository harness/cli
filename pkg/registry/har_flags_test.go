// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package registry

import (
	"slices"
	"testing"

	"github.com/spf13/cobra"

	"github.com/harness/cli/v3/pkg/cmdctx"
	"github.com/harness/cli/v3/pkg/spec"
)

func TestHARCredentialFlagsAreModuleScoped(t *testing.T) {
	r := New()
	registerWorkflowNoun(t, r, "artifact")
	r.RegisterWorkflow("har:push", func(*cmdctx.Ctx) error { return nil })
	r.RegisterWorkflow("pipeline:list", func(*cmdctx.Ctx) error { return nil })

	har := buildWorkflowTestCmd(t, r, &spec.CommandSpec{
		Command: "push artifact", Verb: VerbPush, VerbHandler: VerbPush,
		Noun: "artifact", Module: "har", HandlerType: spec.HandlerWorkflow, WorkflowID: "har:push",
	})
	if har.Flags().Lookup("token") == nil || har.Flags().Lookup("api-url") == nil {
		t.Fatal("artifact registry command is missing --api-url/--token")
	}

	pipeline := buildWorkflowTestCmd(t, r, &spec.CommandSpec{
		Command: "list pipeline", Verb: VerbList, VerbHandler: VerbList,
		Noun: "artifact", Module: "pipeline", HandlerType: spec.HandlerWorkflow, WorkflowID: "pipeline:list",
	})
	if pipeline.Flags().Lookup("token") != nil || pipeline.Flags().Lookup("api-url") != nil {
		t.Fatal("--api-url/--token are not limited to artifact registry commands")
	}

	root := &cobra.Command{Use: "harness"}
	r.AttachGlobalAuthFlags(root)
	if root.PersistentFlags().Lookup("token") != nil || root.PersistentFlags().Lookup("api-url") != nil {
		t.Fatal("--api-url/--token are still global")
	}
}

func TestRedactTokenArgs(t *testing.T) {
	cases := []struct {
		in, want []string
	}{
		{
			in:   []string{"push", "artifact:generic", "--token", "pat.a.b.c", "reg/pkg", "f.txt"},
			want: []string{"push", "artifact:generic", "--token", "[redacted]", "reg/pkg", "f.txt"},
		},
		{
			in:   []string{"--debug", "--token=pat.a.b.c", "list", "registry"},
			want: []string{"--debug", "--token=[redacted]", "list", "registry"},
		},
		{in: []string{"list", "registry", "--token"}, want: []string{"list", "registry", "--token"}},
		{in: []string{"push", "--", "--token", "literal"}, want: []string{"push", "--", "--token", "literal"}},
	}
	for _, tc := range cases {
		in := slices.Clone(tc.in)
		if got := redactTokenArgs(in); !slices.Equal(got, tc.want) {
			t.Errorf("redactTokenArgs(%q) = %q, want %q", tc.in, got, tc.want)
		}
		if !slices.Equal(in, tc.in) {
			t.Errorf("redactTokenArgs modified its input: %q", in)
		}
	}
}
