// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package ci

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/harness/cli/v3/pkg/hlog"
)

// coverageRepositoryPathOptions controls repository-relative path resolution.
type coverageRepositoryPathOptions struct {
	// Tree is the git file inventory. Nil or unavailable inventory keeps a
	// safe stored path in full mode and fails patch mode.
	Tree *coverageTree
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

// coverageGoModule is a go.mod directory and the import path it declares.
type coverageGoModule struct {
	rel  string
	name string
}

// stopOnManifestMiss is true only for Go. A pom.xml or package.json above a
// Cobertura, Clover, LCOV, or Coveralls report must not block a unique match.
func (c *coverageRepositoryPathResolver) resolvePath(raw string, stopOnManifestMiss bool) (string, error) {
	if coverageHasParentTraversal(strings.ReplaceAll(raw, "\\", "/")) {
		if c.opts.Patch {
			return "", fmt.Errorf("patch coverage path %q escapes the repository", raw)
		}
		hlog.Warn("coverage path escapes the repository; dropping the file", "path", raw)
		return "", nil
	}
	normalized := coverageNormalizeRepoPath(raw)
	if normalized == "" {
		if c.opts.Patch {
			return "", fmt.Errorf("patch coverage path %q is empty", raw)
		}
		return "", nil
	}

	if filepath.IsAbs(raw) {
		rel := coverageRelToGitRoot(raw, c.opts.GitRoot)
		stored := coverageStoredUnresolvedPath(raw, rel, normalized)
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
	if prefixed := coverageNonEmptyManifestRels(c.manifestRels); len(prefixed) > 0 {
		exact, err := c.matchManifestPrefixes(raw, normalized, prefixed)
		if err != nil || exact != "" {
			return exact, err
		}
		if stopOnManifestMiss {
			return c.unresolved(raw, normalized)
		}
	}

	switch resolution := c.opts.Tree.ResolveWithOutcome(normalized); resolution.kind {
	case coveragePathResolutionExact:
		return resolution.path, nil
	case coveragePathResolutionUnique:
		// A bare basename can unique-match the wrong file in a monorepo.
		if !strings.Contains(normalized, "/") {
			if c.opts.Patch {
				return "", fmt.Errorf(
					"patch coverage path %q matched %q by basename only; refusing unique-suffix match",
					raw, resolution.path,
				)
			}
			hlog.Warn("coverage path matched by basename only; refusing unique-suffix match",
				"path", raw, "matched", resolution.path, "stored", normalized)
			return normalized, nil
		}
		hlog.Warn("coverage path matched by unique suffix", "path", raw, "matched", resolution.path)
		return resolution.path, nil
	case coveragePathResolutionAmbiguous:
		return "", coverageAmbiguousPathError(raw, resolution.candidates)
	default:
		return c.unresolved(raw, normalized)
	}
}

func (c *coverageRepositoryPathResolver) unresolved(raw, stored string) (string, error) {
	if c.opts.Patch {
		return "", fmt.Errorf("patch coverage path %q was not found in the repository", raw)
	}
	hlog.Warn("coverage path was not found in the repository", "path", raw, "stored", stored)
	return stored, nil
}

// coverageStoredUnresolvedPath chooses what full coverage keeps when a path
// cannot be joined to a tracked repository file. Absolute paths under the git
// root keep the git-relative form; absolute paths outside the repository keep
// the original absolute string so they are not mistaken for repo paths.
func coverageStoredUnresolvedPath(raw, rel, normalized string) string {
	if rel != "" {
		return rel
	}
	if filepath.IsAbs(raw) {
		return filepath.ToSlash(filepath.Clean(raw))
	}
	return normalized
}

// coveragePathChoice is one resolved coverage path after exclude filtering.
// Skip means the file was dropped or had no path to store.
type coveragePathChoice struct {
	Path string
	Skip bool
}

// coverageRepositoryPathResolver resolves each raw parser path once per report.
type coverageRepositoryPathResolver struct {
	opts         coverageRepositoryPathOptions
	choices      map[string]coverageCachedChoice
	goModules    []coverageGoModule
	manifestRels []string
}

type coverageCachedChoice struct {
	choice coveragePathChoice
	err    error
}

// newCoverageRepositoryPathResolver returns a path resolver for one coverage report.
func newCoverageRepositoryPathResolver(opts coverageRepositoryPathOptions) *coverageRepositoryPathResolver {
	modules, manifests := coverageDiscoverModules(opts)
	return &coverageRepositoryPathResolver{
		opts:         opts,
		choices:      make(map[string]coverageCachedChoice),
		goModules:    modules,
		manifestRels: manifests,
	}
}

// Resolve returns the canonical path for raw. extra lists exact repository
// paths supplied by the parser, such as JaCoCo module and source-root paths.
// Select owns the policy, including excludes and full-mode ambiguity.
func (c *coverageRepositoryPathResolver) Resolve(raw string, extra ...string) (string, error) {
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
func (c *coverageRepositoryPathResolver) Select(
	raw string,
	patterns []coverageExcludePattern,
	excluded map[string]struct{},
	extra ...string,
) (coveragePathChoice, error) {
	cacheKey := coverageSelectCacheKey(raw, patterns, extra)
	if cached, ok := c.choices[cacheKey]; ok {
		return cached.choice, cached.err
	}
	choice, err := c.selectUncached(raw, patterns, excluded, extra)
	c.choices[cacheKey] = coverageCachedChoice{choice: choice, err: err}
	return choice, err
}

func (c *coverageRepositoryPathResolver) selectUncached(
	raw string,
	patterns []coverageExcludePattern,
	excluded map[string]struct{},
	extra []string,
) (coveragePathChoice, error) {
	if coverageHasParentTraversal(strings.ReplaceAll(raw, "\\", "/")) {
		if c.opts.Patch {
			return coveragePathChoice{}, fmt.Errorf("patch coverage path %q escapes the repository", raw)
		}
		hlog.Warn("coverage path escapes the repository; dropping the file", "path", raw)
		return coveragePathChoice{Skip: true}, nil
	}
	if len(extra) > 0 && c.opts.Format == coverageFormatCobertura {
		return c.selectSourceCandidates(raw, patterns, excluded, extra)
	}
	if len(extra) > 0 {
		return c.selectNamedCandidates(raw, patterns, excluded, extra)
	}
	canonical, err := c.resolvePath(raw, c.opts.Format == coverageFormatGo)
	return c.finishChoice(canonical, err, patterns, excluded)
}

// selectSourceCandidates tries each Cobertura <source> joined with the class
// filename, in report order. The first tracked file wins. When none is
// tracked, the bare filename goes through the normal lookup.
func (c *coverageRepositoryPathResolver) selectSourceCandidates(
	raw string,
	patterns []coverageExcludePattern,
	excluded map[string]struct{},
	extra []string,
) (coveragePathChoice, error) {
	if c.opts.Tree != nil && c.opts.Tree.Available() {
		sawExcluded := false
		for _, candidate := range extra {
			tracked := c.trackedSourceCandidate(candidate)
			if tracked == "" {
				continue
			}
			if coverageMatchesAnyExcludePattern(tracked, patterns) {
				coverageLogExcludedFileOnce(excluded, tracked)
				sawExcluded = true
				continue
			}
			return coveragePathChoice{Path: tracked}, nil
		}
		if sawExcluded {
			return coveragePathChoice{Skip: true}, nil
		}
	}
	canonical, err := c.resolvePath(raw, false)
	return c.finishChoice(canonical, err, patterns, excluded)
}

// trackedSourceCandidate returns the tracked file for one source candidate.
// An absolute source must be under the git root.
func (c *coverageRepositoryPathResolver) trackedSourceCandidate(candidate string) string {
	if coverageHasParentTraversal(strings.ReplaceAll(candidate, "\\", "/")) {
		return ""
	}
	if filepath.IsAbs(candidate) || strings.HasPrefix(candidate, "/") {
		rel := coverageRelToGitRoot(candidate, c.opts.GitRoot)
		if rel == "" {
			return ""
		}
		return c.opts.Tree.ResolveExact(rel)
	}
	return c.exactCandidate(candidate)
}

func coverageSelectCacheKey(raw string, patterns []coverageExcludePattern, extra []string) string {
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

func (c *coverageRepositoryPathResolver) selectNamedCandidates(
	raw string,
	patterns []coverageExcludePattern,
	excluded map[string]struct{},
	extra []string,
) (coveragePathChoice, error) {
	normalized := coverageNormalizeRepoPath(raw)
	if normalized == "" {
		if c.opts.Patch {
			return coveragePathChoice{}, fmt.Errorf("patch coverage path %q is empty", raw)
		}
		return coveragePathChoice{Skip: true}, nil
	}
	if c.opts.Tree == nil || !c.opts.Tree.Available() {
		if c.opts.Patch {
			return coveragePathChoice{}, fmt.Errorf("patch coverage path %q cannot be resolved: git file inventory is unavailable", raw)
		}
		return coverageDropIfExcluded(normalized, patterns, excluded), nil
	}

	sawExcluded := false
	for _, candidate := range c.orderedJacocoPaths(normalized, extra) {
		exact := c.exactCandidate(candidate)
		if exact == "" {
			continue
		}
		if coverageMatchesAnyExcludePattern(exact, patterns) {
			coverageLogExcludedFileOnce(excluded, exact)
			sawExcluded = true
			continue
		}
		return coveragePathChoice{Path: exact}, nil
	}
	for _, candidate := range coverageNonEmptySourceRootSuffixes(normalized) {
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
		return coveragePathChoice{Skip: true}, nil
	}
	canonical, err := c.resolvePath(raw, false)
	return c.finishChoice(canonical, err, patterns, excluded)
}

// orderedJacocoPaths lists exact paths to try. The report name comes first.
// The module that owns the report file is the fallback. Within each prefix,
// the package path comes first, then production source roots, then test roots.
func (c *coverageRepositoryPathResolver) orderedJacocoPaths(filePath string, extra []string) []string {
	suffixes := coverageSourceRootSuffixes(filePath)
	suffixSet := make(map[string]struct{}, len(suffixes))
	for _, suffix := range suffixes {
		suffixSet[suffix] = struct{}{}
	}
	var qualifier string
	for _, candidate := range extra {
		if coverageHasParentTraversal(strings.ReplaceAll(candidate, "\\", "/")) {
			continue
		}
		if _, ok := suffixSet[coverageNormalizeRepoPath(candidate)]; ok {
			continue
		}
		parsed, ok := coverageQualifierBeforeSuffix(candidate, suffixes)
		if !ok || parsed == "" || coverageHasParentTraversal(parsed) {
			continue
		}
		qualifier = parsed
		break
	}

	var ordered []string
	if qualifier != "" {
		ordered = coverageAppendJoined(ordered, qualifier, suffixes)
	}
	for _, dir := range coverageNonEmptyManifestRels(c.manifestRels) {
		ordered = coverageAppendJoined(ordered, dir, suffixes)
	}
	return ordered
}

func coverageNonEmptySourceRootSuffixes(filePath string) []string {
	suffixes := coverageSourceRootSuffixes(filePath)
	var roots []string
	for _, suffix := range suffixes {
		if suffix == "" || suffix == filePath || coverageHasParentTraversal(suffix) {
			continue
		}
		roots = append(roots, suffix)
	}
	return roots
}

func coverageAppendJoined(paths []string, prefix string, suffixes []string) []string {
	if prefix == "" || coverageHasParentTraversal(prefix) {
		return paths
	}
	for _, suffix := range suffixes {
		if suffix == "" || coverageHasParentTraversal(suffix) {
			continue
		}
		paths = append(paths, path.Join(prefix, suffix))
	}
	return paths
}

func (c *coverageRepositoryPathResolver) exactCandidate(candidate string) string {
	if coverageHasParentTraversal(strings.ReplaceAll(candidate, "\\", "/")) {
		return ""
	}
	candidate = coverageNormalizeRepoPath(candidate)
	if candidate == "" || coverageHasParentTraversal(candidate) {
		return ""
	}
	return c.opts.Tree.ResolveExact(candidate)
}

// matchSourceRoot tries one source-root suffix.
// next means this root did not choose a file, so the following root is tried.
// An ambiguous root stops the search: a later test root must not win.
func (c *coverageRepositoryPathResolver) matchSourceRoot(
	raw string,
	candidate string,
	patterns []coverageExcludePattern,
	excluded map[string]struct{},
) (coveragePathChoice, error, bool) {
	if coverageHasParentTraversal(candidate) || !strings.Contains(candidate, "/") {
		return coveragePathChoice{}, nil, true
	}
	resolution := c.opts.Tree.ResolveWithOutcome(candidate)
	switch resolution.kind {
	case coveragePathResolutionExact, coveragePathResolutionUnique:
		if coverageMatchesAnyExcludePattern(resolution.path, patterns) {
			coverageLogExcludedFileOnce(excluded, resolution.path)
			return coveragePathChoice{Skip: true}, nil, true
		}
		if resolution.kind == coveragePathResolutionUnique {
			hlog.Warn("coverage path matched by unique suffix", "path", raw, "matched", resolution.path)
		}
		return coveragePathChoice{Path: resolution.path}, nil, false
	case coveragePathResolutionAmbiguous:
		kept := coveragePathsKeptAfterExclude(resolution.candidates, patterns, excluded)
		switch len(kept) {
		case 0:
			return coveragePathChoice{Skip: true}, nil, true
		case 1:
			return coveragePathChoice{Path: kept[0]}, nil, false
		default:
			return coveragePathChoice{}, coverageAmbiguousPathError(raw, kept), false
		}
	default:
		return coveragePathChoice{}, nil, true
	}
}

func coveragePathsKeptAfterExclude(paths []string, patterns []coverageExcludePattern, excluded map[string]struct{}) []string {
	kept := make([]string, 0, len(paths))
	for _, candidate := range paths {
		if coverageMatchesAnyExcludePattern(candidate, patterns) {
			coverageLogExcludedFileOnce(excluded, candidate)
			continue
		}
		kept = append(kept, candidate)
	}
	return kept
}

func coverageQualifierBeforeSuffix(candidate string, suffixes []string) (string, bool) {
	candidate = coverageNormalizeRepoPath(candidate)
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

func (c *coverageRepositoryPathResolver) finishChoice(
	path string,
	err error,
	patterns []coverageExcludePattern,
	excluded map[string]struct{},
) (coveragePathChoice, error) {
	if err != nil {
		if !c.opts.Patch && coverageIsAmbiguousPath(err) {
			hlog.Warn("dropping the file from full coverage", "err", err.Error())
			return coveragePathChoice{Skip: true}, nil
		}
		return coveragePathChoice{}, err
	}
	return coverageDropIfExcluded(path, patterns, excluded), nil
}

type coverageAmbiguousPathsError struct {
	raw        string
	candidates []string
}

func (e *coverageAmbiguousPathsError) Error() string {
	return fmt.Sprintf("coverage path %q matches multiple repository files: %s", e.raw, strings.Join(e.candidates, ", "))
}

func coverageIsAmbiguousPath(err error) bool {
	var ambiguous *coverageAmbiguousPathsError
	return errors.As(err, &ambiguous)
}

// coverageJoinPathErrors combines path failures from one report so --patch
// names every unresolved or ambiguous file instead of stopping at the first.
func coverageJoinPathErrors(errs []error) error {
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

// coverageAppendPathError records err once per raw coverage path.
func coverageAppendPathError(errs []error, seen map[string]struct{}, raw string, err error) []error {
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

func coverageDropIfExcluded(path string, patterns []coverageExcludePattern, excluded map[string]struct{}) coveragePathChoice {
	if path == "" {
		return coveragePathChoice{Skip: true}
	}
	if coverageMatchesAnyExcludePattern(path, patterns) {
		coverageLogExcludedFileOnce(excluded, path)
		return coveragePathChoice{Skip: true}
	}
	return coveragePathChoice{Path: path}
}

func (c *coverageRepositoryPathResolver) matchGoModule(normalized string) (string, error) {
	bestLen := -1
	var matches []string
	seen := make(map[string]struct{})
	for _, module := range c.goModules {
		candidate := coverageModulePath(module, normalized)
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
	return coverageOneExactMatch(normalized, matches)
}

func (c *coverageRepositoryPathResolver) matchManifestPrefixes(raw, normalized string, rels []string) (string, error) {
	if coverageHasParentTraversal(normalized) {
		return "", nil
	}
	var matches []string
	for _, rel := range rels {
		if rel != "" && coverageHasParentTraversal(rel) {
			continue
		}
		candidate := normalized
		if rel != "" {
			candidate = path.Join(rel, normalized)
		}
		if coverageHasParentTraversal(candidate) {
			continue
		}
		if exact := c.opts.Tree.ResolveExact(candidate); exact != "" {
			matches = append(matches, exact)
		}
	}
	return coverageOneExactMatch(raw, matches)
}

func coverageNonEmptyManifestRels(rels []string) []string {
	var prefixed []string
	for _, rel := range rels {
		if rel != "" {
			prefixed = append(prefixed, rel)
		}
	}
	return prefixed
}

func coverageOneExactMatch(raw string, matches []string) (string, error) {
	switch len(matches) {
	case 0:
		return "", nil
	case 1:
		return matches[0], nil
	default:
		return "", coverageAmbiguousPathError(raw, matches)
	}
}

func coverageAmbiguousPathError(raw string, candidates []string) error {
	return &coverageAmbiguousPathsError{raw: raw, candidates: append([]string(nil), candidates...)}
}

var coverageProjectManifestNames = map[string]struct{}{
	"go.mod": {}, "package.json": {}, "pom.xml": {}, "build.gradle": {},
	"build.gradle.kts": {}, "pyproject.toml": {}, "setup.py": {},
	"Cargo.toml": {}, "composer.json": {},
}

func coverageDiscoverModules(opts coverageRepositoryPathOptions) ([]coverageGoModule, []string) {
	if opts.Tree == nil || !opts.Tree.Available() {
		return nil, nil
	}
	gitRoot := coverageResolvedDir(opts.GitRoot)
	tracked := opts.Tree.Files()
	manifestDirs := make(map[string]struct{})
	var modules []coverageGoModule
	for _, trackedPath := range tracked {
		base := path.Base(trackedPath)
		if _, ok := coverageProjectManifestNames[base]; !ok {
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
		name, err := coverageReadGoModuleName(filepath.Join(gitRoot, filepath.FromSlash(trackedPath)))
		if err != nil {
			hlog.Warn("failed to read module name", "file", trackedPath, "err", err)
			continue
		}
		if name != "" {
			modules = append(modules, coverageGoModule{rel: rel, name: name})
		}
	}

	// A report directory that belongs to a tracked module is authoritative.
	// The working directory is only a fallback when no report hint identifies
	// a module.
	var reportRels []string
	for _, hint := range opts.HintDirs {
		if rel, ok := coveragePathRelativeToRoot(hint, gitRoot); ok {
			if moduleRel, found := coverageNearestManifestDir(rel, manifestDirs); found {
				reportRels = coverageAppendUnique(reportRels, moduleRel)
			}
		}
	}
	if len(reportRels) > 0 {
		return modules, reportRels
	}
	if wd, err := os.Getwd(); err == nil {
		if rel, ok := coverageRepositoryRelative(wd, gitRoot); ok {
			if moduleRel, found := coverageNearestManifestDir(rel, manifestDirs); found {
				return modules, []string{moduleRel}
			}
		}
	}
	return modules, nil
}

func coveragePathRelativeToRoot(candidate, gitRoot string) (string, bool) {
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
	return coverageRepositoryRelative(candidate, gitRoot)
}

func coverageNearestManifestDir(rel string, manifests map[string]struct{}) (string, bool) {
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

func coverageAppendUnique(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func coverageReadGoModuleName(modPath string) (string, error) {
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

func coverageModulePath(module coverageGoModule, importPath string) string {
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

func coverageResolvedDir(dir string) string {
	if dir == "" {
		return ""
	}
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		return resolved
	}
	return dir
}

// coverageRepositoryRelative reports whether dir is inside gitRoot. An empty
// rel means dir is the repository root.
func coverageRepositoryRelative(dir, gitRoot string) (string, bool) {
	if gitRoot == "" {
		return "", false
	}
	dir = coverageResolvedDir(dir)
	gitRoot = coverageResolvedDir(gitRoot)
	rel, err := filepath.Rel(gitRoot, dir)
	if err != nil || coverageHasParentTraversal(filepath.ToSlash(rel)) {
		return "", false
	}
	if rel == "." {
		return "", true
	}
	return filepath.ToSlash(rel), true
}

func coverageRelToGitRoot(raw, gitRoot string) string {
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
	if err != nil || rel == "." || rel == "" || coverageHasParentTraversal(filepath.ToSlash(rel)) {
		return ""
	}
	return filepath.ToSlash(rel)
}

func coverageHasParentTraversal(filePath string) bool {
	for _, segment := range strings.Split(filepath.ToSlash(filePath), "/") {
		if segment == ".." {
			return true
		}
	}
	return false
}

func coverageNormalizeRepoPath(raw string) string {
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
