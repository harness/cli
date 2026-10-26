// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package client

import (
	"encoding/json"
	"fmt"
	"path"

	"github.com/harness/cli/v3/pkg/client"
	"github.com/harness/cli/v3/pkg/cmdctx"
)

func scopedQuery(scope Scope, accountID string) map[string]string {
	params := scope.query()
	params["routingId"] = accountID
	params["accountId"] = accountID
	return params
}

// EnsureRepo is lookup-or-create for the coverage repository upload
// writes to. Connectors are not sent from here; they are configured in the
// coverage UI.
func EnsureRepo(ctx *cmdctx.Ctx, scope Scope, provider, owner, identifier string) (int64, error) {
	accountID := ctx.Auth.AccountID
	raw, _, err := client.New(ctx).DoRequest(client.Request{
		Method:      "POST",
		Path:        EnsureRepoPath(accountID),
		QueryParams: scopedQuery(scope, accountID),
		Body: EnsureRepoRequest{
			Provider: provider,
			RepoRef:  path.Join(owner, identifier),
		},
		NoAccountID: true,
	})
	if err != nil {
		return 0, fmt.Errorf("ensure coverage repository: %w", err)
	}
	var response EnsureRepoResponse
	if err := DecodeInto(raw, &response); err != nil {
		return 0, fmt.Errorf("ensure coverage repository: %w", err)
	}
	if response.Repository == nil || response.Repository.ID == 0 {
		return 0, fmt.Errorf("ensure coverage repository: empty response")
	}
	return response.Repository.ID, nil
}

// ExcludeText loads exclude_text for an already ensured coverage repository.
func ExcludeText(ctx *cmdctx.Ctx, scope Scope, repoID int64) (*string, error) {
	accountID := ctx.Auth.AccountID
	raw, _, err := client.New(ctx).DoRequest(client.Request{
		Method:      "GET",
		Path:        RepoSettingsPath(accountID, repoID),
		QueryParams: scopedQuery(scope, accountID),
		NoAccountID: true,
	})
	if err != nil {
		return nil, fmt.Errorf("get coverage repository settings: %w", err)
	}
	var response RepoSettings
	if err := DecodeInto(raw, &response); err != nil {
		return nil, fmt.Errorf("get coverage repository settings: %w", err)
	}
	return response.ExcludeText, nil
}

// DecodeInto re-marshals a generically-decoded API response
// (map[string]any, as returned by pkg/client) into a concrete struct, so
// callers get typed field access.
func DecodeInto(raw any, out any) error {
	b, err := json.Marshal(raw)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, out)
}
