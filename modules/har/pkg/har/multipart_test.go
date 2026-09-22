// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package har

import (
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
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/harness/cli/v3/pkg/auth"
	"github.com/harness/cli/v3/pkg/cmdctx"
)

// --- Test helpers ---

func multipartTestCtx(registryURL string) *cmdctx.Ctx {
	return &cmdctx.Ctx{
		Context: context.Background(),
		Auth: &auth.ResolvedAuth{
			AuthType:    auth.AuthTypePAT,
			APIUrl:      registryURL,
			RegistryURL: registryURL,
			AccountID:   "acct",
			PATToken:    "test-token",
		},
	}
}

// fakeRegistry is an httptest-backed stand-in for the registry's multipart endpoints plus the
// object store the presigned part URLs point at. Everything it records is guarded by mu because
// parts are uploaded concurrently.
type fakeRegistry struct {
	t   *testing.T
	srv *httptest.Server

	mu    sync.Mutex
	parts map[int][]byte
	// startBodies records every decoded Start request body, for contract assertions.
	startBodies []map[string]any
	// partFetches records each (from,to) range requested from the parts endpoint.
	partFetches [][2]int
	aborted     bool
	completes   int

	// Knobs the tests set before calling upload.
	partSize       int64
	partCount      int
	presignInStart int    // how many part URLs the Start response carries
	startStatus    int    // HTTP status Start replies with
	startCode      string // values.code Start replies with on a non-2xx
	dedup          bool   // Start replies 200 {"status":"completed"}
	expiredURLs    bool   // Start's part URLs are already expired
	failPartsOnce  map[int]bool
	partFailStatus int
	missingOnce    []int  // parts the first Complete reports as missing
	finalStatus    string // status the poll endpoint reports once complete has been accepted
	finalError     string
}

func newFakeRegistry(t *testing.T) *fakeRegistry {
	f := &fakeRegistry{
		t:              t,
		parts:          map[int][]byte{},
		startStatus:    http.StatusCreated,
		partFailStatus: http.StatusInternalServerError,
		failPartsOnce:  map[int]bool{},
		finalStatus:    uploadStatusCompleted,
	}
	f.srv = httptest.NewServer(f)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeRegistry) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case strings.HasPrefix(r.URL.Path, "/object/"):
		f.servePart(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/pkg/acct/reg/uploads":
		f.serveStart(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/pkg/acct/reg/uploads/up-1/parts":
		f.serveParts(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/pkg/acct/reg/uploads/up-1/complete":
		f.serveComplete(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/pkg/acct/reg/uploads/up-1":
		f.serveStatus(w, r)
	case r.Method == http.MethodDelete && r.URL.Path == "/pkg/acct/reg/uploads/up-1":
		f.mu.Lock()
		f.aborted = true
		f.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	default:
		f.t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		w.WriteHeader(http.StatusNotFound)
	}
}

// writeAPIError mirrors the registry's error envelope: {"message":..,"values":{"code":..}}.
func writeAPIError(w http.ResponseWriter, status int, values map[string]any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"message": "boom", "values": values})
}

func (f *fakeRegistry) serveStart(w http.ResponseWriter, r *http.Request) {
	// Control calls must be authenticated.
	if r.Header.Get("x-api-key") == "" && r.Header.Get("Authorization") == "" {
		f.t.Error("Start request carried no auth header")
	}

	var body map[string]any
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		f.t.Fatalf("decoding start body: %v", err)
	}
	f.mu.Lock()
	f.startBodies = append(f.startBodies, body)
	f.mu.Unlock()

	if f.startStatus != http.StatusCreated {
		writeAPIError(w, f.startStatus, map[string]any{"code": f.startCode})
		return
	}
	if f.dedup {
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(startUploadResponse{Status: uploadStatusCompleted})
		return
	}

	presign := f.presignInStart
	if presign == 0 || presign > f.partCount {
		presign = f.partCount
	}
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(startUploadResponse{
		UploadID:  "up-1",
		Status:    uploadStatusOpen,
		PartSize:  f.partSize,
		PartCount: f.partCount,
		Parts:     f.presign(1, presign),
	})
}

