// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package registry

import (
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
