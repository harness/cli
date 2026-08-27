// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package platform

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/harness/cli/pkg/client"
	"github.com/harness/cli/pkg/cmdctx"
)

// harnessUIDRe matches a Harness user UID: exactly 22 base64url characters.
var harnessUIDRe = regexp.MustCompile(`^[A-Za-z0-9_-]{22}$`)

// resolvePrincipalID resolves a --principal flag value to the identifier the
// NG RBAC APIs (e.g. the role_assignment principal filter) expect, and infers
// --principal_type when it can be. An email resolves to the user's UUID; a
// bare Harness UID passes through unchanged; both are unambiguously USER, so
// this defaults principal_type to "USER" when the caller hasn't set it. A
// service account or user group identifier can't be distinguished from a
// Harness UID's shape, so it requires an explicit --principal_type.
func resolvePrincipalID(ctx *cmdctx.Ctx, raw string) (*cmdctx.FlagResolveResult, error) {
	if strings.Contains(raw, "@") {
		uuid, err := userUUIDFromEmail(ctx, raw)
		if err != nil {
			return nil, err
		}
		return &cmdctx.FlagResolveResult{Value: uuid, Defaults: map[string]string{"principal_type": "USER"}}, nil
	}
	if harnessUIDRe.MatchString(raw) {
		return &cmdctx.FlagResolveResult{Value: raw, Defaults: map[string]string{"principal_type": "USER"}}, nil
	}
	if principalType, _ := ctx.FlagValues["principal_type"].(string); principalType != "" {
		return &cmdctx.FlagResolveResult{Value: raw}, nil
	}
	return nil, fmt.Errorf("--principal_type is required when --principal %q is not an email or Harness UID", raw)
}

// userUUIDFromEmail resolves an email address to a Harness user UUID.
// Returns an error if no match is found or the email matches multiple users.
func userUUIDFromEmail(ctx *cmdctx.Ctx, email string) (string, error) {
	queryParams := map[string]string{
		"orgIdentifier":     ctx.Auth.OrgID,
		"projectIdentifier": ctx.Auth.ProjectID,
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
	switch len(matches) {
	case 0:
		return "", fmt.Errorf("no user found with email %q", email)
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
