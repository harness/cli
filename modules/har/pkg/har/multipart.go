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
	"os"
	"sync"
	"time"

	"github.com/harness/cli/v3/pkg/cmdctx"
)

const (
	// multipartThreshold is the size at or above which a file is uploaded in parallel parts. It
	// matches the registry's default part size: below it the server returns partCount == 1, so
	// multipart would add four round trips (start, put, complete, poll) for no parallelism at all.
	multipartThreshold = 16 * 1024 * 1024

	// defaultMaxConcurrentParts bounds part uploads in flight across every file in one command.
	defaultMaxConcurrentParts = 8

	// maxPresignBatch mirrors the server's cap on how many part URLs a single response carries.
	// Uploads with more parts than this fetch the remainder in batches.
	maxPresignBatch = 100

	// presignRefreshWindow is the validity a presigned URL must have left to be worth using. A URL
	// closer than this to expiry is re-fetched first, so a slow upload does not race the signature.
	presignRefreshWindow = 60 * time.Second

	// partUploadMaxAttempts bounds retries of a single part, including the first attempt.
	partUploadMaxAttempts = 4

	// partRetryInitialDelay and partRetryMaxDelay bound the exponential backoff between attempts at
	// one part. They are deliberately separate from the poll intervals below: the two schedules
	// start at the same values today, but retuning how often the CLI polls for finalization should
	// not silently change how hard it retries a failed part.
	partRetryInitialDelay = 500 * time.Millisecond
	partRetryMaxDelay     = 5 * time.Second

	// completeMaxAttempts bounds how many times the CLI re-uploads missing parts and retries
	// completion before giving up.
	completeMaxAttempts = 3

	// Finalization happens server-side after completion is accepted, so the CLI polls for it.
	pollInitialInterval = 500 * time.Millisecond
	pollMaxInterval     = 5 * time.Second
	pollTimeout         = 30 * time.Minute

	// abortTimeout bounds the best-effort abort issued when an upload fails or is cancelled.
	abortTimeout = 15 * time.Second

	// presignExpiryLayout is the timestamp format the registry uses for a part URL's expiry.
	presignExpiryLayout = time.RFC3339
)

// Multipart session statuses reported by the registry. completed, failed and aborted are terminal.
const (
	uploadStatusOpen       = "open"
	uploadStatusFinalizing = "finalizing"
	uploadStatusCompleted  = "completed"
	uploadStatusFailed     = "failed"
	uploadStatusAborted    = "aborted"
)

// Machine-readable error codes the registry returns in values.code. These are a stable contract
// between the registry and this client, so switch on them rather than on message text.
//
// Only the first four change what the client does. The rest are declared to document the contract
// and are surfaced to the user as-is: the registry's own message for them is already actionable
// (e.g. PATH_CONFLICT reports which path is busy, OPEN_UPLOAD_LIMIT says to abort one and retry),
// and there is no recovery the client could attempt on its own.
const (
	codeMultipartUnsupported = "MULTIPART_UNSUPPORTED"
	codeIncompleteUpload     = "INCOMPLETE_UPLOAD"
	codeUploadNotFound       = "UPLOAD_NOT_FOUND"
	codeUploadNotOpen        = "UPLOAD_NOT_OPEN"
	codeDigestConflict       = "DIGEST_CONFLICT"
	codePathConflict         = "PATH_CONFLICT"
	codeOpenUploadLimit      = "OPEN_UPLOAD_LIMIT"
	codeSizeMismatch         = "SIZE_MISMATCH"
	codeInvalidRequest       = "INVALID_REQUEST"
)

// errMultipartUnsupported reports that this registry cannot do a multipart upload, either because
// the feature is off or because the endpoint does not exist on this server version. It is only ever
// returned before any file bytes are sent, so the caller can fall back to a single upload for free.
var errMultipartUnsupported = errors.New("multipart upload is not supported by this registry")

// startUploadRequest is the body of a start-upload call.
//
// The server rejects unknown fields, so this struct must carry exactly the documented set.
type startUploadRequest struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
	SHA1   string `json:"sha1,omitempty"`
	MD5    string `json:"md5,omitempty"`
	SHA512 string `json:"sha512,omitempty"`
}

// partURL is one presigned part destination.
type partURL struct {
	PartNumber int    `json:"partNumber"`
	URL        string `json:"url"`
	ExpiresAt  string `json:"expiresAt"`
}

