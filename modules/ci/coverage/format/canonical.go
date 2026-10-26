// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package format

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/harness/cli/v3/modules/ci/coverage/env"
	"github.com/harness/cli/v3/pkg/hlog"
)

// RepositoryPathOptions controls repository-relative path resolution.
type RepositoryPathOptions struct {
	// Tree is the git file inventory. Nil or unavailable inventory keeps a
	// safe stored path in full mode and fails patch mode.
	Tree *env.Tree
	// HintDirs are coverage-file directories. They are used only to walk upward
	// to a directory that contains a project manifest. The directory itself is
	// not glued onto a path.
	HintDirs []string
	// GitRoot converts absolute coverage paths into repository-relative paths.
	// Empty skips that conversion.
	GitRoot string
	// Patch fails when a path cannot be resolved or is ambiguous. Full coverage
	// keeps a safe stored path for a miss, and drops an ambiguous file.
	Patch bool
	// Format is the coverage format. Only Go stops when its module directory
	// misses. Other formats continue to a unique repository file.
	Format string
}

// goModule is a go.mod directory and the import path it declares.
type goModule struct {
	rel  string
	name string
}

// stopOnManifestMiss is true only for Go. A pom.xml or package.json above a
// Cobertura, Clover, LCOV, or Coveralls report must not block a unique match.
func (c *RepositoryPathResolver) resolvePath(raw string, stopOnManifestMiss bool) (string, error) {
	if hasParentTraversal(strings.ReplaceAll(raw, "\\", "/")) {
		if c.opts.Patch {
			return "", fmt.Errorf("patch coverage path %q escapes the repository", raw)
		}
		hlog.Warn("coverage path escapes the repository; dropping the file", "path", raw)
		return "", nil
	}
	normalized := normalizeRepoPath(raw)
	if normalized == "" {
		if c.opts.Patch {
			return "", fmt.Errorf("patch coverage path %q is empty", raw)
		}
		return "", nil
	}

	if filepath.IsAbs(raw) {
		rel := relToGitRoot(raw, c.opts.GitRoot)
		stored := storedUnresolvedPath(raw, rel, normalized)
		if c.opts.Tree == nil || !c.opts.Tree.Available() {
			if c.opts.Patch {
				return "", fmt.Errorf("patch coverage path %q cannot be resolved: git file inventory is unavailable", raw)
			}
			return stored, nil
		}
		if rel == "" {
			return c.unresolved(raw, stored)
		}
		if exact := c.opts.Tree.ResolveExact(rel); exact != "" {
			return exact, nil
		}
		// Under the git root but untracked: keep the git-relative path, not a
		// slash-stripped absolute that looks like a tracked repo file.
		return c.unresolved(raw, rel)
	}

	if c.opts.Tree == nil || !c.opts.Tree.Available() {
		if c.opts.Patch {
			return "", fmt.Errorf("patch coverage path %q cannot be resolved: git file inventory is unavailable", raw)
		}
		return normalized, nil
	}

	if exact := c.opts.Tree.ResolveExact(normalized); exact != "" {
		return exact, nil
	}
	if exact, err := c.matchGoModule(normalized); err != nil || exact != "" {
		return exact, err
	}
	// A non-empty module directory is authoritative. A miss must not unique-suffix
	// onto a sibling. A manifest at the repository root does not disable unique suffix.
	if prefixed := nonEmptyManifestRels(c.manifestRels); len(prefixed) > 0 {
		exact, err := c.matchManifestPrefixes(raw, normalized, prefixed)
		if err != nil || exact != "" {
			return exact, err
		}
		if stopOnManifestMiss {
			return c.unresolved(raw, normalized)
		}
	}

	switch resolution := c.opts.Tree.ResolveWithOutcome(normalized); resolution.Kind {
	case env.PathResolutionExact:
		return resolution.Path, nil
	case env.PathResolutionUnique:
		// A bare basename can unique-match the wrong file in a monorepo.
		if !strings.Contains(normalized, "/") {
			if c.opts.Patch {
				return "", fmt.Errorf(
					"patch coverage path %q matched %q by basename only; refusing unique-suffix match",
					raw, resolution.Path,
				)
			}
			hlog.Warn("coverage path matched by basename only; refusing unique-suffix match",
				"path", raw, "matched", resolution.Path, "stored", normalized)
			return normalized, nil
		}
		hlog.Warn("coverage path matched by unique suffix", "path", raw, "matched", resolution.Path)
		return resolution.Path, nil
	case env.PathResolutionAmbiguous:
		return "", ambiguousPathError(raw, resolution.Candidates)
	default:
		return c.unresolved(raw, normalized)
	}
}

