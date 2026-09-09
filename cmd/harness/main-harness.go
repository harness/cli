// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	_ "embed"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/mod/semver"

	"github.com/harness/cli/v3/modules/code"
	"github.com/harness/cli/v3/modules/core"
	"github.com/harness/cli/v3/modules/core/mgmt"
	"github.com/harness/cli/v3/modules/gitops"
	"github.com/harness/cli/v3/modules/iacm"
	"github.com/harness/cli/v3/modules/pipeline"
	"github.com/harness/cli/v3/modules/platform"
	"github.com/harness/cli/v3/modules/rt"
	"github.com/harness/cli/v3/modules/vibeapps"
	"github.com/harness/cli/v3/pkg/console"
	"github.com/harness/cli/v3/pkg/hbase"
	"github.com/harness/cli/v3/pkg/registry"
	"github.com/harness/cli/v3/pkg/rootcmd"
	"github.com/harness/cli/v3/pkg/spec"
	"github.com/harness/cli/v3/pkg/specloader"
)

//go:embed noargs.txt
var noargsText string

func main() {
	rootcmd.MaybeRunBackgroundUpdateCheck()
	rootcmd.MaybeRunPostInstall()
	rootcmd.MaybeRunPostUpgrade()

	if !semver.IsValid("v" + hbase.Version) {
		console.PrintError(fmt.Sprintf("invalid version %q: must be a valid semver (e.g. 1.2.3)", hbase.Version))
		os.Exit(1)
	}

	reg := registry.New()
	reg.IsMainBinary = true
	if err := specloader.LoadSpecs(reg); err != nil {
		console.PrintError(err.Error())
		os.Exit(1)
	}
	code.ModuleInit(reg.Module("code"))
	core.ModuleInit(reg.Module("core"))
	gitops.ModuleInit(reg.Module("gitops"))
	pipeline.ModuleInit(reg.Module("pipeline"))
	platform.ModuleInit(reg.Module("platform"))
	// har is an external module (external_binary: harness-har) — ModuleInit is not loaded here.
	iacm.ModuleInit(reg.Module("iacm"))
	rt.ModuleInit(reg.Module("rt"))
	vibeapps.ModuleInit(reg.Module("vibeapps"))
	rootcmd.MaybeCheckSpecs(reg)

	root := &cobra.Command{
		Use:   "harness",
		Short: "Harness CLI",
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Print(strings.ReplaceAll(noargsText, "{{modules}}", renderModules(reg.GetModuleMetas())))
			return nil
		},
	}
	defaultHelp := root.HelpFunc()
	root.SetHelpFunc(func(cmd *cobra.Command, args []string) {
		if !cmd.HasParent() {
			fmt.Print(strings.ReplaceAll(noargsText, "{{modules}}", renderModules(reg.GetModuleMetas())))
			return
		}
		defaultHelp(cmd, args)
	})
	rootcmd.SetupAndExecuteRootCmd(root, reg)
}

func renderModules(metas []spec.ModuleMeta) string {
	seen := map[string]bool{}
	var visible []spec.ModuleMeta
	for _, m := range metas {
		seen[m.Name] = true
		if !m.Core {
			visible = append(visible, m)
		}
	}
	visible = append(visible, mgmt.UninstalledRegistryPlugins(seen)...)

	// find longest name for alignment
	maxLen := 0
	for _, m := range visible {
		if len(m.Name) > maxLen {
			maxLen = len(m.Name)
		}
	}

	var sb strings.Builder
	for _, m := range visible {
		fmt.Fprintf(&sb, "  %-*s  %s\n", maxLen, m.Name, m.Desc)
	}
	return strings.TrimRight(sb.String(), "\n")
}
