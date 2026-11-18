// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package aievals

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/harness/cli/v3/pkg/auth"
)

func TestBuildArchiveUsesExactRootAndCanonicalPaths(t *testing.T) {
	repo := t.TempDir()
	root := filepath.Join(repo, "selected", "tree")
	writeUploadFile(t, filepath.Join(root, "agents", "chosen", "agent.yaml"), "identifier: chosen\n")
	writeUploadFile(t, filepath.Join(root, "datasets", "cases.jsonl"), `{"input":"selected"}`+"\n")
	writeUploadFile(t, filepath.Join(repo, ".harness", "evals", "agents", "stale", "agent.yaml"), "identifier: stale\n")

	first, err := BuildArchive(root, DefaultLimits())
	if err != nil {
		t.Fatalf("BuildArchive() error = %v", err)
	}
	second, err := BuildArchive(root, DefaultLimits())
	if err != nil {
		t.Fatalf("second BuildArchive() error = %v", err)
	}
	if !bytes.Equal(first, second) {
		t.Fatal("BuildArchive() produced different bytes for the same tree")
	}

	archive, err := zip.NewReader(bytes.NewReader(first), int64(len(first)))
	if err != nil {
		t.Fatalf("reading archive: %v", err)
	}
	if len(archive.File) != 2 {
		t.Fatalf("archive contains %d files, want exactly the selected root's 2 files", len(archive.File))
	}
	want := map[string]string{
		".harness/evals/agents/chosen/agent.yaml": "identifier: chosen\n",
		".harness/evals/datasets/cases.jsonl":     `{"input":"selected"}` + "\n",
	}
	for _, member := range archive.File {
		data, err := member.Open()
		if err != nil {
			t.Fatalf("opening %q: %v", member.Name, err)
		}
		got, err := io.ReadAll(data)
		_ = data.Close()
		if err != nil {
			t.Fatalf("reading %q: %v", member.Name, err)
		}
		if string(got) != want[member.Name] {
			t.Errorf("archive member %q content = %q, want %q", member.Name, got, want[member.Name])
		}
		delete(want, member.Name)
	}
	if len(want) != 0 {
		t.Fatalf("archive is missing canonical paths: %v", want)
	}
}

func TestUploadValidatorUsesCollectedSnapshotAndPinnedArgs(t *testing.T) {
	root := t.TempDir()
	agentPath := filepath.Join(root, "agents", "captured", "agent.yaml")
	const captured = "identifier: captured\n"
	writeUploadFile(t, agentPath, captured)

	layout := RunnerLayout{Python: filepath.Join(t.TempDir(), "bin", "python")}
	runnerHome := filepath.Join(t.TempDir(), "pinned-runner")
	verifyCalled, validatorCalled := false, false
	verify := func(home string, run CommandFunc) (RunnerLayout, error) {
		verifyCalled = true
		if home != runnerHome || run == nil {
			t.Fatalf("VerifyRunner arguments = (%q, nil=%t), want pinned home and command", home, run == nil)
		}
		writeUploadFile(t, agentPath, "schema_version: invalid-after-collection\n")
		return layout, nil
	}
	run := func(name string, args []string, env []string, dir string) (CommandResult, error) {
		validatorCalled = true
		validatorArgs := ValidatorArgs(layout.Python, filepath.Join(dir, "repo", ".harness", "evals"))
		if name != validatorArgs[0] || !reflect.DeepEqual(args, validatorArgs[1:]) || len(env) != 0 {
			t.Fatalf("validator invocation = (%q, %v, %v), want pinned ValidatorArgs", name, args, env)
		}
		staged, err := os.ReadFile(filepath.Join(dir, "repo", ".harness", "evals", "agents", "captured", "agent.yaml"))
		if err != nil || string(staged) != captured {
			t.Fatalf("validator snapshot = %q, error %v; want the collected bytes", staged, err)
		}
		return CommandResult{}, nil
	}
	payload, err := buildValidatedUploadArchive(context.Background(), root, DefaultLimits(), runnerHome, verify, run)
	if err != nil {
		t.Fatalf("buildValidatedUploadArchive() error = %v", err)
	}
	if !verifyCalled || !validatorCalled {
		t.Fatalf("verification and validation calls = (%t, %t), want both", verifyCalled, validatorCalled)
	}
	archive, err := zip.NewReader(bytes.NewReader(payload), int64(len(payload)))
	if err != nil {
		t.Fatalf("reading archive: %v", err)
	}
	member, err := archive.File[0].Open()
	if err != nil {
		t.Fatalf("opening captured archive member: %v", err)
	}
	archived, err := io.ReadAll(member)
	_ = member.Close()
	if err != nil || string(archived) != captured {
		t.Fatalf("archived snapshot = %q, error %v; want the validated bytes", archived, err)
	}
}

