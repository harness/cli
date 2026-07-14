// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"sort"
	"strings"

	"github.com/harness/cli/pkg/auth"
	"github.com/harness/cli/pkg/cmdctx"
	"github.com/harness/cli/pkg/config"
	"github.com/harness/cli/pkg/spec"
)

func ProfilesFetchFn(ctx *cmdctx.Ctx, _ *spec.EndpointSpec, _, _ int, _ any) (*cmdctx.PageResult, error) {
	cfg, err := config.LoadConfig()
	if err != nil {
		return nil, err
	}
	search := cmdctx.GetString(ctx.FlagValues, "search")

	names := make([]string, 0, len(cfg.Profiles))
	for name := range cfg.Profiles {
		if search != "" && !strings.Contains(strings.ToLower(name), strings.ToLower(search)) {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)

	creds, _ := auth.LoadCredentials() // best-effort; nil means no creds file

	items := make([]any, 0, len(names))
	for _, name := range names {
		p := cfg.Profiles[name]
		authType := "PAT"
		if p.AuthType == "sso" {
			authType = "SSO"
		} else if creds != nil {
			if c, ok := creds[name]; ok && auth.TokenType(c.Token) == auth.TokenKindSAT {
				authType = "SAT"
			}
		}
		items = append(items, map[string]any{
			"profile":      name,
			"api_url":      p.APIUrl,
			"account_id":   p.AccountID,
			"org_id":       p.OrgID,
			"project_id":   p.ProjectID,
			"auth_type":    authType,
			"registry_url": p.RegistryURL,
		})
	}

	return &cmdctx.PageResult{
		Items:       items,
		StartOffset: 0,
		Last:        true,
		HasTotal:    true,
		Total:       int64(len(items)),
	}, nil
}
