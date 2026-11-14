// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package har

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/harness/cli/v3/pkg/auth"
	"github.com/harness/cli/v3/pkg/client"
	"github.com/harness/cli/v3/pkg/cmdctx"
	"github.com/harness/cli/v3/pkg/config"
	"github.com/harness/cli/v3/pkg/hbase"
	"github.com/harness/cli/v3/pkg/registry"
)

// withPackageHost resolves the package host from the API URL before fn runs.
// Wrap every handler that reads ctx.Auth.RegistryURL.
func withPackageHost(fn registry.WorkflowFn) registry.WorkflowFn {
	return func(ctx *cmdctx.Ctx) error {
		if err := resolvePackageHost(ctx); err != nil {
			return err
		}
		return fn(ctx)
	}
}

// Off production, a package host the user did not configure must be looked up.
// Pipeline auth applies no default: an empty host there is always looked up.
func resolvePackageHost(ctx *cmdctx.Ctx) error {
	a := ctx.Auth
	if a == nil {
		return nil
	}
	if a.Source == auth.SourcePipeline {
		if a.RegistryURL != "" {
			if !strings.Contains(a.RegistryURL, "://") {
				a.RegistryURL = "https://" + a.RegistryURL
			}
			return nil
		}
	} else if !packageHostIsDefaulted(a) {
		return nil
	}
	res, _, err := client.New(ctx).Get("/gateway/har/api/v3/system/info",
		map[string]string{"account_identifier": a.AccountID})
	if err == nil {
		body, _ := res.(map[string]any)
		data, _ := body["data"].(map[string]any)
		if registryURL, _ := data["registryUrl"].(string); registryURL != "" {
			a.RegistryURL = registryURL
			return nil
		}
		err = errors.New("response has no registryUrl")
	}
	if hint := packageHostHint(a); hint != "" {
		return fmt.Errorf("could not get the package registry URL from %s (%s): %w", a.APIUrl, hint, err)
	}
	return fmt.Errorf("could not get the package registry URL from %s: %w", a.APIUrl, err)
}

// packageHostHint is empty for SSO profiles: SSO login does not save a package
// host and profile mode ignores the env override, so no command would fix it.
func packageHostHint(a *auth.ResolvedAuth) string {
	switch {
	case a.Source == auth.SourceEnv:
		return "set " + hbase.EnvRegistryURL + " to override"
	case a.Source == auth.SourcePipeline:
		return "the pipeline step did not provide a package registry URL"
	case a.AuthType == auth.AuthTypeSSO:
		return ""
	default:
		return "run '" + a.LoginHint("login") + " --overwrite' to save it in the profile"
	}
}

func packageHostIsDefaulted(a *auth.ResolvedAuth) bool {
	return strings.TrimRight(a.APIUrl, "/") != hbase.DefaultAPIURL && !packageHostConfigured(a)
}

// packageHostConfigured reports whether the user chose a.RegistryURL. Core auth
// fills in the production host when none is configured, so that value counts as
// chosen only if the env var or the saved profile actually sets it.
func packageHostConfigured(a *auth.ResolvedAuth) bool {
	registryURL := strings.TrimRight(a.RegistryURL, "/")
	if registryURL == "" {
		return false
	}
	if registryURL != hbase.DefaultRegistryURL {
		return true
	}
	if a.Source == auth.SourceEnv {
		return os.Getenv(hbase.EnvRegistryURL) != ""
	}
	name, ok := strings.CutPrefix(a.Source, "profile:")
	if !ok {
		return false
	}
	cfg, err := config.LoadConfig()
	if err != nil {
		return false
	}
	p := cfg.Profiles[name]
	return p != nil && p.RegistryURL != ""
}
