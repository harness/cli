// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package har

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/harness/cli/v3/pkg/cmdctx"
)

// pushDebianArtifact implements "push artifact:debian".
//
// Supports two file types:
//   - .deb  → PUT /pkg/{account}/{registry}/debian/deb?distribution=…&component=…
//   - .dsc  → PUT /pkg/{account}/{registry}/debian/dsc + optional source files
//     via PUT /pkg/{account}/{registry}/debian/src
func pushDebianArtifact(ctx *cmdctx.Ctx) error {
	if len(ctx.Args) == 0 {
		return fmt.Errorf("push debian artifact requires a file path: push artifact <registry> <file>")
	}

	registry := ctx.Id
	filePath := ctx.Args[0]
	distribution := cmdctx.GetString(ctx.FlagValues, "distribution")
	component := cmdctx.GetString(ctx.FlagValues, "component")

	if distribution == "" {
		return fmt.Errorf("--distribution is required (e.g. focal, bullseye)")
	}
	if component == "" {
		return fmt.Errorf("--component is required (e.g. main, contrib, non-free)")
	}

	ext := strings.ToLower(filepath.Ext(filePath))
	switch ext {
	case ".deb":
		return uploadDebFile(ctx, registry, filePath, distribution, component)
	case ".dsc":
		sourceFile := cmdctx.GetString(ctx.FlagValues, "source-file")
		originSourceFile := cmdctx.GetString(ctx.FlagValues, "origin-source-file")
		return uploadDscPackage(ctx, registry, filePath, distribution, component, sourceFile, originSourceFile)
	default:
		return fmt.Errorf("unsupported file type %q: must be .deb or .dsc", ext)
	}
}

// uploadDebFile uploads a .deb package.
// URL: /pkg/{account}/{registry}/debian/deb?distribution=…&component=…
func uploadDebFile(ctx *cmdctx.Ctx, registry, filePath, distribution, component string) error {
	if err := validateRegularFile(filePath); err != nil {
		return err
	}

	checksums, err := computeFileChecksums(filePath)
	if err != nil {
		return fmt.Errorf("computing checksums: %w", err)
	}

	body, contentType, err := buildMultipartBody(filePath)
	if err != nil {
		return err
	}

	uploadURL, err := debianURL(ctx, registry, "deb", distribution, component, "", "")
	if err != nil {
		return err
	}

	fmt.Fprintf(os.Stderr, "Uploading %s ...\n", filepath.Base(filePath))
	req, err := http.NewRequest("PUT", uploadURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("building request: %w", err)
	}
	setAuthHeader(req, ctx.Auth)
	req.Header.Set("Content-Type", contentType)
	setChecksumHeaders(req.Header, checksums)

	if _, err := doRequest(newHTTPClient(), req); err != nil {
		return fmt.Errorf("upload failed: %w", err)
	}
	fmt.Fprintf(os.Stderr, "Successfully pushed %s to registry %q\n", filepath.Base(filePath), registry)
	return nil
}

// uploadDscPackage uploads a .dsc file plus optional source/origin-source files.
func uploadDscPackage(ctx *cmdctx.Ctx, registry, dscPath, distribution, component, sourceFile, originSourceFile string) error {
	if sourceFile == "" && originSourceFile == "" {
		return fmt.Errorf("at least one of --source-file or --origin-source-file is required for .dsc uploads")
	}

	if err := validateRegularFile(dscPath); err != nil {
		return err
	}

	meta, err := parseDscMetadata(dscPath)
	if err != nil {
		return fmt.Errorf("parsing .dsc file: %w", err)
	}
	fmt.Fprintf(os.Stderr, "Parsed .dsc: source=%s version=%s\n", meta.source, meta.version)

	// Upload .dsc file
	if err := uploadDebianFile(ctx, registry, dscPath, distribution, component, "", "", "dsc"); err != nil {
		return err
	}

	// Upload tar.xz / tar.gz source file
	if sourceFile != "" {
		if err := validateRegularFile(sourceFile); err != nil {
			return err
		}
		if err := uploadDebianFile(ctx, registry, sourceFile, distribution, component, meta.source, meta.version, "src"); err != nil {
			return err
		}
	}

	// Upload orig source tarball
	if originSourceFile != "" {
		if err := validateRegularFile(originSourceFile); err != nil {
			return err
		}
		upstreamVersion := extractUpstreamVersion(meta.version)
		if err := uploadDebianFile(ctx, registry, originSourceFile, distribution, component, meta.source, upstreamVersion, "src"); err != nil {
			return err
		}
	}

	fmt.Fprintf(os.Stderr, "Successfully pushed .dsc package %s to registry %q\n", meta.source, registry)
	return nil
}

