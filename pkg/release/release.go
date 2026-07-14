// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

// Package release manages GitHub release interactions and background update notifications
// for the Harness CLI.
//
// The main process calls MaybeSpawn to potentially launch a detached subprocess, then
// calls NagIfDue to print a notice from the cache. The subprocess is launched as
// "harness --background-update-check" and calls RunBackgroundCheck to do the fetch.
package release

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"golang.org/x/mod/semver"
	"golang.org/x/term"

	"github.com/harness/cli/pkg/hbase"
	"github.com/harness/cli/pkg/hlog"
)

const (
	// FlagName is the hidden flag that triggers the background subprocess behavior.
	FlagName = "--background-update-check"

	cacheFile = "update-check.json"
	// Repo is the GitHub repo for Harness CLI releases.
	Repo          = "harness/cli"
	spawnInterval = 24 * time.Hour
	checkInterval = 24 * time.Hour
	nagInterval   = 24 * time.Hour
	httpTimeout   = 10 * time.Second
)

// cache is the on-disk cache written to ~/.harness/update-check.json.
type cache struct {
	LastSpawn     time.Time `json:"last_spawn"`
	LastChecked   time.Time `json:"last_checked"`
	LatestVersion string    `json:"latest_version,omitempty"`
	LastNotified  time.Time `json:"last_notified"`
}

// MaybeSpawn checks gating conditions and, when appropriate, writes last_spawn
// and launches a detached "harness --background-update-check" subprocess.
// It is a no-op (never errors) — update checking must never break normal commands.
func MaybeSpawn() {
	if !shouldUpdateCheck() {
		return
	}
	c := readCache()
	now := time.Now().UTC()

	if !c.LastChecked.IsZero() && now.Sub(c.LastChecked) < checkInterval {
		hlog.Debug("update check skipped", "reason", "cache fresh", "last_checked", c.LastChecked)
		return
	}
	if !c.LastSpawn.IsZero() && now.Sub(c.LastSpawn) < spawnInterval {
		hlog.Debug("update check skipped", "reason", "spawned recently", "last_spawn", c.LastSpawn)
		return
	}

	// Write last_spawn before spawning so concurrent invocations skip.
	// If the write fails (e.g. read-only filesystem), don't spawn.
	c.LastSpawn = now
	if err := writeCache(c); err != nil {
		hlog.Debug("update check skipped", "reason", "cache write failed", "error", err)
		return
	}

	exe, err := os.Executable()
	if err != nil {
		return
	}
	hlog.Debug("spawning background update check", "exe", exe)
	cmd := exec.Command(exe, FlagName)
	cmd.Stdin = nil
	cmd.Stdout = nil
	cmd.Stderr = nil
	detach(cmd) // platform-specific: sets SysProcAttr to detach from the parent process group
	_ = cmd.Start()
}

// RunBackgroundCheck is the subprocess entry point. It fetches the latest version,
// updates the cache, and always exits 0.
func RunBackgroundCheck() {
	hlog.Debug("background update check starting")
	latest, err := FetchLatestVersion()
	if err != nil {
		hlog.Debug("background update check fetch failed", "error", err)
		return
	}
	hlog.Debug("background update check fetched", "latest", latest)
	c := readCache()
	c.LastChecked = time.Now().UTC()
	c.LatestVersion = latest
	if err := writeCache(c); err != nil {
		hlog.Debug("background update check cache write failed", "error", err)
		return
	}
	hlog.Debug("background update check cache updated")
}

// NagIfDue prints an update notice to stderr if a newer version is known and the
// nag interval has elapsed. It reads from the cache only — no network call.
func NagIfDue(currentVersion string) {
	if !shouldUpdateCheck() {
		return
	}
	c := readCache()
	if c.LatestVersion == "" {
		return
	}
	cur := "v" + currentVersion
	lat := c.LatestVersion
	if !semver.IsValid(cur) || !semver.IsValid(lat) {
		return
	}
	if semver.Compare(lat, cur) <= 0 {
		return // not newer
	}
	now := time.Now().UTC()
	if !c.LastNotified.IsZero() && now.Sub(c.LastNotified) < nagInterval {
		return // nagged recently
	}
	c.LastNotified = now
	if err := writeCache(c); err != nil {
		return
	}
	fmt.Fprintf(os.Stderr, "\nA new version of the Harness CLI is available: %s → %s\nRun: harness install cli\n\n", currentVersion, c.LatestVersion)
}

// shouldUpdateCheck returns false for all gating conditions that mean we skip entirely.
func shouldUpdateCheck() bool {
	if !term.IsTerminal(int(os.Stdout.Fd())) {
		return false
	}
	if hbase.IsPipelineExecution() {
		return false
	}
	if os.Getenv(hbase.EnvNoUpdateCheck) == "1" {
		return false
	}
	if isCompletionInvocation() {
		return false
	}
	return true
}

func isCompletionInvocation() bool {
	for _, arg := range os.Args[1:] {
		if arg == "__complete" || arg == "__completeNoDesc" {
			return true
		}
	}
	return false
}

func cachePath() string {
	return hbase.ExpandHomeDir(filepath.Join(hbase.HarnessHome, cacheFile))
}

func readCache() cache {
	data, err := os.ReadFile(cachePath())
	if err != nil {
		return cache{}
	}
	var c cache
	if err := json.Unmarshal(data, &c); err != nil {
		return cache{}
	}
	return c
}

func writeCache(c cache) error {
	path := cachePath()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
}

// FetchLatestVersion calls the GitHub releases API and returns the latest version tag (e.g. "v1.2.3").
func FetchLatestVersion() (string, error) {
	client := &http.Client{Timeout: httpTimeout}
	url := fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", Repo)
	hlog.Debug("GET", "url", url)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github.v3+json")
	resp, err := client.Do(req)
	if err != nil {
		hlog.Debug("GET failed", "url", url, "error", err)
		return "", err
	}
	defer resp.Body.Close()
	hlog.Debug("GET response", "url", url, "status", resp.StatusCode)
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GitHub API returned status %d", resp.StatusCode)
	}
	var rel struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return "", err
	}
	if rel.TagName == "" {
		return "", fmt.Errorf("empty tag_name in response")
	}
	if !semver.IsValid(rel.TagName) {
		return "", fmt.Errorf("invalid version %q from API", rel.TagName)
	}
	return rel.TagName, nil
}