// expired reports whether this URL has less than presignRefreshWindow of validity left. An
// unparseable or absent expiry is treated as expired so the URL is refreshed rather than trusted.
func (p partURL) expired() bool {
	if p.ExpiresAt == "" {
		return true
	}
	expiresAt, err := time.Parse(presignExpiryLayout, p.ExpiresAt)
	if err != nil {
		return true
	}
	return time.Until(expiresAt) < presignRefreshWindow
}

// startUploadResponse describes the session the server opened, or reports that the content already
// exists. partSize and partCount are authoritative: the client must never compute its own, because
// the object store rejects completion unless every non-final part is exactly partSize bytes.
type startUploadResponse struct {
	UploadID  string    `json:"uploadId"`
	Status    string    `json:"status"`
	PartSize  int64     `json:"partSize"`
	PartCount int       `json:"partCount"`
	StatusURL string    `json:"statusUrl"`
	Parts     []partURL `json:"parts"`
}

type partsResponse struct {
	Parts []partURL `json:"parts"`
}

type uploadStatusResponse struct {
	UploadID string  `json:"uploadId"`
	Status   string  `json:"status"`
	Error    *string `json:"error"`
}

type completeUploadResponse struct {
	UploadID string  `json:"uploadId"`
	Status   string  `json:"status"`
	Error    *string `json:"error"`
}

// multipartUploader uploads files in parallel parts. One instance is shared by every file in a
// command so that partSem bounds the total number of part uploads in flight: a per-file semaphore
// would let file concurrency multiply by part fan-out and open (files x parts) connections at once.
type multipartUploader struct {
	ctx      *cmdctx.Ctx
	registry string

	// control carries the start/status/parts/complete/abort calls, which are authenticated.
	control *http.Client

	// part uploads go to presigned URLs, where the signature in the URL *is* the credential.
	// Sending an Authorization or x-api-key header alongside it makes the object store reject the
	// request, so this client is deliberately separate and has no auth wiring.
	part *http.Client

	partSem chan struct{}
}

func newMultipartUploader(
	ctx *cmdctx.Ctx, control *http.Client, registry string, maxConcurrentParts int,
) *multipartUploader {
	if maxConcurrentParts <= 0 {
		maxConcurrentParts = defaultMaxConcurrentParts
	}
	return &multipartUploader{
		ctx:      ctx,
		registry: registry,
		control:  control,
		part:     &http.Client{Timeout: 10 * time.Minute},
		partSem:  make(chan struct{}, maxConcurrentParts),
	}
}

// upload uploads localPath as relPath using a multipart session.
//
// sums is supplied by the caller rather than computed here because the single-upload fallback needs
// the same digests: hashing in both places would read a file that is large by definition twice.
//
// It returns errMultipartUnsupported if this registry cannot do multipart, in which case no bytes
// were sent and the caller should fall back to a single upload.
func (u *multipartUploader) upload(
	name, version, relPath, localPath string, size int64, sums fileChecksums,
) error {
	session, err := u.startUpload(name, version, relPath, size, sums)
	if err != nil {
		return err
	}

	// The server already holds this content for the account, so there is nothing to upload.
	if session.Status == uploadStatusCompleted {
		fmt.Fprintf(os.Stderr, "%s already exists in the registry, skipped upload\n", relPath)
		return nil
	}

	if err := u.validateSession(session, size); err != nil {
		return err
	}

	// Abort the session on any failure so it does not hold the one-in-flight-upload-per-path slot
	// until it expires, which would make an immediate retry fail with a path conflict.
	done := false
	defer func() {
		if !done {
			u.abortUpload(session.UploadID)
		}
	}()

	if err := u.uploadPartSet(localPath, size, session, allPartNumbers(session.PartCount)); err != nil {
		return err
	}
	if err := u.completeUpload(localPath, size, session); err != nil {
		return err
	}
	if err := u.pollUntilTerminal(relPath, session.UploadID); err != nil {
		return err
	}

	done = true
	return nil
}

// validateSession checks the sizing the server handed back is self-consistent before any bytes are
// sent, so a server bug surfaces as a clear error rather than a corrupted object at completion.
func (u *multipartUploader) validateSession(session *startUploadResponse, size int64) error {
	switch {
	case session.UploadID == "":
		return fmt.Errorf("registry opened a multipart upload without an upload id")
	case session.PartSize <= 0:
		return fmt.Errorf("registry returned an invalid part size %d", session.PartSize)
	case session.PartCount <= 0:
		return fmt.Errorf("registry returned an invalid part count %d", session.PartCount)
	}

	// Every part but the last is exactly partSize, so the declared sizing must cover the file with
	// less than one part of slack.
	if got, want := int64(session.PartCount)*session.PartSize, size; got < want || got-want >= session.PartSize {
		return fmt.Errorf(
			"registry part sizing does not match file: partCount=%d partSize=%d for %d bytes",
			session.PartCount, session.PartSize, size,
		)
	}
	return nil
}

