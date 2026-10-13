// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package ci

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/harness/cli/v3/pkg/auth"
)

// artifactHTTPClient is used for every HAR registry request made by
// `push ci_artifact`/`pull ci_artifact`. Timeout is an
// always-on safety net independent of the core --timeout flag (which
// defaults to unbounded) so a slow or unresponsive registry never hangs the
// process indefinitely.
var artifactHTTPClient = &http.Client{
	Timeout: 10 * time.Minute,
	Transport: &http.Transport{
		DialContext: (&net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		ResponseHeaderTimeout: 60 * time.Second,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
	},
}

func normalizeRegistryURL(raw string) string {
	raw = strings.TrimSpace(raw)
	raw = strings.TrimSuffix(raw, "/")
	if raw == "" {
		return ""
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	return raw
}

// buildArtifactFileURL builds the HAR generic-file URL:
// {registryURL}/pkg/{accountID}/{registry}/files/{artifactPath}.
func buildArtifactFileURL(registryURL, accountID, registry, artifactPath string) (string, error) {
	registryURL = normalizeRegistryURL(registryURL)
	u, err := url.JoinPath(registryURL, "pkg", accountID, registry, "files")
	if err != nil {
		return "", fmt.Errorf("invalid registry URL: %w", err)
	}
	for _, segment := range strings.Split(artifactPath, "/") {
		if segment == "" {
			continue
		}
		u, err = url.JoinPath(u, segment)
		if err != nil {
			return "", fmt.Errorf("invalid artifact path segment %q: %w", segment, err)
		}
	}
	return u, nil
}

// pushGenericFile PUTs body to the registry at artifactPath under registry.
// Content-Type is deliberately left unset so HAR infers it from the
// filename extension instead of storing everything as a generic binary,
// which would break inline preview for text/html files.
func pushGenericFile(ctx context.Context, a *auth.ResolvedAuth, registry, artifactPath string, body io.Reader) (*http.Response, error) {
	pushURL, err := buildArtifactFileURL(a.RegistryURL, a.AccountID, registry, artifactPath)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPut, pushURL, body)
	if err != nil {
		return nil, fmt.Errorf("failed to create push request: %w", err)
	}
	a.SetAuthHeader(req)

	resp, err := artifactHTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("push failed: %w", err)
	}
	return resp, nil
}
