// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package har

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/harness/cli/pkg/cmdctx"
)

// pushGenericArtifact implements "push artifact:generic".
//
// Usage:
//
//	harness push artifact:generic <registry/name> <path> [<path>...] --name <pkg> [--version v]
//
// Each <path> may be a file or a directory. Directories are walked recursively.
// Files are uploaded to: {registryURL}/pkg/{accountID}/{registry}/generic/{name}/{version}/{relPath}
func pushGenericArtifact(ctx *cmdctx.Ctx) error {
	if len(ctx.Args) == 0 {
		return fmt.Errorf("push generic artifact requires at least one file or directory path")
	}

	registry, name, err := parseRegistryAndName(ctx.Id)
	if err != nil {
		return err
	}

	version := cmdctx.GetString(ctx.FlagValues, "version")
	if version == "" {
		version = "1.0.0"
	}

	includeHidden := cmdctx.GetBool(ctx.FlagValues, "include-hidden")

	fmt.Fprintf(os.Stderr, "Scanning %d input(s) ...\n", len(ctx.Args))

	jobs, totalFiles, totalBytes, err := collectGenericJobs(ctx.Args, includeHidden)
	if err != nil {
		return err
	}
	if len(jobs) == 0 {
		return fmt.Errorf("no files to upload (use --include-hidden to include dotfiles inside directory inputs)")
	}

	fmt.Fprintf(os.Stderr, "Found %d file(s) (%s) to upload to %s/%s in registry %q\n",
		totalFiles, formatBytes(totalBytes), name, version, registry)

	client := newHTTPClient()

	sem := make(chan struct{}, defaultMaxConcurrentUploads)
	var wg sync.WaitGroup
	errs := make([]error, len(jobs))

	for i, job := range jobs {
		wg.Add(1)
		go func(i int, job genericUploadJob) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			fmt.Fprintf(os.Stderr, "Uploading %s ...\n", job.relPath)
			if uploadErr := genericPutFile(ctx, client, registry, name, version, job.relPath, job.localPath); uploadErr != nil {
				errs[i] = fmt.Errorf("failed to upload %s: %w", job.relPath, uploadErr)
			}
		}(i, job)
	}
	wg.Wait()

	var firstErr error
	failed := 0
	for _, e := range errs {
		if e != nil {
			fmt.Fprintln(os.Stderr, e)
			failed++
			if firstErr == nil {
				firstErr = e
			}
		}
	}
	if failed > 0 {
		return fmt.Errorf("%d of %d file(s) failed to upload", failed, len(jobs))
	}

	fmt.Fprintf(os.Stderr, "Successfully pushed %d file(s) to %s/%s in registry %q\n", totalFiles, name, version, registry)
	return nil
}

type genericUploadJob struct {
	relPath   string
	localPath string
	size      int64
}

// collectGenericJobs walks all input paths and returns a flat job list.
func collectGenericJobs(paths []string, includeHidden bool) ([]genericUploadJob, int, int64, error) {
	var jobs []genericUploadJob
	var totalBytes int64

	for _, p := range paths {
		abs, err := filepath.Abs(p)
		if err != nil {
			return nil, 0, 0, fmt.Errorf("cannot resolve %q: %w", p, err)
		}
		info, err := os.Stat(abs)
		if err != nil {
			return nil, 0, 0, fmt.Errorf("cannot access %q: %w", p, err)
		}

		if info.IsDir() {
			dirJobs, dirBytes, err := walkDir(abs, includeHidden)
			if err != nil {
				return nil, 0, 0, err
			}
			jobs = append(jobs, dirJobs...)
			totalBytes += dirBytes
		} else if info.Mode().IsRegular() {
			jobs = append(jobs, genericUploadJob{
				relPath:   filepath.Base(abs),
				localPath: abs,
				size:      info.Size(),
			})
			totalBytes += info.Size()
		} else {
			return nil, 0, 0, fmt.Errorf("%s is not a regular file or directory", abs)
		}
	}

	return jobs, len(jobs), totalBytes, nil
}

// walkDir recursively walks srcDir and returns a job per regular file.
// The directory's basename is used as a path prefix so the internal layout is preserved.
func walkDir(srcDir string, includeHidden bool) ([]genericUploadJob, int64, error) {
	var jobs []genericUploadJob
	var totalBytes int64

	sourceBase := filepath.ToSlash(filepath.Base(srcDir))
	if sourceBase == "." || sourceBase == "/" {
		sourceBase = ""
	}

	err := filepath.WalkDir(srcDir, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == srcDir {
			return nil
		}

		base := filepath.Base(path)
		if !includeHidden && strings.HasPrefix(base, ".") {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		if d.IsDir() {
			return nil
		}

		info, err := d.Info()
		if err != nil {
			return fmt.Errorf("stat %s: %w", path, err)
		}
		if !info.Mode().IsRegular() {
			return nil
		}

		relPath, err := filepath.Rel(srcDir, path)
		if err != nil {
			return fmt.Errorf("compute relative path for %s: %w", path, err)
		}
		relPath = filepath.ToSlash(relPath)
		if strings.HasPrefix(relPath, "../") || relPath == ".." {
			return fmt.Errorf("refusing to upload %s: path escapes source directory", path)
		}

		jobRelPath := relPath
		if sourceBase != "" {
			jobRelPath = sourceBase + "/" + relPath
		}

		jobs = append(jobs, genericUploadJob{
			relPath:   jobRelPath,
			localPath: path,
			size:      info.Size(),
		})
		totalBytes += info.Size()
		return nil
	})
	if err != nil {
		return nil, 0, fmt.Errorf("failed to walk %s: %w", srcDir, err)
	}
	return jobs, totalBytes, nil
}

// genericPutFile uploads a single file via PUT.
//
// URL: {registryURL}/pkg/{accountID}/{registry}/generic/{name}/{version}/{relPath}
func genericPutFile(ctx *cmdctx.Ctx, client *http.Client, registry, name, version, relPath, localPath string) error {
	f, err := os.Open(localPath)
	if err != nil {
		return fmt.Errorf("opening %q: %w", localPath, err)
	}
	defer f.Close()

	fi, err := f.Stat()
	if err != nil {
		return fmt.Errorf("stat %q: %w", localPath, err)
	}

	subpath := fmt.Sprintf("%s/files/%s/%s/%s", registry, name, version, relPath)
	uploadURL, err := buildPkgURL(ctx.Auth.RegistryURL, ctx.Auth.AccountID, subpath)
	if err != nil {
		return err
	}

	req, err := http.NewRequest("PUT", uploadURL, f)
	if err != nil {
		return fmt.Errorf("building request: %w", err)
	}
	setAuthHeader(req, ctx.Auth)
	req.Header.Set("Content-Type", "application/octet-stream")
	req.ContentLength = fi.Size()

	if _, err := doRequest(client, req); err != nil {
		return err
	}
	return nil
}

// formatBytes returns a human-readable byte size string.
func formatBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(b)/float64(div), "KMGTPE"[exp])
}
