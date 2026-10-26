// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package errors

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveErrorsCacheDir(t *testing.T) {
	t.Setenv(errorsCacheDirEnvVar, "")
	if got := resolveErrorsCacheDir(""); got != defaultErrorsCacheDir {
		t.Fatalf("got %q, want default %q", got, defaultErrorsCacheDir)
	}

	t.Setenv(errorsCacheDirEnvVar, "/tmp/from-env")
	if got := resolveErrorsCacheDir(""); got != "/tmp/from-env" {
		t.Fatalf("got %q, want env override", got)
	}

	if got := resolveErrorsCacheDir("/explicit"); got != "/explicit" {
		t.Fatalf("got %q, want explicit arg to win over env", got)
	}
}

func TestComputeErrorsCacheKeyIsStableAndPathSensitive(t *testing.T) {
	a := computeErrorsCacheKey("/a/errors.yaml")
	b := computeErrorsCacheKey("/a/errors.yaml")
	c := computeErrorsCacheKey("/b/errors.yaml")
	if a != b {
		t.Fatalf("same path must hash the same, got %q vs %q", a, b)
	}
	if a == c {
		t.Fatalf("different paths must not collide")
	}
}

func TestGetOrParseErrorRulesCacheHitAvoidsReparse(t *testing.T) {
	cacheDir := t.TempDir()
	path := writeYAML(t, `version: "1.0"
ruleGroups:
  - name: "Test"
    conditionExpression:
      key: "standardOutput"
      operand: "contains"
      value: "x"
    actions:
      - type: "setErrorCategory"
        value: "X"
`)

	config1, err := getOrParseErrorRules(path, cacheDir)
	if err != nil {
		t.Fatalf("first call: %v", err)
	}

	// Mutate the file after the first (cold) parse. A path-keyed cache must
	// still serve the stale cached config on the second call.
	if err := os.WriteFile(path, []byte("not even yaml: ["), 0o644); err != nil {
		t.Fatalf("mutate: %v", err)
	}

	config2, err := getOrParseErrorRules(path, cacheDir)
	if err != nil {
		t.Fatalf("second call should hit cache despite the file now being broken: %v", err)
	}
	if config2.RuleGroups[0].Name != config1.RuleGroups[0].Name {
		t.Fatalf("second call did not return the cached config: %+v", config2)
	}
}

func TestGetOrParseErrorRulesCachesParseFailure(t *testing.T) {
	cacheDir := t.TempDir()
	path := writeYAML(t, `not valid yaml: [`)

	_, err1 := getOrParseErrorRules(path, cacheDir)
	if err1 == nil {
		t.Fatal("expected a parse error on first call")
	}

	_, err2 := getOrParseErrorRules(path, cacheDir)
	if err2 == nil || !strings.Contains(err2.Error(), "cached parse error") {
		t.Fatalf("got %v, want the cached-failure path replayed", err2)
	}
}

func TestLoadErrorsFromCacheMiss(t *testing.T) {
	cacheDir := t.TempDir()
	got, err := loadErrorsFromCache(cacheDir, "nonexistent")
	if err != nil || got != nil {
		t.Fatalf("got (%v, %v), want (nil, nil) on a cache miss", got, err)
	}
}

func TestLoadErrorsFromCacheCorruptFileIsDeleted(t *testing.T) {
	cacheDir := t.TempDir()
	cachePath := errorsCachePath(cacheDir, "k")
	if err := os.WriteFile(cachePath, []byte("not gob data"), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}

	if _, err := loadErrorsFromCache(cacheDir, "k"); err == nil {
		t.Fatal("expected a decode error for a corrupt cache file")
	}
	if _, statErr := os.Stat(cachePath); !os.IsNotExist(statErr) {
		t.Fatalf("corrupt cache file should have been deleted, stat err: %v", statErr)
	}
}

func TestGetOrParseErrorRulesMkdirFailureFallsBackToUncached(t *testing.T) {
	// Use a cache dir path that can never be created: a regular file as a
	// path component.
	blocker := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	badCacheDir := filepath.Join(blocker, "cache")

	path := writeYAML(t, `version: "1.0"
ruleGroups:
  - name: "Test"
    conditionExpression:
      key: "standardOutput"
      operand: "contains"
      value: "x"
    actions:
      - type: "setErrorCategory"
        value: "X"
`)

	config, err := getOrParseErrorRules(path, badCacheDir)
	if err != nil {
		t.Fatalf("expected a graceful fallback to uncached parsing, got error: %v", err)
	}
	if config.RuleGroups[0].Name != "Test" {
		t.Fatalf("unexpected config: %+v", config)
	}
}
