// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package aievals

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/harness/cli/v3/pkg/auth"
	"github.com/harness/cli/v3/pkg/cmdctx"
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

func TestCollectUploadFilesStaysPinnedAfterRootPathReplacement(t *testing.T) {
	root := filepath.Join(t.TempDir(), "selected")
	const captured = "identifier: original\n"
	writeUploadFile(t, filepath.Join(root, "agents", "pinned", "agent.yaml"), captured)

	pinnedRoot, err := os.OpenRoot(root)
	if err != nil {
		t.Fatalf("opening fixture root: %v", err)
	}
	defer pinnedRoot.Close()
	movedRoot := root + "-original"
	if err := os.Rename(root, movedRoot); err != nil {
		t.Skipf("cannot rename an open directory on this platform: %v", err)
	}
	writeUploadFile(t, filepath.Join(root, "agents", "pinned", "agent.yaml"), "identifier: replacement\n")

	files, err := collectUploadFilesFromRoot(pinnedRoot, DefaultLimits())
	if err != nil {
		t.Fatalf("collectUploadFilesFromRoot() error = %v", err)
	}
	if len(files) != 1 || files[0].archivePath != ".harness/evals/agents/pinned/agent.yaml" || string(files[0].data) != captured {
		t.Fatalf("collected files = %#v, want the original pinned-root snapshot", files)
	}
}

func TestBuildArchiveCompressionStaysWithinAPIRatio(t *testing.T) {
	root := t.TempDir()
	writeUploadFile(t, filepath.Join(root, "agents", "tiny", "agent.yaml"), "identifier: tiny\n")
	writeUploadFile(t, filepath.Join(root, "agents", "ratio", "agent.yaml"), "identifier: ratio\n#"+strings.Repeat("x", 128*1024)+"\n")
	var dataset strings.Builder
	for index := 0; index < 2000; index++ {
		fmt.Fprintf(&dataset, "{\"value\":%d}\n", index)
	}
	writeUploadFile(t, filepath.Join(root, "datasets", "values.jsonl"), dataset.String())

	payload, err := BuildArchive(root, DefaultLimits())
	if err != nil {
		t.Fatalf("BuildArchive() error = %v", err)
	}
	archive, err := zip.NewReader(bytes.NewReader(payload), int64(len(payload)))
	if err != nil {
		t.Fatalf("reading archive: %v", err)
	}
	methods := make(map[string]uint16, len(archive.File))
	var expanded uint64
	for _, member := range archive.File {
		methods[member.Name] = member.Method
		expanded += member.UncompressedSize64
	}
	if methods[".harness/evals/agents/tiny/agent.yaml"] != zip.Store {
		t.Errorf("small member compression method = %d, want Store when compression is not beneficial", methods[".harness/evals/agents/tiny/agent.yaml"])
	}
	if methods[".harness/evals/agents/ratio/agent.yaml"] != zip.Deflate {
		t.Errorf("high-ratio member method = %d, want Deflate because the aggregate archive is within the API ratio cap", methods[".harness/evals/agents/ratio/agent.yaml"])
	}
	if methods[".harness/evals/datasets/values.jsonl"] != zip.Deflate {
		t.Errorf("compressible member method = %d, want Deflate", methods[".harness/evals/datasets/values.jsonl"])
	}
	if expanded > uint64(len(payload))*MaxCompressionRatio {
		t.Fatalf("archive expands to %d bytes from %d compressed bytes, exceeding ratio %d", expanded, len(payload), MaxCompressionRatio)
	}
}

