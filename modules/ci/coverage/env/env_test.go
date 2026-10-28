// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package env

import (
	"os"
	"testing"
)

func TestCoverageVendor_IsCI(t *testing.T) {
	if (Vendor{Val: "x"}).IsCI() != true {
		t.Error("IsCI() = false, want true when Val is set")
	}
	if (Vendor{}).IsCI() != false {
		t.Error("IsCI() = true, want false when Val is empty")
	}
}

func TestCoverageVendor_AccountID(t *testing.T) {
	t.Setenv("TEST_ACCOUNT_ID", "acct-123")
	v := Vendor{variables: variables{AccountID: "TEST_ACCOUNT_ID"}}
	got, err := v.AccountID()
	if err != nil {
		t.Fatalf("AccountID() error = %v", err)
	}
	if got != "acct-123" {
		t.Fatalf("AccountID() = %q, want acct-123", got)
	}
}

func TestCoverageVendor_AccountID_MissingErrors(t *testing.T) {
	v := Vendor{variables: variables{AccountID: "NONEXISTENT_ACCOUNT_ID_VAR"}}
	if _, err := v.AccountID(); err == nil {
		t.Fatal("AccountID() error = nil, want error when the env var is unset")
	}
}

func TestCoverageVendor_Build(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  int
	}{
		{"valid number", "123", 123},
		{"unset", "", 0},
		{"not a number", "not-a-number", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.value != "" {
				t.Setenv("TEST_BUILD_ID", tt.value)
			}
			v := Vendor{variables: variables{Build: "TEST_BUILD_ID"}}
			if got := v.Build(); got != tt.want {
				t.Errorf("Build() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestCoverageVendor_IsValidCommitBefore(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  bool
	}{
		{"valid sha", "abc123def456789012345678901234567890abcd", true},
		{"all zeros", "0000000000000000000000000000000000000000", false},
		{"empty", "", false},
		{"short sha with valid chars", "abc123", true},
		{"leading zeros but not all", "0000000000000000000000000000000000000001", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.value != "" {
				t.Setenv("TEST_COMMIT_BEFORE", tt.value)
			}
			v := Vendor{variables: variables{CommitBefore: "TEST_COMMIT_BEFORE"}}
			if got := v.IsValidCommitBefore(); got != tt.want {
				t.Errorf("IsValidCommitBefore() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCoverageVendor_IsPRBuild(t *testing.T) {
	tests := []struct {
		name   string
		pr     any
		envVar string
		envVal string
		want   bool
	}{
		{"nil PR", nil, "", "", false},
		{"string env set", "TEST_PR_NUMBER", "TEST_PR_NUMBER", "123", true},
		{"string env unset", "TEST_PR_NUMBER_UNSET", "", "", false},
		{"map value match", map[string]any{"DRONE_BUILD_EVENT": "pull_request"}, "DRONE_BUILD_EVENT", "pull_request", true},
		{"map value mismatch", map[string]any{"DRONE_BUILD_EVENT": "pull_request"}, "DRONE_BUILD_EVENT", "push", false},
		{"env/ne value differs from ne", map[string]any{"env": "BUILDKITE_PULL_REQUEST", "ne": "false"}, "BUILDKITE_PULL_REQUEST", "123", true},
		{"env/ne value equals ne", map[string]any{"env": "BUILDKITE_PULL_REQUEST", "ne": "false"}, "BUILDKITE_PULL_REQUEST", "false", false},
		{"any one set", map[string]any{"any": []any{"CF_PULL_REQUEST_NUMBER", "CF_PULL_REQUEST_ID"}}, "CF_PULL_REQUEST_ID", "456", true},
		{"any none set", map[string]any{"any": []any{"CF_PULL_REQUEST_NUMBER", "CF_PULL_REQUEST_ID"}}, "", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.envVar != "" {
				t.Setenv(tt.envVar, tt.envVal)
			}
			v := Vendor{PR: tt.pr}
			if got := v.IsPRBuild(); got != tt.want {
				t.Errorf("IsPRBuild() = %v, want %v", got, tt.want)
			}
			if got := v.IsPR(); got != tt.want {
				t.Errorf("IsPR() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCoverageParsePullRequestID(t *testing.T) {
	tests := []struct {
		raw  string
		want int64
	}{
		{"", 0},
		{"false", 0},
		{"true", 0},
		{"null", 0},
		{"0", 0},
		{"123", 123},
		{"refs/pull/42/merge", 42},
		{"refs/pull/42/head", 42},
		{"https://github.com/org/repo/pull/99", 99},
		{"https://github.com/org/repo/pull/99#discussion", 99},
		{"https://gitlab.com/org/repo/-/merge_requests/7", 7},
		{"refs/heads/main", 0},
	}
	for _, tt := range tests {
		t.Run(tt.raw, func(t *testing.T) {
			if got := parsePullRequestID(tt.raw); got != tt.want {
				t.Errorf("parsePullRequestID(%q) = %d, want %d", tt.raw, got, tt.want)
			}
		})
	}
}

func TestCoverageVendor_PullRequestID(t *testing.T) {
	t.Run("from pr_id env list, second entry", func(t *testing.T) {
		t.Setenv("GITHUB_REF", "refs/pull/321/merge")
		v := Vendor{variables: variables{PRID: []string{"GITHUB_PR_NUMBER", "GITHUB_REF"}}}
		if got := v.PullRequestID(); got != 321 {
			t.Errorf("PullRequestID() = %d, want 321", got)
		}
	})

	t.Run("falls back to PR config when pr_id unset", func(t *testing.T) {
		t.Setenv("BITBUCKET_PR_ID", "15")
		v := Vendor{PR: "BITBUCKET_PR_ID"}
		if got := v.PullRequestID(); got != 15 {
			t.Errorf("PullRequestID() = %d, want 15", got)
		}
	})

	t.Run("PR config env/ne", func(t *testing.T) {
		t.Setenv("TRAVIS_PULL_REQUEST", "9")
		v := Vendor{PR: map[string]any{"env": "TRAVIS_PULL_REQUEST", "ne": "false"}}
		if got := v.PullRequestID(); got != 9 {
			t.Errorf("PullRequestID() = %d, want 9", got)
		}
	})

	t.Run("travis false sentinel is not an id", func(t *testing.T) {
		t.Setenv("TRAVIS_PULL_REQUEST", "false")
		v := Vendor{PR: map[string]any{"env": "TRAVIS_PULL_REQUEST", "ne": "false"}}
		if got := v.PullRequestID(); got != 0 {
			t.Errorf("PullRequestID() = %d, want 0", got)
		}
	})

	t.Run("empty when nothing set", func(t *testing.T) {
		v := Vendor{}
		if got := v.PullRequestID(); got != 0 {
			t.Errorf("PullRequestID() = %d, want 0", got)
		}
	})
}

func clearCIEnv(t *testing.T) {
	t.Helper()
	vars := []string{
		"CI", "CONTINUOUS_INTEGRATION", "BUILD_NUMBER", "RUN_ID",
		"APPVEYOR", "SYSTEM_TEAMFOUNDATIONCOLLECTIONURI", "AC_APPCIRCLE",
		"bamboo_planKey", "BITBUCKET_COMMIT", "BITRISE_IO", "BUDDY_WORKSPACE_ID",
		"BUILDKITE", "CIRCLECI", "CIRRUS_CI", "CODEBUILD_BUILD_ARN",
		"CF_BUILD_ID", "CI_NAME", "DRONE", "DSARI", "EAS_BUILD",
		"GITHUB_ACTIONS", "GITLAB_CI", "GO_PIPELINE_LABEL", "LAYERCI",
		"HUDSON_URL", "JENKINS_URL", "BUILD_ID", "MAGNUM", "NETLIFY",
		"NEVERCODE", "RENDER", "SAILCI", "SEMAPHORE", "SCREWDRIVER",
		"SHIPPABLE", "TDDIUM", "STRIDER", "TASK_ID", "TEAMCITY_VERSION",
		"TRAVIS", "NOW_BUILDER", "APPCENTER_BUILD_ID",
	}
	for _, v := range vars {
		orig, ok := os.LookupEnv(v)
		os.Unsetenv(v)
		if ok {
			t.Cleanup(func() { os.Setenv(v, orig) })
		}
	}
}

func TestCoverageInfo_VendorEnvShapes(t *testing.T) {
	t.Run("string-shaped env wins over common fallback", func(t *testing.T) {
		clearCIEnv(t)
		t.Setenv("CIRCLECI", "true")
		vendor, err := Info()
		if err != nil {
			t.Fatalf("Info() error = %v", err)
		}
		if vendor.Constant != "CIRCLE" {
			t.Fatalf("vendor.Constant = %q, want CIRCLE", vendor.Constant)
		}
		if vendor.Val != "true" {
			t.Fatalf("vendor.Val = %q, want true", vendor.Val)
		}
	})

	t.Run("list-shaped env matches one entry", func(t *testing.T) {
		clearCIEnv(t)
		t.Setenv("JENKINS_URL", "http://jenkins.example.com")
		vendor, err := Info()
		if err != nil {
			t.Fatalf("Info() error = %v", err)
		}
		if vendor.Constant != "JENKINS" {
			t.Fatalf("vendor.Constant = %q, want JENKINS", vendor.Constant)
		}
		if vendor.Val != "http://jenkins.example.com" {
			t.Fatalf("vendor.Val = %q", vendor.Val)
		}
	})

	t.Run("map-shaped env requires exact value match", func(t *testing.T) {
		clearCIEnv(t)
		t.Setenv("CI_NAME", "codeship")
		vendor, err := Info()
		if err != nil {
			t.Fatalf("Info() error = %v", err)
		}
		if vendor.Val != "codeship" {
			t.Fatalf("vendor.Val = %q, want codeship (exact map-value match)", vendor.Val)
		}
	})

	t.Run("map-shaped env rejects a non-matching value", func(t *testing.T) {
		clearCIEnv(t)
		t.Setenv("CI_NAME", "something-else")
		t.Setenv("CI", "true")
		vendor, err := Info()
		if err != nil {
			t.Fatalf("Info() error = %v", err)
		}
		if vendor.Constant != "COMMON" {
			t.Fatalf("vendor.Constant = %q, want COMMON (CI_NAME value must not match an unrelated vendor)", vendor.Constant)
		}
	})

	t.Run("falls back to common env when no vendor matches", func(t *testing.T) {
		clearCIEnv(t)
		t.Setenv("CI", "true")
		vendor, err := Info()
		if err != nil {
			t.Fatalf("Info() error = %v", err)
		}
		if vendor.Constant != "COMMON" || vendor.Val != "true" {
			t.Fatalf("vendor = %+v, want the Common Var fallback", vendor)
		}
	})

	t.Run("errors when nothing matches", func(t *testing.T) {
		clearCIEnv(t)
		if _, err := Info(); err == nil {
			t.Fatal("Info() error = nil, want \"no CI vendor found\"")
		}
	})
}

func TestCoverageParseGitURL(t *testing.T) {
	tests := []struct {
		name     string
		url      string
		wantErr  bool
		provider string
		owner    string
		repo     string
		baseURL  string
	}{
		{"github https", "https://github.com/user/repo.git", false, "github", "user", "repo", "https://github.com"},
		{"gitlab nested group", "https://gitlab.com/group/subgroup/repo.git", false, "gitlab", "group/subgroup", "repo", ""},
		{"ssh shorthand", "git@github.com:user/repo.git", false, "github", "user", "repo", ""},
		{"no .git suffix", "https://github.com/user/repo", false, "github", "user", "repo", ""},
		{"harness.io host with git/ prefix", "https://git.harness.io/git/user/repo.git", false, HarnessProvider, "user", "repo", ""},
		{"harness.io host with code/git/ devspace prefix", "https://harness.io/code/git/user/repo.git", false, HarnessProvider, "user", "repo", ""},
		{"harness.io devspace full path", "https://teamintelci.pr2.harness.io/code/git/abc123/default/tidev/uuid.git", false, HarnessProvider, "abc123/default/tidev", "uuid", ""},
		{"invalid ssh url, no colon", "git@github.com", true, "", "", "", ""},
		{"invalid repo path, single segment", "https://github.com/repo", true, "", "", "", ""},
		{"invalid url", "://invalid", true, "", "", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info, err := parseGitURL(tt.url)
			if tt.wantErr {
				if err == nil {
					t.Fatal("parseGitURL() error = nil, want error")
				}
				return
			}
			if err != nil {
				t.Fatalf("parseGitURL() error = %v", err)
			}
			if info.Provider != tt.provider {
				t.Errorf("Provider = %q, want %q", info.Provider, tt.provider)
			}
			if info.Owner != tt.owner {
				t.Errorf("Owner = %q, want %q", info.Owner, tt.owner)
			}
			if info.Name != tt.repo {
				t.Errorf("Name = %q, want %q", info.Name, tt.repo)
			}
			if tt.baseURL != "" && info.BaseURL != tt.baseURL {
				t.Errorf("BaseURL = %q, want %q", info.BaseURL, tt.baseURL)
			}
		})
	}
}

func TestCoverageParseGitURL_ProviderExtractionStripsPort(t *testing.T) {
	info, err := parseGitURL("https://git.example.com:8080/user/repo.git")
	if err != nil {
		t.Fatalf("parseGitURL() error = %v", err)
	}
	if info.Provider != "git" {
		t.Fatalf("Provider = %q, want git (host label before the first dot, port stripped)", info.Provider)
	}
}
