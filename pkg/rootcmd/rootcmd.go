// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package rootcmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/harness/harness-cli/pkg/cmdctx"
	"github.com/harness/harness-cli/pkg/console"
	"github.com/harness/harness-cli/pkg/hbase"
	"github.com/harness/harness-cli/pkg/hlog"
	"github.com/harness/harness-cli/modules/core/mgmt"
	"github.com/harness/harness-cli/pkg/registry"
	"github.com/harness/harness-cli/pkg/spec"
	"github.com/harness/harness-cli/pkg/specloader"
)

// MaybeCheckSpecs runs spec validation and exits if HARNESS_CHECKSPECS=1, otherwise returns immediately.
func MaybeCheckSpecs(reg *registry.Registry) {
	if os.Getenv(hbase.EnvCheckSpecs) != "1" {
		return
	}
	if err := reg.CheckFunctions(); err != nil {
		console.PrintError(err.Error())
		os.Exit(1)
	}
	for _, w := range reg.CheckWarnings() {
		fmt.Fprintf(os.Stderr, "warning: %s\n", w)
	}
	var names []string
	for _, m := range reg.GetModuleMetas() {
		names = append(names, m.Name)
	}
	fmt.Printf("specs ok [%s]\n", strings.Join(names, ", "))
	os.Exit(0)
}

// SetupAndExecutePluginRootCmd is like SetupAndExecuteRootCmd but adds hidden
// --spec and --modulehelp flags for use by the plugin host.
func SetupAndExecutePluginRootCmd(root *cobra.Command, reg *registry.Registry, moduleName string) {
	if os.Getenv(hbase.EnvDebugCompletion) == "1" && isCompletionInvocation() {
		hlog.SetDebugFile(hbase.CompletionDebugLogFile)
	}
	hlog.SetPlugin(moduleName)
	root.Flags().Bool("spec", false, "Dump the module spec YAML to stdout")
	root.Flags().Lookup("spec").Hidden = true
	root.Flags().Bool("modulehelp", false, "Dump the rendered module help text to stdout")
	root.Flags().Lookup("modulehelp").Hidden = true

	root.AddCommand(&cobra.Command{
		Use:   "version",
		Short: "Print the " + moduleName + " plugin version",
		RunE: func(cmd *cobra.Command, args []string) error {
			bt := hbase.BuildTime
			if bt == "" {
				bt = "dev"
			}
			fmt.Printf("harness-%s version %s (%s)\n", moduleName, hbase.Version, bt)
			return nil
		},
	})

	origRun := root.RunE
	root.RunE = func(cmd *cobra.Command, args []string) error {
		if ok, _ := cmd.Flags().GetBool("spec"); ok {
			return dumpSpec(moduleName)
		}
		if ok, _ := cmd.Flags().GetBool("modulehelp"); ok {
			return dumpModuleHelp(moduleName, reg)
		}
		if origRun != nil {
			return origRun(cmd, args)
		}
		fmt.Printf("%s\nVersion %s\n", root.Short, hbase.Version)
		return nil
	}
	SetupAndExecuteRootCmd(root, reg)
}

func dumpSpec(moduleName string) error {
	data, err := specloader.ReadSpecFile(moduleName)
	if err != nil {
		return err
	}
	fmt.Print(string(data))
	return nil
}

func dumpModuleHelp(moduleName string, reg *registry.Registry) error {
	var meta *spec.ModuleMeta
	for _, m := range reg.GetModuleMetas() {
		if m.Name == moduleName {
			m := m
			meta = &m
			break
		}
	}
	if meta == nil || meta.HelpText == "" {
		return nil
	}
	var nouns []string
	seen := map[string]bool{}
	for _, n := range meta.NounOrder {
		if !seen[n] {
			seen[n] = true
			nouns = append(nouns, n)
		}
	}
	nounBlock := mgmt.RenderNounBlock(moduleName, nouns, reg)
	fmt.Print(strings.ReplaceAll(meta.HelpText, "{{nouns}}", nounBlock))
	return nil
}

// SetupAndExecuteRootCmd wires common flags, attaches commands, and executes root.
func SetupAndExecuteRootCmd(root *cobra.Command, reg *registry.Registry) {
	if os.Getenv(hbase.EnvDebugCompletion) == "1" && isCompletionInvocation() {
		hlog.SetDebugFile(hbase.CompletionDebugLogFile)
	}
	root.SilenceUsage = true
	root.SilenceErrors = true

	root.PersistentFlags().BoolFunc("debug", "Enable debug logging", func(string) error {
		if !isCompletionInvocation() {
			hlog.SetDebug()
		}
		return nil
	})
	root.PersistentFlags().Float64("timeout", 0, "Command timeout in seconds (0 = no timeout, e.g. 1.5)")
	reg.AttachGlobalAuthFlags(root)

	for _, cmd := range reg.BuildCommands() {
		root.AddCommand(cmd)
	}

	if err := root.Execute(); err != nil {
		console.PrintError(err.Error())
		if cmdctx.IsTimeout(err) {
			os.Exit(hbase.TimeoutExitCode)
		}
		os.Exit(1)
	}
}

func isCompletionInvocation() bool {
	for _, arg := range os.Args[1:] {
		if arg == "__complete" || arg == "__completeNoDesc" {
			return true
		}
	}
	return false
}
