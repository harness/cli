// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package ci

import (
	"os/exec"
	"sort"
	"strings"
	"sync"

	"github.com/harness/cli/v3/pkg/hlog"
)

// coveragePathResolutionKind is the outcome of matching a coverage path
// against the git inventory.
type coveragePathResolutionKind int

const (
	// coveragePathResolutionUnavailable means the git file inventory could not be loaded.
	coveragePathResolutionUnavailable coveragePathResolutionKind = iota
	// coveragePathResolutionExact means the path is already a tracked git-relative path.
	coveragePathResolutionExact
	// coveragePathResolutionUnique means exactly one tracked file ends with the coverage path.
	coveragePathResolutionUnique
	// coveragePathResolutionNotFound means no tracked file matches.
	coveragePathResolutionNotFound
	// coveragePathResolutionAmbiguous means more than one tracked file matches.
	coveragePathResolutionAmbiguous
)

// coverageTreeResolution is one path-matching outcome. Candidates is set for ambiguous matches.
type coverageTreeResolution struct {
	kind       coveragePathResolutionKind
	path       string
	candidates []string
}

// coverageTree is a reverse-path-component suffix trie built from a list of
// repository files. It resolves coverage paths (which may omit leading
// directory components such as a monorepo module prefix or Maven source
// root) to their canonical git-relative path via suffix matching.
//
// Inspired by Codecov's pathmap.py implementation.
type coverageTree struct {
	root *coverageTrieNode

	loader func() ([]string, error)
	once   sync.Once
	ready  bool
}

type coverageTrieNode struct {
	// fullPaths holds the complete git-relative paths that end at this node.
	fullPaths []string
	children  map[string]*coverageTrieNode
}

// newCoverageTree builds a suffix trie from the provided list of git-relative file paths.
func newCoverageTree(files []string) *coverageTree {
	t := &coverageTree{ready: true}
	t.build(files)
	return t
}

func (t *coverageTree) build(files []string) {
	t.root = &coverageTrieNode{children: make(map[string]*coverageTrieNode)}
	for _, f := range files {
		t.insert(strings.TrimSpace(f))
	}
}

// Available reports whether the file inventory is available.
func (t *coverageTree) Available() bool {
	if t == nil {
		return false
	}
	if t.loader != nil {
		t.once.Do(t.load)
	}
	return t.ready
}

func (t *coverageTree) load() {
	files, err := t.loader()
	if err != nil {
		hlog.Warn("git ls-files failed, path resolution disabled", "err", err)
		return
	}
	t.build(files)
	t.ready = true
	hlog.Debug("built git file trie", "files", len(files))
}

// insert adds a single path to the trie in reverse component order.
func (t *coverageTree) insert(path string) {
	if path == "" {
		return
	}
	parts := strings.Split(path, "/")
	node := t.root
	// Walk in reverse: filename first, then parent directories.
	for i := len(parts) - 1; i >= 0; i-- {
		part := parts[i]
		if part == "" {
			continue
		}
		child, ok := node.children[part]
		if !ok {
			child = &coverageTrieNode{children: make(map[string]*coverageTrieNode)}
			node.children[part] = child
		}
		node = child
	}
	node.fullPaths = append(node.fullPaths, path)
}

// Resolve returns an exact or unique full-suffix match. It never drops leading
// path components to force a match.
func (t *coverageTree) Resolve(path string) string {
	resolution := t.ResolveWithOutcome(path)
	if resolution.kind == coveragePathResolutionExact || resolution.kind == coveragePathResolutionUnique {
		return resolution.path
	}
	return ""
}

// ResolveExact is "does this exact git path exist?"
// Walks the whole candidate (filename → parents). Only paths stored on that
// node count — a shorter prefix or a file in a child folder does not match.
func (t *coverageTree) ResolveExact(path string) string {
	if !t.Available() {
		return ""
	}

	path = strings.ReplaceAll(path, "\\", "/")
	path = strings.TrimPrefix(path, "/")
	if path == "" {
		return ""
	}

	parts := strings.Split(path, "/")
	node := t.root
	for i := len(parts) - 1; i >= 0; i-- {
		part := parts[i]
		if part == "" {
			continue
		}
		child, ok := node.children[part]
		if !ok {
			return ""
		}
		node = child
	}

	return coverageUniquePath(node.fullPaths)
}

