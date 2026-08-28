// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package platform

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"github.com/harness/cli/pkg/auth"
	"github.com/harness/cli/pkg/client"
	"github.com/harness/cli/pkg/cmdctx"
	"github.com/harness/cli/pkg/hlog"
)

// harnessUIDRe matches a Harness user UID: exactly 22 base64url characters.
var harnessUIDRe = regexp.MustCompile(`^[A-Za-z0-9_-]{22}$`)

// resolvePrincipalID resolves a --principal flag value to the identifier the
// NG RBAC APIs (e.g. the role_assignment principal filter) expect, and infers
// --principal_type when it can be.
//
// An email resolves to the user's UUID (unambiguously USER; service accounts
// have no email-lookup API, so an email that isn't a user is an error). A
// bare Harness UID passes through unchanged (unambiguously USER). Anything
// else passes through unchanged, but service accounts and user groups use
// their plain identifier as-is with no UUID translation, so when the caller
// hasn't set --principal_type we probe both by identifier (cheap, single-item
// lookups) to infer it; an explicit --principal_type always skips the probe.
func resolvePrincipalID(ctx *cmdctx.Ctx, raw string) (*cmdctx.FlagResolveResult, error) {
	if strings.Contains(raw, "@") {
		hlog.Debug("resolvePrincipalID: raw looks like an email, resolving via user lookup", "raw", raw)
		uuid, err := userUUIDFromEmail(ctx, raw)
		if err != nil {
			return nil, err
		}
		hlog.Debug("resolvePrincipalID: resolved email to user UUID", "raw", raw, "uuid", uuid)
		return &cmdctx.FlagResolveResult{Value: uuid, Defaults: map[string]string{"principal_type": "USER"}}, nil
	}
	if harnessUIDRe.MatchString(raw) {
		hlog.Debug("resolvePrincipalID: raw matches Harness UID shape, treating as USER", "raw", raw)
		return &cmdctx.FlagResolveResult{Value: raw, Defaults: map[string]string{"principal_type": "USER"}}, nil
	}
	if principalType, _ := ctx.FlagValues["principal_type"].(string); principalType != "" {
		hlog.Debug("resolvePrincipalID: --principal_type already set, skipping probe", "raw", raw, "principal_type", principalType)
		return &cmdctx.FlagResolveResult{Value: raw}, nil
	}

	hlog.Debug("resolvePrincipalID: raw is not an email or UID, probing service account and user group identifiers", "raw", raw)
	isServiceAccount, err := serviceAccountExists(ctx, raw)
	if err != nil {
		return nil, err
	}
	isUserGroup, err := userGroupExists(ctx, raw)
	if err != nil {
		return nil, err
	}
	hlog.Debug("resolvePrincipalID: probe results", "raw", raw, "isServiceAccount", isServiceAccount, "isUserGroup", isUserGroup)
	switch {
	case isServiceAccount && isUserGroup:
		return nil, fmt.Errorf("%q matches both a service account and a user group; set --principal_type to disambiguate", raw)
	case isServiceAccount:
		return &cmdctx.FlagResolveResult{Value: raw, Defaults: map[string]string{"principal_type": "SERVICE_ACCOUNT"}}, nil
	case isUserGroup:
		return &cmdctx.FlagResolveResult{Value: raw, Defaults: map[string]string{"principal_type": "USER_GROUP"}}, nil
	default:
		return nil, fmt.Errorf("no service account or user group found with identifier %q; set --principal_type to specify it explicitly", raw)
	}
}

// serviceAccountExists reports whether identifier names a service account in
// the current scope.
func serviceAccountExists(ctx *cmdctx.Ctx, identifier string) (bool, error) {
	return principalIdentifierExists(ctx, "/ng/api/serviceaccount/aggregate/"+url.PathEscape(identifier))
}

// userGroupExists reports whether identifier names a user group in the
// current scope.
func userGroupExists(ctx *cmdctx.Ctx, identifier string) (bool, error) {
	return principalIdentifierExists(ctx, "/ng/api/user-groups/"+url.PathEscape(identifier))
}

