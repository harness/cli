// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"os"

	"github.com/spf13/cobra"

	"github.com/harness/cli/modules/har/pkg/har"
	"github.com/harness/cli/pkg/console"
	"github.com/harness/cli/pkg/registry"
	"github.com/harness/cli/pkg/rootcmd"
	"github.com/harness/cli/pkg/specloader"
)

func main() {
	reg := registry.New()
	if err := specloader.LoadSpec(reg, "har.spec.yaml", true); err != nil {
		console.PrintError(err.Error())
		os.Exit(1)
	}
	har.ModuleInit(reg.Module("har"))
	rootcmd.MaybeCheckSpecs(reg)
	root := &cobra.Command{
		Use:   "harness-har",
		Short: "Harness Artifact Registry CLI",
	}
	rootcmd.SetupAndExecutePluginRootCmd(root, reg, "har")
}
