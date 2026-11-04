// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package har

import (
	"errors"
	"fmt"
	"strings"

	"github.com/harness/cli/v3/pkg/auth"
	"github.com/harness/cli/v3/pkg/client"
	"github.com/harness/cli/v3/pkg/cmdctx"
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

// Core auth falls back to the production package host when none is configured, so
// off production that value is a default, not a choice, and must be looked up.
func resolvePackageHost(ctx *cmdctx.Ctx) error {
	a := ctx.Auth
	if a == nil || a.Source == auth.SourcePipeline || !packageHostIsDefaulted(a) {
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
	return fmt.Errorf("could not get the package registry URL from %s (%s): %w", a.APIUrl, packageHostHint(a), err)
}

func packageHostHint(a *auth.ResolvedAuth) string {
	if a.Source == auth.SourceEnv {
		return "set " + hbase.EnvRegistryURL + " to override"
	}
	return "run '" + a.LoginHint("login") + " --overwrite' to save it in the profile"
}

func packageHostIsDefaulted(a *auth.ResolvedAuth) bool {
	registryURL := strings.TrimRight(a.RegistryURL, "/")
	return (registryURL == "" || registryURL == hbase.DefaultRegistryURL) &&
		strings.TrimRight(a.APIUrl, "/") != hbase.DefaultAPIURL
}
