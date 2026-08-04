// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package harbor

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/harness/cli/modules/har/pkg/har/migrate/types"
	"github.com/harness/cli/modules/har/pkg/har/migrate/util"
)

const (
	harborAPIVersion = "v2.0"
	pageSize         = 100
)

// HarborProject represents a Harbor project.
type HarborProject struct {
	Name string `json:"name"`
	ID   int    `json:"id"`
}

// HarborRepository represents a repository within a Harbor project.
// Name is the full "<project>/<repo>" string returned by the API.
type HarborRepository struct {
	Name          string `json:"name"`
	ArtifactCount int64  `json:"artifact_count"`
}

type basicTransport struct {
	base     http.RoundTripper
	username string
	password string
}

func (t *basicTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	if t.username != "" {
		req.SetBasicAuth(t.username, t.password)
	}
	req.Header.Set("User-Agent", util.UserAgentString())
	return t.base.RoundTrip(req)
}

type client struct {
	http *http.Client
	url  string
}

func newClient(reg *types.RegistryConfig) *client {
	tlsCfg := &tls.Config{InsecureSkipVerify: reg.Insecure} //nolint:gosec
	return &client{
		http: &http.Client{
			Transport: &basicTransport{
				base:     &http.Transport{TLSClientConfig: tlsCfg},
				username: reg.Credentials.Username,
				password: reg.Credentials.Password,
			},
		},
		url: strings.TrimSuffix(reg.Endpoint, "/"),
	}
}

func (c *client) do(req *http.Request) (*http.Response, error) {
	return c.http.Do(req)
}

// health checks Harbor connectivity via the health endpoint.
func (c *client) health() error {
	u := fmt.Sprintf("%s/api/%s/health", c.url, harborAPIVersion)
	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	resp, err := c.do(req)
	if err != nil {
		return fmt.Errorf("execute request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("unexpected status %d: %s", resp.StatusCode, string(body))
	}
	return nil
}

// getProject retrieves project metadata by name.
func (c *client) getProject(project string) (HarborProject, error) {
	u := fmt.Sprintf("%s/api/%s/projects/%s", c.url, harborAPIVersion, project)
	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		return HarborProject{}, fmt.Errorf("create request: %w", err)
	}
	resp, err := c.do(req)
	if err != nil {
		return HarborProject{}, fmt.Errorf("execute request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return HarborProject{}, fmt.Errorf("project %q not found", project)
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return HarborProject{}, fmt.Errorf("unexpected status %d: %s", resp.StatusCode, string(body))
	}
	var p HarborProject
	if err := json.NewDecoder(resp.Body).Decode(&p); err != nil {
		return HarborProject{}, fmt.Errorf("decode response: %w", err)
	}
	return p, nil
}

// listRepositories returns all repositories for a Harbor project, following pagination.
func (c *client) listRepositories(project string) ([]HarborRepository, error) {
	var all []HarborRepository
	page := 1
	for {
		u := fmt.Sprintf("%s/api/%s/projects/%s/repositories?page=%d&page_size=%d",
			c.url, harborAPIVersion, project, page, pageSize)
		req, err := http.NewRequest(http.MethodGet, u, nil)
		if err != nil {
			return nil, fmt.Errorf("create request: %w", err)
		}
		resp, err := c.do(req)
		if err != nil {
			return nil, fmt.Errorf("execute request: %w", err)
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("read response: %w", err)
		}
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("unexpected status %d: %s", resp.StatusCode, string(body))
		}
		var repos []HarborRepository
		if err := json.Unmarshal(body, &repos); err != nil {
			return nil, fmt.Errorf("decode response: %w", err)
		}
		all = append(all, repos...)
		// Stop if no rel=next Link header or fewer results than page size
		if nextPageURL(resp.Header.Get("Link")) == "" || len(repos) < pageSize {
			break
		}
		page++
	}
	return all, nil
}

// nextPageURL extracts the next-page URL from a Link header (rel=next).
func nextPageURL(linkHeader string) string {
	for _, part := range strings.Split(linkHeader, ",") {
		part = strings.TrimSpace(part)
		segments := strings.Split(part, ";")
		if len(segments) < 2 {
			continue
		}
		rel := strings.TrimSpace(segments[1])
		if strings.EqualFold(rel, `rel="next"`) || strings.EqualFold(rel, "rel=next") {
			return strings.Trim(strings.TrimSpace(segments[0]), "<>")
		}
	}
	return ""
}

// repoShortName strips the "<project>/" prefix Harbor prepends to repository names.
func repoShortName(project, fullName string) string {
	return strings.TrimPrefix(fullName, project+"/")
}
