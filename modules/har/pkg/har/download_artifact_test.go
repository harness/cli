// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package har

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/harness/cli/v3/pkg/auth"
	"github.com/harness/cli/v3/pkg/cmdctx"
)

// --- Test helpers ---

func downloadTestAuth(serverURL string) *auth.ResolvedAuth {
	return &auth.ResolvedAuth{
		AuthType:  auth.AuthTypePAT,
		APIUrl:    serverURL,
		AccountID: "acct",
		OrgID:     "org",
		ProjectID: "proj",
		PATToken:  "test-token",
	}
}

func downloadTestCtx(flags map[string]any, serverURL string) *cmdctx.Ctx {
	return &cmdctx.Ctx{
		Context:    context.Background(),
		FlagValues: flags,
		Auth:       downloadTestAuth(serverURL),
	}
}

// downloadTestCtxWithOut is like downloadTestCtx but also sets OutFile on the
// Ctx (the framework populates this from --out; workflow handlers read it
// directly rather than from FlagValues).
func downloadTestCtxWithOut(flags map[string]any, serverURL, outFile string) *cmdctx.Ctx {
	ctx := downloadTestCtx(flags, serverURL)
	ctx.FormatFlags.OutFile = outFile
	return ctx
}

// newDownloadTestServer returns a server that:
//   - POST /har/api/v3/files/search → JSON with each name as a file item whose
//     downloadUrl points back to GET /dl/<name> on this same server
//   - GET /dl/<path> → synthetic content "content:/dl/<path>"
//
// The server is self-referential: it embeds srv.URL into search responses so
// the handler uses download URLs it can actually reach in tests.
func newDownloadTestServer(t *testing.T, names []string) *httptest.Server {
	t.Helper()

	type item struct {
		Name        string `json:"name"`
		Path        string `json:"path"`
		DownloadUrl string `json:"downloadUrl"`
		Size        string `json:"size"`
	}
	// Mirrors ar_v3.ListFilesResponse: items/hasMore are top-level.
	type resp struct {
		Items   []item `json:"items"`
		HasMore bool   `json:"hasMore"`
		Page    int64  `json:"page"`
		Size    int64  `json:"size"`
	}

	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && strings.Contains(r.URL.Path, "files/search") {
			items := make([]item, len(names))
			for i, n := range names {
				items[i] = item{
					Name:        n,
					Path:        n,
					DownloadUrl: srv.URL + "/dl/" + n,
					Size:        "100",
				}
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(resp{Items: items, HasMore: false})
			return
		}
		if r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/dl/") {
			_, _ = w.Write([]byte("content:" + r.URL.Path))
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// --- search wire contract ---

// TestArtifactSearchFiles_WireContract pins the exact request shape against
// ar_v3.SearchRegistryFilesRequest / SearchRegistryFilesV3Params. The registry
// belongs in the body, NOT in a registry_ref query param.
func TestArtifactSearchFiles_WireContract(t *testing.T) {
	var gotBody map[string]any
	var gotQuery url.Values
	var gotPath string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.Query()
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"items":[],"hasMore":false}`))
	}))
	t.Cleanup(srv.Close)

	if _, err := artifactSearchFiles(context.Background(), downloadTestAuth(srv.URL), "my.*regex", "myreg", 50); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if gotPath != "/har/api/v3/files/search" {
		t.Errorf("path = %q, want %q", gotPath, "/har/api/v3/files/search")
	}
	if gotBody["regex"] != "my.*regex" {
		t.Errorf("body.regex = %v, want %q", gotBody["regex"], "my.*regex")
	}
	if gotBody["registry"] != "myreg" {
		t.Errorf("body.registry = %v, want %q", gotBody["registry"], "myreg")
	}
	for k, want := range map[string]string{
		"account_identifier": "acct",
		"page":               "0",
		"size":               "50",
	} {
		if got := gotQuery.Get(k); got != want {
			t.Errorf("query %s = %q, want %q", k, got, want)
		}
	}
	// org_identifier and project_identifier are intentionally not sent: the
	// v3 endpoint has a server-side bug where scoped lookups fail. Ensure
	// they are absent so a refactor doesn't accidentally add them back.
	if gotQuery.Has("org_identifier") {
		t.Error("org_identifier must not be sent until the server-side scoping bug is fixed")
	}
	if gotQuery.Has("project_identifier") {
		t.Error("project_identifier must not be sent until the server-side scoping bug is fixed")
	}
	if gotQuery.Has("registry_ref") {
		t.Error("registry_ref is not a parameter of this endpoint; registry goes in the body")
	}
	if gotQuery.Has("limit") {
		t.Error("page size parameter is 'size', not 'limit'")
	}
}

// TestArtifactSearchFiles_NeverSendsOrgProject verifies that org_identifier and
// project_identifier are never sent (even when populated in auth) because the
// v3 endpoint has a server-side bug where these params cause registry lookups to
// fail. This test documents the intentional deviation from the OpenAPI spec until
// the backend fix lands.
func TestArtifactSearchFiles_NeverSendsOrgProject(t *testing.T) {
	var gotQuery url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"items":[],"hasMore":false}`))
	}))
	t.Cleanup(srv.Close)

	a := &auth.ResolvedAuth{
		AuthType:  auth.AuthTypePAT,
		APIUrl:    srv.URL,
		AccountID: "acct",
		OrgID:     "myorg",
		ProjectID: "myproject",
		PATToken:  "t",
	}
	if _, err := artifactSearchFiles(context.Background(), a, ".*", "myreg", 100); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if gotQuery.Has("org_identifier") {
		t.Errorf("org_identifier must not be sent (server-side scoping bug): got %q", gotQuery.Get("org_identifier"))
	}
	if gotQuery.Has("project_identifier") {
		t.Errorf("project_identifier must not be sent (server-side scoping bug): got %q", gotQuery.Get("project_identifier"))
	}
}

