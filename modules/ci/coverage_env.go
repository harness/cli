// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package ci

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

//go:embed coverage_vendors.json
var coverageVendorsJSON []byte

var coverageVendors []coverageVendor

func init() {
	if err := json.Unmarshal(coverageVendorsJSON, &coverageVendors); err != nil {
		panic(err)
	}
}

var coverageCommonEnv = []string{
	"CI",
	"CONTINUOUS_INTEGRATION",
	"BUILD_NUMBER",
	"RUN_ID",
}

type coverageVariables struct {
	AccountID     string   `json:"account_id,omitempty"`
	OrgID         string   `json:"org_id,omitempty"`
	ProjectID     string   `json:"project_id,omitempty"`
	SCM           string   `json:"scm,omitempty"`
	BaseURL       string   `json:"base_url,omitempty"`
	Owner         string   `json:"owner,omitempty"`
	Identifier    string   `json:"identifier,omitempty"`
	Commit        []string `json:"commit,omitempty"`
	DefaultBranch []string `json:"default_branch,omitempty"`
	Branch        []string `json:"branch,omitempty"`
	Base          []string `json:"base,omitempty"`
	CommitBefore  string   `json:"commit_before,omitempty"`
	CommitAfter   string   `json:"commit_after,omitempty"`
	Build         string   `json:"build,omitempty"`
	Pipeline      string   `json:"pipeline,omitempty"`
	Execution     string   `json:"execution,omitempty"`
	PRID          []string `json:"pr_id,omitempty"`
}

type coverageVendor struct {
	Name     string `json:"name,omitempty"`
	Constant string `json:"constant,omitempty"`
	Env      any    `json:"env,omitempty"`
	PR       any    `json:"pr,omitempty"`
	Val      string `json:"val,omitempty"`
	coverageVariables
}

func (v coverageVendor) String() string {
	return fmt.Sprintf("Name:  %s\nConst: %s\nEnv:   %v\nVal:   %v", v.Name, v.Constant, v.Env, v.Val)
}

func (v coverageVendor) IsCI() bool {
	return v.Val != ""
}

func (v coverageVendor) IsPR() bool {
	return v.IsPRBuild()
}

func (v coverageVendor) AccountID() (string, error) {
	accountID := os.Getenv(v.coverageVariables.AccountID)
	if accountID == "" {
		return "", fmt.Errorf("no account ID found")
	}
	return accountID, nil
}

func (v coverageVendor) OrgID() string {
	return os.Getenv(v.coverageVariables.OrgID)
}

func (v coverageVendor) ProjectID() string {
	return os.Getenv(v.coverageVariables.ProjectID)
}

func (v coverageVendor) SCM() (string, error) {
	if scm := os.Getenv(v.coverageVariables.SCM); scm != "" {
		return scm, nil
	}
	repoInfo, err := coverageGetRepoInfo()
	if err != nil {
		return "", err
	}
	return repoInfo.Provider, nil
}

func (v coverageVendor) Owner() (string, error) {
	if owner := os.Getenv(v.coverageVariables.Owner); owner != "" {
		return owner, nil
	}
	repoInfo, err := coverageGetRepoInfo()
	if err != nil {
		return "", err
	}
	return repoInfo.Owner, nil
}

func (v coverageVendor) Identifier() (string, error) {
	if identifier := os.Getenv(v.coverageVariables.Identifier); identifier != "" {
		return identifier, nil
	}
	repoInfo, err := coverageGetRepoInfo()
	if err != nil {
		return "", err
	}
	return repoInfo.Name, nil
}

