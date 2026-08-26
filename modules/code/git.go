// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package code

import (
	"fmt"
	"net/url"
	"os/exec"
	"strings"
)

// requireGit checks that the git binary is installed and on the PATH.
// exec.LookPath already resolves "git.exe" on Windows via PATHEXT, so no
// OS-specific lookup is needed here.
func requireGit() error {
	if _, err := exec.LookPath("git"); err != nil {
		return fmt.Errorf("git is required for this command but was not found on your PATH; install git and try again")
	}
	return nil
}

// remoteRepoScope is what a Harness Code clone URL encodes in its path:
// https://<git-host>/<accountId>/<orgId>/<projectId>/<repoIdentifier>.git
// The host is deliberately ignored: it varies (git.harness.io, git0.harness.io,
// vanity/SMP domains), but the path shape is fixed regardless of host.
type remoteRepoScope struct {
	AccountID string
	OrgID     string
	ProjectID string
	RepoID    string
}

func parseRemoteRepoScope(rawURL string) (remoteRepoScope, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return remoteRepoScope{}, fmt.Errorf("could not parse git remote URL %q: %w", rawURL, err)
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) != 4 {
		return remoteRepoScope{}, fmt.Errorf("git remote URL %q doesn't look like a Harness Code clone URL (expected /<account>/<org>/<project>/<repo>)", rawURL)
	}
	return remoteRepoScope{
		AccountID: parts[0],
		OrgID:     parts[1],
		ProjectID: parts[2],
		RepoID:    strings.TrimSuffix(parts[3], ".git"),
	}, nil
}