// TestArtifactSearchFiles_PaginatesOnHasMore verifies the v3 paging idiom:
// loop on hasMore, incrementing page, and stop on an empty page.
func TestArtifactSearchFiles_PaginatesOnHasMore(t *testing.T) {
	var pages []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Query().Get("page")
		pages = append(pages, p)
		w.Header().Set("Content-Type", "application/json")
		switch p {
		case "0":
			_, _ = w.Write([]byte(`{"items":[{"path":"a.txt","downloadUrl":"u"}],"hasMore":true}`))
		case "1":
			_, _ = w.Write([]byte(`{"items":[{"path":"b.txt","downloadUrl":"u"}],"hasMore":true}`))
		default:
			// hasMore=true but empty items must still terminate the loop.
			_, _ = w.Write([]byte(`{"items":[],"hasMore":true}`))
		}
	}))
	t.Cleanup(srv.Close)

	got, err := artifactSearchFiles(context.Background(), downloadTestAuth(srv.URL), ".*", "myreg", 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("expected 2 items across pages, got %d: %v", len(got), got)
	}
	if len(pages) != 3 {
		t.Errorf("expected 3 page requests (0,1,2), got %v", pages)
	}
}

// --- isWithinDest ---

func TestIsWithinDest_SafeRelativePath(t *testing.T) {
	if !isWithinDest("/tmp/dest", "art/1.0/file.tar.gz") {
		t.Error("expected safe path to be within dest")
	}
}

func TestIsWithinDest_BareFilename(t *testing.T) {
	if !isWithinDest("/tmp/dest", "file.tar.gz") {
		t.Error("expected bare filename to be within dest")
	}
}

func TestIsWithinDest_TraversalEscapes(t *testing.T) {
	if isWithinDest("/tmp/dest", "../../etc/passwd") {
		t.Error("expected path traversal to be rejected")
	}
}

func TestIsWithinDest_SingleDotDot(t *testing.T) {
	if isWithinDest("/tmp/dest", "../sibling") {
		t.Error("expected single ../ traversal to be rejected")
	}
}

// --- artifactSearchFiles ---

