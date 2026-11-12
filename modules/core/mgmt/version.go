// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package mgmt

import (
	"fmt"
	"sort"

	"github.com/harness/cli/v3/pkg/cmdctx"
	"github.com/harness/cli/v3/pkg/hbase"
)

func VersionHandler(_ *cmdctx.Ctx) error {
	bt := hbase.BuildTime
	if bt == "" {
		bt = "dev"
	}
	fmt.Printf("harness version %s (%s)\n", hbase.Version, bt)
	names := make([]string, 0, len(hbase.EmbeddedPlugins))
	for name := range hbase.EmbeddedPlugins {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		fmt.Printf("  %s version %s (built in)\n", name, hbase.EmbeddedPlugins[name])
	}
	return nil
}
