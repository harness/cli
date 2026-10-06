// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"fmt"
	"os"

	"github.com/harness/cli/v3/pkg/auth"
	"github.com/harness/cli/v3/pkg/cmdctx"
	"github.com/harness/cli/v3/pkg/hbase"
)

func EnvHandler(ctx *cmdctx.Ctx) error {
	profileFlag := cmdctx.GetString(ctx.FlagValues, "profile")
	export := cmdctx.GetBool(ctx.FlagValues, "export")

	resolved, err := auth.Load(profileFlag)
	if err != nil {
		return err
	}

	prefix := ""
	if export {
		prefix = "export "
	}

	var vars []struct{ k, v string }
	switch resolved.AuthType {
	case auth.AuthTypeSSO:
		vars = append(vars, struct{ k, v string }{hbase.EnvAPIJWT, resolved.SSOToken})
	case auth.AuthTypeCI:
		vars = append(vars, struct{ k, v string }{hbase.EnvCIToken, resolved.CIToken})
	default:
		vars = append(vars, struct{ k, v string }{hbase.EnvAPIKey, resolved.PATToken})
	}
	vars = append(vars,
		struct{ k, v string }{hbase.EnvAccount, resolved.AccountID},
		struct{ k, v string }{hbase.EnvAPIURL, resolved.APIUrl},
	)
	if resolved.OrgID != "" {
		vars = append(vars, struct{ k, v string }{hbase.EnvOrg, resolved.OrgID})
	}
	if resolved.ProjectID != "" {
		vars = append(vars, struct{ k, v string }{hbase.EnvProject, resolved.ProjectID})
	}
	if resolved.RegistryURL != "" {
		vars = append(vars, struct{ k, v string }{hbase.EnvRegistryURL, resolved.RegistryURL})
	}

	for _, v := range vars {
		fmt.Fprintf(os.Stdout, "%s%s=%s\n", prefix, v.k, v.v)
	}
	return nil
}
