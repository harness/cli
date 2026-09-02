// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package code

import (
	"encoding/base64"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"strings"

	"github.com/harness/cli/pkg/client"
	"github.com/harness/cli/pkg/cmdctx"
	"github.com/harness/cli/pkg/hlog"
)

const pullRepositoryWorkflowID = "pull_repository"

// scopeQueryParams builds orgIdentifier/projectIdentifier query params from ctx.Auth,
// narrowed by ctx.Level ("org" drops projectIdentifier, "account" drops both) — the
// same narrowing CallEndpoint applies for endpoint-driven commands. accountIdentifier
// is added by client.Client itself, so it's not included here.
func scopeQueryParams(cc *cmdctx.Ctx) map[string]string {
	params := map[string]string{}
	switch cc.Level {
	case "account":
	case "org":
		params["orgIdentifier"] = cc.Auth.OrgID
	default:
		params["orgIdentifier"] = cc.Auth.OrgID
		params["projectIdentifier"] = cc.Auth.ProjectID
	}
	return params
}

// gitCredentialEnv builds the env vars that inject HTTPS basic auth (email:pat) for
// host into a git subprocess, without ever touching argv, ~/.gitconfig, or the URL.
func gitCredentialEnv(host, email, pat string) []string {
	basic := base64.StdEncoding.EncodeToString([]byte(email + ":" + pat))
	return []string{
		"GIT_CONFIG_COUNT=1",
		"GIT_CONFIG_KEY_0=http.https://" + host + "/.extraheader",
		"GIT_CONFIG_VALUE_0=Authorization: Basic " + basic,
	}
}

// runGitCommand execs git with args, streaming stdio through. When credURL is
// non-empty, it injects PAT credentials scoped to credURL's host.
func runGitCommand(cc *cmdctx.Ctx, credURL string, args ...string) error {
	env := append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	if credURL != "" {
		if cc.Auth.PATToken == "" {
			return fmt.Errorf("this operation requires PAT-based auth (email + PAT); the current auth session has no PAT")
		}
		host, err := gitHost(credURL)
		if err != nil {
			return err
		}
		env = append(env, gitCredentialEnv(host, cc.Auth.Email, cc.Auth.PATToken)...)
	}

	hlog.Debug("git " + strings.Join(args, " "))

	cmd := exec.CommandContext(cc.Context, "git", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = env
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return nil
}

func gitHost(rawURL string) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil || u.Host == "" {
		return "", fmt.Errorf("could not parse host from git URL %q", rawURL)
	}
	return u.Host, nil
}

// repoGitURL fetches the clone URL for a repository.
func repoGitURL(cc *cmdctx.Ctx, repoID string) (string, error) {
	c := client.New(cc)
	raw, _, err := c.Get("/code/api/v1/repos/"+repoID, scopeQueryParams(cc))
	if err != nil {
		return "", fmt.Errorf("fetching repo %q: %w", repoID, err)
	}
	m, ok := raw.(map[string]any)
	if !ok {
		return "", fmt.Errorf("unexpected response type from repo endpoint")
	}
	gitURL, ok := m["git_url"].(string)
	if !ok || gitURL == "" {
		return "", fmt.Errorf("repo %q has no git_url in response", repoID)
	}
	return gitURL, nil
}

// pullRepositoryWorkflow implements "pull repository <repo_id> [<dest-dir>]" (git clone).
func pullRepositoryWorkflow(ctx *cmdctx.Ctx) error {
	if err := requireGit(); err != nil {
		return err
	}

	gitURL, err := repoGitURL(ctx, ctx.Id)
	if err != nil {
		return err
	}

	args := []string{"clone", gitURL}
	if len(ctx.Args) > 0 {
		args = append(args, ctx.Args[0])
	}
	return runGitCommand(ctx, gitURL, args...)
}