func (c *RepositoryPathResolver) unresolved(raw, stored string) (string, error) {
	if c.opts.Patch {
		return "", fmt.Errorf("patch coverage path %q was not found in the repository", raw)
	}
	hlog.Warn("coverage path was not found in the repository", "path", raw, "stored", stored)
	return stored, nil
}

// storedUnresolvedPath chooses what full coverage keeps when a path
// cannot be joined to a tracked repository file. Absolute paths under the git
// root keep the git-relative form; absolute paths outside the repository keep
// the original absolute string so they are not mistaken for repo paths.
func storedUnresolvedPath(raw, rel, normalized string) string {
	if rel != "" {
		return rel
	}
	if filepath.IsAbs(raw) {
		return filepath.ToSlash(filepath.Clean(raw))
	}
	return normalized
}

// pathChoice is one resolved coverage path after exclude filtering.
// Skip means the file was dropped or had no path to store.
type pathChoice struct {
	Path string
	Skip bool
}

// RepositoryPathResolver resolves each raw parser path once per report.
type RepositoryPathResolver struct {
	opts         RepositoryPathOptions
	choices      map[string]cachedChoice
	goModules    []goModule
	manifestRels []string
}

type cachedChoice struct {
	choice pathChoice
	err    error
}

// NewRepositoryPathResolver returns a path resolver for one coverage report.
func NewRepositoryPathResolver(opts RepositoryPathOptions) *RepositoryPathResolver {
	modules, manifests := discoverModules(opts)
	return &RepositoryPathResolver{
		opts:         opts,
		choices:      make(map[string]cachedChoice),
		goModules:    modules,
		manifestRels: manifests,
	}
}

// Resolve returns the canonical path for raw. extra lists exact repository
// paths supplied by the parser, such as JaCoCo module and source-root paths.
// Select owns the policy, including excludes and full-mode ambiguity.
func (c *RepositoryPathResolver) Resolve(raw string, extra ...string) (string, error) {
	choice, err := c.Select(raw, nil, nil, extra...)
	if err != nil {
		return "", err
	}
	return choice.Path, nil
}

// Select resolves raw and applies excludes before an ambiguity failure.
// JaCoCo candidates are tried in order. The first path that matches one
// tracked file wins. A miss tries the next path. The report name comes
// before the report directory. An ambiguous file is dropped in full coverage
// and fails patch.
func (c *RepositoryPathResolver) Select(
	raw string,
	patterns []ExcludePattern,
	excluded map[string]struct{},
	extra ...string,
) (pathChoice, error) {
	cacheKey := selectCacheKey(raw, patterns, extra)
	if cached, ok := c.choices[cacheKey]; ok {
		return cached.choice, cached.err
	}
	choice, err := c.selectUncached(raw, patterns, excluded, extra)
	c.choices[cacheKey] = cachedChoice{choice: choice, err: err}
	return choice, err
}

