// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package vibe

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/harness/cli/pkg/auth"
)

type vibeAPI struct {
	baseURL string
	auth    *auth.ResolvedAuth
	client  *http.Client
}

func newVibeAPI(a *auth.ResolvedAuth) *vibeAPI {
	base := strings.TrimRight(os.Getenv("VIBE_API_BASE_URL"), "/")
	if base == "" {
		base = "http://localhost:8090"
	}
	return &vibeAPI{
		baseURL: base,
		auth:    a,
		client:  &http.Client{Timeout: 60 * time.Second},
	}
}

func (c *vibeAPI) applyAuth(req *http.Request) {
	if c.auth == nil {
		return
	}
	c.auth.SetAuthHeader(req)
	if c.auth.AccountID != "" {
		req.Header.Set("x-account-id", c.auth.AccountID)
		req.Header.Set("x-tenant-id", c.auth.AccountID)
	}
	if c.auth.OrgID != "" {
		req.Header.Set("x-org-id", c.auth.OrgID)
	}
	if c.auth.ProjectID != "" {
		req.Header.Set("x-project-id", c.auth.ProjectID)
	}
}

func (c *vibeAPI) do(method, path string) (*http.Response, error) {
	req, err := http.NewRequest(method, c.baseURL+path, nil)
	if err != nil {
		return nil, err
	}
	c.applyAuth(req)
	return c.client.Do(req)
}

func (c *vibeAPI) getJSON(path string, out any) error {
	resp, err := c.do(http.MethodGet, path)
	if err != nil {
		return fmt.Errorf("vibe-api GET %s: %w", path, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusUnauthorized {
		return fmt.Errorf("vibe-api %s: %d (this CLI account cannot read that app — run harness auth)", path, resp.StatusCode)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("vibe-api GET %s: %d %s", path, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("vibe-api GET %s: decode: %w", path, err)
	}
	return nil
}

func (c *vibeAPI) requestJSON(method, path string, body any, out any) error {
	var bodyReader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		bodyReader = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, c.baseURL+path, bodyReader)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	c.applyAuth(req)
	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("vibe-api %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusUnauthorized {
		return fmt.Errorf("vibe-api %s %s: %d (this CLI account cannot access that app — run harness auth)", method, path, resp.StatusCode)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("vibe-api %s %s: %d %s", method, path, resp.StatusCode, strings.TrimSpace(string(respBody)))
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(respBody, out); err != nil {
		return fmt.Errorf("vibe-api %s %s: decode: %w", method, path, err)
	}
	return nil
}

func (c *vibeAPI) postJSON(path string, body any, out any) error {
	return c.requestJSON(http.MethodPost, path, body, out)
}

func (c *vibeAPI) putJSON(path string, body any, out any) error {
	return c.requestJSON(http.MethodPut, path, body, out)
}

func (c *vibeAPI) listJSON(path string) ([]map[string]any, error) {
	raw, err := c.getBytes(path)
	if err != nil {
		return nil, err
	}
	var items []map[string]any
	if err := json.Unmarshal(raw, &items); err == nil {
		return items, nil
	}
	var wrapped struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(raw, &wrapped); err == nil && wrapped.Items != nil {
		return wrapped.Items, nil
	}
	return nil, fmt.Errorf("vibe-api GET %s: unexpected list response", path)
}

func (c *vibeAPI) getBytes(path string) ([]byte, error) {
	resp, err := c.do(http.MethodGet, path)
	if err != nil {
		return nil, fmt.Errorf("vibe-api GET %s: %w", path, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("vibe-api GET %s: %d %s", path, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return body, nil
}
