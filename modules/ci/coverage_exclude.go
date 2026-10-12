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