// presign builds part URLs for from..to inclusive, pointing at this server's object-store route.
func (f *fakeRegistry) presign(from, to int) []partURL {
	expiry := time.Now().Add(time.Hour)
	if f.expiredURLs {
		expiry = time.Now().Add(10 * time.Second) // inside presignRefreshWindow
	}
	var urls []partURL
	for n := from; n <= to; n++ {
		urls = append(urls, partURL{
			PartNumber: n,
			URL:        fmt.Sprintf("%s/object/%d", f.srv.URL, n),
			ExpiresAt:  expiry.Format(presignExpiryLayout),
		})
	}
	return urls
}

func (f *fakeRegistry) serveParts(w http.ResponseWriter, r *http.Request) {
	var from, to int
	fmt.Sscanf(r.URL.Query().Get("from"), "%d", &from)
	fmt.Sscanf(r.URL.Query().Get("to"), "%d", &to)

	f.mu.Lock()
	f.partFetches = append(f.partFetches, [2]int{from, to})
	// Refreshed URLs are always long-lived, so a refresh terminates rather than looping.
	f.expiredURLs = false
	f.mu.Unlock()

	_ = json.NewEncoder(w).Encode(partsResponse{Parts: f.presign(from, to)})
}

func (f *fakeRegistry) servePart(w http.ResponseWriter, r *http.Request) {
	// A presigned URL is self-authenticating; an auth header alongside it makes real object
	// stores reject the request, so assert the client does not send one.
	if r.Header.Get("Authorization") != "" || r.Header.Get("x-api-key") != "" {
		f.t.Error("part upload carried an auth header")
	}
	if r.ContentLength <= 0 {
		f.t.Errorf("part upload had ContentLength %d, want > 0", r.ContentLength)
	}

	var partNumber int
	fmt.Sscanf(strings.TrimPrefix(r.URL.Path, "/object/"), "%d", &partNumber)

	f.mu.Lock()
	if f.failPartsOnce[partNumber] {
		delete(f.failPartsOnce, partNumber)
		f.mu.Unlock()
		w.WriteHeader(f.partFailStatus)
		return
	}
	f.mu.Unlock()

	var buf bytes.Buffer
	if _, err := buf.ReadFrom(r.Body); err != nil {
		f.t.Errorf("reading part %d: %v", partNumber, err)
	}

	f.mu.Lock()
	f.parts[partNumber] = buf.Bytes()
	f.mu.Unlock()

	w.Header().Set("ETag", fmt.Sprintf("%q", fmt.Sprintf("etag-%d", partNumber)))
	w.WriteHeader(http.StatusOK)
}

func (f *fakeRegistry) serveComplete(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	if len(body) != 0 {
		f.t.Errorf("Complete carried a body %q, want empty", body)
	}

	f.mu.Lock()
	f.completes++
	missing := f.missingOnce
	if len(missing) > 0 {
		f.missingOnce = nil
		for _, n := range missing {
			delete(f.parts, n)
		}
	}
	f.mu.Unlock()

	if len(missing) > 0 {
		asAny := make([]any, len(missing))
		for i, n := range missing {
			asAny[i] = float64(n)
		}
		writeAPIError(w, http.StatusConflict, map[string]any{
			"code": codeIncompleteUpload, "missingParts": asAny,
		})
		return
	}

	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(completeUploadResponse{UploadID: "up-1", Status: uploadStatusFinalizing})
}

func (f *fakeRegistry) serveStatus(w http.ResponseWriter, _ *http.Request) {
	resp := uploadStatusResponse{UploadID: "up-1", Status: f.finalStatus}
	if f.finalError != "" {
		resp.Error = &f.finalError
	}
	_ = json.NewEncoder(w).Encode(resp)
}

// assembled returns the parts concatenated in part-number order.
func (f *fakeRegistry) assembled() []byte {
	f.mu.Lock()
	defer f.mu.Unlock()

	nums := make([]int, 0, len(f.parts))
	for n := range f.parts {
		nums = append(nums, n)
	}
	sort.Ints(nums)

	var out []byte
	for _, n := range nums {
		out = append(out, f.parts[n]...)
	}
	return out
}

// writeTempFile writes size bytes of deterministic, non-repeating-per-part content.
func writeTempFile(t *testing.T, size int) string {
	t.Helper()
	data := make([]byte, size)
	for i := range data {
		data[i] = byte(i % 251)
	}
	path := filepath.Join(t.TempDir(), "big.bin")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("writing temp file: %v", err)
	}
	return path
}

