// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package mgmt

import (
	"fmt"
	"os"

	"golang.org/x/mod/semver"

	"github.com/harness/cli/pkg/cmdctx"
	"github.com/harness/cli/pkg/hbase"
	"github.com/harness/cli/pkg/release"
)

func DebugUpdateCheckHandler(_ *cmdctx.Ctx) error {
	fmt.Printf("repo:    %s\n", release.Repo)
	fmt.Printf("current: %s\n", hbase.Version)
	fmt.Printf("fetching latest version...\n")

	latest, err := release.FetchLatestVersion()
	if err != nil {
		fmt.Fprintf(os.Stderr, "fetch failed: %v\n", err)
		return nil
	}
	fmt.Printf("latest:  %s\n", latest)

	release.RunBackgroundCheck()
	fmt.Printf("cache updated\n")

	cur := "v" + hbase.Version
	if semver.Compare(latest, cur) > 0 {
		fmt.Printf("update available: %s → %s\n", hbase.Version, latest)
	} else {
		fmt.Printf("up to date\n")
	}
	return nil
}
