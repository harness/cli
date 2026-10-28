// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package upload

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	covclient "github.com/harness/cli/v3/modules/ci/coverage/client"
	"github.com/harness/cli/v3/pkg/auth"
	"github.com/harness/cli/v3/pkg/cmdctx"
)

// withGitRemote chdirs into a disposable temp git repository whose
// origin remote is a safe placeholder, restoring the original working
// directory on cleanup. This keeps vendor.RepoBaseURL()'s git-remote
// fallback (which always runs `git remote get-url origin` in cwd, with no
// CLI override) from ever consulting this checkout's real origin remote.
func withGitRemote(t *testing.T, originURL string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q", "-b", "main")
	run("remote", "add", "origin", originURL)

	oldWD, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldWD) })
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
}

// setDefaultBranch points refs/remotes/origin/HEAD at branch, the
// same ref vendor.DefaultBranch() reads via `git symbolic-ref`.
func setDefaultBranch(t *testing.T, branch string) {
	t.Helper()
	cmd := exec.Command("git", "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/"+branch)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git symbolic-ref: %v\n%s", err, out)
	}
}

func baseUploadFlags() uploadFlags {
	return uploadFlags{
		accountID:   "acct",
		provider:    "github",
		owner:       "owner",
		identifier:  "repo",
		commitID:    "deadbeef",
		branch:      "feature",
		pipelineID:  "acct/org/proj/pipe",
		executionID: "exec-1",
	}
}

func TestCoverageParseIdentifierOwner(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		account        string
		owner          string
		identifier     string
		wantIdentifier string
		wantOwner      string
	}{
		{
			name:           "owner contained in identifier strips owner and everything before it",
			account:        "acct",
			owner:          "org/project",
			identifier:     "group/org/project/repo",
			wantIdentifier: "repo",
			wantOwner:      "org/project",
		},
		{
			name:           "owner empty splits by last slash and joins under account",
			account:        "acct",
			identifier:     "org/project/repo",
			wantIdentifier: "repo",
			wantOwner:      "acct/org/project",
		},
		{
			name:           "owner empty with single segment identifier",
			account:        "acct",
			identifier:     "repo",
			wantIdentifier: "repo",
			wantOwner:      "acct",
		},
		{
			name:           "owner not found in identifier falls back to split, discarding owner",
			account:        "acct",
			owner:          "other",
			identifier:     "org/project/repo",
			wantIdentifier: "repo",
			wantOwner:      "acct/org/project",
		},
		{
			name:           "owner found at the start of identifier",
			account:        "acct",
			owner:          "acct",
			identifier:     "acct/repo",
			wantIdentifier: "repo",
			wantOwner:      "acct",
		},
		{
			name:           "leading slash is cleaned by path.Join",
			account:        "account",
			identifier:     "/org/project/repo",
			wantIdentifier: "repo",
			wantOwner:      "account/org/project",
		},
		{
			name:           "identifier equal to owner leaves an empty identifier",
			account:        "acct",
			owner:          "org/repo",
			identifier:     "org/repo",
			wantIdentifier: "",
			wantOwner:      "org/repo",
		},
		{
			name:           "empty account and owner with single segment identifier",
			identifier:     "repo",
			wantIdentifier: "repo",
			wantOwner:      "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			gotIdentifier, gotOwner := parseIdentifierOwner(tt.account, tt.owner, tt.identifier)
			if gotIdentifier != tt.wantIdentifier || gotOwner != tt.wantOwner {
				t.Errorf("parseIdentifierOwner(%q,%q,%q) = (%q,%q), want (%q,%q)",
					tt.account, tt.owner, tt.identifier, gotIdentifier, gotOwner, tt.wantIdentifier, tt.wantOwner)
			}
		})
	}
}