// startUpload opens a multipart session, or reports that the content is already stored.
//
// path is the same string the single-upload route carries after /files/, namely
// {package}/{version}/{file...}. The server applies the same layout rules to it as it does to that
// route's remainder: for a GENERIC registry it splits out the package, version and file path and
// requires at least those three segments, while the other /files package types (RAW, HELM_HTTP,
// CRAN) treat it as a flat file path. Sending the identical string either way is what makes the two
// upload flows land the artifact in the same place for every one of those package types.
func (u *multipartUploader) startUpload(
	name, version, relPath string, size int64, sums fileChecksums,
) (*startUploadResponse, error) {
	body, err := json.Marshal(startUploadRequest{
		Path:   fmt.Sprintf("%s/%s/%s", name, version, relPath),
		Size:   size,
		SHA256: sums.SHA256,
		SHA1:   sums.SHA1,
		MD5:    sums.MD5,
		SHA512: sums.SHA512,
	})
	if err != nil {
		return nil, fmt.Errorf("encoding start upload request: %w", err)
	}

	req, err := u.newControlRequest(http.MethodPost, "", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	status, respBody, err := doJSONRequest(u.control, req)
	if err != nil {
		if startFailureMeansUnsupported(u.ctx.Context, err) {
			return nil, errMultipartUnsupported
		}
		return nil, fmt.Errorf("starting multipart upload: %w", err)
	}

	var session startUploadResponse
	if err := json.Unmarshal(respBody, &session); err != nil {
		return nil, fmt.Errorf("decoding start upload response (HTTP %d): %w", status, err)
	}
	return &session, nil
}

// startFailureMeansUnsupported reports whether a failed start should fall back to a single upload
// instead of failing the push.
//
// A 501 with MULTIPART_UNSUPPORTED says the server knows the endpoint but has the feature disabled,
// and a 404 says this server version does not have the endpoint at all. A transport failure counts
// too: no response arrived, so multipart availability is simply unknown, and a single PUT is a
// strictly simpler request that deserves the attempt rather than failing a push outright.
//
// Every other HTTP status is a real answer from the registry and must surface. Falling back on a
// 401, 403 or 5xx would mask the cause behind a second attempt that hits the same wall, and the
// user would see a confusing error from the fallback instead of the actual one.
func startFailureMeansUnsupported(ctx context.Context, err error) bool {
	// Whether to fall back is decided from the command's own context, not from the error text. A
	// cancelled or expired command says nothing about multipart support, and retrying under a message
	// claiming the registry lacks the feature would be wrong. But the error alone cannot tell the two
	// apart: http.Client.Timeout also reports context.DeadlineExceeded, so matching on the error would
	// refuse to fall back in exactly the case that needs it - an unreachable registry, where the
	// retries are exhausted and the client's own timeout fires while awaiting headers.
	if ctx.Err() != nil {
		return false
	}

	var apiErr *apiError
	if !errors.As(err, &apiErr) {
		return true
	}
	return apiErr.Code == codeMultipartUnsupported || apiErr.StatusCode == http.StatusNotFound
}

// uploadPartSet uploads the given part numbers in parallel, bounded by the shared part semaphore.
// It serves both the initial upload of every part and the repair of the specific parts the object
// store turned out to be missing, because the two differ only in which numbers they cover.
func (u *multipartUploader) uploadPartSet(
	localPath string, size int64, session *startUploadResponse, partNumbers []int,
) error {
	f, err := os.Open(localPath)
	if err != nil {
		return fmt.Errorf("opening %q: %w", localPath, err)
	}
	defer f.Close()

	urls := newPartURLCache(u, session)

	var wg sync.WaitGroup
	errs := make([]error, len(partNumbers))

	// ctx is cancelled as soon as one part fails so the remaining parts stop early instead of
	// uploading bytes for a session that is already doomed.
	ctx, cancel := context.WithCancel(u.ctx.Context)
	defer cancel()

	for i, partNumber := range partNumbers {
		wg.Add(1)
		go func(i, partNumber int) {
			defer wg.Done()

			select {
			case u.partSem <- struct{}{}:
			case <-ctx.Done():
				errs[i] = ctx.Err()
				return
			}
			defer func() { <-u.partSem }()

			if err := u.uploadPart(ctx, f, size, session, urls, partNumber); err != nil {
				errs[i] = fmt.Errorf("part %d: %w", partNumber, err)
				cancel()
			}
		}(i, partNumber)
	}
	wg.Wait()

	return joinPartErrors(errs)
}

// allPartNumbers returns every part number of a session, 1-based.
func allPartNumbers(partCount int) []int {
	numbers := make([]int, partCount)
	for i := range numbers {
		numbers[i] = i + 1
	}
	return numbers
}

// uploadPart PUTs one part, refreshing its presigned URL and retrying on transient failures.
func (u *multipartUploader) uploadPart(
	ctx context.Context,
	f *os.File,
	size int64,
	session *startUploadResponse,
	urls *partURLCache,
	partNumber int,
) error {
	offset := int64(partNumber-1) * session.PartSize
	length := session.PartSize
	if remaining := size - offset; remaining < length {
		length = remaining
	}
	if length <= 0 {
		return fmt.Errorf("computed a zero-length part at offset %d of %d bytes", offset, size)
	}

	var lastErr error
	for attempt := 1; attempt <= partUploadMaxAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}

		url, err := urls.get(ctx, partNumber)
		if err != nil {
			return err
		}

		// A SectionReader is re-created per attempt so a retry re-reads the same bytes. Handing the
		// *os.File itself to the request would advance the shared file offset and, because parts
		// upload concurrently, read the wrong region.
		body := io.NewSectionReader(f, offset, length)

		req, err := http.NewRequestWithContext(ctx, http.MethodPut, url, body)
		if err != nil {
			return fmt.Errorf("building part request: %w", err)
		}
		// The presigned URL carries its own credentials; no auth header is set here on purpose.
		// Content-Length is mandatory because object stores reject an unsized part body.
		req.ContentLength = length
		req.Header.Set("Content-Type", "application/octet-stream")

		resp, err := u.part.Do(req)
		if err != nil {
			lastErr = err
		} else {
			// The part ETag is deliberately ignored: completion asks the object store itself which
			// parts it holds, so the client never sends a part list back.
			_, _ = io.Copy(io.Discard, resp.Body)
			resp.Body.Close()

			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				return nil
			}
			lastErr = fmt.Errorf("HTTP %d uploading part to object storage", resp.StatusCode)

			// 403 from an object store almost always means the signature expired rather than a
			// permission change, so drop the cached URL and sign a fresh one for the next attempt.
			if resp.StatusCode == http.StatusForbidden {
				urls.invalidate(partNumber)
			}
			if !retryablePartStatus(resp.StatusCode) {
				return lastErr
			}
		}

		if attempt < partUploadMaxAttempts {
			if err := sleepCtx(ctx, backoffDelay(attempt)); err != nil {
				return err
			}
		}
	}
	return fmt.Errorf("giving up after %d attempts: %w", partUploadMaxAttempts, lastErr)
}

