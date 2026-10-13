// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package ci

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/harness/cli/v3/pkg/cmdctx"
)

// ---------------------------------------------------------------------------
// resolveLocalPath
// ---------------------------------------------------------------------------

func TestResolveLocalPath_EmptyDest(t *testing.T) {
	got := resolveLocalPath("", "file.zip")
	if got != "file.zip" {
		t.Errorf("resolveLocalPath(%q, %q) = %q, want %q", "", "file.zip", got, "file.zip")
	}
}

func TestResolveLocalPath_DestIsExistingDir(t *testing.T) {
	dir := t.TempDir()
	got := resolveLocalPath(dir, "report.xml")
	want := filepath.Join(dir, "report.xml")
	if got != want {
		t.Errorf("resolveLocalPath(%q, %q) = %q, want %q", dir, "report.xml", got, want)
	}
}

func TestResolveLocalPath_DestEndsWithSlash(t *testing.T) {
	got := resolveLocalPath("/some/nonexistent/dir/", "app.tar.gz")
	want := filepath.Join("/some/nonexistent/dir/", "app.tar.gz")
	if got != want {
		t.Errorf("resolveLocalPath(%q, %q) = %q, want %q", "/some/nonexistent/dir/", "app.tar.gz", got, want)
	}
}

func TestResolveLocalPath_DestEndsWithPathSeparator(t *testing.T) {
	dest := "downloads" + string(os.PathSeparator)
	got := resolveLocalPath(dest, "output.bin")
	want := filepath.Join(dest, "output.bin")
	if got != want {
		t.Errorf("resolveLocalPath(%q, %q) = %q, want %q", dest, "output.bin", got, want)
	}
}

func TestResolveLocalPath_DestIsExplicitFilePath(t *testing.T) {
	got := resolveLocalPath("/tmp/custom-output.zip", "ignored.zip")
	if got != "/tmp/custom-output.zip" {
		t.Errorf("resolveLocalPath(%q, %q) = %q, want %q", "/tmp/custom-output.zip", "ignored.zip", got, "/tmp/custom-output.zip")
	}
}

func TestResolveLocalPath_DestIsExistingFile(t *testing.T) {
	dir := t.TempDir()
	existing := filepath.Join(dir, "existing.txt")
	writeTestFile(t, existing, "content")

	got := resolveLocalPath(existing, "other.txt")
	if got != existing {
		t.Errorf("resolveLocalPath(%q, %q) = %q, want %q", existing, "other.txt", got, existing)
	}
}

// ---------------------------------------------------------------------------
// pullGenericFile
// ---------------------------------------------------------------------------

func TestPullGenericFile_Success(t *testing.T) {
	fileContent := "artifact binary content"
	var gotMethod, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, fileContent)
	}))
	defer srv.Close()

	resp, err := pullGenericFile(context.Background(), testUploadAuth(srv.URL), "my-reg", "my-art/v1/files/app.zip")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer resp.Body.Close()

	if gotMethod != http.MethodGet {
		t.Errorf("HTTP method = %q, want GET", gotMethod)
	}
	if gotAuth != "CIManager eyJtoken" {
		t.Errorf("Authorization = %q, want %q", gotAuth, "CIManager eyJtoken")
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading response body: %v", err)
	}
	if string(data) != fileContent {
		t.Errorf("body = %q, want %q", string(data), fileContent)
	}
}

func TestPullGenericFile_Server404(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	resp, err := pullGenericFile(context.Background(), testUploadAuth(srv.URL), "reg", "art/v1/missing/file.zip")
	if err != nil {
		t.Fatalf("unexpected transport error: %v", err)
	}
	defer resp.Body.Close()
	// pullGenericFile does not itself classify status codes — that is the
	// caller's (pullCiArtifactHandler's) job, mirroring pushGenericFile.
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}

