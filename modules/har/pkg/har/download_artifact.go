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
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"

	"github.com/harness/cli/v3/pkg/auth"
	"github.com/harness/cli/v3/pkg/cmdctx"
)

const executeArtifactDownloadHandlerID = "execute_artifact_download"

const (
	downloadDefaultConcurrency = 4
	downloadDefaultPageSize    = 100
)

// fileSearchRequest is the POST body for /har/api/v3/files/search.
// Regex is a POSIX ERE evaluated server-side by Postgres (`~`, case-sensitive).
type fileSearchRequest struct {
	Regex    string `json:"regex"`
	Registry string `json:"registry"`
}

// fileSearchResponse mirrors ListFilesResponse: items and hasMore are top-level,
// not wrapped in a "data" envelope.
type fileSearchResponse struct {
	Items []struct {
		Name        string  `json:"name"`
		Path        string  `json:"path"`
		DownloadUrl *string `json:"downloadUrl"`
		Size        string  `json:"size"`
	} `json:"items"`
	HasMore bool  `json:"hasMore"`
	Page    int64 `json:"page"`
	Size    int64 `json:"size"`
}

// fileItem is one result from the HAR file search API.
type fileItem struct {
	Path        string // registry-relative artifact path
	DownloadURL string // direct URL from the API; empty means item is not downloadable
	Size        string
}

func executeArtifactDownloadHandler(ctx *cmdctx.Ctx) error {
	registry := cmdctx.GetString(ctx.FlagValues, "registry")
	regex := cmdctx.GetString(ctx.FlagValues, "regex")
	dest := cmdctx.GetString(ctx.FlagValues, "dest")
	dryRun := cmdctx.GetBool(ctx.FlagValues, "dry-run")
	overwrite := cmdctx.GetBool(ctx.FlagValues, "overwrite")
	flatten := cmdctx.GetBool(ctx.FlagValues, "flatten")
	concurrencyStr := cmdctx.GetString(ctx.FlagValues, "concurrency")
	pageSizeStr := cmdctx.GetString(ctx.FlagValues, "page-size")

	concurrency := downloadDefaultConcurrency
	if concurrencyStr != "" {
		n, err := strconv.Atoi(concurrencyStr)
		if err != nil || n < 1 {
			return fmt.Errorf("invalid --concurrency %q: must be a positive integer", concurrencyStr)
		}
		concurrency = n
	}

	pageSize := downloadDefaultPageSize
	if pageSizeStr != "" {
		n, err := strconv.Atoi(pageSizeStr)
		if err != nil || n < 1 || n > 100 {
			return fmt.Errorf("invalid --page-size %q: must be an integer between 1 and 100", pageSizeStr)
		}
		pageSize = n
	}

	bgCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigs)
	go func() {
		select {
		case <-sigs:
			fmt.Fprintln(os.Stderr, "\nInterrupted. Shutting down gracefully...")
			cancel()
		case <-bgCtx.Done():
		}
	}()

	allFiles, err := artifactSearchFiles(bgCtx, ctx.Auth, regex, registry, pageSize)
	if err != nil {
		return fmt.Errorf("searching files in %q: %w", registry, err)
	}

	if len(allFiles) == 0 {
		fmt.Println("No files matched.")
		return nil
	}

	// Pre-filter: skip items with no download URL and items whose path would
	// escape dest (path-traversal guard).
	downloadable := make([]fileItem, 0, len(allFiles))
	skippedNoURL, skippedUnsafePath := 0, 0
	for _, item := range allFiles {
		if item.DownloadURL == "" {
			skippedNoURL++
			continue
		}
		pathForCheck := item.Path
		if flatten {
			pathForCheck = filepath.Base(item.Path)
		}
		if !isWithinDest(dest, pathForCheck) {
			skippedUnsafePath++
			continue
		}
		downloadable = append(downloadable, item)
	}

	if len(downloadable) == 0 {
		return fmt.Errorf("no downloadable files: %d missing download URL, %d rejected as unsafe path",
			skippedNoURL, skippedUnsafePath)
	}

	// Flatten collision detection.
	// Dry-run: report collisions as a warning then show the manifest (no abort).
	// Real run: abort before any download starts.
	var collisions []string
	if flatten {
		paths := make([]string, len(downloadable))
		for i, item := range downloadable {
			paths[i] = item.Path
		}
		collisions = flattenCollisions(paths)
	}

	if dryRun {
		fmt.Printf("Dry run — %d file(s) matched", len(downloadable))
		if skippedNoURL > 0 || skippedUnsafePath > 0 {
			fmt.Printf(" (%d no-URL, %d unsafe-path skipped)", skippedNoURL, skippedUnsafePath)
		}
		fmt.Println(":")
		for _, item := range downloadable {
			fmt.Printf("  %s → %s\n", item.Path, resolveOutPath(dest, item.Path, flatten))
		}
		if len(collisions) > 0 {
			fmt.Fprintf(os.Stderr, "\nWarning — %d flatten collision(s) (real run would abort):\n", len(collisions))
			for _, c := range collisions {
				fmt.Fprintln(os.Stderr, "  "+c)
			}
		}
		return nil
	}

	if len(collisions) > 0 {
		fmt.Fprintln(os.Stderr, "Flatten name collisions — multiple paths share the same basename:")
		for _, c := range collisions {
			fmt.Fprintln(os.Stderr, "  "+c)
		}
		return fmt.Errorf("use a narrower --regex or remove --flatten to resolve %d collision(s)", len(collisions))
	}

	if err := os.MkdirAll(dest, 0755); err != nil {
		return fmt.Errorf("creating destination directory %q: %w", dest, err)
	}

	fmt.Fprintf(os.Stderr, "Downloading %d file(s) to %q...\n", len(downloadable), dest)

	var (
		sem        = make(chan struct{}, concurrency)
		wg         sync.WaitGroup
		mu         sync.Mutex
		dlErrors   []string
		downloaded int64
		skipped    int64
	)

	dlClient := newDownloadHTTPClient()

