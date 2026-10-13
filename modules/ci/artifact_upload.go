// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package ci

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/harness/cli/v3/pkg/auth"
	"github.com/harness/cli/v3/pkg/cmdctx"
)

const pushCiArtifactHandlerID = "push_ci_artifact"

const uploadWorkers = 5

const (
	envHARURL           = "HARNESS_HAR_URL"
	envNativeRegistry   = "CI_NATIVE_HAR_REGISTRY"
	envPipelineUniqueID = "HARNESS_PIPELINE_UNIQUE_ID"
	envBuildID          = "HARNESS_BUILD_ID"
)

// permanentUploadError wraps errors caused by configuration or authorization
// problems that will not resolve on retry (e.g. 401, 403, 404).
// uploadWithRetry checks for this type and fails fast instead of retrying.
type permanentUploadError struct{ cause error }

func (e *permanentUploadError) Error() string { return e.cause.Error() }
func (e *permanentUploadError) Unwrap() error { return e.cause }

// pushEntry uploads a single file. Returns the file metadata, the HAR
// package UUID, the HAR version UUID (both from the server response), and
// any error.
func pushEntry(ctx context.Context, entry uploadEntry, a *auth.ResolvedAuth, registry string) (pushedFile, string, string, error) {
	filename := filepath.Base(entry.localPath)
	fmt.Printf("Uploading %q → %s\n", filename, entry.destPath)

	file, err := os.Open(entry.localPath)
	if err != nil {
		return pushedFile{}, "", "", fmt.Errorf("failed to open %s: %w", entry.localPath, err)
	}
	defer file.Close()

	resp, err := pushGenericFile(ctx, a, registry, entry.destPath, file)
	if err != nil {
		return pushedFile{}, "", "", err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))

	if resp.StatusCode >= 400 {
		var msg string
		if len(body) > 0 {
			msg = fmt.Sprintf("server returned error %s for %q: %s", resp.Status, filename, string(body))
		} else {
			msg = fmt.Sprintf("server returned error %s for %q", resp.Status, filename)
		}
		// On auth failures suggest checking storage quota — CI Manager injects an
		// empty token when the account's quota is exceeded, which causes a 401/403.
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			msg += "\nHint: if your account's native artifact storage quota is exceeded, uploads are blocked." +
				" Check your storage usage in Harness or contact your administrator."
		}
		err := fmt.Errorf("%s", msg)
		// 4xx errors (except 429 Too Many Requests) are permanent: a bad token,
		// missing registry, or forbidden path will not self-heal on retry.
		if resp.StatusCode != http.StatusTooManyRequests && resp.StatusCode < 500 {
			return pushedFile{}, "", "", &permanentUploadError{cause: err}
		}
		return pushedFile{}, "", "", err
	}

	pf := pushedFile{
		Name:        filename,
		SourcePath:  entry.localPath,
		Destination: entry.destPath,
		Size:        entry.size,
		UploadedAt:  time.Now().UTC().Format(time.RFC3339),
	}

	// Parse the server response body. QA/prod returns:
	// {"sha256":"<hex>","packageId":"<uuid>","versionId":"<uuid>"}
	var pkgUUID, verUUID string
	if len(body) > 0 {
		var serverResp struct {
			SHA256    string `json:"sha256"`
			PackageID string `json:"packageId"`
			VersionID string `json:"versionId"`
		}
		if err := json.Unmarshal(body, &serverResp); err == nil {
			pf.SHA256 = serverResp.SHA256
			pkgUUID = serverResp.PackageID
			verUUID = serverResp.VersionID
		}
	}

	return pf, pkgUUID, verUUID, nil
}

// uploadWithRetry wraps pushEntry with bounded exponential backoff. It retries
// on any error (network failures, 5xx responses) up to maxAttempts times,
// checking ctx cancellation between attempts so a canceled context aborts
// immediately rather than sleeping through the full backoff.
func uploadWithRetry(ctx context.Context, entry uploadEntry, a *auth.ResolvedAuth, registry string) (pushedFile, string, string, error) {
	const maxAttempts = 3
	backoff := 2 * time.Second
	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		pf, pkgUUID, verUUID, err := pushEntry(ctx, entry, a, registry)
		if err == nil {
			return pf, pkgUUID, verUUID, nil
		}
		lastErr = err
		// Permanent errors will not resolve on retry — fail fast.
		var permErr *permanentUploadError
		if errors.As(err, &permErr) ||
			errors.Is(err, os.ErrNotExist) ||
			errors.Is(err, os.ErrPermission) {
			return pushedFile{}, "", "", err
		}
		if attempt < maxAttempts {
			jitter := time.Duration(rand.Int63n(int64(backoff / 2)))
			select {
			case <-ctx.Done():
				return pushedFile{}, "", "", ctx.Err()
			case <-time.After(backoff + jitter):
			}
			backoff *= 2
		}
	}
	return pushedFile{}, "", "", fmt.Errorf("failed after %d attempts: %w", maxAttempts, lastErr)
}

type uploadResult struct {
	entry          uploadEntry
	file           pushedFile
	harPackageUUID string
	harVersionUUID string
	err            error
}