func (v coverageVendor) Commit() (string, error) {
	for _, envVar := range v.coverageVariables.Commit {
		if commit := os.Getenv(envVar); commit != "" {
			return commit, nil
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	out, err := exec.CommandContext(ctx, "git", "rev-parse", "HEAD").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func (v coverageVendor) DefaultBranch() (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	out, err := exec.CommandContext(ctx, "git", "symbolic-ref", "refs/remotes/origin/HEAD").Output()
	if err != nil {
		return "", err
	}
	branch := strings.TrimSpace(string(out))
	branch = strings.TrimPrefix(branch, "refs/remotes/origin/")
	return branch, nil
}

func (v coverageVendor) Branch() (string, error) {
	for _, envVar := range v.coverageVariables.Branch {
		if branch := os.Getenv(envVar); branch != "" {
			return branch, nil
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	out, err := exec.CommandContext(ctx, "git", "rev-parse", "--abbrev-ref", "HEAD").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func (v coverageVendor) Base() (string, error) {
	for _, envVar := range v.coverageVariables.Base {
		if val := os.Getenv(envVar); val != "" {
			return val, nil
		}
	}
	return "", nil
}

func (v coverageVendor) CommitBefore() string {
	return os.Getenv(v.coverageVariables.CommitBefore)
}

func (v coverageVendor) CommitAfter() string {
	return os.Getenv(v.coverageVariables.CommitAfter)
}

// IsValidCommitBefore reports whether CommitBefore holds a usable SHA: not empty
// and not all zeros. Harness CI PR pipelines using the source-branch strategy
// (rather than merge-commit) can leave DRONE_COMMIT_BEFORE empty or all-zero.
func (v coverageVendor) IsValidCommitBefore() bool {
	commitBefore := v.CommitBefore()
	if commitBefore == "" {
		return false
	}
	for _, c := range commitBefore {
		if c != '0' {
			return true
		}
	}
	return false
}

// IsPRBuild evaluates the vendor's PR field, which may be a bare env-var name,
// an {"env":..., "ne":...} not-equal check, an {"any":[...]} first-match list,
// or a map of {envVar: expectedValue} pairs.
func (v coverageVendor) IsPRBuild() bool {
	if v.PR == nil {
		return false
	}

	switch pr := v.PR.(type) {
	case string:
		return os.Getenv(pr) != ""
	case map[string]any:
		if env, ok := pr["env"].(string); ok {
			val := os.Getenv(env)
			if val == "" {
				return false
			}
			if ne, ok := pr["ne"].(string); ok {
				return val != ne
			}
			return true
		}
		if anyVars, ok := pr["any"].([]any); ok {
			for _, v := range anyVars {
				if envVar, ok := v.(string); ok {
					if os.Getenv(envVar) != "" {
						return true
					}
				}
			}
			return false
		}
		for k, v := range pr {
			if expected, ok := v.(string); ok {
				if os.Getenv(k) == expected {
					return true
				}
			}
		}
	}
	return false
}

func (v coverageVendor) Build() int {
	build := os.Getenv(v.coverageVariables.Build)
	if build != "" {
		i, _ := strconv.Atoi(build)
		return i
	}
	return 0
}

func (v coverageVendor) Pipeline() string {
	return os.Getenv(v.coverageVariables.Pipeline)
}

func (v coverageVendor) Execution() string {
	return os.Getenv(v.coverageVariables.Execution)
}

// PullRequestID returns the numeric pull request ID from CI, or 0 if unknown.
func (v coverageVendor) PullRequestID() int64 {
	for _, envVar := range v.PRID {
		if id := coverageParsePullRequestID(os.Getenv(envVar)); id > 0 {
			return id
		}
	}
	return coveragePullRequestIDFromPRConfig(v.PR)
}

func coveragePullRequestIDFromPRConfig(pr any) int64 {
	switch cfg := pr.(type) {
	case string:
		return coverageParsePullRequestID(os.Getenv(cfg))
	case map[string]any:
		if envVar, ok := cfg["env"].(string); ok {
			return coverageParsePullRequestID(os.Getenv(envVar))
		}
		if anyVars, ok := cfg["any"].([]any); ok {
			for _, item := range anyVars {
				envVar, ok := item.(string)
				if !ok {
					continue
				}
				if id := coverageParsePullRequestID(os.Getenv(envVar)); id > 0 {
					return id
				}
			}
		}
	}
	return 0
}

// coverageParsePullRequestID extracts a positive PR number from a CI env value.
// Accepts a bare number, a GitHub ref (refs/pull/123/merge), or a PR/MR URL.
func coverageParsePullRequestID(raw string) int64 {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.EqualFold(raw, "false") ||
		strings.EqualFold(raw, "true") || strings.EqualFold(raw, "null") {
		return 0
	}

	if i := strings.Index(raw, "refs/pull/"); i >= 0 {
		rest := raw[i+len("refs/pull/"):]
		num, _, _ := strings.Cut(rest, "/")
		return coverageParsePositiveInt(num)
	}

	for _, marker := range []string{"/pull/", "/merge_requests/", "/pull-requests/"} {
		i := strings.LastIndex(raw, marker)
		if i < 0 {
			continue
		}
		rest := raw[i+len(marker):]
		num, _, _ := strings.Cut(rest, "/")
		num, _, _ = strings.Cut(num, "?")
		num, _, _ = strings.Cut(num, "#")
		if id := coverageParsePositiveInt(num); id > 0 {
			return id
		}
	}

	return coverageParsePositiveInt(raw)
}

func coverageParsePositiveInt(s string) int64 {
	id, err := strconv.ParseInt(s, 10, 64)
	if err != nil || id <= 0 {
		return 0
	}
	return id
}

func (v coverageVendor) RepoBaseURL() (string, error) {
	if baseURL := os.Getenv(v.BaseURL); baseURL != "" {
		return baseURL, nil
	}
	repoInfo, err := coverageGetRepoInfo()
	if err != nil {
		return "", err
	}
	return repoInfo.BaseURL, nil
}

// coverageInfo detects the current CI vendor from the environment, consulting
// the data-driven vendor table before falling back to a short list of common
// CI env vars (CI, CONTINUOUS_INTEGRATION, BUILD_NUMBER, RUN_ID).
func coverageInfo() (coverageVendor, error) {
	for _, v := range coverageVendors {
		switch vt := v.Env.(type) {
		case string:
			val := os.Getenv(vt)
			if val != "" {
				v.Val = val
				return v, nil
			}
		case []any:
			for _, ev := range vt {
				env, ok := ev.(string)
				if !ok {
					continue
				}
				val := os.Getenv(env)
				if val != "" {
					v.Env = env
					v.Val = val
					return v, nil
				}
			}
		case map[string]any:
			for ek, ev := range vt {
				evValue, ok := ev.(string)
				if !ok {
					continue
				}
				val := os.Getenv(ek)
				if val == evValue {
					v.Env = ek
					v.Val = evValue
					return v, nil
				}
			}
		}
	}

	for _, ce := range coverageCommonEnv {
		val := os.Getenv(ce)
		if val != "" {
			return coverageVendor{
				Name:     "Common Var",
				Constant: "COMMON",
				Env:      ce,
				Val:      val,
			}, nil
		}
	}
	return coverageVendor{}, fmt.Errorf("no CI vendor found")
}

const coverageHarnessProvider = "Harness"

type coverageRepoInfo struct {
	Provider string
	BaseURL  string
	Owner    string // full path: group/subgroup
	Name     string
}

// coverageParseGitURL parses a git remote URL (SSH shorthand or scheme://host/path)
// into a coverageRepoInfo, special-casing any harness.io host and stripping the
// DevSpace-convention code/git/ or git/ path prefix for Harness/localhost remotes.
func coverageParseGitURL(remoteURL string) (*coverageRepoInfo, error) {
	var scheme, host, repoPath string

	if strings.HasPrefix(remoteURL, "git@") {
		parts := strings.SplitN(remoteURL, ":", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("invalid SSH URL: %s", remoteURL)
		}
		host = strings.TrimPrefix(parts[0], "git@")
		repoPath = parts[1]
		scheme = "ssh"
	} else {
		u, err := url.Parse(remoteURL)
		if err != nil {
			return nil, err
		}
		host = u.Host
		scheme = u.Scheme
		repoPath = strings.TrimPrefix(u.Path, "/")
	}

	provider := strings.Split(host, ".")[0]
	provider = strings.Split(provider, ":")[0]

	if strings.Contains(strings.ToLower(host), "harness.io") {
		provider = coverageHarnessProvider
	}

	repoPath = strings.TrimSuffix(repoPath, ".git")

	if provider == coverageHarnessProvider || provider == "localhost" {
		repoPath = strings.TrimPrefix(repoPath, "code/git/")
		repoPath = strings.TrimPrefix(repoPath, "git/")
	}

	parts := strings.Split(repoPath, "/")
	if len(parts) < 2 {
		return nil, fmt.Errorf("invalid repo path: %s", repoPath)
	}

	owner := strings.Join(parts[:len(parts)-1], "/")
	repoName := parts[len(parts)-1]

	return &coverageRepoInfo{
		Provider: provider,
		BaseURL:  fmt.Sprintf("%s://%s", scheme, host),
		Owner:    owner,
		Name:     repoName,
	}, nil
}

func coverageGetRemoteURL() (string, error) {
	out, err := exec.Command("git", "remote", "get-url", "origin").Output()
	if err != nil {
		return "", fmt.Errorf("failed to get remote URL: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// coverageGetRepoInfo fetches the git remote URL and parses it into a coverageRepoInfo.
func coverageGetRepoInfo() (*coverageRepoInfo, error) {
	remoteURL, err := coverageGetRemoteURL()
	if err != nil {
		return nil, fmt.Errorf("failed to get remote URL: %w", err)
	}
	repoInfo, err := coverageParseGitURL(remoteURL)
	if err != nil {
		return nil, fmt.Errorf("failed to parse remote URL: %w", err)
	}
	return repoInfo, nil
}
