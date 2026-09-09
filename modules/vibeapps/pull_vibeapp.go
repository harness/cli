// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package vibeapps

import (
	"encoding/base64"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path"
	"strings"

	"github.com/harness/cli/v3/pkg/client"
	"github.com/harness/cli/v3/pkg/cmdctx"
	"github.com/harness/cli/v3/pkg/hlog"
)

const pullVibeappWorkflowID = "pull_vibeapp"

// requireGit checks that the git binary is installed and on the PATH.
func requireGit() error {
	if _, err := exec.LookPath("git"); err != nil {
		return fmt.Errorf("git is required for this command but was not found on your PATH; install git and try again")
	}
	return nil
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

func gitHost(rawURL string) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil || u.Host == "" {
		return "", fmt.Errorf("could not parse host from git URL %q", rawURL)
	}
	return u.Host, nil
}

// runGitCommand execs git with args, streaming stdio through, injecting PAT
// credentials scoped to credURL's host.
func runGitCommand(cc *cmdctx.Ctx, credURL string, args ...string) error {
	env := append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	if cc.Auth.PATToken == "" {
		return fmt.Errorf("this operation requires PAT-based auth (email + PAT); the current auth session has no PAT")
	}
	host, err := gitHost(credURL)
	if err != nil {
		return err
	}
	env = append(env, gitCredentialEnv(host, cc.Auth.Email, cc.Auth.PATToken)...)

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

// appRepoURL fetches the backing repo's clone URL for a Vibe App.
func appRepoURL(ctx *cmdctx.Ctx, appID string) (string, error) {
	raw, _, err := client.New(ctx).Get(apiPrefix+"/api/v1/apps/"+appID, nil)
	if err != nil {
		return "", fmt.Errorf("fetching app %q: %w", appID, err)
	}
	m := asMap(raw)
	repoURL, _ := m["repoUrl"].(string)
	if repoURL == "" {
		return "", fmt.Errorf("app %q has no repoUrl in response", appID)
	}
	return repoURL, nil
}

// pullVibeappWorkflow implements "pull vibeapp <id> [<dest-dir>]" (git clone). A
// manually cloned-then-edited checkout is unambiguously that app's directory, so it
// writes the .harness/vibeapp.yaml link file on success — same as "execute
// vibeapp:deploy" does on first deploy — so a later deploy from here updates this app
// instead of creating a duplicate.
func pullVibeappWorkflow(ctx *cmdctx.Ctx) error {
	if err := requireGit(); err != nil {
		return err
	}

	repoURL, err := appRepoURL(ctx, ctx.Id)
	if err != nil {
		return err
	}

	destDir := repoDirFromURL(repoURL)
	args := []string{"clone", repoURL}
	if len(ctx.Args) > 0 {
		destDir = ctx.Args[0]
		args = append(args, destDir)
	}
	if err := runGitCommand(ctx, repoURL, args...); err != nil {
		return err
	}
	return writeVibeappLink(destDir, ctx.Id)
}

// repoDirFromURL mirrors git clone's own default destination-directory rule:
// the URL's last path segment with a trailing ".git" stripped.
func repoDirFromURL(repoURL string) string {
	base := path.Base(repoURL)
	return strings.TrimSuffix(base, ".git")
}
