// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package har

import (
	"errors"
	"fmt"
	"os"
	"os/exec"

	"github.com/harness/cli/v3/pkg/cmdctx"
)

const (
	executeArtifactNpmHandlerID    = "execute_artifact_npm"
	executeArtifactPipHandlerID    = "execute_artifact_pip"
	executeArtifactMvnHandlerID    = "execute_artifact_mvn"
	executeArtifactDotnetHandlerID = "execute_artifact_dotnet"
)

var (
	npmFirewallCommands    = map[string]bool{"install": true, "ci": true}
	pipFirewallCommands    = map[string]bool{"install": true}
	mvnFirewallCommands    = map[string]bool{"install": true}
	dotnetFirewallCommands = map[string]bool{"restore": true}
	pkgmgrExit             = os.Exit
)

type pkgmgrPlanResult struct {
	wrapped    bool
	command    string
	nativeArgs []string
	registry   string
	args       []string
}

// pkgmgrPlan decides whether args run through the firewall wrapper.
// install/ci/restore match hc's explicit subcommands; every other command is
// forwarded to the native binary, including its original arguments.
func pkgmgrPlan(usageName string, args []string, wrapped map[string]bool) (pkgmgrPlanResult, error) {
	if len(args) == 0 {
		return pkgmgrPlanResult{}, fmt.Errorf("harness execute artifact:%s requires a command\nusage: harness execute artifact:%s <command> [-- <args...>]", usageName, usageName)
	}
	registry, nativeArgs := pkgmgrParseArgs(args)
	if len(nativeArgs) == 0 || !wrapped[nativeArgs[0]] {
		return pkgmgrPlanResult{args: args}, nil
	}
	return pkgmgrPlanResult{
		wrapped:    true,
		command:    nativeArgs[0],
		nativeArgs: nativeArgs[1:],
		registry:   registry,
	}, nil
}

func pkgmgrDispatch(ctx *cmdctx.Ctx, client pkgmgrClient, usageName, binary string, wrapped map[string]bool) error {
	plan, err := pkgmgrPlan(usageName, ctx.Args, wrapped)
	if err != nil {
		return err
	}
	if !plan.wrapped {
		return pkgmgrRunNative(binary, plan.args)
	}
	if plan.registry != "" {
		ctx.Id = plan.registry
	}
	return pkgmgrExecute(ctx, client, plan.command, plan.nativeArgs)
}

func pkgmgrRunNative(binary string, args []string) error {
	binPath, err := exec.LookPath(binary)
	if err != nil {
		return fmt.Errorf("%s not found in PATH: %w", binary, err)
	}
	cmd := exec.Command(binPath, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	err = cmd.Run()
	if err == nil {
		return nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		pkgmgrExit(exitErr.ExitCode())
		return nil
	}
	return err
}
