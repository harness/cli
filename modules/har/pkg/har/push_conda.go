// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package har

import (
	"archive/tar"
	"archive/zip"
	"compress/bzip2"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/harness/cli/v3/pkg/cmdctx"
	"github.com/klauspost/compress/zstd"
)

// condaIndexJSON holds the fields we need from info/index.json inside a conda package.
type condaIndexJSON struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Subdir  string `json:"subdir"`
}

// pushCondaArtifact implements "push artifact <registry/name> <local-file>" for conda packages.
//
// Supported formats:
//   - .tar.bz2
//   - .conda (zip containing an info-*.tar.zst metadata archive)
//
// The upload is a raw PUT to:
//
//	{registryURL}/pkg/{accountID}/{registry}/conda/upload
//
// with headers:
//
//	X-File-Name: <filename>
//	X-Subdir:    <subdir from index.json>
func pushCondaArtifact(ctx *cmdctx.Ctx) error {
	if err := rejectExtraArgs(ctx, 1); err != nil {
		return err
	}
	if len(ctx.Args) == 0 {
		return fmt.Errorf("push conda requires a local file path: push artifact <registry/name> <local-file>")
	}
	localFile := ctx.Args[0]
	fileName := filepath.Base(localFile)

	lower := strings.ToLower(fileName)
	var readMeta func(string) (*condaIndexJSON, error)
	switch {
	case strings.HasSuffix(lower, ".conda"):
		readMeta = condaMetaFromConda
	case strings.HasSuffix(lower, ".tar.bz2"):
		readMeta = condaMetaFromBZ2
	default:
		return fmt.Errorf("push conda: unsupported file extension for %q; expected .conda or .tar.bz2", fileName)
	}

	fi, err := os.Stat(localFile)
	if err != nil {
		return fmt.Errorf("cannot access %q: %w", localFile, err)
	}
	if fi.IsDir() {
		return fmt.Errorf("%q is a directory; conda push requires a file", localFile)
	}

	meta, err := readMeta(localFile)
	if err != nil {
		return fmt.Errorf("reading conda metadata from %q: %w", localFile, err)
	}
	if meta.Subdir == "" {
		return fmt.Errorf("conda metadata in %q is missing 'subdir' field", localFile)
	}
	if meta.Name == "" {
		return fmt.Errorf("conda metadata in %q is missing 'name' field", localFile)
	}
	if meta.Version == "" {
		return fmt.Errorf("conda metadata in %q is missing 'version' field", localFile)
	}

	registry, _, err := parseRegistryAndName(ctx.Id)
	if err != nil {
		return err
	}

	// URL: {registryURL}/pkg/{accountID}/{registry}/conda/upload
	subpath := fmt.Sprintf("%s/conda/upload", registry)
	uploadURL, err := buildPkgURL(ctx.Auth.RegistryURL, ctx.Auth.AccountID, subpath)
	if err != nil {
		return err
	}

	f, err := os.Open(localFile)
	if err != nil {
		return fmt.Errorf("opening %q: %w", localFile, err)
	}
	defer f.Close()

	fmt.Fprintf(os.Stderr, "Uploading conda package %s (subdir=%s, name=%s, version=%s) ...\n",
		fileName, meta.Subdir, meta.Name, meta.Version)

	req, err := http.NewRequest("PUT", uploadURL, f)
	if err != nil {
		return fmt.Errorf("building request: %w", err)
	}
	setAuthHeader(req, ctx.Auth)
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("X-File-Name", fileName)
	req.Header.Set("X-Subdir", meta.Subdir)
	req.ContentLength = fi.Size()

	if _, err := doRequest(newHTTPClient(), req); err != nil {
		return fmt.Errorf("conda upload failed: %w", err)
	}

	fmt.Fprintf(os.Stderr, "Successfully pushed %s to %s\n", fileName, ctx.Id)
	return nil
}

// condaMetaFromBZ2 reads index.json from inside a .tar.bz2 conda package and returns
// the name, version, and subdir fields.
func condaMetaFromBZ2(filePath string) (*condaIndexJSON, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return nil, fmt.Errorf("opening file: %w", err)
	}
	defer f.Close()

	return condaMetaFromTar(bzip2.NewReader(f))
}

// condaMetaFromConda reads index.json from the info-*.tar.zst entry of a .conda package
// (a zip archive) and returns the name, version, and subdir fields.
func condaMetaFromConda(filePath string) (*condaIndexJSON, error) {
	zr, err := zip.OpenReader(filePath)
	if err != nil {
		return nil, fmt.Errorf("opening .conda archive: %w", err)
	}
	defer zr.Close()

	for _, entry := range zr.File {
		if !strings.HasPrefix(entry.Name, "info-") || !strings.HasSuffix(entry.Name, ".tar.zst") {
			continue
		}
		rc, err := entry.Open()
		if err != nil {
			return nil, fmt.Errorf("opening %s: %w", entry.Name, err)
		}
		defer rc.Close()

		zstdReader, err := zstd.NewReader(rc)
		if err != nil {
			return nil, fmt.Errorf("creating zstd reader: %w", err)
		}
		defer zstdReader.Close()

		return condaMetaFromTar(zstdReader)
	}
	return nil, fmt.Errorf("info-*.tar.zst not found in .conda archive")
}

func condaMetaFromTar(r io.Reader) (*condaIndexJSON, error) {
	tarReader := tar.NewReader(io.LimitReader(r, 1<<30)) // 1 GiB safety limit

	for {
		hdr, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("reading tar: %w", err)
		}
		if strings.Contains(strings.ToLower(hdr.Name), "index.json") {
			var idx condaIndexJSON
			if err := json.NewDecoder(tarReader).Decode(&idx); err != nil {
				return nil, fmt.Errorf("decoding index.json: %w", err)
			}
			return &idx, nil
		}
	}
	return nil, fmt.Errorf("index.json not found in archive")
}
