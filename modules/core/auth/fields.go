// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/harness/cli/v3/pkg/auth"
	"github.com/harness/cli/v3/pkg/cmdctx"
)

type profileField struct {
	name string
	get  func(*auth.ResolvedAuth) string
}

var profileFields = []profileField{
	{"token", func(r *auth.ResolvedAuth) string { return r.PATToken }},
	{"email", func(r *auth.ResolvedAuth) string { return r.Email }},
	{"user_id", func(r *auth.ResolvedAuth) string { return r.UserID }},
	{"user_type", func(r *auth.ResolvedAuth) string { return r.UserType }},
	{"api_url", func(r *auth.ResolvedAuth) string { return r.APIUrl }},
	{"account", func(r *auth.ResolvedAuth) string { return r.AccountID }},
	{"org", func(r *auth.ResolvedAuth) string { return r.OrgID }},
	{"project", func(r *auth.ResolvedAuth) string { return r.ProjectID }},
}

func FieldsHandler(ctx *cmdctx.Ctx) error {
	resolved, err := auth.LoadWithOverrides(
		cmdctx.GetString(ctx.FlagValues, "profile"),
		cmdctx.GetString(ctx.FlagValues, "org"),
		cmdctx.GetString(ctx.FlagValues, "project"),
	)
	if err != nil {
		return err
	}

	name := ctx.Id
	if name == "" && len(ctx.Args) > 0 {
		name = ctx.Args[0]
	}
	return printFields(os.Stdout, resolved, name)
}

func printFields(w io.Writer, r *auth.ResolvedAuth, name string) error {
	if name == "" {
		for _, f := range profileFields {
			if v := f.get(r); v != "" {
				fmt.Fprintf(w, "%s=%s\n", f.name, v)
			}
		}
		return nil
	}
	for _, f := range profileFields {
		if f.name == name {
			v := f.get(r)
			if v == "" {
				return fmt.Errorf("field %q is not set for this profile", name)
			}
			fmt.Fprintln(w, v)
			return nil
		}
	}
	names := make([]string, len(profileFields))
	for i, f := range profileFields {
		names[i] = f.name
	}
	return fmt.Errorf("unknown field %q (valid: %s)", name, strings.Join(names, ", "))
}