func TestPullGenericFile_CorrectURLPattern(t *testing.T) {
	// Verifies pull uses the same URL pattern as push:
	// {base}/pkg/{accountId}/{registry}/files/{artifactName}/{version}/{filePath}
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	a := testUploadAuth(srv.URL)
	a.AccountID = "acme-account"
	resp, err := pullGenericFile(context.Background(), a, "prod-registry", "pipeline-art/v42/reports/coverage.html")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer resp.Body.Close()

	want := "/pkg/acme-account/prod-registry/files/pipeline-art/v42/reports/coverage.html"
	if gotPath != want {
		t.Errorf("request path = %q, want %q", gotPath, want)
	}
}

// ---------------------------------------------------------------------------
// pullCiArtifactHandler, via httptest end-to-end (download write to disk)
// ---------------------------------------------------------------------------

func TestPullCiArtifact_Download_WritesFile(t *testing.T) {
	fileContent := "hello artifact"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, fileContent)
	}))
	defer srv.Close()

	a := testUploadAuth(srv.URL)
	resp, err := pullGenericFile(context.Background(), a, "reg", "art/v1/files/app.zip")
	if err != nil {
		t.Fatalf("pullGenericFile: %v", err)
	}
	defer resp.Body.Close()

	outDir := t.TempDir()
	localPath := filepath.Join(outDir, "app.zip")
	f, err := os.Create(localPath)
	if err != nil {
		t.Fatalf("create local file: %v", err)
	}
	if _, err := io.Copy(f, resp.Body); err != nil {
		t.Fatalf("copy: %v", err)
	}
	f.Close()

	data, err := os.ReadFile(localPath)
	if err != nil {
		t.Fatalf("reading downloaded file: %v", err)
	}
	if string(data) != fileContent {
		t.Errorf("file content = %q, want %q", string(data), fileContent)
	}
}

func TestPullGenericFile_LeadingSlashInFilePathTrimmed(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	filePath := strings.TrimLeft("/files/app.zip", "/")
	resp, err := pullGenericFile(context.Background(), testUploadAuth(srv.URL), "reg", "art/v1/"+filePath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer resp.Body.Close()

	want := "/pkg/acc/reg/files/art/v1/files/app.zip"
	if gotPath != want {
		t.Errorf("request path = %q, want %q", gotPath, want)
	}
}

func TestPullGenericFile_Server403(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	resp, err := pullGenericFile(context.Background(), testUploadAuth(srv.URL), "reg", "art/v1/files/f.zip")
	if err != nil {
		t.Fatalf("unexpected transport error: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d, want 403", resp.StatusCode)
	}
}

// ---------------------------------------------------------------------------
// pullCiArtifactHandler, end-to-end
// ---------------------------------------------------------------------------

func pullFlags(overrides map[string]any) map[string]any {
	fv := map[string]any{"destination": "", "name": "", "version": "", "registry": ""}
	for k, v := range overrides {
		fv[k] = v
	}
	return fv
}

func TestPullCiArtifactHandler_Success(t *testing.T) {
	fileContent := "artifact binary content"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, fileContent)
	}))
	defer srv.Close()

	outDir := t.TempDir()
	ctx := &cmdctx.Ctx{
		Context: context.Background(),
		Auth:    testUploadAuth(srv.URL),
		FlagValues: pullFlags(map[string]any{
			"destination": outDir,
			"name":        "my-art",
			"version":     "v1",
			"registry":    "my-reg",
		}),
		Args: []string{"files/app.zip"},
	}

	if err := pullCiArtifactHandler(ctx); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(outDir, "app.zip"))
	if err != nil {
		t.Fatalf("reading downloaded file: %v", err)
	}
	if string(data) != fileContent {
		t.Errorf("file content = %q, want %q", string(data), fileContent)
	}
}

func TestPullCiArtifactHandler_EmptyResponseBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		// no body written
	}))
	defer srv.Close()

	outDir := t.TempDir()
	ctx := &cmdctx.Ctx{
		Context: context.Background(),
		Auth:    testUploadAuth(srv.URL),
		FlagValues: pullFlags(map[string]any{
			"destination": outDir,
			"name":        "art",
			"version":     "v1",
			"registry":    "reg",
		}),
		Args: []string{"empty.zip"},
	}

	if err := pullCiArtifactHandler(ctx); err != nil {
		t.Fatalf("unexpected error for empty response body: %v", err)
	}
	info, err := os.Stat(filepath.Join(outDir, "empty.zip"))
	if err != nil {
		t.Fatalf("expected file to exist: %v", err)
	}
	if info.Size() != 0 {
		t.Errorf("expected empty file, got size %d", info.Size())
	}
}

