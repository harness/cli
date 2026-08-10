// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package har

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/harness/cli/pkg/cmdctx"
)

// rubyUploadResponse mirrors the JSON body the Ruby gem push handler returns
// on success, used only to render a nicer success message.
type rubyUploadResponse struct {
	Name     string `json:"name"`
	Version  string `json:"version"`
	Platform string `json:"platform"`
}

// pushRubyArtifact uploads a Ruby gem (.gem) to the Harness Artifact Registry.
//
// ctx.Id      = "<registry>/<ignored-name>" — only the registry part is used
// ctx.Args[0] = local .gem file path
//
// Upload endpoint: POST {registryURL}/pkg/{accountID}/{registry}/ruby/api/v1/gems
func pushRubyArtifact(ctx *cmdctx.Ctx) error {
	if len(ctx.Args) == 0 {
		return fmt.Errorf("push ruby: local file path required as positional argument")
	}
	localFile := ctx.Args[0]

	if !strings.HasSuffix(strings.ToLower(localFile), ".gem") {
		return fmt.Errorf("push ruby: file must have .gem extension, got %q", filepath.Base(localFile))
	}

	fi, err := os.Stat(localFile)
	if err != nil {
		return fmt.Errorf("push ruby: cannot access %q: %w", localFile, err)
	}
	if fi.IsDir() {
		return fmt.Errorf("push ruby: %q is a directory, not a .gem file", localFile)
	}

	registry, _, err := parseRegistryAndName(ctx.Id)
	if err != nil {
		return fmt.Errorf("push ruby: %w", err)
	}

	subpath := fmt.Sprintf("%s/ruby/api/v1/gems", registry)
	uploadURL, err := buildPkgURL(ctx.Auth.RegistryURL, ctx.Auth.AccountID, subpath)
	if err != nil {
		return fmt.Errorf("push ruby: building URL: %w", err)
	}

	f, err := os.Open(localFile)
	if err != nil {
		return fmt.Errorf("push ruby: opening %q: %w", localFile, err)
	}
	defer f.Close()

	fmt.Fprintf(os.Stderr, "Uploading %s → %s/ruby/api/v1/gems ...\n", filepath.Base(localFile), registry)

	req, err := http.NewRequest("POST", uploadURL, f)
	if err != nil {
		return fmt.Errorf("push ruby: building request: %w", err)
	}
	setAuthHeader(req, ctx.Auth)
	req.Header.Set("Content-Type", "application/octet-stream")
	req.ContentLength = fi.Size()

	// Do not send X-Checksum-* headers: the Ruby push handler writes
	// server-generated sidecar files (version_info.json/yaml) in the same
	// request, and the backend would incorrectly validate those against the
	// gem's digests. Native gem push does not send checksum headers either.
	body, err := doRequest(newHTTPClient(), req)
	if err != nil {
		return fmt.Errorf("push ruby: upload failed: %w", err)
	}

	successMsg := fmt.Sprintf("Successfully pushed %s to %s", filepath.Base(localFile), registry)
	if len(body) > 0 {
		var uploadResp rubyUploadResponse
		if err := json.Unmarshal(body, &uploadResp); err == nil && uploadResp.Name != "" && uploadResp.Version != "" {
			if uploadResp.Platform != "" {
				successMsg = fmt.Sprintf("Successfully pushed %s@%s (%s) to %s", uploadResp.Name, uploadResp.Version, uploadResp.Platform, registry)
			} else {
				successMsg = fmt.Sprintf("Successfully pushed %s@%s to %s", uploadResp.Name, uploadResp.Version, registry)
			}
		}
	}

	fmt.Fprintln(os.Stderr, successMsg)
	return nil
}
