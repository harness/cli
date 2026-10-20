// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package ci

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/harness/cli/v3/pkg/auth"
	"github.com/harness/cli/v3/pkg/cmdctx"
)

func coverageTestCtx(t *testing.T, srvURL, accountID string) *cmdctx.Ctx {
	t.Helper()
	return &cmdctx.Ctx{
		Context: context.Background(),
		Auth: &auth.ResolvedAuth{
			APIUrl:    srvURL,
			AccountID: accountID,
			PATToken:  "token",
			AuthType:  auth.AuthTypePAT,
		},
	}
}

func writeCoverageJSON(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Fatalf("encode response: %v", err)
	}
}

func TestCoverageEnsureRepoAndExcludeText(t *testing.T) {
	serverExcludeText := "# generated\nserver/**\nregex:.*_gen\\.go$"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("orgId") != "org" ||
			r.URL.Query().Get("projectId") != "project" ||
			r.URL.Query().Get("routingId") != "account" ||
			r.URL.Query().Get("accountId") != "account" {
			t.Errorf("query = %q", r.URL.RawQuery)
		}

		switch {
		case r.Method == http.MethodPost && r.URL.Path == coverageEnsureRepoPath("account"):
			var req coverageEnsureRepoRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Errorf("decode ensure request: %v", err)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			if req.Provider != "github" || req.RepoRef != "owner/repo" {
				t.Errorf("ensure request = %+v", req)
			}
			w.WriteHeader(http.StatusCreated)
			writeCoverageJSON(t, w, coverageEnsureRepoResponse{Repository: &coverageRepository{ID: 42}})
		case r.Method == http.MethodGet && r.URL.Path == coverageRepoSettingsPath("account", 42):
			writeCoverageJSON(t, w, coverageRepoSettings{ExcludeText: &serverExcludeText})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	ctx := coverageTestCtx(t, server.URL, "account")
	scope := coverageScope{AccountID: "account", OrgID: "org", ProjectID: "project"}
	repoID, err := coverageEnsureRepo(ctx, scope, "github", "owner", "repo")
	if err != nil {
		t.Fatalf("coverageEnsureRepo() error = %v", err)
	}
	if repoID != 42 {
		t.Fatalf("coverageEnsureRepo() = %d, want 42", repoID)
	}
	excludeText, err := coverageExcludeText(ctx, scope, repoID)
	if err != nil {
		t.Fatalf("coverageExcludeText() error = %v", err)
	}
	if excludeText == nil || *excludeText != serverExcludeText {
		t.Fatalf("coverageExcludeText() = %v, want %q", excludeText, serverExcludeText)
	}
}

func TestCoverageEnsureRepoFails(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "denied", http.StatusForbidden)
	}))
	defer server.Close()

	ctx := coverageTestCtx(t, server.URL, "account")
	if _, err := coverageEnsureRepo(ctx, coverageScope{}, "github", "owner", "repo"); err == nil {
		t.Fatal("coverageEnsureRepo() expected an error")
	}
}

func TestCoverageExcludeTextHTTPErrorAfterEnsure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == coverageEnsureRepoPath("account"):
			writeCoverageJSON(t, w, coverageEnsureRepoResponse{Repository: &coverageRepository{ID: 1}})
		case r.URL.Path == coverageRepoSettingsPath("account", 1):
			http.Error(w, "denied", http.StatusForbidden)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	ctx := coverageTestCtx(t, server.URL, "account")
	repoID, err := coverageEnsureRepo(ctx, coverageScope{}, "github", "owner", "repo")
	if err != nil {
		t.Fatalf("coverageEnsureRepo() error = %v", err)
	}
	if _, err := coverageExcludeText(ctx, coverageScope{}, repoID); err == nil {
		t.Fatal("coverageExcludeText() expected an error")
	} else if !strings.Contains(err.Error(), "403") {
		t.Fatalf("coverageExcludeText() error = %v, want it to mention HTTP 403", err)
	}
}