func TestArtifactSearchFiles_ReturnsMatchedItems(t *testing.T) {
	names := []string{"foo/1.0/foo.tar.gz", "bar/2.0/bar.zip"}
	srv := newDownloadTestServer(t, names)

	got, err := artifactSearchFiles(context.Background(), downloadTestAuth(srv.URL), ".*", "myreg", downloadDefaultPageSize)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 results, got %d: %v", len(got), got)
	}
	if got[0].Path != names[0] {
		t.Errorf("path[0]: got %q, want %q", got[0].Path, names[0])
	}
	if got[0].DownloadURL == "" {
		t.Error("expected DownloadURL to be populated from API response")
	}
}

func TestArtifactSearchFiles_EmptyResult(t *testing.T) {
	srv := newDownloadTestServer(t, nil)

	got, err := artifactSearchFiles(context.Background(), downloadTestAuth(srv.URL), "nomatch", "myreg", downloadDefaultPageSize)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected 0 results, got %d", len(got))
	}
}

// --- executeArtifactDownloadHandler ---

func TestExecuteArtifactDownload_DryRun_WritesReportAndDoesNotDownload(t *testing.T) {
	srv := newDownloadTestServer(t, []string{"art/1.0/art.tar.gz"})
	dest := t.TempDir()

	runDir := t.TempDir()
	origDir, _ := os.Getwd()
	if err := os.Chdir(runDir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { os.Chdir(origDir) }) //nolint:errcheck

	ctx := downloadTestCtx(map[string]any{
		"registry": "myreg",
		"regex":    ".*",
		"dest":     dest,
		"dry-run":  true,
	}, srv.URL)

	if err := executeArtifactDownloadHandler(ctx); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Dest should be empty — no actual downloads.
	entries, _ := os.ReadDir(dest)
	if len(entries) != 0 {
		t.Errorf("dry-run should not write files to dest; found %d entries", len(entries))
	}
	// A report file should have been created in dry-run-output/.
	reports, err := os.ReadDir(filepath.Join(runDir, "dry-run-output"))
	if err != nil {
		t.Fatalf("dry-run-output dir not created: %v", err)
	}
	if len(reports) != 1 {
		t.Errorf("expected 1 report file, got %d", len(reports))
	}
}

func TestExecuteArtifactDownload_NoMatches_ReturnsEarly(t *testing.T) {
	srv := newDownloadTestServer(t, nil)
	dest := t.TempDir()
	ctx := downloadTestCtx(map[string]any{
		"registry": "myreg",
		"regex":    "nomatch",
		"dest":     dest,
	}, srv.URL)

	if err := executeArtifactDownloadHandler(ctx); err != nil {
		t.Fatalf("unexpected error for empty result: %v", err)
	}
	entries, _ := os.ReadDir(dest)
	if len(entries) != 0 {
		t.Errorf("expected empty dest, got %d entries", len(entries))
	}
}

func TestExecuteArtifactDownload_DownloadsFiles(t *testing.T) {
	names := []string{"art/1.0/art.tar.gz", "art/2.0/art.tar.gz"}
	srv := newDownloadTestServer(t, names)
	dest := t.TempDir()
	ctx := downloadTestCtx(map[string]any{
		"registry": "myreg",
		"regex":    ".*",
		"dest":     dest,
	}, srv.URL)

	if err := executeArtifactDownloadHandler(ctx); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, name := range names {
		p := filepath.Join(dest, filepath.FromSlash(name))
		if _, err := os.Stat(p); os.IsNotExist(err) {
			t.Errorf("expected file %q to exist", p)
		}
	}
}

func TestExecuteArtifactDownload_SkipsExistingByDefault(t *testing.T) {
	artifactPath := "art/1.0/art.tar.gz"
	srv := newDownloadTestServer(t, []string{artifactPath})
	dest := t.TempDir()

	outPath := filepath.Join(dest, filepath.FromSlash(artifactPath))
	if err := os.MkdirAll(filepath.Dir(outPath), 0755); err != nil {
		t.Fatal(err)
	}
	original := []byte("original")
	if err := os.WriteFile(outPath, original, 0644); err != nil {
		t.Fatal(err)
	}

	ctx := downloadTestCtx(map[string]any{
		"registry": "myreg",
		"regex":    ".*",
		"dest":     dest,
	}, srv.URL)

	if err := executeArtifactDownloadHandler(ctx); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got, _ := os.ReadFile(outPath)
	if string(got) != string(original) {
		t.Errorf("existing file was overwritten; got %q, want %q", got, original)
	}
}