// ResolveWithOutcome reports whether path is exact, a unique suffix, missing, or ambiguous.
// It does not walk shorter suffixes after an ambiguous match.
func (t *coverageTree) ResolveWithOutcome(path string) coverageTreeResolution {
	if !t.Available() {
		return coverageTreeResolution{kind: coveragePathResolutionUnavailable}
	}

	path = coverageNormalizeTriePath(path)
	if path == "" {
		return coverageTreeResolution{kind: coveragePathResolutionNotFound}
	}

	if exact := t.ResolveExact(path); exact != "" {
		return coverageTreeResolution{kind: coveragePathResolutionExact, path: exact}
	}

	// Match the whole path, including a git path that has extra leading
	// directories. Do not drop a leading component: that is what turns
	// build/generated/com/example/Foo.java into src/main/java/com/example/Foo.java.
	return coverageSuffixResolution(coverageDistinctPaths(t.pathsForSuffix(strings.Split(path, "/"))))
}

// Files returns the tracked file paths used to build the inventory.
func (t *coverageTree) Files() []string {
	if !t.Available() {
		return nil
	}
	return coverageDistinctPaths(coverageCollectPaths(t.root))
}

func coverageSuffixResolution(matches []string) coverageTreeResolution {
	switch len(matches) {
	case 0:
		return coverageTreeResolution{kind: coveragePathResolutionNotFound}
	case 1:
		return coverageTreeResolution{kind: coveragePathResolutionUnique, path: matches[0]}
	default:
		return coverageTreeResolution{kind: coveragePathResolutionAmbiguous, candidates: matches}
	}
}

func (t *coverageTree) pathsForSuffix(parts []string) []string {
	node := t.root
	for i := len(parts) - 1; i >= 0; i-- {
		part := parts[i]
		if part == "" {
			continue
		}
		child, ok := node.children[part]
		if !ok {
			return nil
		}
		node = child
	}
	return coverageCollectPaths(node)
}

func coverageNormalizeTriePath(path string) string {
	path = strings.ReplaceAll(path, "\\", "/")
	path = strings.TrimPrefix(path, "/")
	return strings.Trim(path, "/")
}

func coverageDistinctPaths(paths []string) []string {
	if len(paths) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(paths))
	out := make([]string, 0, len(paths))
	for _, candidate := range paths {
		if candidate == "" {
			continue
		}
		if _, ok := seen[candidate]; ok {
			continue
		}
		seen[candidate] = struct{}{}
		out = append(out, candidate)
	}
	sort.Strings(out)
	return out
}

// coverageUniquePath: one distinct path → that path; 0 or 2+ distinct paths → "".
func coverageUniquePath(paths []string) string {
	var seen string
	for _, candidate := range paths {
		if candidate == "" {
			continue
		}
		if seen == "" {
			seen = candidate
			continue
		}
		if candidate != seen {
			return ""
		}
	}
	return seen
}

// coverageCollectPaths gathers all full paths stored at or beneath the given node.
func coverageCollectPaths(n *coverageTrieNode) []string {
	var result []string
	result = append(result, n.fullPaths...)
	for _, child := range n.children {
		result = append(result, coverageCollectPaths(child)...)
	}
	return result
}

// coverageGitLsFiles runs `git ls-files` and returns the list of tracked file
// paths relative to the git root. Returns an error if git is not available or
// the working directory is not inside a git repository.
func coverageGitLsFiles() ([]string, error) {
	out, err := exec.Command("git", "ls-files", "--full-name", "--", ":(top)").Output()
	if err != nil {
		return nil, err
	}
	raw := strings.TrimSpace(string(out))
	if raw == "" {
		return nil, nil
	}
	files := strings.Split(raw, "\n")
	hlog.Debug("git ls-files returned", "files", len(files))
	return files, nil
}

// newCoverageLazyTreeFromGit returns a tree loaded from Git on first use.
func newCoverageLazyTreeFromGit() *coverageTree {
	return &coverageTree{loader: coverageGitLsFiles}
}
