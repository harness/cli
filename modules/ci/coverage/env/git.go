// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package env

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/harness/cli/v3/pkg/hlog"
)

// RefExists checks if a git ref (branch, tag, or commit) exists locally.
func RefExists(ref string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "rev-parse", "--verify", ref)
	err := cmd.Run()
	return err == nil
}

// EnsureRefFetched ensures a ref is available locally, fetching it from
// origin if needed. Returns the ref name to use for git operations (may be
// prefixed with "origin/" if local branch doesn't exist). For refs like
// "origin/main", it will fetch from remote if not present. For refs like
// "main", it will try to fetch and return "origin/main" if local "main" does
// not exist. This function never fails - it logs warnings if fetch fails and
// returns the original ref.
func EnsureRefFetched(ref string) string {
	// If ref already exists locally, use it as-is
	if RefExists(ref) {
		hlog.Debug("ref exists locally", "ref", ref)
		return ref
	}

	hlog.Debug("ref not found locally, attempting to fetch", "ref", ref)

	// Determine what to fetch and what ref to use after fetching
	var fetchRef string
	var useRef string
	if strings.HasPrefix(ref, "origin/") {
		// ref is "origin/main", fetch the branch "main" from origin
		fetchRef = strings.TrimPrefix(ref, "origin/")
		useRef = ref // use "origin/main"
	} else {
		// ref is "main", fetch it from origin, then use "origin/main"
		fetchRef = ref
		useRef = "origin/" + ref
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Fetch the ref from origin
	hlog.Debug("running git fetch", "ref", fetchRef)
	cmd := exec.CommandContext(ctx, "git", "fetch", "origin", fetchRef)
	out, err := cmd.CombinedOutput()
	if err != nil {
		hlog.Debug("git fetch output", "output", string(out))
		hlog.Warn("failed to fetch ref, continuing anyway", "ref", ref, "err", err)
		return ref // return original ref, let subsequent commands fail with better error
	}

	hlog.Debug("successfully fetched ref", "ref", ref, "useRef", useRef)
	return useRef
}

// GetMergeBase returns the merge-base SHA between the base and head refs.
func GetMergeBase(base, head string) (string, error) {
	hlog.Debug("running git merge-base", "base", base, "head", head)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "merge-base", base, head)
	out, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			hlog.Debug("git merge-base stderr", "stderr", string(exitErr.Stderr))
		}
		return "", fmt.Errorf("failed to get merge-base: %w", err)
	}
	result := strings.TrimSpace(string(out))
	hlog.Debug("merge-base result", "result", result)
	return result, nil
}

// RepositoryRoot returns the absolute path of the git repository root.
func RepositoryRoot() (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "rev-parse", "--show-toplevel")
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("failed to get git root: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// ChangedLines returns changed line numbers keyed by repository-relative
// path. baseRev is required - the revision to compare against (e.g., "main",
// "origin/main", or a commit SHA). headRev is optional. An empty head compares
// against HEAD. The comparison is triple-dot: changes head introduced since it
// diverged from base. When both are commit SHAs, the diff is more reproducible
// across environments.
func ChangedLines(baseRev, headRev string) (map[string]map[uint32]bool, error) {
	if baseRev == "" {
		return nil, fmt.Errorf("baseRev is required")
	}

	hlog.Debug("ChangedLines called", "baseRev", baseRev, "headRev", headRev)

	// If baseRev and headRev are the same commit, there's no diff
	if headRev != "" && baseRev == headRev {
		hlog.Debug("baseRev and headRev are the same commit, returning empty diff", "rev", baseRev)
		return make(map[string]map[uint32]bool), nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if headRev == "" {
		headRev = "HEAD"
	}

	// Use triple-dot to get only what headRev introduced since diverging from baseRev.
	// Two-dot ("git diff baseRev") would compare the working tree to baseRev directly,
	// picking up all commits on baseRev not yet merged into the current branch.
	hlog.Debug("running git diff", "base", baseRev, "head", headRev)
	cmd := exec.CommandContext(ctx, "git", "diff", "--unified=0", baseRev+"..."+headRev)

	out, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			hlog.Debug("git diff stderr", "stderr", string(exitErr.Stderr))
		}
		return nil, fmt.Errorf("failed to get git diff: %w", err)
	}

	return parseDiff(string(out))
}

// parseDiff parses the unified diff output and extracts changed line numbers.
func parseDiff(diffOutput string) (map[string]map[uint32]bool, error) {
	result := make(map[string]map[uint32]bool)
	var currentFile string

	lines := strings.Split(diffOutput, "\n")
	for _, line := range lines {
		// Check for the file header (diff --git a/file b/file)
		switch {
		case strings.HasPrefix(line, "diff --git"):
			currentFile = ""
		case strings.HasPrefix(line, "+++"):
			// Extract filename from +++ b/filename
			parts := strings.SplitN(line, " ", 2)
			if len(parts) == 2 {
				currentFile = strings.TrimPrefix(parts[1], "b/")
				if result[currentFile] == nil {
					result[currentFile] = make(map[uint32]bool)
				}
			}
		case strings.HasPrefix(line, "@@") && currentFile != "":
			// Parse hunk header: @@ -old_start,old_count +new_start,new_count @@
			// We're interested in the new file lines (after +)
			parts := strings.Split(line, "@@")
			if len(parts) >= 2 {
				hunkInfo := strings.TrimSpace(parts[1])
				// Split by space to get old and new ranges
				ranges := strings.Split(hunkInfo, " ")
				for _, r := range ranges {
					if !strings.HasPrefix(r, "+") {
						continue
					}
					// Parse +start,count or +start
					rangeStr := strings.TrimPrefix(r, "+")
					var start, count uint32

					if strings.Contains(rangeStr, ",") {
						_, _ = fmt.Sscanf(rangeStr, "%d,%d", &start, &count)
					} else {
						_, _ = fmt.Sscanf(rangeStr, "%d", &start)
						count = 1
					}

					// Mark all lines in this range as changed
					for i := uint32(0); i < count; i++ {
						result[currentFile][start+i] = true
					}
				}
			}
		}
	}

	return result, nil
}