func TestExecuteArtifactDownload_OverwriteReplaces(t *testing.T) {
	artifactPath := "art/1.0/art.tar.gz"
	srv := newDownloadTestServer(t, []string{artifactPath})
	dest := t.TempDir()

	outPath := filepath.Join(dest, filepath.FromSlash(artifactPath))
	if err := os.MkdirAll(filepath.Dir(outPath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(outPath, []byte("stale"), 0644); err != nil {
		t.Fatal(err)
	}

	ctx := downloadTestCtx(map[string]any{
		"registry":  "myreg",
		"regex":     ".*",
		"dest":      dest,
		"overwrite": true,
	}, srv.URL)

	if err := executeArtifactDownloadHandler(ctx); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got, _ := os.ReadFile(outPath)
	if string(got) == "stale" {
		t.Error("expected file to be overwritten but content is unchanged")
	}
}

func TestExecuteArtifactDownload_Flatten_WritesFilesFlat(t *testing.T) {
	names := []string{"art/1.0/foo.tar.gz", "other/bar.zip"}
	srv := newDownloadTestServer(t, names)
	dest := t.TempDir()
	ctx := downloadTestCtx(map[string]any{
		"registry": "myreg",
		"regex":    ".*",
		"dest":     dest,
		"flatten":  true,
	}, srv.URL)

	if err := executeArtifactDownloadHandler(ctx); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, base := range []string{"foo.tar.gz", "bar.zip"} {
		p := filepath.Join(dest, base)
		if _, err := os.Stat(p); os.IsNotExist(err) {
			t.Errorf("expected %q to exist in dest", p)
		}
	}
	entries, _ := os.ReadDir(dest)
	for _, e := range entries {
		if e.IsDir() {
			t.Errorf("flatten should not create subdirectories; found dir %q", e.Name())
		}
	}
}

func TestExecuteArtifactDownload_Flatten_CollisionErrors(t *testing.T) {
	names := []string{"art/1.0/art.tar.gz", "art/2.0/art.tar.gz"}
	srv := newDownloadTestServer(t, names)
	dest := t.TempDir()

	runDir := t.TempDir()
	origDir, _ := os.Getwd()
	if err := os.Chdir(runDir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { os.Chdir(origDir) }) //nolint:errcheck

	ctx := downloadTestCtx(map[string]any{
		"registry": "myreg",
		"regex":    ".*",
		"dest":     dest,
		"flatten":  true,
	}, srv.URL)

	if err := executeArtifactDownloadHandler(ctx); err == nil {
		t.Fatal("expected error for flatten collision, got nil")
	}

	// No files should have been downloaded.
	entries, _ := os.ReadDir(dest)
	if len(entries) != 0 {
		t.Errorf("expected dest to be empty after collision error, got %d entries", len(entries))
	}

	// A conflict file should have been written.
	conflicts, err := os.ReadDir(filepath.Join(runDir, "dry-run-output"))
	if err != nil {
		t.Fatalf("dry-run-output dir not created: %v", err)
	}
	if len(conflicts) != 1 || !strings.HasPrefix(conflicts[0].Name(), "conflict-download-") {
		t.Errorf("expected 1 conflict-download-*.json file, got %v", conflicts)
	}
}

func TestExecuteArtifactDownload_Flatten_DryRunReportsCollisionsInFile(t *testing.T) {
	// In dry-run mode, collisions are included in the report file, not an error.
	names := []string{"art/1.0/art.tar.gz", "art/2.0/art.tar.gz"}
	srv := newDownloadTestServer(t, names)
	dest := t.TempDir()

	runDir := t.TempDir()
	origDir, _ := os.Getwd()
	if err := os.Chdir(runDir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { os.Chdir(origDir) }) //nolint:errcheck

	ctx := downloadTestCtx(map[string]any{
		"registry": "myreg",
		"regex":    ".*",
		"dest":     dest,
		"flatten":  true,
		"dry-run":  true,
	}, srv.URL)

	if err := executeArtifactDownloadHandler(ctx); err != nil {
		t.Fatalf("expected nil error in dry-run despite collision, got: %v", err)
	}

	entries, _ := os.ReadDir(dest)
	if len(entries) != 0 {
		t.Errorf("dry-run should not write files; found %d entries", len(entries))
	}

	// The report file should include the collision.
	reports, _ := os.ReadDir(filepath.Join(runDir, "dry-run-output"))
	if len(reports) != 1 {
		t.Fatalf("expected 1 report file, got %d", len(reports))
	}
	raw, _ := os.ReadFile(filepath.Join(runDir, "dry-run-output", reports[0].Name()))
	var out dryRunOutput
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("invalid JSON in report: %v", err)
	}
	if len(out.Conflicts) != 1 {
		t.Errorf("expected 1 conflict in report, got %d", len(out.Conflicts))
	}
}

func TestExecuteArtifactDownload_SkipsItemsWithNoURL(t *testing.T) {
	// Server returns items with no downloadUrl field.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"items":[{"name":"art.tar.gz","path":"art/1.0/art.tar.gz"}],"hasMore":false}`))
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)

	dest := t.TempDir()
	ctx := downloadTestCtx(map[string]any{
		"registry": "myreg",
		"regex":    ".*",
		"dest":     dest,
	}, srv.URL)

	err := executeArtifactDownloadHandler(ctx)
	if err == nil {
		t.Fatal("expected error when all items have no download URL, got nil")
	}
	if !strings.Contains(err.Error(), "no downloadable") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestExecuteArtifactDownload_RejectsUnsafePaths(t *testing.T) {
	// Server returns an item with a path-traversal payload.
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			dlURL := srv.URL + "/dl/evil"
			body := fmt.Sprintf(`{"items":[{"name":"evil","path":"../../etc/passwd","downloadUrl":%q}],"hasMore":false}`, dlURL)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(body))
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)

	dest := t.TempDir()
	ctx := downloadTestCtx(map[string]any{
		"registry": "myreg",
		"regex":    ".*",
		"dest":     dest,
	}, srv.URL)

	err := executeArtifactDownloadHandler(ctx)
	if err == nil {
		t.Fatal("expected error when all paths are unsafe, got nil")
	}
	// Nothing should be written to dest.
	entries, _ := os.ReadDir(dest)
	if len(entries) != 0 {
		t.Errorf("expected empty dest but found %d entries", len(entries))
	}
}