loop:
	for _, item := range downloadable {
		select {
		case <-bgCtx.Done():
			break loop
		default:
		}
		select {
		case sem <- struct{}{}:
		case <-bgCtx.Done():
			break loop
		}
		wg.Add(1)
		go func(it fileItem) {
			defer wg.Done()
			defer func() { <-sem }()

			outPath := resolveOutPath(dest, it.Path, flatten)

			if !overwrite {
				if _, statErr := os.Stat(outPath); statErr == nil {
					fmt.Fprintf(os.Stderr, "  skip (exists) %s\n", it.Path)
					atomic.AddInt64(&skipped, 1)
					return
				}
			}

			if dlErr := artifactDownloadFile(bgCtx, dlClient, ctx.Auth, it.DownloadURL, outPath); dlErr != nil {
				mu.Lock()
				dlErrors = append(dlErrors, fmt.Sprintf("%s: %v", it.Path, dlErr))
				mu.Unlock()
				return
			}
			atomic.AddInt64(&downloaded, 1)
		}(item)
	}
	wg.Wait()

	dl := atomic.LoadInt64(&downloaded)
	sk := atomic.LoadInt64(&skipped)
	fmt.Fprintf(os.Stderr, "Done. Downloaded: %d, Skipped: %d, Failed: %d", dl, sk, int64(len(dlErrors)))
	if skippedNoURL > 0 || skippedUnsafePath > 0 {
		fmt.Fprintf(os.Stderr, " (%d no-URL, %d unsafe-path not attempted)", skippedNoURL, skippedUnsafePath)
	}
	fmt.Fprintln(os.Stderr)

	if len(dlErrors) > 0 {
		fmt.Fprintln(os.Stderr, "Errors:")
		for _, e := range dlErrors {
			fmt.Fprintln(os.Stderr, "  "+e)
		}
		return fmt.Errorf("%d file(s) failed to download", len(dlErrors))
	}
	return nil
}

// resolveOutPath returns the filesystem path under destDir for an artifact.
// With flatten, the registry sub-path is collapsed to just the filename.
func resolveOutPath(destDir, artifactPath string, flatten bool) string {
	rel := filepath.FromSlash(artifactPath)
	if flatten {
		rel = filepath.Base(rel)
	}
	return filepath.Join(destDir, rel)
}