func TestCoverageUploadTargetScope(t *testing.T) {
	t.Parallel()

	harness := &uploadParams{
		provider:      "Harness",
		resolvedScope: covclient.Scope{AccountID: "a", OrgID: "o", ProjectID: "p"},
	}
	if got := uploadTargetScope(harness); got != "" {
		t.Errorf("Harness target scope = %q, want empty", got)
	}

	other := &uploadParams{
		provider:      "github",
		resolvedScope: covclient.Scope{AccountID: "a", OrgID: "o", ProjectID: "p"},
	}
	if got := uploadTargetScope(other); got != "a/o/p" {
		t.Errorf("non-Harness target scope = %q, want a/o/p", got)
	}
}

func TestCoverageResolveUploadParams(t *testing.T) {
	t.Run("resolves scope from the pipeline path and leaves base/commit-before empty", func(t *testing.T) {
		withGitRemote(t, "https://example.com/test-owner/test-repo.git")

		params, err := resolveUploadParams(baseUploadFlags())
		if err != nil {
			t.Fatalf("resolveUploadParams() error = %v", err)
		}
		wantScope := covclient.Scope{AccountID: "acct", OrgID: "org", ProjectID: "proj"}
		if params.resolvedScope != wantScope {
			t.Errorf("resolvedScope = %+v, want %+v", params.resolvedScope, wantScope)
		}
		if params.baseURL != "https://example.com" {
			t.Errorf("baseURL = %q, want https://example.com", params.baseURL)
		}
		if params.base != "" {
			t.Errorf("base = %q, want empty when no default branch is known", params.base)
		}
		if params.commitBefore != "" {
			t.Errorf("commitBefore = %q, want empty without a CI vendor", params.commitBefore)
		}
	})

	t.Run("errors when execution id is missing and not auto-detectable", func(t *testing.T) {
		withGitRemote(t, "https://example.com/test-owner/test-repo.git")

		f := baseUploadFlags()
		f.executionID = ""
		if _, err := resolveUploadParams(f); err != errCoverageExecutionIDMandatory {
			t.Fatalf("resolveUploadParams() error = %v, want errCoverageExecutionIDMandatory", err)
		}
	})

	t.Run("errors when patch is requested without a base", func(t *testing.T) {
		withGitRemote(t, "https://example.com/test-owner/test-repo.git")

		f := baseUploadFlags()
		f.patch = true
		_, err := resolveUploadParams(f)
		if err == nil || !strings.Contains(err.Error(), "--patch requires --base") {
			t.Fatalf("resolveUploadParams() error = %v, want it to mention --patch requires --base", err)
		}
	})

	t.Run("propagates a target scope outside the account", func(t *testing.T) {
		withGitRemote(t, "https://example.com/test-owner/test-repo.git")

		f := baseUploadFlags()
		f.targetScope = "other-account/org"
		if _, err := resolveUploadParams(f); err == nil {
			t.Fatal("resolveUploadParams() expected an error for a target scope outside the account")
		}
	})

	t.Run("falls back to the default branch as base when the branch diverges", func(t *testing.T) {
		withGitRemote(t, "https://example.com/test-owner/test-repo.git")
		setDefaultBranch(t, "main")

		params, err := resolveUploadParams(baseUploadFlags())
		if err != nil {
			t.Fatalf("resolveUploadParams() error = %v", err)
		}
		if params.defaultBranch != "main" {
			t.Fatalf("defaultBranch = %q, want main", params.defaultBranch)
		}
		if params.base != "main" {
			t.Errorf("base = %q, want main (fallback from default branch)", params.base)
		}
	})

	t.Run("does not override an explicit base with the default branch", func(t *testing.T) {
		withGitRemote(t, "https://example.com/test-owner/test-repo.git")
		setDefaultBranch(t, "main")

		f := baseUploadFlags()
		f.base = "release"
		params, err := resolveUploadParams(f)
		if err != nil {
			t.Fatalf("resolveUploadParams() error = %v", err)
		}
		if params.base != "release" {
			t.Errorf("base = %q, want release (explicit flag kept)", params.base)
		}
	})
}

