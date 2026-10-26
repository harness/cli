// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package client

import (
	"fmt"
	"strings"

	"github.com/harness/cli/v3/modules/ci/coverage/env"
)

// ResolveScope returns the coverage repository scope, matching
// coverage-server upload targeting: Harness Code owner path, else
// --target-scope, else the pipeline path. analyze is local-only and does not
// call coverage-server, so this is only used by upload/wait-until.
func ResolveScope(accountID, provider, owner, targetScope, pipelineID string) (Scope, error) {
	if strings.EqualFold(provider, env.HarnessProvider) {
		if targetScope != "" {
			return Scope{}, fmt.Errorf(
				"--target-scope is not supported for Harness Code; coverage is stored on the repository",
			)
		}
		scope, err := scopeFromSpacePath(owner)
		if err != nil {
			return Scope{}, fmt.Errorf("invalid Harness Code repository owner: %w", err)
		}
		if !strings.EqualFold(scope.AccountID, accountID) {
			return Scope{}, fmt.Errorf(
				"repository owner %q is not in account %q",
				owner, accountID,
			)
		}
		return scope, nil
	}
	if target := strings.Trim(targetScope, "/"); target != "" {
		scope, err := scopeFromSpacePath(target)
		if err != nil {
			return Scope{}, fmt.Errorf("--target-scope: %w", err)
		}
		if !strings.EqualFold(scope.AccountID, accountID) {
			return Scope{}, fmt.Errorf("--target-scope must be within account %q", accountID)
		}
		return scope, nil
	}
	return scopeFromPipelinePath(accountID, pipelineID)
}

func scopeFromSpacePath(spacePath string) (Scope, error) {
	parts := strings.Split(strings.Trim(spacePath, "/"), "/")
	if len(parts) == 0 || parts[0] == "" || len(parts) > 3 {
		return Scope{}, fmt.Errorf("space path %q must contain an account, organization, or project", spacePath)
	}
	return parseScopeSegments(parts, spacePath)
}

// scopeFromPipelinePath derives scope from a pipeline path such as
// "account/org/project/pipeline", matching coverage-server ScopeFromPipelinePath.
func scopeFromPipelinePath(accountID, pipelineFullPath string) (Scope, error) {
	trimmed := strings.Trim(pipelineFullPath, "/")
	if trimmed == "" {
		return Scope{AccountID: accountID}, nil
	}
	i := strings.LastIndex(trimmed, "/")
	if i <= 0 {
		return Scope{AccountID: accountID}, nil
	}
	parts := strings.Split(trimmed[:i], "/")
	return parseScopeSegments(parts, pipelineFullPath)
}

func parseScopeSegments(parts []string, source string) (Scope, error) {
	if len(parts) == 0 || parts[0] == "" {
		return Scope{}, fmt.Errorf("space path %q does not resolve to a valid scope", source)
	}
	for _, part := range parts {
		if part == "" || part == "." || part == ".." {
			return Scope{}, fmt.Errorf("space path %q contains an invalid segment", source)
		}
	}
	scope := Scope{AccountID: parts[0]}
	if len(parts) > 1 {
		scope.OrgID = parts[1]
	}
	if len(parts) > 2 {
		scope.ProjectID = parts[2]
	}
	return scope, nil
}