func (c *RepositoryPathResolver) selectUncached(
	raw string,
	patterns []ExcludePattern,
	excluded map[string]struct{},
	extra []string,
) (pathChoice, error) {
	if hasParentTraversal(strings.ReplaceAll(raw, "\\", "/")) {
		if c.opts.Patch {
			return pathChoice{}, fmt.Errorf("patch coverage path %q escapes the repository", raw)
		}
		hlog.Warn("coverage path escapes the repository; dropping the file", "path", raw)
		return pathChoice{Skip: true}, nil
	}
	if len(extra) > 0 && c.opts.Format == FormatCobertura {
		return c.selectSourceCandidates(raw, patterns, excluded, extra)
	}
	if len(extra) > 0 {
		return c.selectNamedCandidates(raw, patterns, excluded, extra)
	}
	canonical, err := c.resolvePath(raw, c.opts.Format == FormatGo)
	return c.finishChoice(canonical, err, patterns, excluded)
}

// selectSourceCandidates tries each Cobertura <source> joined with the class
// filename, in report order. The first tracked file wins. When none is
// tracked, the bare filename goes through the normal lookup.
func (c *RepositoryPathResolver) selectSourceCandidates(
	raw string,
	patterns []ExcludePattern,
	excluded map[string]struct{},
	extra []string,
) (pathChoice, error) {
	if c.opts.Tree != nil && c.opts.Tree.Available() {
		sawExcluded := false
		for _, candidate := range extra {
			tracked := c.trackedSourceCandidate(candidate)
			if tracked == "" {
				continue
			}
			if matchesAnyExcludePattern(tracked, patterns) {
				logExcludedFileOnce(excluded, tracked)
				sawExcluded = true
				continue
			}
			return pathChoice{Path: tracked}, nil
		}
		if sawExcluded {
			return pathChoice{Skip: true}, nil
		}
	}
	canonical, err := c.resolvePath(raw, false)
	return c.finishChoice(canonical, err, patterns, excluded)
}

// trackedSourceCandidate returns the tracked file for one source candidate.
// An absolute source must be under the git root.
func (c *RepositoryPathResolver) trackedSourceCandidate(candidate string) string {
	if hasParentTraversal(strings.ReplaceAll(candidate, "\\", "/")) {
		return ""
	}
	if filepath.IsAbs(candidate) || strings.HasPrefix(candidate, "/") {
		rel := relToGitRoot(candidate, c.opts.GitRoot)
		if rel == "" {
			return ""
		}
		return c.opts.Tree.ResolveExact(rel)
	}
	return c.exactCandidate(candidate)
}

func selectCacheKey(raw string, patterns []ExcludePattern, extra []string) string {
	var b strings.Builder
	b.WriteString(raw)
	b.WriteByte(0)
	for _, pattern := range patterns {
		b.WriteString(pattern.raw)
		b.WriteByte(0)
	}
	b.WriteByte(0)
	for _, candidate := range extra {
		b.WriteString(candidate)
		b.WriteByte(0)
	}
	return b.String()
}

func (c *RepositoryPathResolver) selectNamedCandidates(
	raw string,
	patterns []ExcludePattern,
	excluded map[string]struct{},
	extra []string,
) (pathChoice, error) {
	normalized := normalizeRepoPath(raw)
	if normalized == "" {
		if c.opts.Patch {
			return pathChoice{}, fmt.Errorf("patch coverage path %q is empty", raw)
		}
		return pathChoice{Skip: true}, nil
	}
	if c.opts.Tree == nil || !c.opts.Tree.Available() {
		if c.opts.Patch {
			return pathChoice{}, fmt.Errorf("patch coverage path %q cannot be resolved: git file inventory is unavailable", raw)
		}
		return dropIfExcluded(normalized, patterns, excluded), nil
	}

	sawExcluded := false
	for _, candidate := range c.orderedJacocoPaths(normalized, extra) {
		exact := c.exactCandidate(candidate)
		if exact == "" {
			continue
		}
		if matchesAnyExcludePattern(exact, patterns) {
			logExcludedFileOnce(excluded, exact)
			sawExcluded = true
			continue
		}
		return pathChoice{Path: exact}, nil
	}
	for _, candidate := range nonEmptySourceRootSuffixes(normalized) {
		choice, err, next := c.matchSourceRoot(raw, candidate, patterns, excluded)
		if err != nil {
			return c.finishChoice("", err, patterns, excluded)
		}
		if next {
			if choice.Skip {
				sawExcluded = true
			}
			continue
		}
		return choice, nil
	}
	if sawExcluded {
		return pathChoice{Skip: true}, nil
	}
	canonical, err := c.resolvePath(raw, false)
	return c.finishChoice(canonical, err, patterns, excluded)
}

