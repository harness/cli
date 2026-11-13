// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package pipeline

import (
	"fmt"
	"strings"

	"github.com/harness/cli/v3/pkg/cmdctx"
	"github.com/harness/cli/v3/pkg/console"
	"github.com/harness/cli/v3/pkg/registry"
)

const (
	approveInstanceHandlerID = "approve_instance"
	rejectInstanceHandlerID  = "reject_instance"
)

func approveInstanceHandler(ctx *cmdctx.Ctx) error { return resolveApproval(ctx, "approve") }

func rejectInstanceHandler(ctx *cmdctx.Ctx) error { return resolveApproval(ctx, "reject") }

// resolveApproval runs the existing execute approval_instance:<action> endpoint
// from the --ui detail view, after prompting for a comment and confirmation.
func resolveApproval(ctx *cmdctx.Ctx, action string) error {
	ctx.UIWantBack = true
	if !console.IsBothTTY() {
		return fmt.Errorf("%s requires an interactive terminal (TTY)", action)
	}
	cs := ctx.Resolver.GetSpec("execute", "approval_instance:"+action)
	if cs == nil || cs.Endpoint == nil {
		return fmt.Errorf("execute approval_instance:%s is not available", action)
	}

	ctx.Id = ctx.Id[strings.LastIndex(ctx.Id, "/")+1:]
	comment, err := console.ReadPrompt("Comment (optional)", "")
	if err != nil {
		return err
	}
	if !console.PromptYesNo(fmt.Sprintf("%s %s?", strings.ToUpper(action[:1])+action[1:], ctx.Id)) {
		fmt.Println("Cancelled")
		return nil
	}

	ctx.SetFlag("comment", comment)
	if _, err := registry.CallEndpoint(ctx, cs.Endpoint); err != nil {
		return err
	}
	fmt.Printf("%s submitted for %s\n", strings.ToUpper(action[:1])+action[1:], ctx.Id)
	return nil
}
