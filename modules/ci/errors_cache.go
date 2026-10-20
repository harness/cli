// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package ci

import (
	"crypto/sha256"
	"encoding/gob"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
)

const (
	defaultErrorsCacheDir = "/tmp/.harness-errors-cache"
	errorsCacheDirEnvVar  = "HARNESS_ERRORS_CACHE_DIR"

	// errorsCacheDirPerm is 0777 for cross-container/cross-user access: lite-engine
	// and the step process that writes this cache may run as different users.
	errorsCacheDirPerm = 0o777
)

// errorsCachedResult wraps both success and failure cases so a prior parse
// failure can be replayed without re-parsing a still-broken errors.yaml.
type errorsCachedResult struct {
	Config *errorRulesConfig
	Error  string
}

func resolveErrorsCacheDir(explicit string) string {
	if explicit != "" {
		return explicit
	}
	if dir := os.Getenv(errorsCacheDirEnvVar); dir != "" {
		return dir
	}
	return defaultErrorsCacheDir
}

// computeErrorsCacheKey hashes the yaml path, not its content: one errors.yaml
// is parsed once per pipeline stage and reused by every step in that stage, so
// the path alone is a sufficient and stable cache key for that lifetime.
func computeErrorsCacheKey(yamlPath string) string {
	hash := sha256.Sum256([]byte(yamlPath))
	return hex.EncodeToString(hash[:])
}

func errorsCachePath(cacheDir, key string) string {
	return filepath.Join(cacheDir, key+".bin")
}

// loadErrorsFromCache returns nil, nil on a cache miss (not an error). A
// corrupt cache file is deleted and also treated as a miss.
func loadErrorsFromCache(cacheDir, key string) (*errorsCachedResult, error) {
	cachePath := errorsCachePath(cacheDir, key)

	if _, err := os.Stat(cachePath); os.IsNotExist(err) {
		return nil, nil
	}

	file, err := os.Open(cachePath)
	if err != nil {
		return nil, fmt.Errorf("failed to open cache file: %w", err)
	}
	defer file.Close()

	var result errorsCachedResult
	if err := gob.NewDecoder(file).Decode(&result); err != nil {
		file.Close()
		os.Remove(cachePath)
		return nil, fmt.Errorf("cache decode failed (deleted): %w", err)
	}
	return &result, nil
}

func saveErrorsToCache(cacheDir, key string, result *errorsCachedResult) error {
	cachePath := errorsCachePath(cacheDir, key)

	file, err := os.Create(cachePath)
	if err != nil {
		return fmt.Errorf("failed to create cache file: %w", err)
	}
	defer file.Close()

	if err := gob.NewEncoder(file).Encode(result); err != nil {
		file.Close()
		os.Remove(cachePath)
		return fmt.Errorf("failed to encode to cache: %w", err)
	}
	return nil
}

func parseErrorsYAMLWithoutCache(yamlPath string) (*errorRulesConfig, error) {
	config, err := parseErrorsYAML(yamlPath)
	if err != nil {
		return nil, fmt.Errorf("failed to parse YAML: %w", err)
	}
	return config, nil
}

// getOrParseErrorRules is the cached entry point used by `execute error:evaluate`:
// resolve the cache dir, hash the path, replay a cache hit (success or a
// previously cached failure), or parse fresh and cache the outcome either way.
// Any cache-layer failure (mkdir, decode, encode) degrades to parsing without a
// cache rather than failing the call — evaluate must never hard-fail a step
// just because its cache directory is unwritable.
func getOrParseErrorRules(yamlPath, explicitCacheDir string) (*errorRulesConfig, error) {
	cacheDir := resolveErrorsCacheDir(explicitCacheDir)

	if err := os.MkdirAll(cacheDir, errorsCacheDirPerm); err != nil {
		fmt.Fprintf(os.Stderr, "cache disabled (mkdir failed): %v\n", err)
		return parseErrorsYAMLWithoutCache(yamlPath)
	}

	key := computeErrorsCacheKey(yamlPath)

	cached, err := loadErrorsFromCache(cacheDir, key)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cache load failed (will re-parse): %v\n", err)
	}
	if cached != nil {
		if cached.Error != "" {
			return nil, fmt.Errorf("cached parse error: %s", cached.Error)
		}
		return cached.Config, nil
	}

	config, err := parseErrorsYAML(yamlPath)
	if err != nil {
		parseErr := fmt.Errorf("failed to parse YAML: %w", err)
		if cacheErr := saveErrorsToCache(cacheDir, key, &errorsCachedResult{Error: parseErr.Error()}); cacheErr != nil {
			fmt.Fprintf(os.Stderr, "cache save failed: %v\n", cacheErr)
		}
		return nil, parseErr
	}

	if err := saveErrorsToCache(cacheDir, key, &errorsCachedResult{Config: config}); err != nil {
		fmt.Fprintf(os.Stderr, "cache save failed (continuing without cache): %v\n", err)
	}
	return config, nil
}
