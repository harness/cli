// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package har

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/harness/cli/v3/pkg/cmdctx"
)

// pushCargoArtifact uploads a .crate file to the Harness Artifact Registry using
// the Cargo sparse registry upload protocol.
//
// ctx.Id      = "<registry>/<ignored-name>" — only the registry part is used
// ctx.Args[0] = local .crate file path
//
// The upload endpoint is:
//
//	PUT {registryURL}/pkg/{accountID}/{registry}/cargo/api/v1/crates/new
//
// Body format (Cargo publish wire format):
//
//	[4 bytes LE uint32: metadata JSON length]
//	[N bytes: metadata JSON {"name":"<name>","vers":"<version>"}]
//	[4 bytes LE uint32: .crate file length]
//	[N bytes: .crate file bytes]
func pushCargoArtifact(ctx *cmdctx.Ctx) error {
	if len(ctx.Args) == 0 {
		return fmt.Errorf("push cargo: local file path required as positional argument")
	}
	localFile := ctx.Args[0]

	if !strings.HasSuffix(strings.ToLower(localFile), ".crate") {
		return fmt.Errorf("push cargo: file must have .crate extension, got %q", filepath.Base(localFile))
	}

	fi, err := os.Stat(localFile)
	if err != nil {
		return fmt.Errorf("push cargo: cannot access %q: %w", localFile, err)
	}
	if fi.IsDir() {
		return fmt.Errorf("push cargo: %q is a directory, not a .crate file", localFile)
	}

	// Extract Cargo.toml from the .crate archive to get name and version.
	tomlBytes, err := readFileFromTarGz(localFile, "Cargo.toml")
	if err != nil {
		return fmt.Errorf("push cargo: extracting Cargo.toml from %q: %w", filepath.Base(localFile), err)
	}

	name, version, err := parseCargoToml(tomlBytes)
	if err != nil {
		return fmt.Errorf("push cargo: %w", err)
	}

	// Read the full .crate bytes.
	crateBytes, err := os.ReadFile(localFile)
	if err != nil {
		return fmt.Errorf("push cargo: reading %q: %w", localFile, err)
	}

	// Build the Cargo publish wire-format payload.
	payload, err := buildCargoPayload(name, version, crateBytes)
	if err != nil {
		return fmt.Errorf("push cargo: building payload: %w", err)
	}

	// The registry part of the id is all we need; the crate name/version come from the file.
	registry, _, err := parseRegistryAndName(ctx.Id)
	if err != nil {
		return fmt.Errorf("push cargo: %w", err)
	}

	subpath := fmt.Sprintf("%s/cargo/api/v1/crates/new", registry)
	uploadURL, err := buildPkgURL(ctx.Auth.RegistryURL, ctx.Auth.AccountID, subpath)
	if err != nil {
		return fmt.Errorf("push cargo: building URL: %w", err)
	}

	fmt.Fprintf(os.Stderr, "Uploading %s (%s@%s) → %s ...\n",
		filepath.Base(localFile), name, version, registry)

	req, err := http.NewRequest("PUT", uploadURL, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("push cargo: building request: %w", err)
	}
	setAuthHeader(req, ctx.Auth)
	req.Header.Set("Content-Type", "application/octet-stream")
	req.ContentLength = int64(len(payload))

	if sums, sumErr := computeFileChecksums(localFile); sumErr == nil {
		setChecksumHeaders(req.Header, sums)
	}

	if _, err := doRequest(newHTTPClient(), req); err != nil {
		return fmt.Errorf("push cargo: upload failed: %w", err)
	}

	fmt.Fprintf(os.Stderr, "Successfully pushed %s@%s to %s\n", name, version, registry)
	applyPostPushMetadata(ctx, cmdctx.GetString(ctx.FlagValues, "metadata"), registry, name, version)
	return nil
}

// parseCargoToml extracts the package name and version from the contents of a
// Cargo.toml file.
func parseCargoToml(data []byte) (name, version string, err error) {
	var manifest struct {
		Package struct {
			Name    string `toml:"name"`
			Version string `toml:"version"`
		} `toml:"package"`
	}
	if err := toml.Unmarshal(data, &manifest); err != nil {
		return "", "", fmt.Errorf("failed parsing Cargo.toml: %w", err)
	}
	if manifest.Package.Name == "" {
		return "", "", fmt.Errorf("Cargo.toml: package name not found")
	}
	if manifest.Package.Version == "" {
		return "", "", fmt.Errorf("Cargo.toml: package version not found")
	}
	return manifest.Package.Name, manifest.Package.Version, nil
}

// buildCargoPayload constructs the binary upload body used by the Cargo
// sparse-registry publish protocol:
//
//	[4 LE bytes: len(metadataJSON)]
//	[metadataJSON bytes]
//	[4 LE bytes: len(crateBytes)]
//	[crateBytes]
func buildCargoPayload(name, version string, crateBytes []byte) ([]byte, error) {
	meta := map[string]string{
		"name": name,
		"vers": version,
	}
	metaJSON, err := json.Marshal(meta)
	if err != nil {
		return nil, fmt.Errorf("marshaling metadata: %w", err)
	}

	metaLen := uint32(len(metaJSON))    // #nosec G115
	crateLen := uint32(len(crateBytes)) // #nosec G115

	buf := make([]byte, 4+metaLen+4+crateLen)
	binary.LittleEndian.PutUint32(buf[0:4], metaLen)
	copy(buf[4:], metaJSON)
	binary.LittleEndian.PutUint32(buf[4+metaLen:4+metaLen+4], crateLen)
	copy(buf[4+metaLen+4:], crateBytes)

	return buf, nil
}
