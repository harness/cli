// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/harness/cli/pkg/auth"
	"github.com/harness/cli/pkg/cmdctx"
	"github.com/harness/cli/pkg/config"
	"github.com/harness/cli/pkg/console"
	"github.com/harness/cli/pkg/hlog"
)

var profileNameRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,63}$`)

func LoginHandler(ctx *cmdctx.Ctx) error {
	overwrite := cmdctx.GetBool(ctx.FlagValues, "overwrite")

	profileName := cmdctx.GetString(ctx.FlagValues, "profile")
	if profileName == "" {
		profileName = "default"
	}
	if !profileNameRe.MatchString(profileName) {
		return fmt.Errorf("invalid profile name %q: must match ^[a-zA-Z0-9][a-zA-Z0-9_-]{0,63}$", profileName)
	}

	apiURL := cmdctx.GetString(ctx.FlagValues, "api-url")
	token := cmdctx.GetString(ctx.FlagValues, "api-token")
	accountID := cmdctx.GetString(ctx.FlagValues, "account")
	orgID := cmdctx.GetString(ctx.FlagValues, "org")
	projectID := cmdctx.GetString(ctx.FlagValues, "project")
	noValidate := cmdctx.GetBool(ctx.FlagValues, "no-validate")
	sso := cmdctx.GetBool(ctx.FlagValues, "sso")

	const defaultAPIURL = "https://app.harness.io"

	if sso {
		if apiURL != "" || token != "" {
			return fmt.Errorf("--sso cannot be combined with --api-url or --api-token")
		}
	}

	// isInteractive: both stdin+stdout are TTYs and at least one required value is missing.
	isInteractive := !sso && console.IsBothTTY() && (apiURL == "" || token == "")

	// Load config early so we can check for an existing profile.
	cfg, err := config.LoadConfig()
	if err != nil {
		return err
	}

	if sso {
		// Skip the overwrite prompt when re-authenticating a profile whose SSO
		// session has already fully expired (access + refresh) — this is the
		// exact condition that tells the user to run 'auth login --sso' again,
		// so there is nothing valid left to lose, and requiring --overwrite
		// would otherwise break that recovery path without a TTY.
		if !ssoSessionExpired(cfg, profileName) {
			if err := confirmOverwrite(cfg, profileName, overwrite); err != nil {
				return err
			}
		}
		return runSSOLogin(ctx, cfg, profileName)
	}

	var registryURL string

	if isInteractive {
		// Run the bubbletea wizard — handles URL, PAT, validation, org/project pickers.
		otherURLs := profileAPIURLs(cfg)
		existing := &WizardExisting{OtherURLs: otherURLs}
		if existingProfile, exists := cfg.Profiles[profileName]; exists {
			if err := confirmOverwrite(cfg, profileName, overwrite); err != nil {
				return err
			}
			// Load existing token so the wizard can offer "use existing".
			// Gateway URLs (SSO's mcp.harness.io/cli, and any other "/cli"-suffixed
			// internal/test gateway) are never valid PAT/token API URLs.
			existingURL := existingProfile.APIUrl
			if isGatewayURL(existingURL) {
				existingURL = ""
			}
			existingToken := ""
			if creds, cerr := auth.LoadCredentials(); cerr == nil {
				if c := creds[profileName]; c != nil {
					existingToken = c.Token
				}
			}
			existing.APIURL = existingURL
			existing.Token = existingToken
			existing.OrgID = existingProfile.OrgID
			existing.ProjectID = existingProfile.ProjectID
		}

		result, err := RunLoginWizard(ctx, existing)
		if err != nil {
			return err
		}
		if result == nil {
			return fmt.Errorf("canceled by user — config not written")
		}
		if result.SSOSelected {
			// Overwrite was already confirmed above; hand off to the same flow as --sso.
			return runSSOLogin(ctx, cfg, profileName)
		}

		apiURL = result.APIURL
		token = result.Token
		accountID = result.Account
		registryURL = result.RegURL
		if orgID == "" {
			orgID = result.OrgID
		}
		if projectID == "" {
			projectID = result.Project
		}
		if result.ScopeNotSet {
			profileArg := profileArgSuffix(profileName)
			if result.ScopeSkipped {
				fmt.Fprintf(os.Stderr, "\nNote: Org and project not set — run 'harness auth setscope%s' to configure\n", profileArg)
			} else {
				fmt.Fprintf(os.Stderr, "\nNote: Token does not have permission to list organizations or projects — run 'harness auth setscope%s' to manually configure org and project\n", profileArg)
			}
		}
	} else {
		// Non-interactive: all values from flags/env.
		if _, exists := cfg.Profiles[profileName]; exists && !overwrite {
			return fmt.Errorf("profile %q already exists — pass --overwrite to replace it", profileName)
		}

		fmt.Fprintf(os.Stderr, "Logging in for profile %q\n\n", profileName)

		if ctx.IsPty {
			// pty but all flags provided — validate URL only
			if apiURL == "" {
				apiURL = defaultAPIURL
			} else {
				apiURL = auth.NormalizeAPIURL(apiURL)
				if err := auth.ValidateAPIURL(apiURL); err != nil {
					return err
				}
			}
		} else {
			if token == "" {
				return fmt.Errorf("not a terminal — pass --api-token (and --api-url if not using the default)")
			}
			if apiURL == "" {
				apiURL = defaultAPIURL
			} else {
				apiURL = auth.NormalizeAPIURL(apiURL)
				if err := auth.ValidateAPIURL(apiURL); err != nil {
					return err
				}
			}
		}

		if token == "" {
			return fmt.Errorf("API token is required")
		}

		if err := auth.ValidatePATFormat(token); err != nil {
			return fmt.Errorf("invalid token: %w", err)
		}
		tokenAccountID := auth.AccountIDFromToken(token)
		if accountID == "" {
			accountID = tokenAccountID
		} else if accountID != tokenAccountID {
			return fmt.Errorf("--account %q does not match account ID in token (%q)", accountID, tokenAccountID)
		}

		if noValidate {
			fmt.Fprintln(os.Stderr, "Warning: token validation skipped — credentials written but not verified")
		} else {
			if err := validateToken(apiURL, token, accountID); err != nil {
				return err
			}
		}

		registryURL, err = fetchRegistryURL(apiURL, token, accountID)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: could not fetch registry URL: %v\n", err)
		}
	}

	email := fetchTokenEmail(apiURL, token, accountID)

	cfg.Profiles[profileName] = &config.Profile{
		APIUrl:      apiURL,
		AccountID:   accountID,
		OrgID:       orgID,
		ProjectID:   projectID,
		RegistryURL: registryURL,
		Email:       email,
	}
	if err := config.SaveConfig(cfg); err != nil {
		return fmt.Errorf("saving profile: %w", err)
	}
	if err := auth.SetCredential(profileName, token); err != nil {
		return fmt.Errorf("saving credentials: %w", err)
	}

	fmt.Printf("Logged in. Profile %q written.\n\n", profileName)
	printStatus(runStatusChecks(profileName))
	return nil
}

// profileArgSuffix returns " --profile <name>" for non-default profiles, for use in hint text.
func profileArgSuffix(profileName string) string {
	if profileName == "default" {
		return ""
	}
	return " --profile " + profileName
}

// confirmOverwrite resolves what to do when profileName already exists: honor
// --overwrite, otherwise prompt on a terminal and error without one.
func confirmOverwrite(cfg *config.Config, profileName string, overwrite bool) error {
	if _, exists := cfg.Profiles[profileName]; !exists {
		return nil
	}
	switch {
	case overwrite:
		return nil
	case !console.IsBothTTY():
		return fmt.Errorf("profile %q already exists — pass --overwrite to replace it", profileName)
	}
	fmt.Fprintf(os.Stderr, "WARNING: profile %q already exists, continuing will overwrite it\n\n", profileName)
	if !console.PromptYesNo("Overwrite?") {
		return fmt.Errorf("canceled by user — config not written")
	}
	fmt.Fprintln(os.Stderr)
	return nil
}

// ssoSessionExpired reports whether profileName is an SSO-authenticated profile
// whose access and refresh tokens have both expired — the same condition that
// makes auth.CheckAndUpdateAccessToken return ErrSSOSessionExpired and point the
// user at 'auth login --sso'. Returns false if the profile doesn't exist, isn't
// SSO, or its expiry can't be determined, so callers fall back to a normal
// overwrite confirmation.
func ssoSessionExpired(cfg *config.Config, profileName string) bool {
	p, exists := cfg.Profiles[profileName]
	if !exists || p.AuthType != config.AuthTypeSSO {
		return false
	}
	creds, err := auth.LoadCredentials()
	if err != nil {
		return false
	}
	c := creds[profileName]
	if c == nil {
		return false
	}
	now := time.Now()
	return auth.IsAccessTokenExpiringSoon(c.SSOToken, now) && auth.IsAccessTokenExpiringSoon(c.RefreshToken, now)
}

// isGatewayURL reports whether apiURL is a gateway URL rather than a real cluster
// API URL — the SSO-only mcp.harness.io/cli, or any other "/cli"-suffixed
// internal/test gateway. These are never valid PAT/token API URLs and must
// never be offered as picker options.
func isGatewayURL(apiURL string) bool {
	return apiURL == mcpBaseURL || strings.HasSuffix(apiURL, "/cli")
}

// profileAPIURLs returns the deduped set of API URLs saved across all profiles,
// excluding gateway URLs (see isGatewayURL).
func profileAPIURLs(cfg *config.Config) []string {
	seen := make(map[string]bool, len(cfg.Profiles))
	urls := make([]string, 0, len(cfg.Profiles))
	for _, p := range cfg.Profiles {
		if p.APIUrl == "" || isGatewayURL(p.APIUrl) || seen[p.APIUrl] {
			continue
		}
		seen[p.APIUrl] = true
		urls = append(urls, p.APIUrl)
	}
	return urls
}

// fetchRegistryURL calls GET /gateway/har/api/v3/system/info to get the package registry base URL.
// Returns empty string (not an error) when the field is absent — the caller falls back gracefully.
func fetchRegistryURL(apiURL, token, accountID string) (string, error) {
	c := &http.Client{Timeout: 10 * time.Second}
	url := fmt.Sprintf("%s/gateway/har/api/v3/system/info?account_identifier=%s", apiURL, accountID)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return "", fmt.Errorf("building request: %w", err)
	}
	req.Header.Set("x-api-key", token)

	resp, err := c.Do(req)
	if err != nil {
		return "", fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != 200 {
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

// validateToken verifies the token against the API. PATs are checked by reading the account
// resource; SATs use the token introspection endpoint instead, since service accounts are
// rarely granted account-view permission and would otherwise fail with a 403.
func validateToken(apiURL, token, accountID string) error {
	c := &http.Client{Timeout: 10 * time.Second}
	isSAT := auth.TokenType(token) == auth.TokenKindSAT

	var req *http.Request
	var err error
	if isSAT {
		url := fmt.Sprintf("%s/ng/api/token/validate?accountIdentifier=%s", apiURL, accountID)
		hlog.Debug("validating token", "kind", "SAT", "method", "POST", "url", url)
		req, err = http.NewRequest("POST", url, strings.NewReader(token))
		if err == nil {
			req.Header.Set("Content-Type", "text/plain")
		}
	} else {
		url := fmt.Sprintf("%s/ng/api/accounts/%s?accountIdentifier=%s", apiURL, accountID, accountID)
		hlog.Debug("validating token", "kind", "PAT", "method", "GET", "url", url)
		req, err = http.NewRequest("GET", url, nil)
	}
	if err != nil {
		return fmt.Errorf("building validation request: %w", err)
	}
	req.Header.Set("x-api-key", token)

	resp, err := c.Do(req)
	if err != nil {
		return fmt.Errorf("cannot reach %s — check your API URL: %w", apiURL, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	switch resp.StatusCode {
	case 200:
		return nil
	case 401:
		return fmt.Errorf("token rejected (401) — check that your API token is valid\n\nTip: run 'harness auth profiles' to see available profiles, then retry with --profile <name>")
	case 403:
		if isSAT {
			return fmt.Errorf("service account token rejected (403) — the service account may be disabled or lack access to this account\n\nTip: pass --no-validate to write the profile anyway, then run 'harness auth status' to see which scopes are reachable")
		}
		return fmt.Errorf("token valid but access denied (403) — check account ID or RBAC permissions")
	default:
		// Try to extract a message from JSON
		var parsed struct {
			Message string `json:"message"`
		}
		if json.Unmarshal(body, &parsed) == nil && parsed.Message != "" {
			return fmt.Errorf("validation failed (%d): %s", resp.StatusCode, parsed.Message)
		}
		return fmt.Errorf("validation failed with status %d", resp.StatusCode)
	}
}
