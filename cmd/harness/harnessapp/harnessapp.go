// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

// Package harnessapp is the harness binary's entire main(), exported so a
// downstream repo can build a custom distribution of the CLI: the stock
// builtin modules plus extra modules compiled in (see Options.Embedded).
package harnessapp

import (
	_ "embed"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/mod/semver"

	"github.com/harness/cli/v3/modules/ci"
	"github.com/harness/cli/v3/modules/code"
	"github.com/harness/cli/v3/modules/core"
	"github.com/harness/cli/v3/modules/core/mgmt"
	"github.com/harness/cli/v3/modules/fme"
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

// Options customizes the binary. The zero value is the stock open-source harness.
type Options struct {
	// Distribution names a custom build (e.g. "harness-ci"). Setting it turns off
	// self-update, since the stock release is not a valid replacement.
	Distribution string
	// Embedded lists modules compiled into this binary that would otherwise be
	// separate plugin binaries.
	Embedded []EmbeddedModule
}

// EmbeddedModule is a plugin-style module linked into the binary. It runs
// in-process, and `install plugin <Name>` is refused.
type EmbeddedModule struct {
	Name string
	// Version is shown by `harness version`; it is independent of the core version.
	Version string
	// Spec is the module's spec YAML; it is registered as <Name>.spec.yaml.
	Spec []byte
	Init func(registry.ModuleRegistrar)
}

func Run(opts Options) {
	rootcmd.MaybeRunBackgroundUpdateCheck()
	rootcmd.MaybeRunPostInstall()
	rootcmd.MaybeRunPostUpgrade()

	if !semver.IsValid("v" + hbase.Version) {
		console.PrintError(fmt.Sprintf("invalid version %q: must be a valid semver (e.g. 1.2.3)", hbase.Version))
		hbase.Exit(1)
	}

	hbase.Distribution = opts.Distribution
	hbase.EmbeddedPlugins = map[string]string{}

	reg := registry.New()
	reg.IsMainBinary = true
	// Embedded modules load before LoadSpecs so they win over a same-named
	// plugin spec installed in ~/.harness/spec.
	for _, m := range opts.Embedded {
		if _, err := specloader.LoadSpecBytes(reg, m.Name+".spec.yaml", m.Spec, true); err != nil {
			console.PrintError(err.Error())
			hbase.Exit(1)
		}
		reg.MarkEmbedded(m.Name, m.Version)
		hbase.EmbeddedPlugins[m.Name] = m.Version
	}
	if err := specloader.LoadSpecs(reg); err != nil {
		console.PrintError(err.Error())
		hbase.Exit(1)
	}
	ci.ModuleInit(reg.Module("ci"))
	code.ModuleInit(reg.Module("code"))
	core.ModuleInit(reg.Module("core"))
	fme.ModuleInit(reg.Module("fme"))
	gitops.ModuleInit(reg.Module("gitops"))
	pipeline.ModuleInit(reg.Module("pipeline"))
	platform.ModuleInit(reg.Module("platform"))
	// har is an external module (external_binary: harness-har) — ModuleInit is not loaded here.
	iacm.ModuleInit(reg.Module("iacm"))
	rt.ModuleInit(reg.Module("rt"))
	vibeapps.ModuleInit(reg.Module("vibeapps"))
	for _, m := range opts.Embedded {
		m.Init(reg.Module(m.Name))
	}
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
