// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package har

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/harness/cli/v3/pkg/cmdctx"
)

func configurePip(ctx *cmdctx.Ctx) error {
	a := ctx.Auth
	registryID := ctx.Id
	global := cmdctx.GetBool(ctx.FlagValues, "global")

	registryURL := fmt.Sprintf("%s/pkg/%s/%s/pypi/simple/", a.RegistryURL, a.AccountID, registryID)

	var pipConfPath string
	if global {
		home, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("getting home directory: %w", err)
		}
		pipConfDir := filepath.Join(home, ".config", "pip")
		if err := os.MkdirAll(pipConfDir, 0755); err != nil {
			return fmt.Errorf("creating pip config directory: %w", err)
		}
		pipConfPath = filepath.Join(pipConfDir, "pip.conf")
	} else {
		pipConfPath = "pip.conf"
	}

	if err := writePipConf(pipConfPath, registryURL, a.PATToken); err != nil {
		return fmt.Errorf("writing pip.conf: %w", err)
	}

	_ = savePkgmgrConfig("pip", pkgmgrSavedConfig{
		RegistryIdentifier: registryID,
		RegistryURL:        registryURL,
		OrgID:              a.OrgID,
		ProjectID:          a.ProjectID,
	})

	fmt.Printf("Configured pip → %s (%s)\n", registryURL, pipConfPath)
	return nil
}

func writePipConf(pipConfPath, registryURL, authToken string) error {
	authedURL := strings.Replace(registryURL, "://", fmt.Sprintf("://harness:%s@", authToken), 1)
	content := fmt.Sprintf("[global]\nindex-url = %s\n", authedURL)
	return atomicWrite(pipConfPath, []byte(content), 0600)
}