func TestExecuteArtifactDownload_PageSizeValidation(t *testing.T) {
	srv := newDownloadTestServer(t, nil)
	dest := t.TempDir()

	for _, bad := range []string{"0", "101", "abc", "-5"} {
		ctx := downloadTestCtx(map[string]any{
			"registry":  "myreg",
			"regex":     ".*",
			"dest":      dest,
			"page-size": bad,
		}, srv.URL)
		if err := executeArtifactDownloadHandler(ctx); err == nil {
			t.Errorf("expected error for --page-size %q, got nil", bad)
		}
	}
}

func TestExecuteArtifactDownload_FailedDownload_ReturnsError(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			dlURL := srv.URL + "/dl/art.tar.gz"
			body := fmt.Sprintf(`{"items":[{"name":"art/1.0/art.tar.gz","path":"art/1.0/art.tar.gz","downloadUrl":%q}],"hasMore":false}`, dlURL)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(body))
			return
		}
		// All GET requests return 404.
		http.Error(w, "not found", http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)

	dest := t.TempDir()
	ctx := downloadTestCtx(map[string]any{
		"registry": "myreg",
		"regex":    ".*",
		"dest":     dest,
	}, srv.URL)

	if err := executeArtifactDownloadHandler(ctx); err == nil {
		t.Fatal("expected error when download fails, got nil")
	}
}