func uploadTestCoverageFile(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "coverage.out")
	content := "mode: set\nexample.com/pkg/file.go:1.1,2.1 1 1\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestCoverageUpload_FullSequence(t *testing.T) {
	withGitRemote(t, "https://example.com/test-owner/test-repo.git")
	coverFile := uploadTestCoverageFile(t)

	var mu sync.Mutex
	var calls []string
	record := func(name string) {
		mu.Lock()
		calls = append(calls, name)
		mu.Unlock()
	}

	mux := http.NewServeMux()
	server := httptest.NewServer(mux)
	defer server.Close()
	storageURL := server.URL + "/storage/upload"

	mux.HandleFunc(covclient.EnsureRepoPath("acct"), func(w http.ResponseWriter, r *http.Request) {
		record("ensure")
		var req covclient.EnsureRepoRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode ensure request: %v", err)
		}
		if req.Provider != "github" || req.RepoRef != "owner/repo" {
			t.Errorf("ensure request = %+v", req)
		}
		w.WriteHeader(http.StatusCreated)
		writeCoverageJSON(t, w, covclient.EnsureRepoResponse{Repository: &covclient.Repository{ID: 7}})
	})
	mux.HandleFunc(covclient.RepoSettingsPath("acct", 7), func(w http.ResponseWriter, _ *http.Request) {
		record("settings")
		writeCoverageJSON(t, w, covclient.RepoSettings{})
	})
	mux.HandleFunc(uploadPath("acct"), func(w http.ResponseWriter, r *http.Request) {
		record("presign")
		var req signURLRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode presign request: %v", err)
		}
		if req.Provider != "github" || req.Owner != "owner" || req.Identifier != "repo" ||
			req.CommitID != "deadbeef" || req.Branch != "feature" || req.BaseURL != "https://example.com" ||
			req.PipelineID != "acct/org/proj/pipe" || req.ExecutionID != "exec-1" ||
			req.Version != 1 || req.Format != "parquet" ||
			req.PullRequestID != 42 || !reflect.DeepEqual(req.Tags, []string{"nightly", "release"}) ||
			req.TargetScope != "acct/org/proj" {
			t.Errorf("presign request = %+v", req)
		}
		writeCoverageJSON(t, w, signURLResponse{ExternalID: "ext-1", Location: storageURL})
	})
	mux.HandleFunc("/storage/upload", func(w http.ResponseWriter, r *http.Request) {
		record("put")
		if got := r.Header.Get("Authorization"); got != "" {
			t.Errorf("storage PUT carried an Authorization header: %q, want none", got)
		}
		if _, err := io.ReadAll(r.Body); err != nil {
			t.Errorf("read storage PUT body: %v", err)
		}
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc(completePath("acct", "ext-1"), func(w http.ResponseWriter, _ *http.Request) {
		record("complete")
		w.WriteHeader(http.StatusOK)
	})

	ctx := testCtx(t, server.URL, "acct")
	f := baseUploadFlags()
	f.files = []string{coverFile}
	f.pullReqID = 42
	f.tags = []string{"nightly", "release"}

	if err := upload(ctx, f); err != nil {
		t.Fatalf("upload() error = %v", err)
	}

	want := []string{"ensure", "settings", "presign", "put", "complete"}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("call sequence = %v, want %v", calls, want)
	}
}