// testSums hashes path the way the push command does before choosing an upload path.
func testSums(t *testing.T, path string) fileChecksums {
	t.Helper()
	sums, err := computeFileChecksums(path)
	if err != nil {
		t.Fatalf("computing checksums for %s: %v", path, err)
	}
	return sums
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	return data
}

// --- Tests ---

func TestMultipartUploadRoundTrip(t *testing.T) {
	const size = 1000
	f := newFakeRegistry(t)
	f.partSize = 300 // 4 parts: 300, 300, 300, 100
	f.partCount = 4

	path := writeTempFile(t, size)
	u := newMultipartUploader(multipartTestCtx(f.srv.URL), newHTTPClient(), "reg", 3)

	if err := u.upload("mypkg", "1.0.0", "sub/dir/big.bin", path, size, testSums(t, path)); err != nil {
		t.Fatalf("upload: %v", err)
	}

	if got := f.assembled(); !bytes.Equal(got, readFile(t, path)) {
		t.Errorf("assembled object differs from source: got %d bytes, want %d", len(got), size)
	}
	if len(f.parts) != 4 {
		t.Errorf("uploaded %d parts, want 4", len(f.parts))
	}
	// The final part must be the remainder, not a full partSize.
	if got := len(f.parts[4]); got != 100 {
		t.Errorf("final part was %d bytes, want 100", got)
	}

	// The Start body must carry exactly the documented fields; the server rejects unknown ones.
	body := f.startBodies[0]
	wantKeys := map[string]bool{"path": true, "size": true, "sha256": true, "sha1": true, "md5": true, "sha512": true}
	for k := range body {
		if !wantKeys[k] {
			t.Errorf("Start body carried unexpected field %q", k)
		}
	}
	if got, want := body["path"], "mypkg/1.0.0/sub/dir/big.bin"; got != want {
		t.Errorf("Start path = %q, want %q", got, want)
	}
	if got, want := body["size"], float64(size); got != want {
		t.Errorf("Start size = %v, want %v", got, want)
	}
	if body["sha256"] == "" {
		t.Error("Start body had an empty sha256")
	}
}

func TestMultipartUploadDedupSkipsUpload(t *testing.T) {
	f := newFakeRegistry(t)
	f.dedup = true

	path := writeTempFile(t, 100)
	u := newMultipartUploader(multipartTestCtx(f.srv.URL), newHTTPClient(), "reg", 2)

	if err := u.upload("mypkg", "1.0.0", "big.bin", path, 100, testSums(t, path)); err != nil {
		t.Fatalf("upload: %v", err)
	}
	if len(f.parts) != 0 {
		t.Errorf("dedup path uploaded %d parts, want 0", len(f.parts))
	}
	if f.completes != 0 {
		t.Errorf("dedup path called Complete %d times, want 0", f.completes)
	}
}

// noRetryHTTPClient returns a client that does not retry, unlike newHTTPClient. Tests that make
// Start fail on a retryable condition (a 5xx, or nothing listening at all) use it so they assert
// the fallback decision without also sitting through five backoff waits.
func noRetryHTTPClient() *http.Client {
	return &http.Client{Timeout: 10 * time.Second}
}

// TestMultipartUploadStartFailures pins down which Start failures hand the push back to the
// single-PUT fallback and which ones must surface. Falling back on an auth or server error would
// mask the real cause behind a second attempt that hits the same wall.
func TestMultipartUploadStartFailures(t *testing.T) {
	tests := []struct {
		name         string
		status       int
		code         string
		wantFallback bool
	}{
		{"feature disabled", http.StatusNotImplemented, codeMultipartUnsupported, true},
		{"endpoint absent", http.StatusNotFound, "", true},
		{"unauthenticated", http.StatusUnauthorized, "", false},
		{"forbidden", http.StatusForbidden, "", false},
		{"server error", http.StatusInternalServerError, "", false},
		{"path conflict", http.StatusConflict, codePathConflict, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeRegistry(t)
			f.startStatus = tc.status
			f.startCode = tc.code

			path := writeTempFile(t, 100)
			u := newMultipartUploader(multipartTestCtx(f.srv.URL), noRetryHTTPClient(), "reg", 2)

			err := u.upload("mypkg", "1.0.0", "big.bin", path, 100, testSums(t, path))
			if err == nil {
				t.Fatal("upload succeeded, want an error")
			}
			if got := errors.Is(err, errMultipartUnsupported); got != tc.wantFallback {
				t.Fatalf("errors.Is(err, errMultipartUnsupported) = %t, want %t (err = %v)",
					got, tc.wantFallback, err)
			}
			if len(f.parts) != 0 {
				t.Errorf("uploaded %d parts before giving up, want 0", len(f.parts))
			}
		})
	}
}

