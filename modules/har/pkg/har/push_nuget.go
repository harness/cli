// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package har

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"fmt"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/harness/cli/v3/pkg/cmdctx"
)

// pushNugetArtifact uploads a .nupkg file to a Harness NuGet registry.
//
// ctx.Id      = "<registry>" (a legacy "<registry>/<name>" is accepted; name is ignored)
// ctx.Args[0] = local .nupkg file path
//
// The upload endpoint is: PUT {registryURL}/pkg/{accountID}/{registry}/nuget/
// Body: multipart/form-data with a single field named "package" containing the file.
func pushNugetArtifact(ctx *cmdctx.Ctx) error {
	if err := rejectExtraArgs(ctx, 1); err != nil {
		return err
	}
	if len(ctx.Args) == 0 {
		return fmt.Errorf("push nuget requires a local file path: push artifact <registry/name> <local-file>")
	}
	localFile := ctx.Args[0]

	if !strings.HasSuffix(strings.ToLower(localFile), ".nupkg") {
		return fmt.Errorf("nuget push requires a .nupkg file, got %q", localFile)
	}

	fi, err := os.Stat(localFile)
	if err != nil {
		return fmt.Errorf("cannot access %q: %w", localFile, err)
	}
	if fi.IsDir() {
		return fmt.Errorf("%q is a directory; nuget push requires a .nupkg file", localFile)
	}

	registry, err := parseRegistry(ctx.Id)
	if err != nil {
		return err
	}

	// URL: PUT {registryURL}/pkg/{accountID}/{registry}/nuget/
	subpath := fmt.Sprintf("%s/nuget/", registry)
	uploadURL, err := buildPkgURL(ctx.Auth.RegistryURL, ctx.Auth.AccountID, subpath)
	if err != nil {
		return err
	}

	// Read file content
	fileData, err := os.ReadFile(localFile)
	if err != nil {
		return fmt.Errorf("reading %q: %w", localFile, err)
	}

	// Build multipart body
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	part, err := mw.CreateFormFile("package", filepath.Base(localFile))
	if err != nil {
		return fmt.Errorf("creating multipart field: %w", err)
	}
	if _, err := part.Write(fileData); err != nil {
		return fmt.Errorf("writing multipart data: %w", err)
	}
	if err := mw.Close(); err != nil {
		return fmt.Errorf("closing multipart writer: %w", err)
	}

	fmt.Fprintf(os.Stderr, "Uploading %s → %s/nuget/ ...\n", filepath.Base(localFile), registry)

	req, err := http.NewRequest("PUT", uploadURL, &buf)
	if err != nil {
		return fmt.Errorf("building request: %w", err)
	}
	setAuthHeader(req, ctx.Auth)
	req.Header.Set("Content-Type", mw.FormDataContentType())

	if sums, sumErr := computeFileChecksums(localFile); sumErr == nil {
		setChecksumHeaders(req.Header, sums)
	}

	if _, err := doRequest(newHTTPClient(), req); err != nil {
		return fmt.Errorf("upload failed: %w", err)
	}

	fmt.Fprintf(os.Stderr, "Successfully pushed %s to %s\n", filepath.Base(localFile), registry)

	if metadataStr := cmdctx.GetString(ctx.FlagValues, "metadata"); metadataStr != "" {
		id, version, err := readNugetIdentity(localFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: skipping --metadata publish for %s: %v\n", filepath.Base(localFile), err)
			return nil
		}
		applyPostPushMetadata(ctx, metadataStr, registry, id, version)
	}
	return nil
}

// Must match the registry's NuGet version regex (registry/pkg/nuget/helper.go)
// so metadata targets the version the upload created.
var nugetVersionRegexp = regexp.MustCompile(`^v?([0-9]+(\.[0-9]+)*?)` +
	`(-([0-9]+[0-9A-Za-z\-]*(\.[0-9A-Za-z\-]+)*)|(-([A-Za-z\-]+[0-9A-Za-z\-]*(\.[0-9A-Za-z\-]+)*)))?` +
	`(\+([0-9A-Za-z\-]+(\.[0-9A-Za-z\-]+)*))?$`)

type nuspecIdentity struct {
	Metadata struct {
		ID      string `xml:"id"`
		Version string `xml:"version"`
	} `xml:"metadata"`
}

// readNugetIdentity returns the package id and version in the lowercased,
// normalized form the registry stores them under. It selects the .nuspec the
// way the registry's buildMetadata does (registry/pkg/nuget/local.go): every
// entry ending in ".nuspec" (case-sensitive, any directory) is parsed and the
// last one wins.
func readNugetIdentity(nupkgPath string) (string, string, error) {
	zr, err := zip.OpenReader(nupkgPath)
	if err != nil {
		return "", "", fmt.Errorf("failed to open package as a zip archive: %w", err)
	}
	defer zr.Close()

	var spec *nuspecIdentity
	for _, f := range zr.File {
		if !strings.HasSuffix(f.Name, ".nuspec") {
			continue
		}
		parsed, err := parseNuspecIdentity(f)
		if err != nil {
			return "", "", err
		}
		spec = parsed
	}
	if spec == nil {
		return "", "", fmt.Errorf("no .nuspec found in the package")
	}

	id := strings.TrimSpace(spec.Metadata.ID)
	rawVersion := strings.TrimSpace(spec.Metadata.Version)
	if id == "" || rawVersion == "" {
		return "", "", fmt.Errorf(".nuspec is missing <id> or <version>")
	}
	version, err := normalizeNugetVersion(rawVersion)
	if err != nil {
		return "", "", err
	}
	return strings.ToLower(id), version, nil
}

func parseNuspecIdentity(f *zip.File) (*nuspecIdentity, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, fmt.Errorf("failed to open %s: %w", f.Name, err)
	}
	defer rc.Close()
	var spec nuspecIdentity
	if err := xml.NewDecoder(rc).Decode(&spec); err != nil {
		return nil, fmt.Errorf("failed to parse %s: %w", f.Name, err)
	}
	return &spec, nil
}

// normalizeNugetVersion mirrors the registry's validateAndNormaliseVersion:
// pad to three numeric segments, keep a non-zero fourth, keep the prerelease
// label, drop build metadata, and lowercase.
func normalizeNugetVersion(v string) (string, error) {
	matches := nugetVersionRegexp.FindStringSubmatch(v)
	if matches == nil {
		return "", fmt.Errorf("malformed nuget version: %s", v)
	}
	parts := strings.Split(matches[1], ".")
	if len(parts) > 4 {
		return "", fmt.Errorf("malformed nuget version: %s", v)
	}
	segments := make([]int64, 0, 4)
	for _, s := range parts {
		n, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			return "", fmt.Errorf("malformed nuget version %s: %w", v, err)
		}
		segments = append(segments, n)
	}
	for len(segments) < 3 {
		segments = append(segments, 0)
	}

	normalized := fmt.Sprintf("%d.%d.%d", segments[0], segments[1], segments[2])
	if len(segments) > 3 && segments[3] > 0 {
		normalized = fmt.Sprintf("%s.%d", normalized, segments[3])
	}
	normalized += matches[3]
	return strings.ToLower(normalized), nil
}
