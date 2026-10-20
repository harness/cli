// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package ci

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/bmatcuk/doublestar/v4"

	"github.com/harness/cli/v3/pkg/hlog"
)

// coverageExcludePattern is a compiled exclude pattern (glob or regex).
type coverageExcludePattern struct {
	raw   string
	regex *regexp.Regexp // non-nil when pattern uses "regex:" prefix
}

// coverageCompileExcludePatterns compiles a list of exclude patterns.
// Patterns prefixed with "regex:" are treated as regular expressions;
// all others are treated as doublestar glob patterns.
func coverageCompileExcludePatterns(patterns []string) ([]coverageExcludePattern, error) {
	compiled := make([]coverageExcludePattern, 0, len(patterns))
	for _, p := range patterns {
		if rest, ok := strings.CutPrefix(p, "regex:"); ok {
			re, err := regexp.Compile(rest)
			if err != nil {
				return nil, fmt.Errorf("invalid regex exclude pattern %q: %w", p, err)
			}
			compiled = append(compiled, coverageExcludePattern{raw: p, regex: re})
		} else {
			if !doublestar.ValidatePattern(p) {
				return nil, fmt.Errorf("invalid glob exclude pattern %q", p)
			}
			compiled = append(compiled, coverageExcludePattern{raw: p})
		}
	}
	return compiled, nil
}

// coverageMatchesAnyExcludePattern reports whether path matches any of the compiled patterns.
func coverageMatchesAnyExcludePattern(path string, patterns []coverageExcludePattern) bool {
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

func coverageLogExcludePatterns(patterns []coverageExcludePattern) {
	for _, p := range patterns {
		hlog.Debug("excluding files matching pattern", "pattern", p.raw)
	}
}

// coverageLogExcludedFileOnce prints a debug line for each unique source file
// that was actually dropped by exclude matching.
func coverageLogExcludedFileOnce(seen map[string]struct{}, path string) {
	if seen != nil {
		if _, ok := seen[path]; ok {
			return
		}
		seen[path] = struct{}{}
	}
	hlog.Debug("excluding file", "path", path)
}

// coverageParseExcludeText splits coverage_repo_settings exclude_text into
// patterns, dropping blank lines and "#"-comment lines.
func coverageParseExcludeText(text string) []string {
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

// coverageCompileCLIExcludePatterns compiles --exclude / settings patterns.
// An empty list is a no-op.
func coverageCompileCLIExcludePatterns(patterns []string) ([]coverageExcludePattern, error) {
	if len(patterns) == 0 {
		return nil, nil
	}
	compiled, err := coverageCompileExcludePatterns(patterns)
	if err != nil {
		return nil, fmt.Errorf("invalid --exclude: %w", err)
	}
	return compiled, nil
}

// coveragePatternsFromSettings returns compiled repository exclude patterns
// when configured. nil excludeText means settings were never saved, so
// cliPatterns still applies. A present value — including "" or comment-only
// text — replaces cliPatterns: coverage-server clears excludes by storing an
// empty string. Invalid repository patterns fail the upload rather than
// silently changing which source files are included.
func coveragePatternsFromSettings(excludeText *string, cliPatterns []string) ([]coverageExcludePattern, error) {
	if excludeText == nil {
		coverageLogUsingCLIExclude(cliPatterns, "coverage repository settings are not configured")
		return coverageCompileCLIExcludePatterns(cliPatterns)
	}
	patterns := coverageParseExcludeText(*excludeText)
	if len(patterns) == 0 {
		coverageLogUsingServerExclude(cliPatterns, 0)
		return nil, nil
	}
	compiled, err := coverageCompileExcludePatterns(patterns)
	if err != nil {
		return nil, fmt.Errorf("invalid coverage repository exclude settings: %w", err)
	}
	coverageLogUsingServerExclude(cliPatterns, len(compiled))
	return compiled, nil
}

func coverageLogUsingCLIExclude(cliPatterns []string, reason string) {
	if len(cliPatterns) == 0 {
		return
	}
	hlog.Info("using --exclude", "reason", reason)
}

func coverageLogUsingServerExclude(cliPatterns []string, count int) {
	hlog.Info("using exclude pattern(s) from coverage repository settings", "count", count)
	if len(cliPatterns) > 0 {
		if count == 0 {
			hlog.Warn("ignoring --exclude; coverage repository settings explicitly clear all exclusions")
			return
		}
		hlog.Info("ignoring --exclude; coverage repository settings are configured")
	}
}