// TestMultipartUploadStartUnreachableFallsBack covers a Start that never gets a reply: no response
// means multipart availability is unknown, and a single PUT is a strictly simpler request that
// deserves the attempt rather than failing the push outright.
func TestMultipartUploadStartUnreachableFallsBack(t *testing.T) {
	// A server that is closed before the call: its address is real but nothing is listening, so
	// Start fails in transport and produces no apiError to inspect.
	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := dead.URL
	dead.Close()

	path := writeTempFile(t, 100)
	u := newMultipartUploader(multipartTestCtx(url), noRetryHTTPClient(), "reg", 2)

	err := u.upload("mypkg", "1.0.0", "big.bin", path, 100, testSums(t, path))
	if !errors.Is(err, errMultipartUnsupported) {
		t.Fatalf("upload error = %v, want errMultipartUnsupported", err)
	}
}

// TestStartFailureMeansUnsupportedIgnoresCancellation checks that a cancelled or expired command is
// not reported as a registry lacking multipart: the fallback would just repeat the failure.
func TestStartFailureMeansUnsupportedIgnoresCancellation(t *testing.T) {
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	expired, cancelExpired := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancelExpired()

	for name, ctx := range map[string]context.Context{"cancelled": cancelled, "expired": expired} {
		t.Run(name, func(t *testing.T) {
			err := fmt.Errorf("starting multipart upload: %w", ctx.Err())
			if startFailureMeansUnsupported(ctx, err) {
				t.Error("startFailureMeansUnsupported = true, want false for an ended command context")
			}
		})
	}
}

// TestStartFailureMeansUnsupportedFallsBackOnClientTimeout guards a regression found on QA: an
// unreachable registry exhausts the HTTP client's retries and then trips http.Client.Timeout, which
// reports context.DeadlineExceeded even though the command's own context is still live. Deciding
// from the error rather than the context made this refuse to fall back - the exact opposite of what
// an unreachable Start should do.
func TestStartFailureMeansUnsupportedFallsBackOnClientTimeout(t *testing.T) {
	err := fmt.Errorf(`starting multipart upload: Post "http://127.0.0.1:9/pkg/a/r/uploads": %w`+
		" (Client.Timeout exceeded while awaiting headers)", context.DeadlineExceeded)
	if !startFailureMeansUnsupported(context.Background(), err) {
		t.Error("startFailureMeansUnsupported = false, want true: the command context is still live, " +
			"so a client-side timeout means multipart availability is unknown")
	}
}

func TestMultipartUploadFetchesPartsBeyondStartBatch(t *testing.T) {
	const size = 500
	f := newFakeRegistry(t)
	f.partSize = 100
	f.partCount = 5
	f.presignInStart = 2 // parts 3..5 must be fetched separately

	path := writeTempFile(t, size)
	u := newMultipartUploader(multipartTestCtx(f.srv.URL), newHTTPClient(), "reg", 1)

	if err := u.upload("mypkg", "1.0.0", "big.bin", path, size, testSums(t, path)); err != nil {
		t.Fatalf("upload: %v", err)
	}
	if got := f.assembled(); !bytes.Equal(got, readFile(t, path)) {
		t.Error("assembled object differs from source")
	}
	if len(f.partFetches) == 0 {
		t.Fatal("expected at least one parts fetch for the un-presigned parts")
	}
	// The fetch window must be clamped to the real part count, not extended past it.
	for _, fetch := range f.partFetches {
		if fetch[1] > f.partCount {
			t.Errorf("parts fetch range %v exceeds partCount %d", fetch, f.partCount)
		}
	}
}

