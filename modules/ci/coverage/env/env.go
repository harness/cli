// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package env

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

//go:embed vendors.json
var vendorsJSON []byte

var vendors []Vendor

func init() {
	if err := json.Unmarshal(vendorsJSON, &vendors); err != nil {
		panic(err)
	}
}

var commonEnv = []string{
	"CI",
	"CONTINUOUS_INTEGRATION",
	"BUILD_NUMBER",
	"RUN_ID",
}

type variables struct {
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

type Vendor struct {
	Name     string `json:"name,omitempty"`
	Constant string `json:"constant,omitempty"`
	Env      any    `json:"env,omitempty"`
	PR       any    `json:"pr,omitempty"`
	Val      string `json:"val,omitempty"`
	variables
}

func (v Vendor) String() string {
	return fmt.Sprintf("Name:  %s\nConst: %s\nEnv:   %v\nVal:   %v", v.Name, v.Constant, v.Env, v.Val)
}

func (v Vendor) IsCI() bool {
	return v.Val != ""
}

func (v Vendor) IsPR() bool {
	return v.IsPRBuild()
}

func (v Vendor) AccountID() (string, error) {
	accountID := os.Getenv(v.variables.AccountID)
	if accountID == "" {
		return "", fmt.Errorf("no account ID found")
	}
	return accountID, nil
}

func (v Vendor) OrgID() string {
	return os.Getenv(v.variables.OrgID)
}

func (v Vendor) ProjectID() string {
	return os.Getenv(v.variables.ProjectID)
}

func (v Vendor) SCM() (string, error) {
	if scm := os.Getenv(v.variables.SCM); scm != "" {
		return scm, nil
	}
	repoInfo, err := getRepoInfo()
	if err != nil {
		return "", err
	}
	return repoInfo.Provider, nil
}

func (v Vendor) Owner() (string, error) {
	if owner := os.Getenv(v.variables.Owner); owner != "" {
		return owner, nil
	}
	repoInfo, err := getRepoInfo()
	if err != nil {
		return "", err
	}
	return repoInfo.Owner, nil
}

func (v Vendor) Identifier() (string, error) {
	if identifier := os.Getenv(v.variables.Identifier); identifier != "" {
		return identifier, nil
	}
	repoInfo, err := getRepoInfo()
	if err != nil {
		return "", err
	}
	return repoInfo.Name, nil
}

func (v Vendor) Commit() (string, error) {
	for _, envVar := range v.variables.Commit {
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

func (v Vendor) DefaultBranch() (string, error) {
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

func (v Vendor) Branch() (string, error) {
	for _, envVar := range v.variables.Branch {
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

func (v Vendor) Base() (string, error) {
	for _, envVar := range v.variables.Base {
		if val := os.Getenv(envVar); val != "" {
			return val, nil
		}
	}
	return "", nil
}

func (v Vendor) CommitBefore() string {
	return os.Getenv(v.variables.CommitBefore)
}

func (v Vendor) CommitAfter() string {
	return os.Getenv(v.variables.CommitAfter)
}

// IsValidCommitBefore reports whether CommitBefore holds a usable SHA: not empty
// and not all zeros. Harness CI PR pipelines using the source-branch strategy
// (rather than merge-commit) can leave DRONE_COMMIT_BEFORE empty or all-zero.
func (v Vendor) IsValidCommitBefore() bool {
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
func (v Vendor) IsPRBuild() bool {
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

func (v Vendor) Build() int {
	build := os.Getenv(v.variables.Build)
	if build != "" {
		i, _ := strconv.Atoi(build)
		return i
	}
	return 0
}

func (v Vendor) Pipeline() string {
	return os.Getenv(v.variables.Pipeline)
}

func (v Vendor) Execution() string {
	return os.Getenv(v.variables.Execution)
}

// PullRequestID returns the numeric pull request ID from CI, or 0 if unknown.
func (v Vendor) PullRequestID() int64 {
	for _, envVar := range v.PRID {
		if id := parsePullRequestID(os.Getenv(envVar)); id > 0 {
			return id
		}
	}
	return pullRequestIDFromPRConfig(v.PR)
}

func pullRequestIDFromPRConfig(pr any) int64 {
	switch cfg := pr.(type) {
	case string:
		return parsePullRequestID(os.Getenv(cfg))
	case map[string]any:
		if envVar, ok := cfg["env"].(string); ok {
			return parsePullRequestID(os.Getenv(envVar))
		}
		if anyVars, ok := cfg["any"].([]any); ok {
			for _, item := range anyVars {
				envVar, ok := item.(string)
				if !ok {
					continue
				}
				if id := parsePullRequestID(os.Getenv(envVar)); id > 0 {
					return id
				}
			}
		}
	}
	return 0
}

// parsePullRequestID extracts a positive PR number from a CI env value.
// Accepts a bare number, a GitHub ref (refs/pull/123/merge), or a PR/MR URL.
func parsePullRequestID(raw string) int64 {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.EqualFold(raw, "false") ||
		strings.EqualFold(raw, "true") || strings.EqualFold(raw, "null") {
		return 0
	}

	if i := strings.Index(raw, "refs/pull/"); i >= 0 {
		rest := raw[i+len("refs/pull/"):]
		num, _, _ := strings.Cut(rest, "/")
		return parsePositiveInt(num)
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
		if id := parsePositiveInt(num); id > 0 {
			return id
		}
	}

	return parsePositiveInt(raw)
}

func parsePositiveInt(s string) int64 {
	id, err := strconv.ParseInt(s, 10, 64)
	if err != nil || id <= 0 {
		return 0
	}
	return id
}

func (v Vendor) RepoBaseURL() (string, error) {
	if baseURL := os.Getenv(v.BaseURL); baseURL != "" {
		return baseURL, nil
	}
	repoInfo, err := getRepoInfo()
	if err != nil {
		return "", err
	}
	return repoInfo.BaseURL, nil
}

// Info detects the current CI vendor from the environment, consulting
// the data-driven vendor table before falling back to a short list of common
// CI env vars (CI, CONTINUOUS_INTEGRATION, BUILD_NUMBER, RUN_ID).
func Info() (Vendor, error) {
	for _, v := range vendors {
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

	for _, ce := range commonEnv {
		val := os.Getenv(ce)
		if val != "" {
			return Vendor{
				Name:     "Common Var",
				Constant: "COMMON",
				Env:      ce,
				Val:      val,
			}, nil
		}
	}
	return Vendor{}, fmt.Errorf("no CI vendor found")
}

const HarnessProvider = "Harness"

type repoInfo struct {
	Provider string
	BaseURL  string
	Owner    string // full path: group/subgroup
	Name     string
}

// parseGitURL parses a git remote URL (SSH shorthand or scheme://host/path)
// into a repoInfo, special-casing any harness.io host and stripping the
// DevSpace-convention code/git/ or git/ path prefix for Harness/localhost remotes.
func parseGitURL(remoteURL string) (*repoInfo, error) {
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
		provider = HarnessProvider
	}

	repoPath = strings.TrimSuffix(repoPath, ".git")

	if provider == HarnessProvider || provider == "localhost" {
		repoPath = strings.TrimPrefix(repoPath, "code/git/")
		repoPath = strings.TrimPrefix(repoPath, "git/")
	}

	parts := strings.Split(repoPath, "/")
	if len(parts) < 2 {
		return nil, fmt.Errorf("invalid repo path: %s", repoPath)
	}

	owner := strings.Join(parts[:len(parts)-1], "/")
	repoName := parts[len(parts)-1]

	return &repoInfo{
		Provider: provider,
		BaseURL:  fmt.Sprintf("%s://%s", scheme, host),
		Owner:    owner,
		Name:     repoName,
	}, nil
}

func getRemoteURL() (string, error) {
	out, err := exec.Command("git", "remote", "get-url", "origin").Output()
	if err != nil {
		return "", fmt.Errorf("failed to get remote URL: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// getRepoInfo fetches the git remote URL and parses it into a repoInfo.
func getRepoInfo() (*repoInfo, error) {
	remoteURL, err := getRemoteURL()
	if err != nil {
		return nil, fmt.Errorf("failed to get remote URL: %w", err)
	}
	repoInfo, err := parseGitURL(remoteURL)
	if err != nil {
		return nil, fmt.Errorf("failed to parse remote URL: %w", err)
	}
	return repoInfo, nil
}
