// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package ci

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"

	"github.com/harness/cli/v3/pkg/auth"
)

func testUploadAuth(url string) *auth.ResolvedAuth {
	return &auth.ResolvedAuth{RegistryURL: url, AccountID: "acc", Headers: map[string]string{"Authorization": "CIManager eyJtoken"}}
}

func TestPermanentUploadError(t *testing.T) {
	cause := errors.New("bad token")
	pErr := &permanentUploadError{cause: cause}

	if pErr.Error() != cause.Error() {
		t.Errorf("Error() = %q, want %q", pErr.Error(), cause.Error())
	}
	if !errors.Is(pErr, cause) {
		t.Error("errors.Is(pErr, cause) should be true via Unwrap")
	}

	var target *permanentUploadError
	wrapped := fmt.Errorf("outer: %w", pErr)
	if !errors.As(wrapped, &target) {
		t.Error("errors.As should find *permanentUploadError inside wrapped error")
	}
}

func TestPushEntry_OmitsContentType(t *testing.T) {
	var gotContentType string
	var sawRequest bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawRequest = true
		gotContentType = r.Header.Get("Content-Type")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]string{"sha256": "abc", "packageId": "pkg", "versionId": "ver"})
	}))
	defer srv.Close()

	dir := t.TempDir()
	path := filepath.Join(dir, "circle.html")
	writeTestFile(t, path, "<html></html>")

	_, _, _, err := pushEntry(context.Background(), uploadEntry{
		localPath: path,
		destPath:  "first-cloud/v271/artifacts/circle.html",
		size:      13,
	}, testUploadAuth(srv.URL), "reg")
	if err != nil {
		t.Fatalf("pushEntry: %v", err)
	}
	if !sawRequest {
		t.Fatal("expected an upload request")
	}
	if gotContentType != "" {
		t.Errorf("Content-Type = %q, want it unset so HAR infers text/html from the filename", gotContentType)
	}
}

func TestParallelUpload_AllSuccess(t *testing.T) {
	var mu sync.Mutex
	var uploaded []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		uploaded = append(uploaded, r.URL.Path)
		mu.Unlock()
		resp := map[string]string{
			"sha256":    "deadbeef",
			"packageId": "pkg-uuid",
			"versionId": "ver-uuid",
		}
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "a.txt"), "aaa")
	writeTestFile(t, filepath.Join(dir, "b.txt"), "bbb")

	entries := []uploadEntry{
		{localPath: filepath.Join(dir, "a.txt"), destPath: "art/v1/a.txt", size: 3},
		{localPath: filepath.Join(dir, "b.txt"), destPath: "art/v1/b.txt", size: 3},
	}

	results := parallelUpload(context.Background(), entries, testUploadAuth(srv.URL), "my-reg")
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	for _, r := range results {
		if r.err != nil {
			t.Errorf("unexpected error for %s: %v", r.entry.localPath, r.err)
		}
		if r.file.SHA256 != "deadbeef" {
			t.Errorf("SHA256 = %q, want %q", r.file.SHA256, "deadbeef")
		}
	}
	if len(uploaded) != 2 {
		t.Errorf("expected 2 upload requests, got %d", len(uploaded))
	}
}

