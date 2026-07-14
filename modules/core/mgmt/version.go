// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package mgmt

import (
	"fmt"

	"github.com/harness/cli/pkg/cmdctx"
	"github.com/harness/cli/pkg/hbase"
)

func VersionHandler(_ *cmdctx.Ctx) error {
	bt := hbase.BuildTime
	if bt == "" {
		bt = "dev"
	}
	fmt.Printf("harness version %s (%s)\n", hbase.Version, bt)
	return nil
}