func TestBuildArchiveFallsBackOnlySmallestMembersWhenAggregateRatioExceedsLimit(t *testing.T) {
	root := t.TempDir()
	writeUploadFile(t, filepath.Join(root, "agents", "large", "agent.yaml"), "identifier: large\n#"+strings.Repeat("x", 128*1024)+"\n")
	writeUploadFile(t, filepath.Join(root, "agents", "small", "agent.yaml"), "identifier: small\n#"+strings.Repeat("y", 8*1024)+"\n")

	payload, err := BuildArchive(root, DefaultLimits())
	if err != nil {
		t.Fatalf("BuildArchive() error = %v", err)
	}
	archive, err := zip.NewReader(bytes.NewReader(payload), int64(len(payload)))
	if err != nil {
		t.Fatalf("reading archive: %v", err)
	}
	methods := make(map[string]uint16, len(archive.File))
	var expanded uint64
	for _, member := range archive.File {
		methods[member.Name] = member.Method
		expanded += member.UncompressedSize64
	}
	if methods[".harness/evals/agents/small/agent.yaml"] != zip.Store {
		t.Errorf("small member method = %d, want Store to bring the aggregate ratio under the API cap", methods[".harness/evals/agents/small/agent.yaml"])
	}
	if methods[".harness/evals/agents/large/agent.yaml"] != zip.Deflate {
		t.Errorf("large member method = %d, want Deflate to preserve useful compression", methods[".harness/evals/agents/large/agent.yaml"])
	}
	if expanded > uint64(len(payload))*MaxCompressionRatio {
		t.Fatalf("archive expands to %d bytes from %d compressed bytes, exceeding ratio %d", expanded, len(payload), MaxCompressionRatio)
	}
}

func TestFormatArchiveImportAllowsPasswordResetIdentifier(t *testing.T) {
	const (
		agentID = "11111111-1111-4111-8111-111111111111"
		evalID  = "22222222-2222-4222-8222-222222222222"
	)
	a := &auth.ResolvedAuth{APIUrl: "https://example.test", AccountID: "acct", OrgID: "org", ProjectID: "project"}
	body, err := json.Marshal(archiveImportResponse{
		Agents: []archiveImportedResource{{
			Identifier: "password-reset",
			ID:         agentID,
			UIPath:     "/account/acct/all/ai-evals/orgs/org/projects/project/targets/" + agentID,
		}},
		Evals: []archiveImportedResource{{
			Identifier: "eval-one",
			ID:         evalID,
			UIPath:     "/account/acct/all/ai-evals/orgs/org/projects/project/evals/" + evalID,
		}},
	})
	if err != nil {
		t.Fatalf("marshaling response: %v", err)
	}
	text, err := formatArchiveImport(a, body)
	if err != nil {
		t.Fatalf("formatArchiveImport() error = %v", err)
	}
	if !strings.Contains(text, "Agent password-reset ") {
		t.Fatalf("formatted response = %q, missing password-reset identifier", text)
	}
}

func TestFormatArchiveImportRejectsSecretShapedIdentifier(t *testing.T) {
	const (
		agentID = "11111111-1111-4111-8111-111111111111"
		evalID  = "22222222-2222-4222-8222-222222222222"
	)
	secretShapedIdentifier := "sk-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	a := &auth.ResolvedAuth{APIUrl: "https://example.test", AccountID: "acct", OrgID: "org", ProjectID: "project"}
	body, err := json.Marshal(archiveImportResponse{
		Agents: []archiveImportedResource{{
			Identifier: secretShapedIdentifier,
			ID:         agentID,
			UIPath:     "/account/acct/all/ai-evals/orgs/org/projects/project/targets/" + agentID,
		}},
		Evals: []archiveImportedResource{{
			Identifier: "eval-one",
			ID:         evalID,
			UIPath:     "/account/acct/all/ai-evals/orgs/org/projects/project/evals/" + evalID,
		}},
	})
	if err != nil {
		t.Fatalf("marshaling response: %v", err)
	}
	_, err = formatArchiveImport(a, body)
	if err == nil || !strings.Contains(err.Error(), "invalid resource identity") {
		t.Fatalf("formatArchiveImport() error = %v, want secret-shaped identifier rejection", err)
	}
}

func TestBuildValidatedUploadArchivePropagatesRunnerVerifyCause(t *testing.T) {
	root := t.TempDir()
	writeUploadFile(t, filepath.Join(root, "agents", "verify", "agent.yaml"), "identifier: verify\n")
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	cause := errors.New("verification canceled by caller")

	_, err := buildValidatedUploadArchive(ctx, root, DefaultLimits(), "runner-home", func(string, CommandFunc) (RunnerLayout, error) {
		cancel(cause)
		return RunnerLayout{}, errors.New("pinned runner is unavailable")
	}, nil)
	if !errors.Is(err, cause) {
		t.Fatalf("buildValidatedUploadArchive() error = %v, want context cause", err)
	}
}