// isWithinDest guards against path-traversal payloads such as "../../etc/passwd".
func isWithinDest(destDir, registryPath string) bool {
	destPath := filepath.Join(destDir, filepath.FromSlash(registryPath))
	rel, err := filepath.Rel(destDir, destPath)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// artifactSearchFiles pages through the HAR file search API and returns
// matched artifacts with their direct download URLs.
func artifactSearchFiles(ctx context.Context, a *auth.ResolvedAuth, regex, registry string, pageSize int) ([]fileItem, error) {
	apiBase := strings.TrimRight(a.APIUrl, "/")

	body, err := json.Marshal(fileSearchRequest{Regex: regex, Registry: registry})
	if err != nil {
		return nil, fmt.Errorf("encoding search request: %w", err)
	}

	var all []fileItem
	page := 0
	client := newHTTPClient()

	for {
		u, err := url.Parse(apiBase + "/har/api/v3/files/search")
		if err != nil {
			return nil, fmt.Errorf("parsing search URL: %w", err)
		}
		q := u.Query()
		q.Set("account_identifier", a.AccountID)
		// org_identifier and project_identifier are intentionally omitted here.
		// The v3 files/search endpoint has a server-side bug where passing these
		// params causes it to fail resolving the registry's parent space (it uses
		// the account's internal numeric ID rather than the project's). The hc CLI
		// spec includes these params, but they should be added back once the backend
		// fixes the scoping logic. Without them, search falls back to account-level
		// lookup which correctly finds project-scoped registries.
		q.Set("page", strconv.Itoa(page))
		q.Set("size", strconv.Itoa(pageSize))
		u.RawQuery = q.Encode()

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader(body))
		if err != nil {
			return nil, fmt.Errorf("building search request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")
		setAuthHeader(req, a)

		respBody, err := doRequest(client, req)
		if err != nil {
			return nil, fmt.Errorf("search request: %w", err)
		}

		var result fileSearchResponse
		if err := json.Unmarshal(respBody, &result); err != nil {
			return nil, fmt.Errorf("parsing search response: %w", err)
		}

		for _, item := range result.Items {
			// Path is the canonical field; fall back to Name if absent.
			path := item.Path
			if path == "" {
				path = item.Name
			}
			dlURL := ""
			if item.DownloadUrl != nil {
				dlURL = *item.DownloadUrl
			}
			all = append(all, fileItem{Path: path, DownloadURL: dlURL, Size: item.Size})
		}

		// Guard on an empty page too: a server returning hasMore=true with no
		// items would otherwise re-request the same page forever.
		if !result.HasMore || len(result.Items) == 0 {
			break
		}
		page++
	}
	return all, nil
}

// artifactDownloadFile downloads from downloadURL and writes atomically to outPath.
// Auth headers are stripped on cross-host redirects by newDownloadHTTPClient.
func artifactDownloadFile(ctx context.Context, client *http.Client, a *auth.ResolvedAuth, downloadURL, outPath string) error {
	if err := os.MkdirAll(filepath.Dir(outPath), 0755); err != nil {
		return fmt.Errorf("creating directory: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, downloadURL, nil)
	if err != nil {
		return fmt.Errorf("building request: %w", err)
	}
	setAuthHeader(req, a)

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("download request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}

	tmp, err := os.CreateTemp(filepath.Dir(outPath), ".harness-download-*")
	if err != nil {
		return fmt.Errorf("creating temp file: %w", err)
	}
	tmpPath := tmp.Name()

	written, copyErr := io.Copy(tmp, resp.Body)
	closeErr := tmp.Close()
	if copyErr != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("writing download: %w", copyErr)
	}
	if closeErr != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("flushing download: %w", closeErr)
	}

	if err := os.Rename(tmpPath, outPath); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("renaming to %q: %w", outPath, err)
	}

	fmt.Fprintf(os.Stderr, "  downloaded %s (%d bytes)\n", filepath.Base(outPath), written)
	return nil
}

// newDownloadHTTPClient extends newHTTPClient with a redirect policy that strips
// auth headers when a redirect changes host or scheme, preventing API tokens
// from reaching third-party storage providers such as presigned S3 URLs.
func newDownloadHTTPClient() *http.Client {
	client := newHTTPClient()
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return errors.New("stopped after 10 redirects")
		}
		if len(via) > 0 {
			orig := via[0].URL
			if !strings.EqualFold(req.URL.Host, orig.Host) ||
				!strings.EqualFold(req.URL.Scheme, orig.Scheme) {
				req.Header.Del("x-api-key")
				req.Header.Del("Authorization")
			}
		}
		return nil
	}
	return client
}

// flattenCollisions returns one descriptive string per basename shared by more
// than one path. Empty slice means no collisions.
func flattenCollisions(files []string) []string {
	seen := map[string]string{}
	var out []string
	for _, f := range files {
		base := filepath.Base(filepath.FromSlash(f))
		if first, ok := seen[base]; ok {
			out = append(out, fmt.Sprintf("%q and %q both flatten to %q", first, f, base))
			seen[base] = f // advance so the next collision pairs with the most recent file
		} else {
			seen[base] = f
		}
	}
	return out
}