// orderedJacocoPaths lists exact paths to try. The report name comes first.
// The module that owns the report file is the fallback. Within each prefix,
// the package path comes first, then production source roots, then test roots.
func (c *RepositoryPathResolver) orderedJacocoPaths(filePath string, extra []string) []string {
	suffixes := sourceRootSuffixes(filePath)
	suffixSet := make(map[string]struct{}, len(suffixes))
	for _, suffix := range suffixes {
		suffixSet[suffix] = struct{}{}
	}
	var qualifier string
	for _, candidate := range extra {
		if hasParentTraversal(strings.ReplaceAll(candidate, "\\", "/")) {
			continue
		}
		if _, ok := suffixSet[normalizeRepoPath(candidate)]; ok {
			continue
		}
		parsed, ok := qualifierBeforeSuffix(candidate, suffixes)
		if !ok || parsed == "" || hasParentTraversal(parsed) {
			continue
		}
		qualifier = parsed
		break
	}

	var ordered []string
	if qualifier != "" {
		ordered = appendJoined(ordered, qualifier, suffixes)
	}
	for _, dir := range nonEmptyManifestRels(c.manifestRels) {
		ordered = appendJoined(ordered, dir, suffixes)
	}
	return ordered
}

func nonEmptySourceRootSuffixes(filePath string) []string {
	suffixes := sourceRootSuffixes(filePath)
	var roots []string
	for _, suffix := range suffixes {
		if suffix == "" || suffix == filePath || hasParentTraversal(suffix) {
			continue
		}
		roots = append(roots, suffix)
	}
	return roots
}

func appendJoined(paths []string, prefix string, suffixes []string) []string {
	if prefix == "" || hasParentTraversal(prefix) {
		return paths
	}
	for _, suffix := range suffixes {
		if suffix == "" || hasParentTraversal(suffix) {
			continue
		}
		paths = append(paths, path.Join(prefix, suffix))
	}
	return paths
}

func (c *RepositoryPathResolver) exactCandidate(candidate string) string {
	if hasParentTraversal(strings.ReplaceAll(candidate, "\\", "/")) {
		return ""
	}
	candidate = normalizeRepoPath(candidate)
	if candidate == "" || hasParentTraversal(candidate) {
		return ""
	}
	return c.opts.Tree.ResolveExact(candidate)
}

// matchSourceRoot tries one source-root suffix.
// next means this root did not choose a file, so the following root is tried.
// An ambiguous root stops the search: a later test root must not win.
func (c *RepositoryPathResolver) matchSourceRoot(
	raw string,
	candidate string,
	patterns []ExcludePattern,
	excluded map[string]struct{},
) (pathChoice, error, bool) {
	if hasParentTraversal(candidate) || !strings.Contains(candidate, "/") {
		return pathChoice{}, nil, true
	}
	resolution := c.opts.Tree.ResolveWithOutcome(candidate)
	switch resolution.Kind {
	case env.PathResolutionExact, env.PathResolutionUnique:
		if matchesAnyExcludePattern(resolution.Path, patterns) {
			logExcludedFileOnce(excluded, resolution.Path)
			return pathChoice{Skip: true}, nil, true
		}
		if resolution.Kind == env.PathResolutionUnique {
			hlog.Warn("coverage path matched by unique suffix", "path", raw, "matched", resolution.Path)
		}
		return pathChoice{Path: resolution.Path}, nil, false
	case env.PathResolutionAmbiguous:
		kept := pathsKeptAfterExclude(resolution.Candidates, patterns, excluded)
		switch len(kept) {
		case 0:
			return pathChoice{Skip: true}, nil, true
		case 1:
			return pathChoice{Path: kept[0]}, nil, false
		default:
			return pathChoice{}, ambiguousPathError(raw, kept), false
		}
	default:
		return pathChoice{}, nil, true
	}
}