func TestBuildArchiveSkipsSymlinks(t *testing.T) {
	root := t.TempDir()
	writeUploadFile(t, filepath.Join(root, "agents", "safe", "agent.yaml"), "identifier: safe\n")
	external := filepath.Join(t.TempDir(), "outside.yaml")
	writeUploadFile(t, external, "identifier: outside\n")
	if err := os.Symlink(external, filepath.Join(root, "agents", "safe", "linked.yaml")); err != nil {
		t.Skipf("symlinks are not available: %v", err)
	}

	payload, err := BuildArchive(root, DefaultLimits())
	if err != nil {
		t.Fatalf("BuildArchive() error = %v", err)
	}
	archive, err := zip.NewReader(bytes.NewReader(payload), int64(len(payload)))
	if err != nil {
		t.Fatalf("reading archive: %v", err)
	}
	if len(archive.File) != 1 || archive.File[0].Name != ".harness/evals/agents/safe/agent.yaml" {
		t.Fatalf("archive members = %v, want only the regular agent file", archiveMemberNames(archive.File))
	}
	rootLink := filepath.Join(t.TempDir(), "tree-link")
	if err := os.Symlink(root, rootLink); err != nil {
		t.Fatalf("creating root symlink: %v", err)
	}
	if _, err := BuildArchive(rootLink, DefaultLimits()); err == nil {
		t.Fatal("BuildArchive() accepted a symlink as the supplied root")
	}
}

func TestUploadArchiveUsesMultipartScopeContextAndSafeLinks(t *testing.T) {
	const (
		accountID = "acct"
		orgID     = "org"
		projectID = "project"
		apiToken  = "pat.acct.test.secret"
		agentID   = "11111111-1111-4111-8111-111111111111"
		evalID    = "22222222-2222-4222-8222-222222222222"
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/gateway/ai-evals/api/v1/orgs/org/projects/project/evals/import-archive" {
			t.Errorf("request target = %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Harness-Account") != accountID || r.Header.Get("x-api-key") != apiToken {
			t.Errorf("scoped authentication headers are missing")
		}
		reader, err := r.MultipartReader()
		if err != nil {
			t.Errorf("request is not multipart: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		part, err := reader.NextPart()
		if err != nil {
			t.Errorf("reading multipart part: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if part.FormName() != "archive" || part.FileName() != "evals.zip" || part.Header.Get("Content-Type") != "application/zip" {
			t.Errorf("multipart file part = name %q, filename %q, content type %q", part.FormName(), part.FileName(), part.Header.Get("Content-Type"))
		}
		if body, err := io.ReadAll(part); err != nil || string(body) != "zip-bytes" {
			t.Errorf("multipart archive body = %q, error %v", body, err)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"agents": []any{map[string]string{
				"identifier": "agent-one",
				"id":         agentID,
				"ui_path":    "/account/acct/all/ai-evals/orgs/org/projects/project/targets/" + agentID,
			}},
			"evals": []any{map[string]string{
				"identifier": "eval-one",
				"id":         evalID,
				"ui_path":    "/account/acct/all/ai-evals/orgs/org/projects/project/evals/" + evalID,
			}},
		})
	}))
	defer server.Close()

	type contextKey struct{}
	ctx := context.WithValue(context.Background(), contextKey{}, "upload-context")
	resolved := &auth.ResolvedAuth{
		APIUrl:    server.URL,
		AccountID: accountID,
		OrgID:     orgID,
		ProjectID: projectID,
		PATToken:  apiToken,
	}
	var sawContext bool
	text, err := UploadArchiveContext(ctx, resolved, []byte("zip-bytes"), "", func(req *http.Request) (*http.Response, error) {
		sawContext = req.Context().Value(contextKey{}) == "upload-context"
		return http.DefaultClient.Do(req)
	})
	if err != nil {
		t.Fatalf("UploadArchiveContext() error = %v", err)
	}
	if !sawContext {
		t.Fatal("upload request did not carry the caller's context")
	}
	for _, expected := range []string{
		"Agent agent-one " + server.URL + "/account/acct/all/ai-evals/orgs/org/projects/project/targets/" + agentID,
		"Eval eval-one " + server.URL + "/account/acct/all/ai-evals/orgs/org/projects/project/evals/" + evalID,
	} {
		if !strings.Contains(text, expected) {
			t.Errorf("upload output %q does not contain scoped link %q", text, expected)
		}
	}
}