// uploadDebianFile uploads a single file to the given debian endpoint type (dsc or src).
func uploadDebianFile(ctx *cmdctx.Ctx, registry, filePath, distribution, component, pkg, version, endpointType string) error {
	checksums, err := computeFileChecksums(filePath)
	if err != nil {
		return fmt.Errorf("computing checksums for %s: %w", filePath, err)
	}

	body, contentType, err := buildMultipartBody(filePath)
	if err != nil {
		return err
	}

	uploadURL, err := debianURL(ctx, registry, endpointType, distribution, component, pkg, version)
	if err != nil {
		return err
	}

	fmt.Fprintf(os.Stderr, "Uploading %s ...\n", filepath.Base(filePath))
	req, err := http.NewRequest("PUT", uploadURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("building request: %w", err)
	}
	setAuthHeader(req, ctx.Auth)
	req.Header.Set("Content-Type", contentType)
	setChecksumHeaders(req.Header, checksums)

	if _, err := doRequest(newHTTPClient(), req); err != nil {
		return fmt.Errorf("upload of %s failed: %w", filepath.Base(filePath), err)
	}
	return nil
}

// debianURL builds the upload URL for a debian endpoint.
// endpointType: "deb", "dsc", or "src"
// URL pattern: {pkgURL}/pkg/{account}/{registry}/debian/{type}?distribution=…&component=…[&package=…&version=…]
func debianURL(ctx *cmdctx.Ctx, registry, endpointType, distribution, component, pkg, version string) (string, error) {
	base, err := url.Parse(ctx.Auth.RegistryURL)
	if err != nil {
		return "", fmt.Errorf("invalid registry URL: %w", err)
	}
	base.Path = fmt.Sprintf("/pkg/%s/%s/debian/%s",
		url.PathEscape(ctx.Auth.AccountID),
		url.PathEscape(registry),
		endpointType,
	)
	q := url.Values{}
	q.Set("distribution", distribution)
	q.Set("component", component)
	if pkg != "" {
		q.Set("package", pkg)
	}
	if version != "" {
		q.Set("version", version)
	}
	base.RawQuery = q.Encode()
	return base.String(), nil
}

// buildMultipartBody builds a multipart/form-data body with a single "file" field.
// Returns the body bytes and the content-type header value (with boundary).
func buildMultipartBody(filePath string) ([]byte, string, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return nil, "", fmt.Errorf("opening %q: %w", filePath, err)
	}
	defer f.Close()

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	part, err := mw.CreateFormFile("file", filepath.Base(filePath))
	if err != nil {
		return nil, "", fmt.Errorf("creating multipart field: %w", err)
	}
	if _, err := io.Copy(part, f); err != nil {
		return nil, "", fmt.Errorf("reading file into multipart: %w", err)
	}
	mw.Close()
	return buf.Bytes(), mw.FormDataContentType(), nil
}

type dscMetadata struct {
	source  string
	version string
}

// parseDscMetadata reads Source and Version fields from a .dsc file.
func parseDscMetadata(filePath string) (*dscMetadata, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return nil, fmt.Errorf("opening .dsc file: %w", err)
	}
	defer f.Close()

	meta := &dscMetadata{}
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "Source:") {
			meta.source = strings.TrimSpace(strings.TrimPrefix(line, "Source:"))
		} else if strings.HasPrefix(line, "Version:") {
			meta.version = strings.TrimSpace(strings.TrimPrefix(line, "Version:"))
		}
		if meta.source != "" && meta.version != "" {
			break
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("reading .dsc file: %w", err)
	}
	if meta.source == "" {
		return nil, fmt.Errorf("Source field not found in .dsc file")
	}
	if meta.version == "" {
		return nil, fmt.Errorf("Version field not found in .dsc file")
	}
	return meta, nil
}

// extractUpstreamVersion strips the debian revision suffix from a version string.
// e.g. "1.2.3-4" → "1.2.3", "1.2.3" → "1.2.3"
func extractUpstreamVersion(version string) string {
	if idx := strings.LastIndex(version, "-"); idx >= 0 {
		return version[:idx]
	}
	return version
}

// validateRegularFile returns an error if the path doesn't exist or is a directory.
func validateRegularFile(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("cannot access %q: %w", path, err)
	}
	if info.IsDir() {
		return fmt.Errorf("%q is a directory, expected a file", path)
	}
	return nil
}