func pathsKeptAfterExclude(paths []string, patterns []ExcludePattern, excluded map[string]struct{}) []string {
	kept := make([]string, 0, len(paths))
	for _, candidate := range paths {
		if matchesAnyExcludePattern(candidate, patterns) {
			logExcludedFileOnce(excluded, candidate)
			continue
		}
		kept = append(kept, candidate)
	}
	return kept
}

func qualifierBeforeSuffix(candidate string, suffixes []string) (string, bool) {
	candidate = normalizeRepoPath(candidate)
	// The longest suffix is the source-root path, so the remainder is only
	// the module directory and not src/main/java.
	bestSuffix := ""
	for _, suffix := range suffixes {
		if suffix == "" || candidate == suffix || len(suffix) < len(bestSuffix) {
			continue
		}
		if strings.HasSuffix(candidate, "/"+suffix) {
			bestSuffix = suffix
		}
	}
	if bestSuffix == "" {
		return "", false
	}
	return strings.TrimSuffix(candidate, "/"+bestSuffix), true
}

func (c *RepositoryPathResolver) finishChoice(
	path string,
	err error,
	patterns []ExcludePattern,
	excluded map[string]struct{},
) (pathChoice, error) {
	if err != nil {
		if !c.opts.Patch && isAmbiguousPath(err) {
			hlog.Warn("dropping the file from full coverage", "err", err.Error())
			return pathChoice{Skip: true}, nil
		}
		return pathChoice{}, err
	}
	return dropIfExcluded(path, patterns, excluded), nil
}

type ambiguousPathsError struct {
	raw        string
	candidates []string
}

func (e *ambiguousPathsError) Error() string {
	return fmt.Sprintf("coverage path %q matches multiple repository files: %s", e.raw, strings.Join(e.candidates, ", "))
}

func isAmbiguousPath(err error) bool {
	var ambiguous *ambiguousPathsError
	return errors.As(err, &ambiguous)
}