func TestCoverageResolveScope(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		accountID   string
		provider    string
		owner       string
		targetScope string
		pipelineID  string
		want        coverageScope
		wantErr     bool
	}{
		{
			name:       "harness code reads the owner path, not the pipeline",
			accountID:  "account",
			provider:   "Harness",
			owner:      "account/myOrg/myProject",
			pipelineID: "account/pipelineOrg/pipelineProject/pipe",
			want:       coverageScope{AccountID: "account", OrgID: "myOrg", ProjectID: "myProject"},
		},
		{
			name:      "harness code owner path is matched case-insensitively",
			accountID: "Account",
			provider:  "harness",
			owner:     "account/myOrg/myProject",
			want:      coverageScope{AccountID: "account", OrgID: "myOrg", ProjectID: "myProject"},
		},
		{
			name:        "harness code rejects target scope",
			accountID:   "account",
			provider:    "Harness",
			owner:       "account/myOrg/myProject",
			targetScope: "account/otherOrg/otherProject",
			pipelineID:  "account/pipelineOrg/pipelineProject/pipe",
			wantErr:     true,
		},
		{
			name:        "target scope overrides the pipeline",
			accountID:   "account",
			provider:    "github",
			owner:       "owner",
			targetScope: "/account/myOrg/myProject/",
			pipelineID:  "account/pipelineOrg/pipelineProject/pipe",
			want:        coverageScope{AccountID: "account", OrgID: "myOrg", ProjectID: "myProject"},
		},
		{
			name:        "org-only target scope leaves the project empty",
			accountID:   "account",
			provider:    "github",
			targetScope: "account/myOrg",
			pipelineID:  "account/pipelineOrg/pipelineProject/pipe",
			want:        coverageScope{AccountID: "account", OrgID: "myOrg"},
		},
		{
			name:        "account-only target scope leaves org and project empty",
			accountID:   "account",
			provider:    "github",
			targetScope: "account",
			pipelineID:  "account/pipelineOrg/pipelineProject/pipe",
			want:        coverageScope{AccountID: "account"},
		},
		{
			name:        "rejects target scope in another account",
			accountID:   "account",
			provider:    "github",
			targetScope: "other/org",
			pipelineID:  "account/pipelineOrg/pipelineProject/pipe",
			wantErr:     true,
		},
		{
			name:        "rejects target scope with an empty segment",
			accountID:   "account",
			provider:    "github",
			targetScope: "account//project",
			pipelineID:  "account/pipelineOrg/pipelineProject/pipe",
			wantErr:     true,
		},
		{
			name:        "rejects target scope that is too deep",
			accountID:   "account",
			provider:    "github",
			targetScope: "account/org/project/extra",
			pipelineID:  "account/pipelineOrg/pipelineProject/pipe",
			wantErr:     true,
		},
		{
			name:       "falls back to the pipeline path",
			accountID:  "account",
			provider:   "github",
			owner:      "owner",
			pipelineID: "account/pipelineOrg/pipelineProject/pipe",
			want:       coverageScope{AccountID: "account", OrgID: "pipelineOrg", ProjectID: "pipelineProject"},
		},
		{
			name:       "pipeline identifier only uses the account",
			accountID:  "account",
			provider:   "github",
			pipelineID: "pipe",
			want:       coverageScope{AccountID: "account"},
		},
		{
			name:      "rejects Harness Code owner with too many segments",
			accountID: "account",
			provider:  "Harness",
			owner:     "account/org/project/extra",
			wantErr:   true,
		},
		{
			name:      "rejects Harness Code owner with an invalid segment",
			accountID: "account",
			provider:  "Harness",
			owner:     "account/../project",
			wantErr:   true,
		},
		{
			name:      "rejects Harness Code owner in another account",
			accountID: "account",
			provider:  "Harness",
			owner:     "other-account/myOrg/myProject",
			wantErr:   true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got, err := coverageResolveScope(
				test.accountID, test.provider, test.owner, test.targetScope, test.pipelineID,
			)
			if (err != nil) != test.wantErr {
				t.Fatalf("coverageResolveScope() error = %v, wantErr %t", err, test.wantErr)
			}
			if got != test.want {
				t.Errorf("coverageResolveScope() = %+v, want %+v", got, test.want)
			}
		})
	}
}

func TestCoverageScopeQueryOmitsEmpty(t *testing.T) {
	t.Parallel()

	if got := (coverageScope{AccountID: "acc"}).query(); len(got) != 0 {
		t.Errorf("account-only query = %v, want empty", got)
	}
	got := coverageScope{AccountID: "acc", OrgID: "org", ProjectID: "proj"}.query()
	want := map[string]string{"orgId": "org", "projectId": "proj"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("query() = %v, want %v", got, want)
	}
}

func TestCoverageScopeSpacePath(t *testing.T) {
	t.Parallel()

	if got := (coverageScope{AccountID: "acc", OrgID: "org", ProjectID: "proj"}).SpacePath(); got != "acc/org/proj" {
		t.Errorf("SpacePath() = %q", got)
	}
	if got := (coverageScope{AccountID: "acc"}).SpacePath(); got != "acc" {
		t.Errorf("account SpacePath() = %q", got)
	}
}

func TestCoverageClientPaths(t *testing.T) {
	t.Parallel()

	if got := coverageEnsureRepoPath("acc"); got != "/coverage/api/v1/accounts/acc/repos" {
		t.Errorf("coverageEnsureRepoPath() = %q", got)
	}
	if got := coverageRepoSettingsPath("acc", 42); got != "/coverage/api/v1/accounts/acc/repos/42/settings" {
		t.Errorf("coverageRepoSettingsPath() = %q", got)
	}
}
