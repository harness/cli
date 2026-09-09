// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package code

import (
	"fmt"
	"os/exec"
	"strings"

	"github.com/harness/cli/v3/pkg/client"
	"github.com/harness/cli/v3/pkg/cmdctx"
)

const pullPRWorkflowID = "pull_pr"

// remoteURL reads the URL of the named git remote in the current directory's repo.
func remoteURL(cc *cmdctx.Ctx, name string) (string, error) {
	out, err := exec.CommandContext(cc.Context, "git", "remote", "get-url", name).Output()
	if err != nil {
		return "", fmt.Errorf("running 'git remote get-url %s' (must be run inside an existing clone): %w", name, err)
	}
	return strings.TrimSpace(string(out)), nil
}

// pullPRWorkflow implements "pull pr [<repo_id>/]<pr_number>": fetch + checkout the PR's
// source branch, mirroring `gh pr checkout`. Must be run inside an existing clone of the
// target repository. The remote (default "origin", override with --remote) resolves the
// repo when <repo_id> is omitted, and verifies it when given explicitly.
func pullPRWorkflow(ctx *cmdctx.Ctx) error {
	if err := requireGit(); err != nil {
		return err
	}

	remoteName := cmdctx.GetString(ctx.FlagValues, "remote")
	if remoteName == "" {
		remoteName = "origin"
	}

	var repoID, prNumber string
	switch parts := strings.SplitN(ctx.Id, "/", 2); len(parts) {
	case 2:
		repoID, prNumber = parts[0], parts[1]
	case 1:
		prNumber = parts[0]
	}
	if prNumber == "" {
		return fmt.Errorf("expected [<repo_id>/]<pr_number>")
	}

	rURL, err := remoteURL(ctx, remoteName)
	if err != nil {
		return err
	}
	scope, err := parseRemoteRepoScope(rURL)
	if err != nil {
		return err
	}
	if scope.AccountID != ctx.Auth.AccountID {
		return fmt.Errorf("remote %q (%s) belongs to a different account than the current auth session", remoteName, rURL)
	}

	params := map[string]string{"orgIdentifier": scope.OrgID, "projectIdentifier": scope.ProjectID}
	if repoID == "" {
		repoID = scope.RepoID
	} else if repoID != scope.RepoID || scope.OrgID != ctx.Auth.OrgID || scope.ProjectID != ctx.Auth.ProjectID {
		return fmt.Errorf("remote %q (%s) resolves to repo %s/%s/%s, which doesn't match the requested repo %s/%s/%s; omit <repo_id> to use the remote's repo, or check out the matching clone",
			remoteName, rURL, scope.OrgID, scope.ProjectID, scope.RepoID, ctx.Auth.OrgID, ctx.Auth.ProjectID, repoID)
	}

	c := client.New(ctx)
	raw, _, err := c.Get(fmt.Sprintf("/code/api/v1/repos/%s/pullreq/%s", repoID, prNumber), params)
	if err != nil {
		return fmt.Errorf("fetching PR: %w", err)
	}
	m, ok := raw.(map[string]any)
	if !ok {
		return fmt.Errorf("unexpected PR response type")
	}
	sourceBranch, _ := m["source_branch"].(string)
	if sourceBranch == "" {
		return fmt.Errorf("PR response missing source_branch")
	}

	if err := runGitCommand(ctx, rURL, "fetch", remoteName, sourceBranch); err != nil {
		return err
	}
	return runGitCommand(ctx, "", "checkout", sourceBranch)
}
