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
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"time"

	"golang.org/x/mod/semver"
	"golang.org/x/term"

	"github.com/harness/cli/pkg/hbase"
	"github.com/harness/cli/pkg/hlog"
)

// ErrReleaseNotFound is returned by FetchReleaseByTag when the GitHub API
// responds 404 — the tag exists in git but has no release, or does not exist.
var ErrReleaseNotFound = errors.New("release not found")

// Asset is a single downloadable file attached to a GitHub release.
//
// BrowserDownloadURL works unauthenticated, but only for a published (non-draft)
// release on a public repo. URL is the GitHub API asset endpoint, which — with
// an Authorization header and Accept: application/octet-stream — also serves
// drafts and private-repo assets; it's what a --github-token download must use
// instead.
type Asset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
	URL                string `json:"url"`
}

// Release is the subset of the GitHub release API response the CLI resolves
// installs against: the tag it was published under and its downloadable
// assets.
type Release struct {
	TagName    string  `json:"tag_name"`
	Draft      bool    `json:"draft"`
	Prerelease bool    `json:"prerelease"`
	Assets     []Asset `json:"assets"`
}

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
	upgradeCmd := "harness install cli"
	if _, ok := hbase.BrewManagedBinary(); ok {
		upgradeCmd = "brew upgrade --cask " + hbase.BrewCaskName
	}
	fmt.Fprintf(os.Stderr, "\nA new version of the Harness CLI is available: %s → %s\nRun: %s\n\n", currentVersion, c.LatestVersion, upgradeCmd)
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

func updateCheckCachePath() string {
	return filepath.Join(hbase.GetHarnessHomeDir(), cacheFile)
}

func readCache() cache {
	data, err := os.ReadFile(updateCheckCachePath())
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
	path := updateCheckCachePath()
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
	rel, err := fetchRelease(fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", Repo), "")
	if err != nil {
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

// ResolveRelease finds the release in repo matching prefix and version, and
// returns it with its asset list attached.
//
// version, when non-empty, must already be a normalized "vX.Y.Z" string — the
// caller validates user input before this is reached, since that's a CLI/UX
// concern, not a GitHub-API-mechanics one. version == "" resolves to the
// latest release for prefix.
//
// prefix == "" means the plain "vX.Y.Z" tag convention core itself releases
// under: latest resolves via GitHub's own "latest" marker, since only core
// releases are ever marked latest. prefix != "" means the "{prefix}/vX.Y.Z"
// convention every module releases under: GitHub's "latest" marker doesn't
// apply, so latest is resolved by listing releases and picking the
// highest-semver tag matching the prefix.
//
// token, when non-empty, is sent as a bearer token on every GitHub API call —
// this is how a caller can see draft releases or releases in a private repo,
// neither of which an unauthenticated request can see.
//
// allowDrafts includes draft releases when resolving "latest" for a prefixed
// (module/plugin) release — only meaningful with a token, since an
// unauthenticated request never sees a draft's assets to begin with. An
// explicit --version tag lookup always returns a draft when it matches,
// token or not; allowDrafts only affects the "list and pick highest semver"
// path below, where a draft would otherwise be filtered out alongside
// prereleases.
func ResolveRelease(repo, prefix, version, token string, allowDrafts bool) (*Release, error) {
	if version != "" {
		tag := version
		if prefix != "" {
			tag = prefix + "/" + version
		}
		rel, err := fetchRelease(fmt.Sprintf("https://api.github.com/repos/%s/releases/tags/%s", repo, url.PathEscape(tag)), token)
		if errors.Is(err, ErrReleaseNotFound) {
			return nil, fmt.Errorf("no release tagged %s in %s", tag, repo)
		}
		return rel, err
	}
	if prefix == "" {
		return fetchRelease(fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", repo), token)
	}
	return latestPrefixedRelease(repo, prefix, token, allowDrafts)
}

// latestPrefixedRelease lists the most recent releases in repo and returns the
// highest-semver one whose tag matches "{prefix}/vX.Y.Z", skipping drafts
// (unless allowDrafts) and prereleases. GitHub's "latest" marker only ever
// points at a bare-tag core release, so a prefixed release is found by
// listing rather than by that endpoint.
func latestPrefixedRelease(repo, prefix, token string, allowDrafts bool) (*Release, error) {
	releases, err := fetchReleaseList(fmt.Sprintf("https://api.github.com/repos/%s/releases?per_page=100", repo), token)
	if err != nil {
		return nil, err
	}
	tagVersion := regexp.MustCompile(`^` + regexp.QuoteMeta(prefix) + `/(v\d+\.\d+\.\d+)$`)

	var best *Release
	var bestVersion string
	for i := range releases {
		rel := &releases[i]
		if (rel.Draft && !allowDrafts) || rel.Prerelease {
			continue
		}
		m := tagVersion.FindStringSubmatch(rel.TagName)
		if m == nil {
			continue
		}
		if best == nil || semver.Compare(m[1], bestVersion) > 0 {
			best = rel
			bestVersion = m[1]
		}
	}
	if best == nil {
		return nil, fmt.Errorf("no release tagged %s/vX.Y.Z found in %s (checked the most recent %d releases)", prefix, repo, len(releases))
	}
	return best, nil
}

// fetchRelease GETs a single-release GitHub API endpoint and decodes it. token,
// when non-empty, is sent as a bearer token so drafts and private-repo
// releases the caller has access to are visible.
func fetchRelease(apiURL, token string) (*Release, error) {
	body, err := getGitHubAPI(apiURL, token)
	if err != nil {
		return nil, err
	}
	var rel Release
	if err := json.Unmarshal(body, &rel); err != nil {
		return nil, err
	}
	return &rel, nil
}

// fetchReleaseList GETs a release-list GitHub API endpoint and decodes it.
func fetchReleaseList(apiURL, token string) ([]Release, error) {
	body, err := getGitHubAPI(apiURL, token)
	if err != nil {
		return nil, err
	}
	var releases []Release
	if err := json.Unmarshal(body, &releases); err != nil {
		return nil, err
	}
	return releases, nil
}

// getGitHubAPI issues an authenticated-format GET against the GitHub API and
// returns the raw response body, translating a 404 into ErrReleaseNotFound.
func getGitHubAPI(apiURL, token string) ([]byte, error) {
	client := &http.Client{Timeout: httpTimeout}
	hlog.Debug("GET", "url", apiURL)
	req, err := http.NewRequest("GET", apiURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github.v3+json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := client.Do(req)
	if err != nil {
		hlog.Debug("GET failed", "url", apiURL, "error", err)
		return nil, err
	}
	defer resp.Body.Close()
	hlog.Debug("GET response", "url", apiURL, "status", resp.StatusCode)
	if resp.StatusCode == http.StatusNotFound {
		return nil, ErrReleaseNotFound
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub API returned status %d", resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}