func TestMultipartUploadRefreshesNearlyExpiredURLs(t *testing.T) {
	const size = 200
	f := newFakeRegistry(t)
	f.partSize = 100
	f.partCount = 2
	f.expiredURLs = true // Start's URLs are inside presignRefreshWindow

	path := writeTempFile(t, size)
	u := newMultipartUploader(multipartTestCtx(f.srv.URL), newHTTPClient(), "reg", 1)

	if err := u.upload("mypkg", "1.0.0", "big.bin", path, size, testSums(t, path)); err != nil {
		t.Fatalf("upload: %v", err)
	}
	if len(f.partFetches) == 0 {
		t.Error("expected the near-expiry URLs to be refreshed before use")
	}
	if got := f.assembled(); !bytes.Equal(got, readFile(t, path)) {
		t.Error("assembled object differs from source")
	}
}

func TestMultipartUploadRetriesMissingParts(t *testing.T) {
	const size = 300
	f := newFakeRegistry(t)
	f.partSize = 100
	f.partCount = 3
	f.missingOnce = []int{2}

	path := writeTempFile(t, size)
	u := newMultipartUploader(multipartTestCtx(f.srv.URL), newHTTPClient(), "reg", 2)

	if err := u.upload("mypkg", "1.0.0", "big.bin", path, size, testSums(t, path)); err != nil {
		t.Fatalf("upload: %v", err)
	}
	if f.completes != 2 {
		t.Errorf("Complete called %d times, want 2 (one rejection then success)", f.completes)
	}
	if got := f.assembled(); !bytes.Equal(got, readFile(t, path)) {
		t.Error("assembled object differs from source after re-uploading the missing part")
	}
}

func TestMultipartUploadRetriesTransientPartFailure(t *testing.T) {
	const size = 200
	f := newFakeRegistry(t)
	f.partSize = 100
	f.partCount = 2
	f.failPartsOnce = map[int]bool{2: true}
	f.partFailStatus = http.StatusServiceUnavailable

	path := writeTempFile(t, size)
	u := newMultipartUploader(multipartTestCtx(f.srv.URL), newHTTPClient(), "reg", 2)

	if err := u.upload("mypkg", "1.0.0", "big.bin", path, size, testSums(t, path)); err != nil {
		t.Fatalf("upload: %v", err)
	}
	if got := f.assembled(); !bytes.Equal(got, readFile(t, path)) {
		t.Error("assembled object differs from source after retrying a part")
	}
}

func TestMultipartUploadAbortsSessionOnFatalPartFailure(t *testing.T) {
	const size = 200
	f := newFakeRegistry(t)
	f.partSize = 100
	f.partCount = 2
	// 400 is not retryable, so the part fails on its first attempt and the upload gives up fast.
	f.failPartsOnce = map[int]bool{1: true, 2: true}
	f.partFailStatus = http.StatusBadRequest

	path := writeTempFile(t, size)
	u := newMultipartUploader(multipartTestCtx(f.srv.URL), newHTTPClient(), "reg", 2)

	if err := u.upload("mypkg", "1.0.0", "big.bin", path, size, testSums(t, path)); err == nil {
		t.Fatal("upload succeeded, want an error")
	}
	if !f.aborted {
		t.Error("session was not aborted after a fatal part failure")
	}
	if f.completes != 0 {
		t.Errorf("Complete called %d times after a part failure, want 0", f.completes)
	}
}

func TestMultipartUploadSurfacesFinalizeFailure(t *testing.T) {
	const size = 100
	f := newFakeRegistry(t)
	f.partSize = 100
	f.partCount = 1
	f.finalStatus = uploadStatusFailed
	f.finalError = "sha256 mismatch"

	path := writeTempFile(t, size)
	u := newMultipartUploader(multipartTestCtx(f.srv.URL), newHTTPClient(), "reg", 2)

	err := u.upload("mypkg", "1.0.0", "big.bin", path, size, testSums(t, path))
	if err == nil {
		t.Fatal("upload succeeded, want a finalize failure")
	}
	if !strings.Contains(err.Error(), "sha256 mismatch") {
		t.Errorf("error = %v, want it to mention the server's reason", err)
	}
	if !f.aborted {
		t.Error("expected a best-effort abort after a failed finalize")
	}
}

