// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/harness/cli/v3/pkg/hbase"
	"github.com/harness/cli/v3/pkg/hlog"
	"github.com/harness/cli/v3/pkg/httpclient"
)

const registryURLLookupTimeout = 10 * time.Second

// FetchRegistryURL calls GET /gateway/har/api/v3/system/info to get the package registry base URL.
// Returns empty string (not an error) when the field is absent — the caller falls back gracefully.
func FetchRegistryURL(apiURL, accountID string, setAuth func(*http.Request)) (string, error) {
	c := &http.Client{Timeout: registryURLLookupTimeout}
	reqURL := fmt.Sprintf("%s/gateway/har/api/v3/system/info?account_identifier=%s",
		strings.TrimRight(apiURL, "/"), url.QueryEscape(accountID))
	req, err := http.NewRequest(http.MethodGet, reqURL, nil)
	if err != nil {
		return "", fmt.Errorf("building request: %w", err)
	}
	setAuth(req)

	resp, err := httpclient.DoWithProxyCheck(c, req)
	if err != nil {
		return "", fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("status %d", resp.StatusCode)
	}
	var parsed struct {
		Data struct {
			RegistryURL string `json:"registryUrl"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", fmt.Errorf("decoding response: %w", err)
	}
	return parsed.Data.RegistryURL, nil
}

// CredentialFlags are the Artifact Registry --api-url and --token overrides.
type CredentialFlags struct {
	APIURL             string
	Token              string
	APIURLSet          bool
	TokenSet           bool
	ResolveRegistryURL bool
}

// ApplyCredentialFlags applies explicit flags on top of auth. --token replaces
// the credential, and its account ID is taken from the token. Artifact Registry
// callers request package registry URL resolution after applying overrides.
func ApplyCredentialFlags(r *ResolvedAuth, f CredentialFlags) error {
	if r == nil {
		return nil
	}
	if f.TokenSet {
		if err := ValidatePATFormat(f.Token); err != nil {
			return fmt.Errorf("--token is invalid: %w", err)
		}
		accountID := AccountIDFromToken(f.Token)
		if accountID == "" {
			return fmt.Errorf("could not determine account from --token")
		}
		r.AuthType = AuthTypePAT
		r.PATToken = f.Token
		r.SSOToken = ""
		r.RefreshToken = ""
		r.AccountID = accountID
		r.TokenKind = TokenType(f.Token)
	}
	if f.APIURLSet {
		r.APIUrl = NormalizeAPIURL(f.APIURL)
	}
	if f.ResolveRegistryURL && r.Source != SourcePipeline {
		r.RegistryURL = fetchRegistryURLFromSystem(r)
	}
	return nil
}

// fetchRegistryURLFromSystem looks up the package host for r.APIUrl from /system/info.
// A failed lookup falls back to the production package host.
func fetchRegistryURLFromSystem(r *ResolvedAuth) string {
	registryURL, err := FetchRegistryURL(r.APIUrl, r.AccountID, r.SetAuthHeader)
	if err != nil || registryURL == "" {
		hlog.Debug("registry URL lookup failed, using default", "api_url", r.APIUrl, "error", err)
		return hbase.DefaultRegistryURL
	}
	return registryURL
}
