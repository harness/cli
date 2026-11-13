// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"fmt"

	"github.com/harness/cli/v3/pkg/auth"
	"github.com/harness/cli/v3/pkg/cmdctx"
)

const fieldNames = "token, email, user_id, user_type, api_url, account, org, project"

func FieldsHandler(ctx *cmdctx.Ctx) error {
	r, err := auth.LoadWithOverrides(
		cmdctx.GetString(ctx.FlagValues, "profile"),
		cmdctx.GetString(ctx.FlagValues, "org"),
		cmdctx.GetString(ctx.FlagValues, "project"),
	)
	if err != nil {
		return err
	}
	v, err := fieldValue(r, ctx.Id)
	if err != nil {
		return err
	}
	fmt.Println(v)
	return nil
}

func fieldValue(r *auth.ResolvedAuth, name string) (string, error) {
	var v string
	switch name {
	case "token":
		v = r.PATToken
	case "email":
		v = r.Email
	case "user_id":
		v = r.UserID
	case "user_type":
		v = r.UserType
	case "api_url":
		v = r.APIUrl
	case "account":
		v = r.AccountID
	case "org":
		v = r.OrgID
	case "project":
		v = r.ProjectID
	default:
		return "", fmt.Errorf("usage: harness auth fields <name> (%s)", fieldNames)
	}
	if v == "" {
		return "", fmt.Errorf("field %q is not set for this profile", name)
	}
	return v, nil
}