// JoinPathErrors combines path failures from one report so --patch
// names every unresolved or ambiguous file instead of stopping at the first.
func JoinPathErrors(errs []error) error {
	if len(errs) == 0 {
		return nil
	}
	if len(errs) == 1 {
		return errs[0]
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d coverage paths could not be resolved:", len(errs))
	for _, err := range errs {
		b.WriteByte('\n')
		b.WriteString(err.Error())
	}
	return errors.New(b.String())
}

// AppendPathError records err once per raw coverage path.
func AppendPathError(errs []error, seen map[string]struct{}, raw string, err error) []error {
	if err == nil {
		return errs
	}
	if _, ok := seen[raw]; ok {
		return errs
	}
	if seen != nil {
		seen[raw] = struct{}{}
	}
	return append(errs, err)
}

func dropIfExcluded(path string, patterns []ExcludePattern, excluded map[string]struct{}) pathChoice {
	if path == "" {
		return pathChoice{Skip: true}
	}
	if matchesAnyExcludePattern(path, patterns) {
		logExcludedFileOnce(excluded, path)
		return pathChoice{Skip: true}
	}
	return pathChoice{Path: path}
}

func (c *RepositoryPathResolver) matchGoModule(normalized string) (string, error) {
	bestLen := -1
	var matches []string
	seen := make(map[string]struct{})
	for _, module := range c.goModules {
		candidate := modulePath(module, normalized)
		if candidate == "" || len(module.name) < bestLen {
			continue
		}
		exact := c.opts.Tree.ResolveExact(candidate)
		if exact == "" {
			continue
		}
		if len(module.name) > bestLen {
			bestLen = len(module.name)
			matches = matches[:0]
			seen = map[string]struct{}{exact: {}}
			matches = append(matches, exact)
			continue
		}
		if _, ok := seen[exact]; ok {
			continue
		}
		seen[exact] = struct{}{}
		matches = append(matches, exact)
	}
	return oneExactMatch(normalized, matches)
}

func (c *RepositoryPathResolver) matchManifestPrefixes(raw, normalized string, rels []string) (string, error) {
	if hasParentTraversal(normalized) {
		return "", nil
	}
	var matches []string
	for _, rel := range rels {
		if rel != "" && hasParentTraversal(rel) {
			continue
		}
		candidate := normalized
		if rel != "" {
			candidate = path.Join(rel, normalized)
		}
		if hasParentTraversal(candidate) {
			continue
		}
		if exact := c.opts.Tree.ResolveExact(candidate); exact != "" {
			matches = append(matches, exact)
		}
	}
	return oneExactMatch(raw, matches)
}

func nonEmptyManifestRels(rels []string) []string {
	var prefixed []string
	for _, rel := range rels {
		if rel != "" {
			prefixed = append(prefixed, rel)
		}
	}
	return prefixed
}

func oneExactMatch(raw string, matches []string) (string, error) {
	switch len(matches) {
	case 0:
		return "", nil
	case 1:
		return matches[0], nil
	default:
		return "", ambiguousPathError(raw, matches)
	}
}

func ambiguousPathError(raw string, candidates []string) error {
	return &ambiguousPathsError{raw: raw, candidates: append([]string(nil), candidates...)}
}

var projectManifestNames = map[string]struct{}{
	"go.mod": {}, "package.json": {}, "pom.xml": {}, "build.gradle": {},
	"build.gradle.kts": {}, "pyproject.toml": {}, "setup.py": {},
	"Cargo.toml": {}, "composer.json": {},
}

func discoverModules(opts RepositoryPathOptions) ([]goModule, []string) {
	if opts.Tree == nil || !opts.Tree.Available() {
		return nil, nil
	}
	gitRoot := resolvedDir(opts.GitRoot)
	tracked := opts.Tree.Files()
	manifestDirs := make(map[string]struct{})
	var modules []goModule
	for _, trackedPath := range tracked {
		base := path.Base(trackedPath)
		if _, ok := projectManifestNames[base]; !ok {
			continue
		}
		rel := path.Dir(trackedPath)
		if rel == "." {
			rel = ""
		}
		manifestDirs[rel] = struct{}{}
		if base != "go.mod" || gitRoot == "" {
			continue
		}
		name, err := readGoModuleName(filepath.Join(gitRoot, filepath.FromSlash(trackedPath)))
		if err != nil {
			hlog.Warn("failed to read module name", "file", trackedPath, "err", err)
			continue
		}
		if name != "" {
			modules = append(modules, goModule{rel: rel, name: name})
		}
	}

	// A report directory that belongs to a tracked module is authoritative.
	// The working directory is only a fallback when no report hint identifies
	// a module.
	var reportRels []string
	for _, hint := range opts.HintDirs {
		if rel, ok := pathRelativeToRoot(hint, gitRoot); ok {
			if moduleRel, found := nearestManifestDir(rel, manifestDirs); found {
				reportRels = appendUnique(reportRels, moduleRel)
			}
		}
	}
	if len(reportRels) > 0 {
		return modules, reportRels
	}
	if wd, err := os.Getwd(); err == nil {
		if rel, ok := repositoryRelative(wd, gitRoot); ok {
			if moduleRel, found := nearestManifestDir(rel, manifestDirs); found {
				return modules, []string{moduleRel}
			}
		}
	}
	return modules, nil
}

func pathRelativeToRoot(candidate, gitRoot string) (string, bool) {
	if candidate == "" || gitRoot == "" {
		return "", false
	}
	if !filepath.IsAbs(candidate) {
		absolute, err := filepath.Abs(candidate)
		if err != nil {
			return "", false
		}
		candidate = absolute
	}
	return repositoryRelative(candidate, gitRoot)
}

func nearestManifestDir(rel string, manifests map[string]struct{}) (string, bool) {
	rel = strings.Trim(filepath.ToSlash(rel), "/")
	for {
		if _, ok := manifests[rel]; ok {
			return rel, true
		}
		if rel == "" {
			return "", false
		}
		parent := path.Dir(rel)
		if parent == "." || parent == rel {
			rel = ""
		} else {
			rel = parent
		}
	}
}

func appendUnique(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func readGoModuleName(modPath string) (string, error) {
	moduleFile, err := os.Open(modPath)
	if err != nil {
		return "", err
	}
	defer func() { _ = moduleFile.Close() }()

	scanner := bufio.NewScanner(moduleFile)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "module ") {
			continue
		}
		name := strings.TrimSpace(strings.TrimPrefix(line, "module "))
		if comment := strings.Index(name, "//"); comment >= 0 {
			name = strings.TrimSpace(name[:comment])
		}
		name = strings.Trim(name, `"`)
		if name == "" {
			return "", fmt.Errorf("empty module name in %s", modPath)
		}
		return name, nil
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	return "", fmt.Errorf("module name not found in %s", modPath)
}

func modulePath(module goModule, importPath string) string {
	if module.name == "" || importPath == module.name {
		return module.rel
	}
	prefix := module.name + "/"
	if !strings.HasPrefix(importPath, prefix) {
		return ""
	}
	rest := strings.TrimPrefix(importPath, prefix)
	if module.rel == "" {
		return rest
	}
	return path.Join(module.rel, rest)
}

func resolvedDir(dir string) string {
	if dir == "" {
		return ""
	}
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		return resolved
	}
	return dir
}