// parallelUpload uploads all entries concurrently using a fixed worker pool
// (capped at uploadWorkers). Order of results is not guaranteed.
func parallelUpload(ctx context.Context, entries []uploadEntry, a *auth.ResolvedAuth, registry string) []uploadResult {
	numWorkers := uploadWorkers
	if len(entries) < numWorkers {
		numWorkers = len(entries)
	}

	type job struct{ entry uploadEntry }
	jobCh := make(chan job, len(entries))
	resCh := make(chan uploadResult, len(entries))

	var wg sync.WaitGroup
	wg.Add(numWorkers)
	for i := 0; i < numWorkers; i++ {
		go func() {
			defer wg.Done()
			for j := range jobCh {
				select {
				case <-ctx.Done():
					resCh <- uploadResult{entry: j.entry, err: ctx.Err()}
					for range jobCh {
					} // drain remaining jobs without uploading
					return
				default:
					pf, pkgUUID, verUUID, err := uploadWithRetry(ctx, j.entry, a, registry)
					resCh <- uploadResult{entry: j.entry, file: pf, harPackageUUID: pkgUUID, harVersionUUID: verUUID, err: err}
				}
			}
		}()
	}

	for _, e := range entries {
		jobCh <- job{entry: e}
	}
	close(jobCh)

	wg.Wait()
	close(resCh)

	results := make([]uploadResult, 0, len(entries))
	for r := range resCh {
		results = append(results, r)
	}
	return results
}

// pushCiArtifactHandler is the `push ci_artifact` workflow handler. It
// uploads one or more local files/directories/globs to Harness Artifact
// Registry under ctx.Auth's resolved registry — populated from pipeline_auth
// inside a pipeline step, or from the active profile outside one — then
// writes a merged JSON summary to $HARNESS_ARTIFACT_FILE_PATH when set.
func pushCiArtifactHandler(ctx *cmdctx.Ctx) error {
	if ctx.Auth.RegistryURL == "" {
		return fmt.Errorf("registry URL is not configured — run 'harness auth login', or set %s in a pipeline step", envHARURL)
	}
	if len(ctx.Args) == 0 {
		return fmt.Errorf("push ci_artifact requires at least one <source> argument")
	}

	name := strings.TrimSpace(cmdctx.GetString(ctx.FlagValues, "name"))
	if name == "" {
		name = os.Getenv(envPipelineUniqueID)
	}
	if name == "" {
		return fmt.Errorf("--name is required when %s is unset", envPipelineUniqueID)
	}

	version := strings.TrimSpace(cmdctx.GetString(ctx.FlagValues, "version"))
	if version == "" {
		buildID := os.Getenv(envBuildID)
		if buildID == "" {
			return fmt.Errorf("--version is required when %s is unset", envBuildID)
		}
		version = "v" + buildID
	}

	registry := strings.TrimSpace(cmdctx.GetString(ctx.FlagValues, "registry"))
	if registry == "" {
		registry = os.Getenv(envNativeRegistry)
	}
	if registry == "" {
		return fmt.Errorf("--registry is required when %s is unset", envNativeRegistry)
	}

	destination := strings.TrimSpace(cmdctx.GetString(ctx.FlagValues, "destination"))

	// Expand comma-separated patterns and shell-expanded globs uniformly.
	var patterns []string
	for _, arg := range ctx.Args {
		for _, p := range strings.Split(arg, ",") {
			if p = strings.TrimSpace(p); p != "" {
				patterns = append(patterns, p)
			}
		}
	}
	if len(patterns) == 0 {
		return fmt.Errorf("no source patterns given")
	}

	fmt.Printf("Scanning %d source(s)...\n", len(patterns))
	entries, err := collectEntries(patterns, name, version, destination)
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		return fmt.Errorf("no files found in source(s): %s", strings.Join(patterns, ", "))
	}

	var totalBytes int64
	for _, e := range entries {
		totalBytes += e.size
	}
	fmt.Printf("Found %d file(s) (%s) — uploading to registry %q...\n",
		len(entries), formatBytes(totalBytes), registry)

	results := parallelUpload(ctx.Context, entries, ctx.Auth, registry)

	var failed []uploadResult
	output := artifactOutput{
		ArtifactName: name,
		Version:      version,
		Registry:     registry,
	}
	for _, r := range results {
		if r.err != nil {
			failed = append(failed, r)
		} else {
			output.Files = append(output.Files, r.file)
			output.TotalBytes += r.file.Size
		}
	}
	output.FileCount = len(output.Files)

	// Promote HAR UUIDs from per-file results to top-level
	// (all files in one call share the same package/version).
	for _, r := range results {
		if r.err == nil {
			if r.harPackageUUID != "" && output.HarPackageUUID == "" {
				output.HarPackageUUID = r.harPackageUUID
			}
			if r.harVersionUUID != "" && output.HarVersionUUID == "" {
				output.HarVersionUUID = r.harVersionUUID
			}
		}
	}

	if len(failed) > 0 {
		fmt.Fprintf(os.Stderr, "\nFailed uploads (%d):\n", len(failed))
		for _, r := range failed {
			fmt.Fprintf(os.Stderr, "  - %s: %v\n", filepath.Base(r.entry.localPath), r.err)
		}
		return fmt.Errorf("%d of %d file(s) failed to upload", len(failed), len(entries))
	}

	if err := writeArtifactOutput(output); err != nil {
		return fmt.Errorf("failed to write artifact output file: %w", err)
	}

	fmt.Printf("Successfully uploaded %d file(s) to registry %q\n", output.FileCount, registry)
	return nil
}