// retryablePartStatus reports whether a failed part PUT is worth retrying. A 403 is included
// because an expired signature presents as one and is fixed by re-signing.
func retryablePartStatus(status int) bool {
	switch {
	case status == http.StatusRequestTimeout,
		status == http.StatusTooManyRequests,
		status == http.StatusForbidden:
		return true
	case status >= 500:
		return true
	default:
		return false
	}
}

// backoffDelay returns an exponential delay for retrying a part, given a 1-based attempt number.
func backoffDelay(attempt int) time.Duration {
	delay := partRetryInitialDelay << (attempt - 1)
	if delay > partRetryMaxDelay {
		delay = partRetryMaxDelay
	}
	return delay
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// partURLCache hands out presigned part URLs, fetching them from the registry in batches and
// re-signing any that are expired or near expiry. The server caps a single response at
// maxPresignBatch parts, so an upload with more parts than that necessarily fetches more than once.
type partURLCache struct {
	uploader *multipartUploader
	uploadID string
	total    int

	mu    sync.Mutex
	byNum map[int]partURL
}

func newPartURLCache(uploader *multipartUploader, session *startUploadResponse) *partURLCache {
	cache := &partURLCache{
		uploader: uploader,
		uploadID: session.UploadID,
		total:    session.PartCount,
		byNum:    make(map[int]partURL, session.PartCount),
	}
	for _, part := range session.Parts {
		cache.byNum[part.PartNumber] = part
	}
	return cache
}

// get returns a usable URL for partNumber, fetching a fresh batch if the cached one is missing or
// close to expiry.
func (c *partURLCache) get(ctx context.Context, partNumber int) (string, error) {
	c.mu.Lock()
	cached, ok := c.byNum[partNumber]
	c.mu.Unlock()
	if ok && !cached.expired() {
		return cached.URL, nil
	}

	// The lock is released before fetching on purpose: holding it across a network call would
	// serialise every part behind one re-sign. The cost is that two parts needing the same window
	// may both fetch it, which is harmless — signing is idempotent and the results are identical.
	//
	// Fetch a window starting at this part rather than just this one part: parts are uploaded in
	// ascending order, so the neighbours are about to be needed too.
	from := partNumber
	to := from + maxPresignBatch - 1
	if to > c.total {
		to = c.total
	}

	fetched, err := c.uploader.fetchParts(ctx, c.uploadID, from, to)
	if err != nil {
		return "", err
	}

	c.mu.Lock()
	for _, part := range fetched {
		c.byNum[part.PartNumber] = part
	}
	refreshed, ok := c.byNum[partNumber]
	c.mu.Unlock()

	if !ok {
		return "", fmt.Errorf("registry did not return a URL for part %d", partNumber)
	}
	return refreshed.URL, nil
}

// invalidate drops the cached URL for partNumber so the next get re-signs it.
func (c *partURLCache) invalidate(partNumber int) {
	c.mu.Lock()
	delete(c.byNum, partNumber)
	c.mu.Unlock()
}

// fetchParts asks the registry for presigned URLs for parts from..to inclusive.
func (u *multipartUploader) fetchParts(ctx context.Context, uploadID string, from, to int) ([]partURL, error) {
	req, err := u.newControlRequest(http.MethodGet, uploadID+"/parts", nil)
	if err != nil {
		return nil, err
	}
	req = req.WithContext(ctx)

	q := req.URL.Query()
	q.Set("from", fmt.Sprint(from))
	q.Set("to", fmt.Sprint(to))
	req.URL.RawQuery = q.Encode()

	_, body, err := doJSONRequest(u.control, req)
	if err != nil {
		return nil, fmt.Errorf("fetching part URLs %d-%d: %w", from, to, err)
	}

	var parsed partsResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("decoding part URLs response: %w", err)
	}
	return parsed.Parts, nil
}