// principalIdentifierExists performs a single-item lookup and treats the NG
// APIs' "not found" 400 response as a false result rather than an error.
// Service accounts and user groups are scoped entities with no roll-up or
// roll-down between account/org/project — a narrower lookup never sees a
// broader-scoped entity and vice versa (it 400s, it doesn't inherit) — so we
// probe from broadest to narrowest: account scope, then org-only scope (if
// the command has an org in play), then the command's actual --level scope,
// stopping as soon as one probe finds it. Only the first (account) probe's
// error is treated as authoritative; later probes are opportunistic — a
// caller may lack permission to look up entities at a narrower scope, and
// since a broader check already gave a "not found," an error from a later
// probe shouldn't fail the command — it's treated as not found there either.
func principalIdentifierExists(ctx *cmdctx.Ctx, path string) (bool, error) {
	exists, err := principalIdentifierExistsAt(ctx, path, ctx.AccountAuth(), "account")
	if err != nil || exists || ctx.Level == "account" {
		return exists, err
	}
	if ctx.Level != "org" && ctx.Auth.OrgID != "" {
		exists, _ = principalIdentifierExistsAt(ctx, path, ctx.OrgAuth(), "org")
		if exists {
			return true, nil
		}
	}
	exists, _ = principalIdentifierExistsAt(ctx, path, ctx.ScopedAuth(), "command")
	return exists, nil
}

func principalIdentifierExistsAt(ctx *cmdctx.Ctx, path string, a *auth.ResolvedAuth, probe string) (bool, error) {
	queryParams := map[string]string{
		"orgIdentifier":     a.OrgID,
		"projectIdentifier": a.ProjectID,
	}
	_, _, err := client.New(ctx).Get(path, queryParams)
	if err == nil {
		hlog.Debug("principalIdentifierExists: found", "probe", probe, "path", path, "org", a.OrgID, "project", a.ProjectID)
		return true, nil
	}
	if strings.Contains(err.Error(), "API error 400") {
		hlog.Debug("principalIdentifierExists: not found", "probe", probe, "path", path, "org", a.OrgID, "project", a.ProjectID)
		return false, nil
	}
	hlog.Debug("principalIdentifierExists: probe error", "probe", probe, "path", path, "org", a.OrgID, "project", a.ProjectID, "err", err)
	return false, err
}

// userUUIDFromEmail resolves an email address to a Harness user UUID.
// Returns an error if no match is found or the email matches multiple users.
func userUUIDFromEmail(ctx *cmdctx.Ctx, email string) (string, error) {
	scoped := ctx.ScopedAuth()
	queryParams := map[string]string{
		"orgIdentifier":     scoped.OrgID,
		"projectIdentifier": scoped.ProjectID,
		"searchTerm":        email,
	}
	raw, _, err := client.New(ctx).Post("/ng/api/user/aggregate", queryParams, nil)
	if err != nil {
		return "", fmt.Errorf("looking up user %q: %w", email, err)
	}
	content, err := userAggregateContent(raw)
	if err != nil {
		return "", err
	}
	var matches []string
	for _, item := range content {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		u, ok := m["user"].(map[string]any)
		if !ok {
			continue
		}
		if e, _ := u["email"].(string); e != email {
			continue
		}
		if uuid, _ := u["uuid"].(string); uuid != "" {
			matches = append(matches, uuid)
		}
	}
	hlog.Debug("userUUIDFromEmail: matched users", "email", email, "count", len(matches))
	switch len(matches) {
	case 0:
		return "", fmt.Errorf("email %q does not resolve to a user; if it belongs to a service account, filter by the service account's identifier instead of its email", email)
	case 1:
		return matches[0], nil
	default:
		return "", fmt.Errorf("multiple users found with email %q", email)
	}
}

// userAggregateContent extracts the content array from a /ng/api/user/aggregate response.
func userAggregateContent(raw any) ([]any, error) {
	m, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("unexpected response type from user aggregate endpoint")
	}
	data, ok := m["data"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("unexpected response shape from user aggregate endpoint")
	}
	content, ok := data["content"].([]any)
	if !ok {
		return nil, fmt.Errorf("unexpected response shape from user aggregate endpoint")
	}
	return content, nil
}