func TestValidateSession(t *testing.T) {
	u := &multipartUploader{}
	tests := []struct {
		name    string
		session startUploadResponse
		size    int64
		wantErr bool
	}{
		{"exact multiple", startUploadResponse{UploadID: "x", PartSize: 100, PartCount: 2}, 200, false},
		{"partial final part", startUploadResponse{UploadID: "x", PartSize: 100, PartCount: 3}, 250, false},
		{"missing upload id", startUploadResponse{PartSize: 100, PartCount: 2}, 200, true},
		{"zero part size", startUploadResponse{UploadID: "x", PartSize: 0, PartCount: 2}, 200, true},
		{"zero part count", startUploadResponse{UploadID: "x", PartSize: 100, PartCount: 0}, 200, true},
		{"too few parts", startUploadResponse{UploadID: "x", PartSize: 100, PartCount: 1}, 250, true},
		{"one part too many", startUploadResponse{UploadID: "x", PartSize: 100, PartCount: 4}, 250, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := u.validateSession(&tc.session, tc.size)
			if (err != nil) != tc.wantErr {
				t.Errorf("validateSession(%d bytes) error = %v, wantErr %v", tc.size, err, tc.wantErr)
			}
		})
	}
}

func TestPartURLExpired(t *testing.T) {
	tests := []struct {
		name      string
		expiresAt string
		want      bool
	}{
		{"absent expiry is treated as expired", "", true},
		{"unparseable expiry is treated as expired", "not-a-time", true},
		{"inside refresh window", time.Now().Add(10 * time.Second).Format(presignExpiryLayout), true},
		{"already past", time.Now().Add(-time.Minute).Format(presignExpiryLayout), true},
		{"plenty of validity left", time.Now().Add(time.Hour).Format(presignExpiryLayout), false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := (partURL{ExpiresAt: tc.expiresAt}).expired(); got != tc.want {
				t.Errorf("expired() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestMissingParts(t *testing.T) {
	err := &apiError{
		StatusCode: http.StatusConflict,
		Code:       codeIncompleteUpload,
		Values:     map[string]any{"code": codeIncompleteUpload, "missingParts": []any{float64(2), float64(5)}},
	}
	got := missingParts(err)
	if len(got) != 2 || got[0] != 2 || got[1] != 5 {
		t.Errorf("missingParts = %v, want [2 5]", got)
	}

	if got := missingParts(fmt.Errorf("plain error")); got != nil {
		t.Errorf("missingParts on a non-apiError = %v, want nil", got)
	}
	// SIZE_MISMATCH shares the 409 status but carries no part list.
	sizeErr := &apiError{StatusCode: http.StatusConflict, Code: codeSizeMismatch, Values: map[string]any{}}
	if got := missingParts(sizeErr); got != nil {
		t.Errorf("missingParts without the key = %v, want nil", got)
	}
}

func TestDoJSONRequestParsesErrorCode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeAPIError(w, http.StatusConflict, map[string]any{"code": codePathConflict})
	}))
	defer srv.Close()

	req, err := http.NewRequest(http.MethodGet, srv.URL, nil)
	if err != nil {
		t.Fatalf("building request: %v", err)
	}

	status, _, err := doJSONRequest(&http.Client{}, req)
	if status != http.StatusConflict {
		t.Errorf("status = %d, want 409", status)
	}
	if got := apiErrorCode(err); got != codePathConflict {
		t.Errorf("apiErrorCode = %q, want %q", got, codePathConflict)
	}
	if got := apiErrorStatus(err); got != http.StatusConflict {
		t.Errorf("apiErrorStatus = %d, want 409", got)
	}
	if got := apiErrorCode(fmt.Errorf("plain")); got != "" {
		t.Errorf("apiErrorCode on a plain error = %q, want empty", got)
	}
}

func TestDoJSONRequestReportsSuccessStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodPost, srv.URL, nil)
	status, body, err := doJSONRequest(&http.Client{}, req)
	if err != nil {
		t.Fatalf("doJSONRequest: %v", err)
	}
	// 202 must be distinguishable from 200: the two mean different things at Complete.
	if status != http.StatusAccepted {
		t.Errorf("status = %d, want 202", status)
	}
	if len(body) != 0 {
		t.Errorf("body = %q, want empty", body)
	}
}