func TestParallelUpload_PermanentError_NoRetry(t *testing.T) {
	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"message":"unauthorized"}`)
	}))
	defer srv.Close()

	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "file.txt"), "hello")

	entries := []uploadEntry{
		{localPath: filepath.Join(dir, "file.txt"), destPath: "art/v1/file.txt", size: 5},
	}

	results := parallelUpload(context.Background(), entries, testUploadAuth(srv.URL), "reg")
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].err == nil {
		t.Fatal("expected error for 401 response, got nil")
	}
	// A 401 is a permanent error — should not retry (call count must be 1).
	if callCount != 1 {
		t.Errorf("callCount = %d; 401 is permanent, should not retry", callCount)
	}
}

func TestParallelUpload_CancelledContext(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	dir := t.TempDir()
	for i := 0; i < 10; i++ {
		writeTestFile(t, filepath.Join(dir, fmt.Sprintf("f%d.txt", i)), "x")
	}
	var entries []uploadEntry
	for i := 0; i < 10; i++ {
		entries = append(entries, uploadEntry{
			localPath: filepath.Join(dir, fmt.Sprintf("f%d.txt", i)),
			destPath:  fmt.Sprintf("art/v1/f%d.txt", i),
			size:      1,
		})
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	results := parallelUpload(ctx, entries, testUploadAuth(srv.URL), "reg")

	// When context is pre-cancelled, each worker sends a result for the job it
	// picked up, then drains the remaining jobs without producing results.
	// So result count is ≤ len(entries) (up to one per worker = up to uploadWorkers).
	if len(results) > len(entries) {
		t.Errorf("results (%d) cannot exceed entries (%d)", len(results), len(entries))
	}
	// All results that were produced should carry the cancellation error.
	for _, r := range results {
		if r.err == nil {
			t.Errorf("expected cancellation error for %s, got nil", r.entry.localPath)
		}
	}
}

func TestParallelUpload_WorkerCapBelowUploadWorkers(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{}`)
	}))
	defer srv.Close()

	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "only.txt"), "x")
	entries := []uploadEntry{
		{localPath: filepath.Join(dir, "only.txt"), destPath: "art/v1/only.txt", size: 1},
	}

	// Only 1 entry — worker pool should cap to 1 rather than uploadWorkers (5).
	results := parallelUpload(context.Background(), entries, testUploadAuth(srv.URL), "reg")
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].err != nil {
		t.Errorf("unexpected error: %v", results[0].err)
	}
}

func TestParallelUpload_ServerResponseParsed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"sha256":"aabbcc","packageId":"pkg-123","versionId":"ver-456"}`)
	}))
	defer srv.Close()

	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "f.zip"), "content")
	entries := []uploadEntry{
		{localPath: filepath.Join(dir, "f.zip"), destPath: "art/v1/f.zip", size: 7},
	}

	results := parallelUpload(context.Background(), entries, testUploadAuth(srv.URL), "reg")
	if len(results) != 1 || results[0].err != nil {
		t.Fatalf("unexpected error or count: %v", results)
	}
	if results[0].file.SHA256 != "aabbcc" {
		t.Errorf("SHA256 = %q, want %q", results[0].file.SHA256, "aabbcc")
	}
	if results[0].harPackageUUID != "pkg-123" {
		t.Errorf("harPackageUUID = %q, want %q", results[0].harPackageUUID, "pkg-123")
	}
	if results[0].harVersionUUID != "ver-456" {
		t.Errorf("harVersionUUID = %q, want %q", results[0].harVersionUUID, "ver-456")
	}
}

// TestUploadWithRetry_SuccessAfterTransientError is a regression test for the
// retry path itself: HCli's own suite only ever exercises the no-retry
// (permanent error) and all-success cases, never a transient failure that
// actually gets retried and then succeeds.
func TestUploadWithRetry_SuccessAfterTransientError(t *testing.T) {
	var callCount int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		if callCount < 2 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"sha256":"ok"}`)
	}))
	defer srv.Close()

	dir := t.TempDir()
	path := filepath.Join(dir, "retry.txt")
	writeTestFile(t, path, "x")

	entry := uploadEntry{localPath: path, destPath: "art/v1/retry.txt", size: 1}
	pf, _, _, err := uploadWithRetry(context.Background(), entry, testUploadAuth(srv.URL), "reg")
	if err != nil {
		t.Fatalf("expected eventual success, got: %v", err)
	}
	if callCount != 2 {
		t.Errorf("callCount = %d, want 2 (one failure, one retry success)", callCount)
	}
	if pf.SHA256 != "ok" {
		t.Errorf("SHA256 = %q, want %q", pf.SHA256, "ok")
	}
}
