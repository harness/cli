// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package rootcmd

import (
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/harness/cli/v3/pkg/registry"
)

func init() {
	cobra.AddTemplateFunc("ownFlags", func(c *cobra.Command) *pflag.FlagSet {
		own, _ := splitLocalFlags(c)
		return own
	})
	cobra.AddTemplateFunc("verbFlags", func(c *cobra.Command) *pflag.FlagSet {
		_, core := splitLocalFlags(c)
		return core
	})
	cobra.AddTemplateFunc("globalFlags", func(c *cobra.Command) *pflag.FlagSet {
		global := pflag.NewFlagSet(c.Name(), pflag.ContinueOnError)
		c.InheritedFlags().VisitAll(func(f *pflag.Flag) {
			global.AddFlag(f)
		})
		if help := c.LocalFlags().Lookup("help"); help != nil {
			global.AddFlag(help)
		}
		return global
	})
}

// splitLocalFlags partitions cmd's local flags into command-specific ("own")
// and built-in ("core") flags, per registry.IsCoreFlag. Cobra's own
// auto-added "help" flag is excluded from both, since it's displayed under
// Global Flags (see the "globalFlags" template func) rather than mixed in
// with a command's real flags. Both returned sets share the same *pflag.Flag
// pointers as cmd.LocalFlags(), so hidden/deprecated flags are still skipped
// by FlagUsages() as normal.
func splitLocalFlags(c *cobra.Command) (own, core *pflag.FlagSet) {
	own = pflag.NewFlagSet(c.Name(), pflag.ContinueOnError)
	core = pflag.NewFlagSet(c.Name(), pflag.ContinueOnError)
	c.LocalFlags().VisitAll(func(f *pflag.Flag) {
		if f.Name == "help" {
			return
		}
		if registry.IsCoreFlag(f.Name) {
			core.AddFlag(f)
		} else {
			own.AddFlag(f)
		}
	})
	return own, core
}

// usageTemplate is Cobra's defaultUsageTemplate with the Flags section split
// into command-specific flags and built-in verb/endpoint-level flags, so
// --help doesn't mix a command's own flags with framework flags that are
// mechanically the same across every command sharing its verb.
const usageTemplate = `Usage:{{if .Runnable}}
  {{.UseLine}}{{end}}{{if .HasAvailableSubCommands}}
  {{.CommandPath}} [command]{{end}}{{if gt (len .Aliases) 0}}

Aliases:
  {{.NameAndAliases}}{{end}}{{if .HasExample}}

Examples:
{{.Example}}{{end}}{{if .HasAvailableSubCommands}}{{$cmds := .Commands}}{{if eq (len .Groups) 0}}

Available Commands:{{range $cmds}}{{if (or .IsAvailableCommand (eq .Name "help"))}}
  {{rpad .Name .NamePadding }} {{.Short}}{{end}}{{end}}{{else}}{{range $group := .Groups}}

{{.Title}}{{range $cmds}}{{if (and (eq .GroupID $group.ID) (or .IsAvailableCommand (eq .Name "help")))}}
  {{rpad .Name .NamePadding }} {{.Short}}{{end}}{{end}}{{end}}{{if not .AllChildCommandsHaveGroup}}

Additional Commands:{{range $cmds}}{{if (and (eq .GroupID "") (or .IsAvailableCommand (eq .Name "help")))}}
  {{rpad .Name .NamePadding }} {{.Short}}{{end}}{{end}}{{end}}{{end}}{{end}}{{if (ownFlags .).HasAvailableFlags}}

Command Flags:
{{(ownFlags .).FlagUsages | trimTrailingWhitespaces}}{{end}}{{if (verbFlags .).HasAvailableFlags}}

Common Flags:
{{(verbFlags .).FlagUsages | trimTrailingWhitespaces}}{{end}}{{if (globalFlags .).HasAvailableFlags}}

Global Flags:
{{(globalFlags .).FlagUsages | trimTrailingWhitespaces}}{{end}}{{if .HasHelpSubCommands}}

Additional help topics:{{range .Commands}}{{if .IsAdditionalHelpTopicCommand}}
  {{rpad .CommandPath .CommandPathPadding}} {{.Short}}{{end}}{{end}}{{end}}{{if .HasAvailableSubCommands}}

Use "{{.CommandPath}} [command] --help" for more information about a command.{{end}}
`
