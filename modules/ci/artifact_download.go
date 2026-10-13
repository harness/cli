// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package ci

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/harness/cli/v3/pkg/auth"
	"github.com/harness/cli/v3/pkg/cmdctx"
)

const pullCiArtifactHandlerID = "pull_ci_artifact"

// pullGenericFile GETs the file at artifactPath under registry.
func pullGenericFile(ctx context.Context, a *auth.ResolvedAuth, registry, artifactPath string) (*http.Response, error) {
	pullURL, err := buildArtifactFileURL(a.RegistryURL, a.AccountID, registry, artifactPath)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, pullURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create pull request: %w", err)
	}
	a.SetAuthHeader(req)

	resp, err := artifactHTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("pull failed: %w", err)
	}
	return resp, nil
}

// resolveLocalPath determines where to save a downloaded file.
//   - dest empty      → save filename in current directory
//   - dest is a dir    → save filename inside dest
//   - dest is a path  → use as the exact output path
func resolveLocalPath(dest, filename string) string {
	if dest == "" {
		return filename
	}
	info, err := os.Stat(dest)
	if err == nil && info.IsDir() {
		return filepath.Join(dest, filename)
	}
	if strings.HasSuffix(dest, string(os.PathSeparator)) || strings.HasSuffix(dest, "/") {
		return filepath.Join(dest, filename)
	}
	return dest
}

// pullCiArtifactHandler is the `pull ci_artifact` workflow handler. It
// downloads a single file from the current artifact version in Harness
// Artifact Registry, under ctx.Auth's resolved registry — populated from
// pipeline_auth inside a pipeline step, or from the active profile outside
// one.
func pullCiArtifactHandler(ctx *cmdctx.Ctx) error {
	if ctx.Auth.RegistryURL == "" {
		return fmt.Errorf("registry URL is not configured — run 'harness auth login', or set %s in a pipeline step", envHARURL)
	}
	if len(ctx.Args) != 1 {
		return fmt.Errorf("pull ci_artifact requires exactly one <path-in-version> argument")
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

	filePath := strings.TrimLeft(ctx.Args[0], "/")
	localPath := resolveLocalPath(destination, filepath.Base(filePath))
	if err := os.MkdirAll(filepath.Dir(localPath), 0o755); err != nil {
		return fmt.Errorf("create directory: %w", err)
	}

	fmt.Printf("Downloading %q → %s\n", filePath, localPath)

	resp, err := pullGenericFile(ctx.Context, ctx.Auth, registry, name+"/"+version+"/"+filePath)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("server returned %s for %q", resp.Status, filePath)
	}

	f, err := os.Create(localPath)
	if err != nil {
		return fmt.Errorf("create local file %s: %w", localPath, err)
	}
	defer f.Close()

	if _, err := io.Copy(f, resp.Body); err != nil {
		return fmt.Errorf("write %s: %w", localPath, err)
	}

	fmt.Printf("Successfully downloaded %q\n", filePath)
	return nil
}