func TestCoverageUpload_ExcludeSettingsFailureStopsBeforePresign(t *testing.T) {
	tests := []struct {
		name          string
		ensureStatus  int
		settingsOK    bool
		settingsFails bool
		wantErrSubstr string
	}{
		{
			name:          "ensure repo fails",
			ensureStatus:  http.StatusForbidden,
			wantErrSubstr: "resolve exclude settings repository",
		},
		{
			name:          "settings fetch fails",
			ensureStatus:  http.StatusCreated,
			settingsFails: true,
			wantErrSubstr: "load coverage repository exclude settings",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			withGitRemote(t, "https://example.com/test-owner/test-repo.git")
			coverFile := uploadTestCoverageFile(t)

			var presignCalled bool
			mux := http.NewServeMux()
			server := httptest.NewServer(mux)
			defer server.Close()

			mux.HandleFunc(covclient.EnsureRepoPath("acct"), func(w http.ResponseWriter, _ *http.Request) {
				if tt.ensureStatus != http.StatusCreated {
					http.Error(w, "denied", tt.ensureStatus)
					return
				}
				w.WriteHeader(http.StatusCreated)
				writeCoverageJSON(t, w, covclient.EnsureRepoResponse{Repository: &covclient.Repository{ID: 7}})
			})
			mux.HandleFunc(covclient.RepoSettingsPath("acct", 7), func(w http.ResponseWriter, _ *http.Request) {
				if tt.settingsFails {
					http.Error(w, "denied", http.StatusInternalServerError)
					return
				}
				writeCoverageJSON(t, w, covclient.RepoSettings{})
			})
			mux.HandleFunc(uploadPath("acct"), func(w http.ResponseWriter, _ *http.Request) {
				presignCalled = true
				writeCoverageJSON(t, w, signURLResponse{})
			})

			ctx := testCtx(t, server.URL, "acct")
			f := baseUploadFlags()
			f.files = []string{coverFile}

			err := upload(ctx, f)
			if err == nil || !strings.Contains(err.Error(), tt.wantErrSubstr) {
				t.Fatalf("upload() error = %v, want it to contain %q", err, tt.wantErrSubstr)
			}
			if presignCalled {
				t.Error("presign was called despite the exclude-settings lookup failing")
			}
		})
	}
}

func TestCoverageUpload_PresignFailureStopsBeforeComplete(t *testing.T) {
	withGitRemote(t, "https://example.com/test-owner/test-repo.git")
	coverFile := uploadTestCoverageFile(t)

	var completeCalled bool
	mux := http.NewServeMux()
	server := httptest.NewServer(mux)
	defer server.Close()

	mux.HandleFunc(covclient.EnsureRepoPath("acct"), func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
		writeCoverageJSON(t, w, covclient.EnsureRepoResponse{Repository: &covclient.Repository{ID: 7}})
	})
	mux.HandleFunc(covclient.RepoSettingsPath("acct", 7), func(w http.ResponseWriter, _ *http.Request) {
		writeCoverageJSON(t, w, covclient.RepoSettings{})
	})
	mux.HandleFunc(uploadPath("acct"), func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/complete") {
			completeCalled = true
		}
		http.NotFound(w, r)
	})

	ctx := testCtx(t, server.URL, "acct")
	f := baseUploadFlags()
	f.files = []string{coverFile}

	err := upload(ctx, f)
	if err == nil || !strings.Contains(err.Error(), "presign failed") {
		t.Fatalf("upload() error = %v, want it to contain \"presign failed\"", err)
	}
	if completeCalled {
		t.Error("complete was called despite the presign request failing")
	}
}

func TestCoverageUpload_StorageUploadFailureStopsBeforeComplete(t *testing.T) {
	withGitRemote(t, "https://example.com/test-owner/test-repo.git")
	coverFile := uploadTestCoverageFile(t)

	var completeCalled bool
	mux := http.NewServeMux()
	server := httptest.NewServer(mux)
	defer server.Close()
	storageURL := server.URL + "/storage/upload"

	mux.HandleFunc(covclient.EnsureRepoPath("acct"), func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
		writeCoverageJSON(t, w, covclient.EnsureRepoResponse{Repository: &covclient.Repository{ID: 7}})
	})
	mux.HandleFunc(covclient.RepoSettingsPath("acct", 7), func(w http.ResponseWriter, _ *http.Request) {
		writeCoverageJSON(t, w, covclient.RepoSettings{})
	})
	mux.HandleFunc(uploadPath("acct"), func(w http.ResponseWriter, _ *http.Request) {
		writeCoverageJSON(t, w, signURLResponse{ExternalID: "ext-1", Location: storageURL})
	})
	mux.HandleFunc("/storage/upload", func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "storage down", http.StatusServiceUnavailable)
	})
	mux.HandleFunc(completePath("acct", "ext-1"), func(w http.ResponseWriter, _ *http.Request) {
		completeCalled = true
		w.WriteHeader(http.StatusOK)
	})

	ctx := testCtx(t, server.URL, "acct")
	f := baseUploadFlags()
	f.files = []string{coverFile}

	err := upload(ctx, f)
	if err == nil || !strings.Contains(err.Error(), "upload failed") {
		t.Fatalf("upload() error = %v, want it to contain \"upload failed\"", err)
	}
	if completeCalled {
		t.Error("complete was called despite the storage PUT failing")
	}
}

