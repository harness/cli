// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package vibe

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func gitClone(url, dest string) error {
	fmt.Printf("Cloning %s…\n", url)
	return runGit("clone", "--", url, dest)
}

func gitFetch(repo string) error {
	fmt.Printf("Fetching…\n")
	return runGit("-C", repo, "fetch", "--all", "--tags")
}

func gitEnsureClone(url, dest string) error {
	if gitRepoExists(dest) {
		_ = runGit("-C", dest, "remote", "set-url", "origin", url)
		if err := gitFetch(dest); err != nil {
			return fmt.Errorf("git fetch: %w", err)
		}
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	if err := gitClone(url, dest); err != nil {
		return fmt.Errorf("git clone: %w", err)
	}
	return nil
}

func gitWorktreeAdd(repo, dest, branch, commit string) error {
	if gitRepoExists(dest) {
		fmt.Printf("Reusing git worktree %s\n", dest)
		return nil
	}
	if err := os.RemoveAll(dest); err != nil && !os.IsNotExist(err) {
		return err
	}
	fmt.Printf("Creating worktree %s at %s…\n", dest, commit)
	if err := runGit("-C", repo, "worktree", "add", "-B", branch, dest, commit); err != nil {
		return fmt.Errorf("git worktree add: %w", err)
	}
	return nil
}

func gitEnsureRepo(dir string) error {
	if gitRepoExists(dir) {
		return nil
	}
	if err := runGit("-C", dir, "init", "-b", "main"); err != nil {
		return fmt.Errorf("git init: %w", err)
	}
	identity := []string{"-C", dir, "-c", "user.email=vibe-mode@harness.io", "-c", "user.name=Vibe Mode"}
	if err := runGit(append(identity, "add", "-A")...); err != nil {
		return fmt.Errorf("git add: %w", err)
	}
	if err := runGit(append(identity, "commit", "-m", "Vibe import", "--allow-empty")...); err != nil {
		return fmt.Errorf("git commit: %w", err)
	}
	return nil
}

func gitRepoExists(dir string) bool {
	if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
		return true
	}
	_, err := os.Stat(filepath.Join(dir, "HEAD"))
	return err == nil
}

func runGit(args ...string) error {
	cmd := exec.Command("git", args...)
	out, err := cmd.CombinedOutput()
	if len(out) > 0 {
		fmt.Print(string(out))
	}
	if err != nil {
		return fmt.Errorf("git %s: %w\n%s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}
