// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package github

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/harness/cli/modules/har/pkg/har/migrate/util"
)

const pageSize = 100

type ghPackage struct {
	Name string `json:"name"`
}

type packument struct {
	Name     string `json:"name"`
	Versions map[string]struct {
		Dist struct {
			Tarball string `json:"tarball"`
		} `json:"dist"`
	} `json:"versions"`
	Time map[string]string `json:"time"`
}

type client struct {
	ctx    context.Context
	http   *http.Client
	apiURL string
	npmURL string
	token  string
}

func newClient(ctx context.Context, apiURL, npmURL, token string, insecure bool) *client {
	tlsCfg := &tls.Config{InsecureSkipVerify: insecure} //nolint:gosec
	return &client{
		ctx: ctx,
		http: &http.Client{
			Transport: &http.Transport{Proxy: http.ProxyFromEnvironment, TLSClientConfig: tlsCfg},
		},
		apiURL: strings.TrimSuffix(apiURL, "/"),
		npmURL: strings.TrimSuffix(npmURL, "/"),
		token:  token,
	}
}

// newRequest sets Authorization on the original request (not in a transport)
// so net/http drops it when GitHub redirects tarball downloads to another host.
func (c *client) newRequest(u string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(c.ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	req.Header.Set("User-Agent", util.UserAgentString())
	return req, nil
}

func (c *client) getJSON(u string, out interface{}) error {
	req, err := c.newRequest(u)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("GET %s: %w", u, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("GET %s: unexpected status %d: %s", u, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("GET %s: decode response: %w", u, err)
	}
	return nil
}

func (c *client) validate() error {
	var user struct {
		Login string `json:"login"`
	}
	return c.getJSON(c.apiURL+"/user", &user)
}

// ownerPath returns "orgs/<owner>" or "users/<owner>"; the package listing
// endpoints differ by owner type.
func (c *client) ownerPath(owner string) (string, error) {
	var account struct {
		Type string `json:"type"`
	}
	if err := c.getJSON(fmt.Sprintf("%s/users/%s", c.apiURL, url.PathEscape(owner)), &account); err != nil {
		return "", fmt.Errorf("look up GitHub owner %q: %w", owner, err)
	}
	if account.Type == "Organization" {
		return "orgs/" + url.PathEscape(owner), nil
	}
	return "users/" + url.PathEscape(owner), nil
}

func (c *client) listNpmPackages(owner string) ([]ghPackage, error) {
	ownerPath, err := c.ownerPath(owner)
	if err != nil {
		return nil, err
	}
	var all []ghPackage
	for page := 1; ; page++ {
		u := fmt.Sprintf("%s/%s/packages?package_type=npm&per_page=%d&page=%d", c.apiURL, ownerPath, pageSize, page)
		var pkgs []ghPackage
		if err := c.getJSON(u, &pkgs); err != nil {
			return nil, fmt.Errorf("list npm packages for %q: %w", owner, err)
		}
		all = append(all, pkgs...)
		if len(pkgs) < pageSize {
			return all, nil
		}
	}
}

// getPackument fetches npm metadata for a scoped name such as "@acme/widgets".
func (c *client) getPackument(scopedName string) (packument, error) {
	var p packument
	u := c.npmURL + "/" + strings.Replace(scopedName, "/", "%2f", 1)
	if err := c.getJSON(u, &p); err != nil {
		return packument{}, fmt.Errorf("get npm metadata for %s: %w", scopedName, err)
	}
	return p, nil
}

func (c *client) download(tarballURL string) (io.ReadCloser, http.Header, error) {
	req, err := c.newRequest(tarballURL)
	if err != nil {
		return nil, nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("download %s: %w", tarballURL, err)
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, nil, fmt.Errorf("download %s: unexpected status %d: %s",
			tarballURL, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return resp.Body, resp.Header, nil
}
