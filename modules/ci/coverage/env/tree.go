// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package env

import (
	"os/exec"
	"sort"
	"strings"
	"sync"

	"github.com/harness/cli/v3/pkg/hlog"
)

// PathResolutionKind is the outcome of matching a coverage path
// against the git inventory.
type PathResolutionKind int

const (
	// PathResolutionUnavailable means the git file inventory could not be loaded.
	PathResolutionUnavailable PathResolutionKind = iota
	// PathResolutionExact means the path is already a tracked git-relative path.
	PathResolutionExact
	// PathResolutionUnique means exactly one tracked file ends with the coverage path.
	PathResolutionUnique
	// PathResolutionNotFound means no tracked file matches.
	PathResolutionNotFound
	// PathResolutionAmbiguous means more than one tracked file matches.
	PathResolutionAmbiguous
)

// TreeResolution is one path-matching outcome. Candidates is set for ambiguous matches.
type TreeResolution struct {
	Kind       PathResolutionKind
	Path       string
	Candidates []string
}

// Tree is a reverse-path-component suffix trie built from a list of
// repository files. It resolves coverage paths (which may omit leading
// directory components such as a monorepo module prefix or Maven source
// root) to their canonical git-relative path via suffix matching.
//
// Inspired by Codecov's pathmap.py implementation.
type Tree struct {
	root *trieNode

	loader func() ([]string, error)
	once   sync.Once
	ready  bool
}

type trieNode struct {
	// fullPaths holds the complete git-relative paths that end at this node.
	fullPaths []string
	children  map[string]*trieNode
}

// NewTree builds a suffix trie from the provided list of git-relative file paths.
func NewTree(files []string) *Tree {
	t := &Tree{ready: true}
	t.build(files)
	return t
}

func (t *Tree) build(files []string) {
	t.root = &trieNode{children: make(map[string]*trieNode)}
	for _, f := range files {
		t.insert(strings.TrimSpace(f))
	}
}

// Available reports whether the file inventory is available.
func (t *Tree) Available() bool {
	if t == nil {
		return false
	}
	if t.loader != nil {
		t.once.Do(t.load)
	}
	return t.ready
}

func (t *Tree) load() {
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
func (t *Tree) insert(path string) {
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
			child = &trieNode{children: make(map[string]*trieNode)}
			node.children[part] = child
		}
		node = child
	}
	node.fullPaths = append(node.fullPaths, path)
}

// Resolve returns an exact or unique full-suffix match. It never drops leading
// path components to force a match.
func (t *Tree) Resolve(path string) string {
	resolution := t.ResolveWithOutcome(path)
	if resolution.Kind == PathResolutionExact || resolution.Kind == PathResolutionUnique {
		return resolution.Path
	}
	return ""
}

// ResolveExact is "does this exact git path exist?"
// Walks the whole candidate (filename → parents). Only paths stored on that
// node count — a shorter prefix or a file in a child folder does not match.
func (t *Tree) ResolveExact(path string) string {
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

	return uniquePath(node.fullPaths)
}

// ResolveWithOutcome reports whether path is exact, a unique suffix, missing, or ambiguous.
// It does not walk shorter suffixes after an ambiguous match.
func (t *Tree) ResolveWithOutcome(path string) TreeResolution {
	if !t.Available() {
		return TreeResolution{Kind: PathResolutionUnavailable}
	}

	path = normalizeTriePath(path)
	if path == "" {
		return TreeResolution{Kind: PathResolutionNotFound}
	}

	if exact := t.ResolveExact(path); exact != "" {
		return TreeResolution{Kind: PathResolutionExact, Path: exact}
	}

	// Match the whole path, including a git path that has extra leading
	// directories. Do not drop a leading component: that is what turns
	// build/generated/com/example/Foo.java into src/main/java/com/example/Foo.java.
	return suffixResolution(distinctPaths(t.pathsForSuffix(strings.Split(path, "/"))))
}

// Files returns the tracked file paths used to build the inventory.
func (t *Tree) Files() []string {
	if !t.Available() {
		return nil
	}
	return distinctPaths(collectPaths(t.root))
}

func suffixResolution(matches []string) TreeResolution {
	switch len(matches) {
	case 0:
		return TreeResolution{Kind: PathResolutionNotFound}
	case 1:
		return TreeResolution{Kind: PathResolutionUnique, Path: matches[0]}
	default:
		return TreeResolution{Kind: PathResolutionAmbiguous, Candidates: matches}
	}
}

func (t *Tree) pathsForSuffix(parts []string) []string {
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
	return collectPaths(node)
}

func normalizeTriePath(path string) string {
	path = strings.ReplaceAll(path, "\\", "/")
	path = strings.TrimPrefix(path, "/")
	return strings.Trim(path, "/")
}

func distinctPaths(paths []string) []string {
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

// uniquePath: one distinct path → that path; 0 or 2+ distinct paths → "".
func uniquePath(paths []string) string {
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

// collectPaths gathers all full paths stored at or beneath the given node.
func collectPaths(n *trieNode) []string {
	var result []string
	result = append(result, n.fullPaths...)
	for _, child := range n.children {
		result = append(result, collectPaths(child)...)
	}
	return result
}

// gitLsFiles runs `git ls-files` and returns the list of tracked file
// paths relative to the git root. Returns an error if git is not available or
// the working directory is not inside a git repository.
func gitLsFiles() ([]string, error) {
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

// NewLazyTreeFromGit returns a tree loaded from Git on first use.
func NewLazyTreeFromGit() *Tree {
	return &Tree{loader: gitLsFiles}
}