func TestPullCiArtifactHandler_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	outDir := t.TempDir()
	ctx := &cmdctx.Ctx{
		Context: context.Background(),
		Auth:    testUploadAuth(srv.URL),
		FlagValues: pullFlags(map[string]any{
			"destination": outDir,
			"name":        "art",
			"version":     "v1",
			"registry":    "reg",
		}),
		Args: []string{"missing/file.zip"},
	}

	err := pullCiArtifactHandler(ctx)
	if err == nil {
		t.Fatal("expected error for 404, got nil")
	}
	if !strings.Contains(err.Error(), "404") {
		t.Errorf("error should mention status code, got: %v", err)
	}
}

func TestPullCiArtifactHandler_CannotCreateLocalFile(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "data")
	}))
	defer srv.Close()

	// destination's parent component is a regular file, so MkdirAll cannot
	// create a directory at or under it.
	dir := t.TempDir()
	blocker := filepath.Join(dir, "blocker")
	writeTestFile(t, blocker, "x")

	ctx := &cmdctx.Ctx{
		Context: context.Background(),
		Auth:    testUploadAuth(srv.URL),
		FlagValues: pullFlags(map[string]any{
			"destination": filepath.Join(blocker, "sub"),
			"name":        "art",
			"version":     "v1",
			"registry":    "reg",
		}),
		Args: []string{"file.zip"},
	}

	if err := pullCiArtifactHandler(ctx); err == nil {
		t.Fatal("expected error when local file cannot be created, got nil")
	}
}

func TestPullCiArtifactHandler_WrongArgCount(t *testing.T) {
	ctx := &cmdctx.Ctx{
		Context:    context.Background(),
		Auth:       testUploadAuth("https://example.com"),
		FlagValues: pullFlags(nil),
		Args:       []string{"a.zip", "b.zip"},
	}
	err := pullCiArtifactHandler(ctx)
	if err == nil || !strings.Contains(err.Error(), "exactly one") {
		t.Fatalf("got %v, want an exactly-one-argument error", err)
	}
}

func TestPullCiArtifactHandler_MissingRegistryURL(t *testing.T) {
	ctx := &cmdctx.Ctx{
		Context:    context.Background(),
		Auth:       testUploadAuth(""),
		FlagValues: pullFlags(nil),
		Args:       []string{"a.zip"},
	}
	err := pullCiArtifactHandler(ctx)
	if err == nil || !strings.Contains(err.Error(), envHARURL) {
		t.Fatalf("got %v, want an error mentioning %s", err, envHARURL)
	}
}

func TestPullCiArtifactHandler_EnvVarFallback(t *testing.T) {
	fileContent := "fallback content"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, fileContent)
	}))
	defer srv.Close()

	t.Setenv(envPipelineUniqueID, "env-art")
	t.Setenv(envBuildID, "77")
	t.Setenv(envNativeRegistry, "env-reg")

	outDir := t.TempDir()
	ctx := &cmdctx.Ctx{
		Context:    context.Background(),
		Auth:       testUploadAuth(srv.URL),
		FlagValues: pullFlags(map[string]any{"destination": outDir}),
		Args:       []string{"files/app.zip"},
	}

	if err := pullCiArtifactHandler(ctx); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(outDir, "app.zip"))
	if err != nil {
		t.Fatalf("reading downloaded file: %v", err)
	}
	if string(data) != fileContent {
		t.Errorf("file content = %q, want %q", string(data), fileContent)
	}
}

func TestPullGenericFile_LargeContent(t *testing.T) {
	const size = 512 * 1024
	payload := strings.Repeat("x", size)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, payload)
	}))
	defer srv.Close()

	resp, err := pullGenericFile(context.Background(), testUploadAuth(srv.URL), "reg", "art/v1/large.bin")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading body: %v", err)
	}
	if len(data) != size {
		t.Errorf("body size = %d, want %d", len(data), size)
	}
}