// TestCoverageUpload_AllCallsUseTheAuthAccountID is a regression test for a
// bug where completeReport keyed its URL off the CI-vendor/flag
// account instead of ctx.Auth.AccountID, diverging from ensure-repo/settings/
// presign. f.accountID is deliberately set to a different value than
// ctx.Auth.AccountID: every handler is registered only under the auth
// account's path, so a stray params.accountID reference 404s.
func TestCoverageUpload_AllCallsUseTheAuthAccountID(t *testing.T) {
	withGitRemote(t, "https://example.com/test-owner/test-repo.git")
	coverFile := uploadTestCoverageFile(t)

	var calls []string
	mux := http.NewServeMux()
	server := httptest.NewServer(mux)
	defer server.Close()
	storageURL := server.URL + "/storage/upload"

	mux.HandleFunc(covclient.EnsureRepoPath("auth-acct"), func(w http.ResponseWriter, _ *http.Request) {
		calls = append(calls, "ensure")
		w.WriteHeader(http.StatusCreated)
		writeCoverageJSON(t, w, covclient.EnsureRepoResponse{Repository: &covclient.Repository{ID: 7}})
	})
	mux.HandleFunc(covclient.RepoSettingsPath("auth-acct", 7), func(w http.ResponseWriter, _ *http.Request) {
		calls = append(calls, "settings")
		writeCoverageJSON(t, w, covclient.RepoSettings{})
	})
	mux.HandleFunc(uploadPath("auth-acct"), func(w http.ResponseWriter, _ *http.Request) {
		calls = append(calls, "presign")
		writeCoverageJSON(t, w, signURLResponse{ExternalID: "ext-1", Location: storageURL})
	})
	mux.HandleFunc("/storage/upload", func(w http.ResponseWriter, _ *http.Request) {
		calls = append(calls, "put")
	})
	mux.HandleFunc(completePath("auth-acct", "ext-1"), func(w http.ResponseWriter, _ *http.Request) {
		calls = append(calls, "complete")
		w.WriteHeader(http.StatusOK)
	})

	ctx := testCtx(t, server.URL, "auth-acct")
	f := baseUploadFlags()
	f.accountID = "flag-acct"
	f.files = []string{coverFile}

	if err := upload(ctx, f); err != nil {
		t.Fatalf("upload() error = %v (a stray params.accountID reference 404s against this server)", err)
	}

	want := []string{"ensure", "settings", "presign", "put", "complete"}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("call sequence = %v, want %v", calls, want)
	}
}

// TestCoverageUpload_MissingCoverageFileNeverCallsNetwork confirms the
// coverage-file lookup failure is caught before any coverage-service call,
// mirroring HCli's TestUpload_MissingCoverageFile.
func TestCoverageUpload_MissingCoverageFileNeverCallsNetwork(t *testing.T) {
	withGitRemote(t, "https://example.com/test-owner/test-repo.git")

	var networkCalled bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		networkCalled = true
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	ctx := testCtx(t, server.URL, "acct")
	f := baseUploadFlags()
	f.files = []string{filepath.Join(t.TempDir(), "does-not-exist.out")}

	if err := upload(ctx, f); err == nil {
		t.Fatal("upload() expected an error for a missing coverage file")
	}
	if networkCalled {
		t.Error("a network call was made despite the coverage file lookup failing")
	}
}

func writeCoverageJSON(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Fatalf("encode response: %v", err)
	}
}

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
