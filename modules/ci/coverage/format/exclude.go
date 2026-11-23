// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package format

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/bmatcuk/doublestar/v4"

	"github.com/harness/cli/v3/pkg/hlog"
)

// ExcludePattern is a compiled exclude pattern (glob or regex).
type ExcludePattern struct {
	raw   string
	regex *regexp.Regexp // non-nil when pattern uses "regex:" prefix
}

// compileExcludePatterns compiles a list of exclude patterns.
// Patterns prefixed with "regex:" are treated as regular expressions;
// all others are treated as doublestar glob patterns.
func compileExcludePatterns(patterns []string) ([]ExcludePattern, error) {
	compiled := make([]ExcludePattern, 0, len(patterns))
	for _, p := range patterns {
		if rest, ok := strings.CutPrefix(p, "regex:"); ok {
			re, err := regexp.Compile(rest)
			if err != nil {
				return nil, fmt.Errorf("invalid regex exclude pattern %q: %w", p, err)
			}
			compiled = append(compiled, ExcludePattern{raw: p, regex: re})
		} else {
			if !doublestar.ValidatePattern(p) {
				return nil, fmt.Errorf("invalid glob exclude pattern %q", p)
			}
			compiled = append(compiled, ExcludePattern{raw: p})
		}
	}
	return compiled, nil
}

// matchesAnyExcludePattern reports whether path matches any of the compiled patterns.
func matchesAnyExcludePattern(path string, patterns []ExcludePattern) bool {
	for _, p := range patterns {
		if p.regex != nil {
			if p.regex.MatchString(path) {
				return true
			}
		} else {
			if matched, _ := doublestar.Match(p.raw, path); matched {
				return true
			}
		}
	}
	return false
}

func logExcludePatterns(patterns []ExcludePattern) {
	for _, p := range patterns {
		hlog.Debug("excluding files matching pattern", "pattern", p.raw)
	}
}

// logExcludedFileOnce prints a debug line for each unique source file
// that was actually dropped by exclude matching.
func logExcludedFileOnce(seen map[string]struct{}, path string) {
	if seen != nil {
		if _, ok := seen[path]; ok {
			return
		}
		seen[path] = struct{}{}
	}
	hlog.Debug("excluding file", "path", path)
}

// parseExcludeText splits coverage_repo_settings exclude_text into
// patterns, dropping blank lines and "#"-comment lines.
func parseExcludeText(text string) []string {
	rawLines := strings.Split(text, "\n")
	patterns := make([]string, 0, len(rawLines))
	for _, rawLine := range rawLines {
		pattern := strings.TrimSpace(rawLine)
		if pattern == "" || strings.HasPrefix(pattern, "#") {
			continue
		}
		patterns = append(patterns, pattern)
	}
	return patterns
}

// CompileCLIExcludePatterns compiles --exclude / settings patterns.
// An empty list is a no-op.
func CompileCLIExcludePatterns(patterns []string) ([]ExcludePattern, error) {
	if len(patterns) == 0 {
		return nil, nil
	}
	compiled, err := compileExcludePatterns(patterns)
	if err != nil {
		return nil, fmt.Errorf("invalid --exclude: %w", err)
	}
	return compiled, nil
}

// PatternsFromSettings returns compiled repository exclude patterns
// when configured. nil excludeText means settings were never saved, so
// cliPatterns still applies. A present value — including "" or comment-only
// text — replaces cliPatterns: coverage-server clears excludes by storing an
// empty string. Invalid repository patterns fail the upload rather than
// silently changing which source files are included.
func PatternsFromSettings(excludeText *string, cliPatterns []string) ([]ExcludePattern, error) {
	if excludeText == nil {
		logUsingCLIExclude(cliPatterns, "coverage repository settings are not configured")
		return CompileCLIExcludePatterns(cliPatterns)
	}
	patterns := parseExcludeText(*excludeText)
	if len(patterns) == 0 {
		logUsingServerExclude(cliPatterns, 0)
		return nil, nil
	}
	compiled, err := compileExcludePatterns(patterns)
	if err != nil {
		return nil, fmt.Errorf("invalid coverage repository exclude settings: %w", err)
	}
	logUsingServerExclude(cliPatterns, len(compiled))
	return compiled, nil
}

func logUsingCLIExclude(cliPatterns []string, reason string) {
	if len(cliPatterns) == 0 {
		return
	}
	hlog.Info("using --exclude", "reason", reason)
}

func logUsingServerExclude(cliPatterns []string, count int) {
	hlog.Info("using exclude pattern(s) from coverage repository settings", "count", count)
	if len(cliPatterns) > 0 {
		if count == 0 {
			hlog.Warn("ignoring --exclude; coverage repository settings explicitly clear all exclusions")
			return
		}
		hlog.Info("ignoring --exclude; coverage repository settings are configured")
	}
}