// --- flattenCollisions ---

func TestFlattenCollisions_NoCollisions(t *testing.T) {
	files := []string{"a/1.0/foo.tar.gz", "b/2.0/bar.zip", "c/baz.tgz"}
	if got := flattenCollisions(files); len(got) != 0 {
		t.Errorf("expected no collisions, got: %v", got)
	}
}

func TestFlattenCollisions_DetectsCollisions(t *testing.T) {
	files := []string{"a/1.0/foo.tar.gz", "b/2.0/foo.tar.gz", "c/bar.zip", "d/bar.zip"}
	got := flattenCollisions(files)
	if len(got) != 2 {
		t.Errorf("expected 2 collisions, got %d: %v", len(got), got)
	}
}

// --- writeDryRunJSON / flattenCollisionMap ---

func TestWriteDryRunJSON_BasicOutput(t *testing.T) {
	items := []fileItem{
		{Path: "art/1.0/foo.tar.gz", DownloadURL: "http://x/foo", Size: "100"},
		{Path: "art/2.0/bar.zip", DownloadURL: "http://x/bar", Size: "200"},
	}
	dest := "/tmp/out"

	var buf strings.Builder
	_, err := writeDryRunJSON(&buf, items, dest, false, 0, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var out dryRunOutput
	if err := json.Unmarshal([]byte(buf.String()), &out); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, buf.String())
	}
	if out.Matched != 2 {
		t.Errorf("matched: got %d, want 2", out.Matched)
	}
	if len(out.Files) != 2 {
		t.Errorf("files: got %d, want 2", len(out.Files))
	}
	if out.Files[0].SourcePath != "art/1.0/foo.tar.gz" {
		t.Errorf("file[0].source_path = %q", out.Files[0].SourcePath)
	}
	if out.Files[0].Size != "100" {
		t.Errorf("file[0].size = %q", out.Files[0].Size)
	}
	if out.Conflicts != nil {
		t.Errorf("expected no conflicts, got %v", out.Conflicts)
	}
	if out.Skipped != nil {
		t.Errorf("expected no skipped, got %v", out.Skipped)
	}
}

func TestWriteDryRunJSON_SkippedCounts(t *testing.T) {
	var buf strings.Builder
	_, err := writeDryRunJSON(&buf, []fileItem{{Path: "a/b.gz", DownloadURL: "http://x", Size: "1"}}, "/out", false, 2, 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var out dryRunOutput
	if err := json.Unmarshal([]byte(buf.String()), &out); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if out.Skipped == nil || out.Skipped.NoURL != 2 || out.Skipped.UnsafePath != 1 {
		t.Errorf("skipped: got %+v", out.Skipped)
	}
}

func TestWriteDryRunJSON_FlattenConflictsIncluded(t *testing.T) {
	items := []fileItem{
		{Path: "a/1.0/img.png", DownloadURL: "http://x/1", Size: "10"},
		{Path: "b/2.0/img.png", DownloadURL: "http://x/2", Size: "20"},
		{Path: "c/3.0/other.png", DownloadURL: "http://x/3", Size: "30"},
	}
	var buf strings.Builder
	_, err := writeDryRunJSON(&buf, items, "/out", true, 0, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var out dryRunOutput
	if err := json.Unmarshal([]byte(buf.String()), &out); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if len(out.Conflicts) != 1 {
		t.Fatalf("expected 1 conflict, got %d: %v", len(out.Conflicts), out.Conflicts)
	}
	if len(out.Conflicts[0].Sources) != 2 {
		t.Errorf("conflict should list 2 sources, got %d", len(out.Conflicts[0].Sources))
	}
}

func TestDryRunJSON_WrittenToFile(t *testing.T) {
	srv := newDownloadTestServer(t, []string{"art/1.0/art.tar.gz"})
	dest := t.TempDir()
	outFile := filepath.Join(t.TempDir(), "dryrun.json")
	ctx := downloadTestCtxWithOut(map[string]any{
		"registry": "myreg",
		"regex":    ".*",
		"dest":     dest,
		"dry-run":  true,
	}, srv.URL, outFile)

	if err := executeArtifactDownloadHandler(ctx); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	raw, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatalf("output file not written: %v", err)
	}
	var out dryRunOutput
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("invalid JSON in output file: %v\n%s", err, raw)
	}
	if out.Matched != 1 {
		t.Errorf("matched: got %d, want 1", out.Matched)
	}
	// Ensure nothing was downloaded.
	entries, _ := os.ReadDir(dest)
	if len(entries) != 0 {
		t.Errorf("dry-run should not write files; found %d entries", len(entries))
	}
}