// completeUpload asks the registry to assemble the parts into the final object.
//
// The body is empty by design: the server lists the parts from the object store itself, so it needs
// neither ETags nor a part list. If the store is missing parts the server reports which ones, and
// the session stays open so they can be re-uploaded and completion retried.
func (u *multipartUploader) completeUpload(localPath string, size int64, session *startUploadResponse) error {
	for attempt := 1; ; attempt++ {
		req, err := u.newControlRequest(http.MethodPost, session.UploadID+"/complete", nil)
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/json")

		_, body, err := doJSONRequest(u.control, req)
		if err == nil {
			var parsed completeUploadResponse
			// A malformed body is not fatal here: the poll that follows is the real source of truth.
			if jsonErr := json.Unmarshal(body, &parsed); jsonErr == nil && parsed.Status == uploadStatusFailed {
				return fmt.Errorf("registry failed to finalize the upload: %s", derefOr(parsed.Error, "unknown error"))
			}
			return nil
		}

		missing := missingParts(err)
		if apiErrorCode(err) != codeIncompleteUpload || len(missing) == 0 || attempt >= completeMaxAttempts {
			return fmt.Errorf("completing multipart upload: %w", err)
		}

		fmt.Fprintf(os.Stderr, "Re-uploading %d missing part(s) ...\n", len(missing))
		if err := u.uploadPartSet(localPath, size, session, missing); err != nil {
			return err
		}
	}
}

// missingParts extracts the part numbers an INCOMPLETE_UPLOAD error reports as absent.
func missingParts(err error) []int {
	var apiErr *apiError
	if !errors.As(err, &apiErr) {
		return nil
	}
	raw, ok := apiErr.Values["missingParts"].([]any)
	if !ok {
		return nil
	}

	parts := make([]int, 0, len(raw))
	for _, v := range raw {
		// JSON numbers decode to float64 through an any-typed map.
		if n, ok := v.(float64); ok {
			parts = append(parts, int(n))
		}
	}
	return parts
}