func TestUploadArchiveRejectsOutOfScopeResponsePath(t *testing.T) {
	const (
		accountID = "acct"
		orgID     = "org"
		projectID = "project"
		resource  = "11111111-1111-4111-8111-111111111111"
	)
	response := `{"agents":[{"identifier":"agent-one","id":"` + resource + `","ui_path":"/account/other/all/ai-evals/orgs/org/projects/project/targets/` + resource + `"}],"evals":[{"identifier":"eval-one","id":"22222222-2222-4222-8222-222222222222","ui_path":"/account/acct/all/ai-evals/orgs/org/projects/project/evals/22222222-2222-4222-8222-222222222222"}]}`
	resolved := &auth.ResolvedAuth{APIUrl: "https://example.test", AccountID: accountID, OrgID: orgID, ProjectID: projectID}
	_, err := UploadArchiveWithBasePath(resolved, []byte("zip"), "", func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusCreated,
			Body:       io.NopCloser(strings.NewReader(response)),
			Header:     make(http.Header),
		}, nil
	})
	if err == nil || !strings.Contains(err.Error(), "outside the active scope") {
		t.Fatalf("UploadArchiveWithBasePath() error = %v, want out-of-scope rejection", err)
	}
}

func TestUploadArchiveDoesNotFollowRedirectsOrEchoResponseSecrets(t *testing.T) {
	const token = "pat.acct.test.secret"
	var redirectedRequests int
	destination := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		redirectedRequests++
	}))
	defer destination.Close()
	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, destination.URL, http.StatusFound)
	}))
	defer redirector.Close()
	resolved := &auth.ResolvedAuth{
		APIUrl:    redirector.URL,
		AccountID: "acct",
		OrgID:     "org",
		ProjectID: "project",
		PATToken:  token,
	}
	_, err := UploadArchiveContext(context.Background(), resolved, []byte("zip"), "", nil)
	if err == nil || !strings.Contains(err.Error(), "HTTP 302") {
		t.Fatalf("UploadArchiveContext() error = %v, want HTTP 302", err)
	}
	if redirectedRequests != 0 {
		t.Fatalf("redirect target received %d requests", redirectedRequests)
	}

	resolved.APIUrl = "https://example.test"
	secretBody := `{"message":"` + token + `"}`
	_, err = UploadArchiveWithBasePath(resolved, []byte("zip"), "", func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusForbidden,
			Body:       io.NopCloser(strings.NewReader(secretBody)),
			Header:     make(http.Header),
		}, nil
	})
	if err == nil || strings.Contains(err.Error(), token) {
		t.Fatalf("non-2xx response error leaked response content or token: %v", err)
	}
}

func writeUploadFile(t *testing.T, name, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func archiveMemberNames(files []*zip.File) []string {
	names := make([]string, len(files))
	for index, file := range files {
		names[index] = file.Name
	}
	return names
}