func TestDryRunJSON_AutoGeneratesFile(t *testing.T) {
	// When --report is used without --out, a file should be auto-generated
	// under dry-run-output/ with a timestamped name (matching hc behaviour).
	srv := newDownloadTestServer(t, []string{"art/1.0/art.tar.gz"})
	dest := t.TempDir()

	// Run from a temp dir so dry-run-output/ is created there, not in the repo root.
	origDir, _ := os.Getwd()
	runDir := t.TempDir()
	if err := os.Chdir(runDir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { os.Chdir(origDir) }) //nolint:errcheck

	ctx := downloadTestCtx(map[string]any{
		"registry": "myreg",
		"regex":    ".*",
		"dest":     dest,
		"dry-run":  true,
	}, srv.URL)

	if err := executeArtifactDownloadHandler(ctx); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Exactly one JSON file should exist under dry-run-output/.
	entries, err := os.ReadDir(filepath.Join(runDir, "dry-run-output"))
	if err != nil {
		t.Fatalf("dry-run-output dir not created: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 output file, got %d: %v", len(entries), entries)
	}
	name := entries[0].Name()
	if !strings.HasPrefix(name, "download-dryrun-output-") || !strings.HasSuffix(name, ".json") {
		t.Errorf("unexpected filename format: %q", name)
	}

	raw, _ := os.ReadFile(filepath.Join(runDir, "dry-run-output", name))
	var out dryRunOutput
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("invalid JSON in auto-generated file: %v\n%s", err, raw)
	}
	if out.Matched != 1 {
		t.Errorf("matched: got %d, want 1", out.Matched)
	}
}

func TestFlattenCollisionMap_Empty(t *testing.T) {
	m := flattenCollisionMap([]string{"a/foo.gz", "b/bar.gz"})
	if len(m) != 0 {
		t.Errorf("expected no collisions, got %v", m)
	}
}

func TestFlattenCollisionMap_DetectsCollision(t *testing.T) {
	m := flattenCollisionMap([]string{"a/1.0/img.png", "b/2.0/img.png", "c/other.png"})
	if len(m) != 1 {
		t.Fatalf("expected 1 collision, got %d: %v", len(m), m)
	}
	srcs, ok := m["img.png"]
	if !ok || len(srcs) != 2 {
		t.Errorf("expected 2 sources for img.png, got %v", srcs)
	}
}

// TestFlattenCollisions_ThreeWayCollision guards against the bug where seen[base]
// was never updated after the first collision, causing the second collision message
// to always pair with the original first file rather than the most recent one.
func TestFlattenCollisions_ThreeWayCollision(t *testing.T) {
	files := []string{"a/1.0/foo.gz", "b/2.0/foo.gz", "c/3.0/foo.gz"}
	got := flattenCollisions(files)
	if len(got) != 2 {
		t.Fatalf("expected 2 collision messages for 3-way collision, got %d: %v", len(got), got)
	}
	// Second message must reference b/2.0/foo.gz and c/3.0/foo.gz, not a/1.0/foo.gz again.
	if !strings.Contains(got[1], "b/2.0/foo.gz") {
		t.Errorf("second collision message should reference b/2.0/foo.gz (the previous file), got: %s", got[1])
	}
	if !strings.Contains(got[1], "c/3.0/foo.gz") {
		t.Errorf("second collision message should reference c/3.0/foo.gz (the new file), got: %s", got[1])
	}
}