// pollUntilTerminal waits for the registry to finish finalizing the upload.
//
// Completion is accepted asynchronously: the registry assembles the object and a background job
// verifies the digest before the artifact becomes visible, so the upload is only really done once
// the session reaches a terminal status.
func (u *multipartUploader) pollUntilTerminal(relPath, uploadID string) error {
	ctx, cancel := context.WithTimeout(u.ctx.Context, pollTimeout)
	defer cancel()

	fmt.Fprintf(os.Stderr, "Finalizing %s ...\n", relPath)

	interval := pollInitialInterval
	for {
		status, err := u.getUploadStatus(ctx, uploadID)
		if err != nil {
			return err
		}

		switch status.Status {
		case uploadStatusCompleted:
			return nil
		case uploadStatusFailed:
			return fmt.Errorf("registry failed to finalize the upload: %s", derefOr(status.Error, "unknown error"))
		case uploadStatusAborted:
			return fmt.Errorf("the upload was aborted before it finished")
		case uploadStatusOpen, uploadStatusFinalizing:
			// Still working; fall through to wait.
		default:
			return fmt.Errorf("registry reported an unknown upload status %q", status.Status)
		}

		if err := sleepCtx(ctx, interval); err != nil {
			if errors.Is(err, context.DeadlineExceeded) {
				return fmt.Errorf("upload did not finish finalizing within %s", pollTimeout)
			}
			return err
		}
		if interval < pollMaxInterval {
			interval *= 2
			if interval > pollMaxInterval {
				interval = pollMaxInterval
			}
		}
	}
}

func (u *multipartUploader) getUploadStatus(ctx context.Context, uploadID string) (*uploadStatusResponse, error) {
	req, err := u.newControlRequest(http.MethodGet, uploadID, nil)
	if err != nil {
		return nil, err
	}
	req = req.WithContext(ctx)

	_, body, err := doJSONRequest(u.control, req)
	if err != nil {
		return nil, fmt.Errorf("checking upload status: %w", err)
	}

	var status uploadStatusResponse
	if err := json.Unmarshal(body, &status); err != nil {
		return nil, fmt.Errorf("decoding upload status response: %w", err)
	}
	return &status, nil
}

// abortUpload releases a session that will not be completed. It is best effort: the caller is
// already returning an error, and the server expires abandoned sessions anyway.
//
// It deliberately does not use the command context, which may already be cancelled — that is one of
// the main reasons an abort is needed.
func (u *multipartUploader) abortUpload(uploadID string) {
	if uploadID == "" {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), abortTimeout)
	defer cancel()

	req, err := u.newControlRequest(http.MethodDelete, uploadID, nil)
	if err != nil {
		return
	}
	req = req.WithContext(ctx)

	if _, _, err := doJSONRequest(u.control, req); err != nil {
		// An already-gone session is the expected outcome of racing the server's own cleanup.
		if code := apiErrorCode(err); code == codeUploadNotFound || code == codeUploadNotOpen {
			return
		}
		fmt.Fprintf(os.Stderr, "Warning: could not abort multipart upload %s: %v\n", uploadID, err)
	}
}

// newControlRequest builds an authenticated request against the registry's uploads endpoints.
// suffix is appended to the collection path, e.g. "" for the collection itself,
// "{uploadID}" for one session, or "{uploadID}/complete".
func (u *multipartUploader) newControlRequest(method, suffix string, body io.Reader) (*http.Request, error) {
	subpath := u.registry + "/uploads"
	if suffix != "" {
		subpath += "/" + suffix
	}

	url, err := buildPkgURL(u.ctx.Auth.RegistryURL, u.ctx.Auth.AccountID, subpath)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(u.ctx.Context, method, url, body)
	if err != nil {
		return nil, fmt.Errorf("building %s %s request: %w", method, subpath, err)
	}
	setAuthHeader(req, u.ctx.Auth)
	return req, nil
}

// joinPartErrors combines per-part errors into one. Cancellations are dropped when a real failure
// is present, because the first failing part cancels the others and their context.Canceled errors
// would otherwise bury the actual cause.
func joinPartErrors(errs []error) error {
	real := make([]error, 0, len(errs))
	for _, err := range errs {
		if err != nil && !errors.Is(err, context.Canceled) {
			real = append(real, err)
		}
	}
	if len(real) > 0 {
		return errors.Join(real...)
	}
	return errors.Join(errs...)
}

// derefOr returns *s, or fallback when s is nil or empty.
func derefOr(s *string, fallback string) string {
	if s == nil || *s == "" {
		return fallback
	}
	return *s
}
