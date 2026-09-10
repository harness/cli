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
	"time"

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
	outFile := ctx.FormatFlags.OutFile

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
		// Always write a JSON report file; never dump the file list to the terminal.
		// Use --out to specify a custom path; otherwise auto-generate under dry-run-output/.
		target := outFile
		if target == "" {
			ts := time.Now().Format("20060102_150405")
			target = filepath.Join("dry-run-output", fmt.Sprintf("download-dryrun-output-%s.json", ts))
		}
		if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			return fmt.Errorf("creating dry-run output directory: %w", err)
		}
		b, err := writeDryRunJSON(io.Discard, downloadable, dest, flatten, skippedNoURL, skippedUnsafePath)
		if err != nil {
			return err
		}
		if err := os.WriteFile(target, b, 0644); err != nil {
			return fmt.Errorf("writing dry-run output to %q: %w", target, err)
		}
		summary := fmt.Sprintf("Dry run — %d file(s) found, %d matched", len(allFiles), len(downloadable))
		if skippedNoURL > 0 || skippedUnsafePath > 0 {
			summary += fmt.Sprintf(" (%d no-URL, %d unsafe-path skipped)", skippedNoURL, skippedUnsafePath)
		}
		if len(collisions) > 0 {
			summary += fmt.Sprintf(", %d flatten collision(s)", len(collisions))
		}
		fmt.Fprintf(os.Stderr, "%s. Output written to %s\n", summary, target)
		return nil
	}

	if len(collisions) > 0 {
		// Write the collision details to a file; keep terminal output minimal.
		ts := time.Now().Format("20060102_150405")
		conflictPath := filepath.Join("dry-run-output", fmt.Sprintf("conflict-download-%s.json", ts))
		if err := writeConflictJSON(conflictPath, downloadable, dest); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: could not write conflict file: %v\n", err)
		} else {
			fmt.Fprintf(os.Stderr, "%d file(s) found, %d matched, %d flatten collision(s) detected. Run with --dry-run to see the full file list.\nConflict details: %s\n",
				len(allFiles), len(downloadable), len(collisions), conflictPath)
		}
		return errors.New("")
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

// dryRunOutput is the structured JSON representation of a dry-run result.
// Emitted when --format json (or --json) is combined with --dry-run.
type dryRunOutput struct {
	Matched   int              `json:"matched"`
	Skipped   *dryRunSkipped   `json:"skipped,omitempty"`
	Files     []dryRunFile     `json:"files"`
	Conflicts []dryRunConflict `json:"conflicts,omitempty"`
}

type dryRunSkipped struct {
	NoURL      int `json:"no_url,omitempty"`
	UnsafePath int `json:"unsafe_path,omitempty"`
}

type dryRunFile struct {
	SourcePath string `json:"source_path"`
	DestPath   string `json:"dest_path"`
	Size       string `json:"size,omitempty"`
}

type dryRunConflict struct {
	DestPath string   `json:"dest_path"`
	Sources  []string `json:"sources"`
}

// writeDryRunJSON marshals the dry-run result as indented JSON and writes it to w.
// Returns the marshalled bytes so callers can also write to a file.
func writeDryRunJSON(w io.Writer, items []fileItem, dest string, flatten bool, skippedNoURL, skippedUnsafePath int) ([]byte, error) {
	files := make([]dryRunFile, 0, len(items))
	for _, item := range items {
		files = append(files, dryRunFile{
			SourcePath: item.Path,
			DestPath:   resolveOutPath(dest, item.Path, flatten),
			Size:       item.Size,
		})
	}

	var conflicts []dryRunConflict
	if flatten {
		paths := make([]string, len(items))
		for i, item := range items {
			paths[i] = item.Path
		}
		for base, srcs := range flattenCollisionMap(paths) {
			conflicts = append(conflicts, dryRunConflict{
				DestPath: resolveOutPath(dest, srcs[0], true),
				Sources:  srcs,
			})
			_ = base
		}
	}

	var skipped *dryRunSkipped
	if skippedNoURL > 0 || skippedUnsafePath > 0 {
		skipped = &dryRunSkipped{NoURL: skippedNoURL, UnsafePath: skippedUnsafePath}
	}

	out := dryRunOutput{
		Matched:   len(items),
		Skipped:   skipped,
		Files:     files,
		Conflicts: conflicts,
	}

	b, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshalling dry-run output: %w", err)
	}
	_, err = fmt.Fprintln(w, string(b))
	return b, err
}

// writeConflictJSON writes a JSON file listing all flatten collisions to conflictPath.
func writeConflictJSON(conflictPath string, items []fileItem, dest string) error {
	if err := os.MkdirAll(filepath.Dir(conflictPath), 0755); err != nil {
		return fmt.Errorf("creating conflict output directory: %w", err)
	}
	paths := make([]string, len(items))
	for i, item := range items {
		paths[i] = item.Path
	}
	var conflicts []dryRunConflict
	for base, srcs := range flattenCollisionMap(paths) {
		conflicts = append(conflicts, dryRunConflict{
			DestPath: resolveOutPath(dest, srcs[0], true),
			Sources:  srcs,
		})
		_ = base
	}
	b, err := json.MarshalIndent(struct {
		Conflicts []dryRunConflict `json:"conflicts"`
	}{Conflicts: conflicts}, "", "  ")
	if err != nil {
		return fmt.Errorf("marshalling conflict output: %w", err)
	}
	return os.WriteFile(conflictPath, b, 0644)
}

// flattenCollisionMap returns a map of basename → all source paths that share it,
// for basenames claimed by more than one path. Empty map means no collisions.
func flattenCollisionMap(files []string) map[string][]string {
	all := make(map[string][]string, len(files))
	for _, f := range files {
		base := filepath.Base(filepath.FromSlash(f))
		all[base] = append(all[base], f)
	}
	out := make(map[string][]string)
	for base, srcs := range all {
		if len(srcs) > 1 {
			out[base] = srcs
		}
	}
	return out
}

// flattenCollisions returns one descriptive string per basename shared by more
// than one path. Empty slice means no collisions.
func flattenCollisions(files []string) []string {
	colMap := flattenCollisionMap(files)
	if len(colMap) == 0 {
		return nil
	}
	seen := map[string]string{}
	var out []string
	for _, f := range files {
		base := filepath.Base(filepath.FromSlash(f))
		if _, hasCollision := colMap[base]; !hasCollision {
			continue
		}
		if first, ok := seen[base]; ok {
			out = append(out, fmt.Sprintf("%q and %q both flatten to %q", first, f, base))
			seen[base] = f // advance so the next collision pairs with the most recent file
		} else {
			seen[base] = f
		}
	}
	return out
}