// repositoryRelative reports whether dir is inside gitRoot. An empty
// rel means dir is the repository root.
func repositoryRelative(dir, gitRoot string) (string, bool) {
	if gitRoot == "" {
		return "", false
	}
	dir = resolvedDir(dir)
	gitRoot = resolvedDir(gitRoot)
	rel, err := filepath.Rel(gitRoot, dir)
	if err != nil || hasParentTraversal(filepath.ToSlash(rel)) {
		return "", false
	}
	if rel == "." {
		return "", true
	}
	return filepath.ToSlash(rel), true
}

func relToGitRoot(raw, gitRoot string) string {
	if gitRoot == "" || !filepath.IsAbs(raw) {
		return ""
	}
	if resolved, err := filepath.EvalSymlinks(gitRoot); err == nil {
		gitRoot = resolved
	}
	if resolved, err := filepath.EvalSymlinks(raw); err == nil {
		raw = resolved
	}
	rel, err := filepath.Rel(gitRoot, raw)
	if err != nil || rel == "." || rel == "" || hasParentTraversal(filepath.ToSlash(rel)) {
		return ""
	}
	return filepath.ToSlash(rel)
}

func hasParentTraversal(filePath string) bool {
	for _, segment := range strings.Split(filepath.ToSlash(filePath), "/") {
		if segment == ".." {
			return true
		}
	}
	return false
}

func normalizeRepoPath(raw string) string {
	cleaned := strings.TrimSpace(strings.ReplaceAll(raw, "\\", "/"))
	if cleaned == "" {
		return ""
	}
	absolute := strings.HasPrefix(cleaned, "/") || filepath.IsAbs(raw)
	cleaned = path.Clean(cleaned)
	if absolute {
		cleaned = strings.TrimPrefix(cleaned, "/")
	}
	cleaned = strings.TrimPrefix(cleaned, "./")
	if cleaned == "." || cleaned == "" {
		return ""
	}
	return cleaned
}