func TestBuildValidatedUploadArchivePreservesRunnerVerifyError(t *testing.T) {
	root := t.TempDir()
	writeUploadFile(t, filepath.Join(root, "agents", "verify", "agent.yaml"), "identifier: verify\n")
	want := errors.New("pinned runner is unavailable")

	_, err := buildValidatedUploadArchive(context.Background(), root, DefaultLimits(), "runner-home", func(string, CommandFunc) (RunnerLayout, error) {
		return RunnerLayout{}, want
	}, nil)
	if !errors.Is(err, want) {
		t.Fatalf("buildValidatedUploadArchive() error = %v, want the sanitized runner error", err)
	}
}

func TestBuildValidatedUploadArchivePropagatesValidatorCause(t *testing.T) {
	root := t.TempDir()
	writeUploadFile(t, filepath.Join(root, "agents", "validator", "agent.yaml"), "identifier: validator\n")
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	cause := errors.New("validation canceled by caller")
	layout := RunnerLayout{Python: filepath.Join(t.TempDir(), "python")}

	_, err := buildValidatedUploadArchive(ctx, root, DefaultLimits(), "runner-home", func(string, CommandFunc) (RunnerLayout, error) {
		return layout, nil
	}, func(string, []string, []string, string) (CommandResult, error) {
		cancel(cause)
		return CommandResult{}, errors.New("validator process stopped")
	})
	if !errors.Is(err, cause) {
		t.Fatalf("buildValidatedUploadArchive() error = %v, want context cause", err)
	}
}

func TestUploadArchivePropagatesRequestContextCause(t *testing.T) {
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	cause := errors.New("upload canceled by caller")
	a := &auth.ResolvedAuth{APIUrl: "https://example.test", AccountID: "acct", OrgID: "org", ProjectID: "project"}
	_, err := UploadArchiveContext(ctx, a, []byte("zip"), "", func(*http.Request) (*http.Response, error) {
		cancel(cause)
		return nil, errors.New("request interrupted")
	})
	if !errors.Is(err, cause) {
		t.Fatalf("UploadArchiveContext() error = %v, want context cause", err)
	}
}

func TestUploadArchiveClosesResponseBodyBeforeReturningContextCause(t *testing.T) {
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	cause := errors.New("upload canceled by caller")
	a := &auth.ResolvedAuth{APIUrl: "https://example.test", AccountID: "acct", OrgID: "org", ProjectID: "project"}
	body := &closeTrackingBody{}
	_, err := UploadArchiveContext(ctx, a, []byte("zip"), "", func(*http.Request) (*http.Response, error) {
		cancel(cause)
		return &http.Response{StatusCode: http.StatusOK, Body: body, Header: make(http.Header)}, nil
	})
	if !errors.Is(err, cause) {
		t.Fatalf("UploadArchiveContext() error = %v, want context cause", err)
	}
	if !body.closed {
		t.Fatal("UploadArchiveContext() returned the cancellation cause without closing the response body")
	}
}

type closeTrackingBody struct {
	closed bool
}

func (*closeTrackingBody) Read([]byte) (int, error) { return 0, io.EOF }

func (body *closeTrackingBody) Close() error {
	body.closed = true
	return nil
}

func TestUploadArchivePropagatesResponseReadContextCause(t *testing.T) {
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	cause := errors.New("response read canceled by caller")
	a := &auth.ResolvedAuth{APIUrl: "https://example.test", AccountID: "acct", OrgID: "org", ProjectID: "project"}
	_, err := UploadArchiveContext(ctx, a, []byte("zip"), "", func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       cancelOnReadCloser{cancel: cancel, cause: cause},
			Header:     make(http.Header),
		}, nil
	})
	if !errors.Is(err, cause) {
		t.Fatalf("UploadArchiveContext() error = %v, want body-read context cause", err)
	}
}

type cancelOnReadCloser struct {
	cancel context.CancelCauseFunc
	cause  error
}

func (body cancelOnReadCloser) Read([]byte) (int, error) {
	body.cancel(body.cause)
	return 0, errors.New("response read interrupted")
}

func (cancelOnReadCloser) Close() error { return nil }

func TestUploadWorkflowRejectsPositionalID(t *testing.T) {
	err := uploadWorkflow(&cmdctx.Ctx{Id: "unexpected-id"})
	if err == nil || !strings.Contains(err.Error(), "--root") {
		t.Fatalf("uploadWorkflow() error = %v, want guidance to use --root", err)
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
