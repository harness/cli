// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package ci

import (
	"encoding/json"
	"fmt"
	"github.com/harness/cli/v3/modules/ci/coverage/env"
	"path"
	"strings"

	"github.com/harness/cli/v3/pkg/client"
	"github.com/harness/cli/v3/pkg/cmdctx"
)

const coverageAPIPrefix = "/coverage/api/v1"

func coverageEnsureRepoPath(accountID string) string {
	return fmt.Sprintf(coverageAPIPrefix+"/accounts/%s/repos", accountID)
}

func coverageRepoSettingsPath(accountID string, repoID int64) string {
	return fmt.Sprintf(coverageAPIPrefix+"/accounts/%s/repos/%d/settings", accountID, repoID)
}

// coverageScope is the Harness account/org/project used on coverage-server requests.
type coverageScope struct {
	AccountID string
	OrgID     string
	ProjectID string
}

// SpacePath joins the non-empty segments into a Harness space path.
func (s coverageScope) SpacePath() string {
	segments := make([]string, 0, 3)
	if s.AccountID != "" {
		segments = append(segments, s.AccountID)
	}
	if s.OrgID != "" {
		segments = append(segments, s.OrgID)
	}
	if s.ProjectID != "" {
		segments = append(segments, s.ProjectID)
	}
	return strings.Join(segments, "/")
}

func (s coverageScope) query() map[string]string {
	params := make(map[string]string)
	if s.OrgID != "" {
		params["orgId"] = s.OrgID
	}
	if s.ProjectID != "" {
		params["projectId"] = s.ProjectID
	}
	return params
}

// coverageResolveScope returns the coverage repository scope, matching
// coverage-server upload targeting: Harness Code owner path, else
// --target-scope, else the pipeline path. analyze is local-only and does not
// call coverage-server, so this is only used by upload/wait-until.
func coverageResolveScope(accountID, provider, owner, targetScope, pipelineID string) (coverageScope, error) {
	if strings.EqualFold(provider, env.HarnessProvider) {
		if targetScope != "" {
			return coverageScope{}, fmt.Errorf(
				"--target-scope is not supported for Harness Code; coverage is stored on the repository",
			)
		}
		scope, err := coverageScopeFromSpacePath(owner)
		if err != nil {
			return coverageScope{}, fmt.Errorf("invalid Harness Code repository owner: %w", err)
		}
		if !strings.EqualFold(scope.AccountID, accountID) {
			return coverageScope{}, fmt.Errorf(
				"repository owner %q is not in account %q",
				owner, accountID,
			)
		}
		return scope, nil
	}
	if target := strings.Trim(targetScope, "/"); target != "" {
		scope, err := coverageScopeFromSpacePath(target)
		if err != nil {
			return coverageScope{}, fmt.Errorf("--target-scope: %w", err)
		}
		if !strings.EqualFold(scope.AccountID, accountID) {
			return coverageScope{}, fmt.Errorf("--target-scope must be within account %q", accountID)
		}
		return scope, nil
	}
	return coverageScopeFromPipelinePath(accountID, pipelineID)
}

func coverageScopeFromSpacePath(spacePath string) (coverageScope, error) {
	parts := strings.Split(strings.Trim(spacePath, "/"), "/")
	if len(parts) == 0 || parts[0] == "" || len(parts) > 3 {
		return coverageScope{}, fmt.Errorf("space path %q must contain an account, organization, or project", spacePath)
	}
	return coverageParseScopeSegments(parts, spacePath)
}

// coverageScopeFromPipelinePath derives scope from a pipeline path such as
// "account/org/project/pipeline", matching coverage-server ScopeFromPipelinePath.
func coverageScopeFromPipelinePath(accountID, pipelineFullPath string) (coverageScope, error) {
	trimmed := strings.Trim(pipelineFullPath, "/")
	if trimmed == "" {
		return coverageScope{AccountID: accountID}, nil
	}
	i := strings.LastIndex(trimmed, "/")
	if i <= 0 {
		return coverageScope{AccountID: accountID}, nil
	}
	parts := strings.Split(trimmed[:i], "/")
	return coverageParseScopeSegments(parts, pipelineFullPath)
}

func coverageParseScopeSegments(parts []string, source string) (coverageScope, error) {
	if len(parts) == 0 || parts[0] == "" {
		return coverageScope{}, fmt.Errorf("space path %q does not resolve to a valid scope", source)
	}
	for _, part := range parts {
		if part == "" || part == "." || part == ".." {
			return coverageScope{}, fmt.Errorf("space path %q contains an invalid segment", source)
		}
	}
	scope := coverageScope{AccountID: parts[0]}
	if len(parts) > 1 {
		scope.OrgID = parts[1]
	}
	if len(parts) > 2 {
		scope.ProjectID = parts[2]
	}
	return scope, nil
}

type coverageEnsureRepoRequest struct {
	Provider string `json:"provider"`
	RepoRef  string `json:"repo_ref"`
}

type coverageEnsureRepoResponse struct {
	Repository *coverageRepository `json:"repository"`
}

type coverageRepository struct {
	ID int64 `json:"id"`
}

type coverageRepoSettings struct {
	ExcludeText *string `json:"exclude_text"`
}

func coverageScopedQuery(scope coverageScope, accountID string) map[string]string {
	params := scope.query()
	params["routingId"] = accountID
	params["accountId"] = accountID
	return params
}

// coverageEnsureRepo is lookup-or-create for the coverage repository upload
// writes to. Connectors are not sent from here; they are configured in the
// coverage UI.
func coverageEnsureRepo(ctx *cmdctx.Ctx, scope coverageScope, provider, owner, identifier string) (int64, error) {
	accountID := ctx.Auth.AccountID
	raw, _, err := client.New(ctx).DoRequest(client.Request{
		Method:      "POST",
		Path:        coverageEnsureRepoPath(accountID),
		QueryParams: coverageScopedQuery(scope, accountID),
		Body: coverageEnsureRepoRequest{
			Provider: provider,
			RepoRef:  path.Join(owner, identifier),
		},
		NoAccountID: true,
	})
	if err != nil {
		return 0, fmt.Errorf("ensure coverage repository: %w", err)
	}
	var response coverageEnsureRepoResponse
	if err := coverageDecodeInto(raw, &response); err != nil {
		return 0, fmt.Errorf("ensure coverage repository: %w", err)
	}
	if response.Repository == nil || response.Repository.ID == 0 {
		return 0, fmt.Errorf("ensure coverage repository: empty response")
	}
	return response.Repository.ID, nil
}

// coverageExcludeText loads exclude_text for an already ensured coverage repository.
func coverageExcludeText(ctx *cmdctx.Ctx, scope coverageScope, repoID int64) (*string, error) {
	accountID := ctx.Auth.AccountID
	raw, _, err := client.New(ctx).DoRequest(client.Request{
		Method:      "GET",
		Path:        coverageRepoSettingsPath(accountID, repoID),
		QueryParams: coverageScopedQuery(scope, accountID),
		NoAccountID: true,
	})
	if err != nil {
		return nil, fmt.Errorf("get coverage repository settings: %w", err)
	}
	var response coverageRepoSettings
	if err := coverageDecodeInto(raw, &response); err != nil {
		return nil, fmt.Errorf("get coverage repository settings: %w", err)
	}
	return response.ExcludeText, nil
}

// coverageDecodeInto re-marshals a generically-decoded API response
// (map[string]any, as returned by pkg/client) into a concrete struct, so
// callers get typed field access.
func coverageDecodeInto(raw any, out any) error {
	b, err := json.Marshal(raw)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, out)
}
