// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package client

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

func testCtx(t *testing.T, srvURL, accountID string) *cmdctx.Ctx {
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
		case r.Method == http.MethodPost && r.URL.Path == EnsureRepoPath("account"):
			var req EnsureRepoRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Errorf("decode ensure request: %v", err)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			if req.Provider != "github" || req.RepoRef != "owner/repo" {
				t.Errorf("ensure request = %+v", req)
			}
			w.WriteHeader(http.StatusCreated)
			writeCoverageJSON(t, w, EnsureRepoResponse{Repository: &Repository{ID: 42}})
		case r.Method == http.MethodGet && r.URL.Path == RepoSettingsPath("account", 42):
			writeCoverageJSON(t, w, RepoSettings{ExcludeText: &serverExcludeText})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	ctx := testCtx(t, server.URL, "account")
	scope := Scope{AccountID: "account", OrgID: "org", ProjectID: "project"}
	repoID, err := EnsureRepo(ctx, scope, "github", "owner", "repo")
	if err != nil {
		t.Fatalf("EnsureRepo() error = %v", err)
	}
	if repoID != 42 {
		t.Fatalf("EnsureRepo() = %d, want 42", repoID)
	}
	excludeText, err := ExcludeText(ctx, scope, repoID)
	if err != nil {
		t.Fatalf("ExcludeText() error = %v", err)
	}
	if excludeText == nil || *excludeText != serverExcludeText {
		t.Fatalf("ExcludeText() = %v, want %q", excludeText, serverExcludeText)
	}
}

func TestCoverageEnsureRepoFails(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "denied", http.StatusForbidden)
	}))
	defer server.Close()

	ctx := testCtx(t, server.URL, "account")
	if _, err := EnsureRepo(ctx, Scope{}, "github", "owner", "repo"); err == nil {
		t.Fatal("EnsureRepo() expected an error")
	}
}

func TestCoverageExcludeTextHTTPErrorAfterEnsure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == EnsureRepoPath("account"):
			writeCoverageJSON(t, w, EnsureRepoResponse{Repository: &Repository{ID: 1}})
		case r.URL.Path == RepoSettingsPath("account", 1):
			http.Error(w, "denied", http.StatusForbidden)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	ctx := testCtx(t, server.URL, "account")
	repoID, err := EnsureRepo(ctx, Scope{}, "github", "owner", "repo")
	if err != nil {
		t.Fatalf("EnsureRepo() error = %v", err)
	}
	if _, err := ExcludeText(ctx, Scope{}, repoID); err == nil {
		t.Fatal("ExcludeText() expected an error")
	} else if !strings.Contains(err.Error(), "403") {
		t.Fatalf("ExcludeText() error = %v, want it to mention HTTP 403", err)
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
		want        Scope
		wantErr     bool
	}{
		{
			name:       "harness code reads the owner path, not the pipeline",
			accountID:  "account",
			provider:   "Harness",
			owner:      "account/myOrg/myProject",
			pipelineID: "account/pipelineOrg/pipelineProject/pipe",
			want:       Scope{AccountID: "account", OrgID: "myOrg", ProjectID: "myProject"},
		},
		{
			name:      "harness code owner path is matched case-insensitively",
			accountID: "Account",
			provider:  "harness",
			owner:     "account/myOrg/myProject",
			want:      Scope{AccountID: "account", OrgID: "myOrg", ProjectID: "myProject"},
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
			want:        Scope{AccountID: "account", OrgID: "myOrg", ProjectID: "myProject"},
		},
		{
			name:        "org-only target scope leaves the project empty",
			accountID:   "account",
			provider:    "github",
			targetScope: "account/myOrg",
			pipelineID:  "account/pipelineOrg/pipelineProject/pipe",
			want:        Scope{AccountID: "account", OrgID: "myOrg"},
		},
		{
			name:        "account-only target scope leaves org and project empty",
			accountID:   "account",
			provider:    "github",
			targetScope: "account",
			pipelineID:  "account/pipelineOrg/pipelineProject/pipe",
			want:        Scope{AccountID: "account"},
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
			want:       Scope{AccountID: "account", OrgID: "pipelineOrg", ProjectID: "pipelineProject"},
		},
		{
			name:       "pipeline identifier only uses the account",
			accountID:  "account",
			provider:   "github",
			pipelineID: "pipe",
			want:       Scope{AccountID: "account"},
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

			got, err := ResolveScope(
				test.accountID, test.provider, test.owner, test.targetScope, test.pipelineID,
			)
			if (err != nil) != test.wantErr {
				t.Fatalf("ResolveScope() error = %v, wantErr %t", err, test.wantErr)
			}
			if got != test.want {
				t.Errorf("ResolveScope() = %+v, want %+v", got, test.want)
			}
		})
	}
}

func TestCoverageScopeQueryOmitsEmpty(t *testing.T) {
	t.Parallel()

	if got := (Scope{AccountID: "acc"}).query(); len(got) != 0 {
		t.Errorf("account-only query = %v, want empty", got)
	}
	got := Scope{AccountID: "acc", OrgID: "org", ProjectID: "proj"}.query()
	want := map[string]string{"orgId": "org", "projectId": "proj"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("query() = %v, want %v", got, want)
	}
}

func TestCoverageScopeSpacePath(t *testing.T) {
	t.Parallel()

	if got := (Scope{AccountID: "acc", OrgID: "org", ProjectID: "proj"}).SpacePath(); got != "acc/org/proj" {
		t.Errorf("SpacePath() = %q", got)
	}
	if got := (Scope{AccountID: "acc"}).SpacePath(); got != "acc" {
		t.Errorf("account SpacePath() = %q", got)
	}
}

func TestCoverageClientPaths(t *testing.T) {
	t.Parallel()

	if got := EnsureRepoPath("acc"); got != "/coverage/api/v1/accounts/acc/repos" {
		t.Errorf("EnsureRepoPath() = %q", got)
	}
	if got := RepoSettingsPath("acc", 42); got != "/coverage/api/v1/accounts/acc/repos/42/settings" {
		t.Errorf("RepoSettingsPath() = %q", got)
	}
}
