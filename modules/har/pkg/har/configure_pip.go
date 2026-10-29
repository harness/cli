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

	registryURL := fmt.Sprintf("%s/pkg/%s/%s/pypi/simple/", a.RegistryURL, a.AccountID, registryID)

	pipConfPath, err := pipConfigPath(ctx)
	if err != nil {
		return err
	}

	backupPath, err := backupFile(pipConfPath, "pip-conf")
	if err != nil {
		return fmt.Errorf("backing up pip.conf: %w", err)
	}
	if backupPath != "" {
		fmt.Printf("Backed up existing pip.conf → %s\n", backupPath)
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

// pip does not read ./pip.conf, so the local file only takes effect when the
// user points PIP_CONFIG_FILE at it; global is therefore the default.
func pipConfigPath(ctx *cmdctx.Ctx) (string, error) {
	if ctx.ProvidedFlags["global"] && !cmdctx.GetBool(ctx.FlagValues, "global") {
		return "pip.conf", nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("getting home directory: %w", err)
	}
	pipConfDir := filepath.Join(home, ".config", "pip")
	if err := os.MkdirAll(pipConfDir, 0755); err != nil {
		return "", fmt.Errorf("creating pip config directory: %w", err)
	}
	return filepath.Join(pipConfDir, "pip.conf"), nil
}

func writePipConf(pipConfPath, registryURL, authToken string) error {
	authedURL := strings.Replace(registryURL, "://", fmt.Sprintf("://harness:%s@", authToken), 1)
	content := fmt.Sprintf("[global]\nindex-url = %s\n", authedURL)
	return atomicWrite(pipConfPath, []byte(content), 0600)
}
