// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"

	"github.com/harness/cli/v3/pkg/config"
	"github.com/harness/cli/v3/pkg/hbase"
)

// AuthType is re-exported from pkg/config for callers that only import pkg/auth.
type AuthType = config.AuthType

const (
	AuthTypePAT = config.AuthTypePAT
	AuthTypeSSO = config.AuthTypeSSO
)

const (
	SourceEnv      = "env"
	SourcePipeline = "pipeline"
)

// Reserved --profile / HARNESS_PROFILE values that force a specific auth
// mode instead of naming a config-file profile.
const (
	ProfileSentinelEnv      = "env"
	ProfileSentinelPipeline = "pipeline"
)

// IsReservedProfileName reports whether name is a reserved sentinel and
// therefore not usable as a config-file profile name.
func IsReservedProfileName(name string) bool {
	return name == ProfileSentinelEnv || name == ProfileSentinelPipeline
}

// EffectiveProfileSelector returns the profile name/sentinel that should
// drive resolution: profileFlag if set, else HARNESS_PROFILE. The single
// source of truth for "an explicit --profile beats HARNESS_PROFILE".
func EffectiveProfileSelector(profileFlag string) string {
	if profileFlag != "" {
		return profileFlag
	}
	return os.Getenv(hbase.EnvProfile)
}

// ResolvedAuth is the result of auth resolution — the active credentials for a command invocation.
// Credential fields are never printed; callers that display auth context must omit them.
type ResolvedAuth struct {
	Source          string   // "profile:<name>" or SourceEnv
	AuthType        AuthType // AuthTypePAT or AuthTypeSSO
	ExplicitProfile string   // non-empty only when --profile flag was explicitly passed
	APIUrl          string
	UIUrl           string // Harness UI base URL; only set for SSO profiles (from JWT subdomain)
	AccountID       string
	OrgID           string
	ProjectID       string
	RegistryURL     string
	Email           string    // user email from profile; empty for env-var auth or legacy profiles
	UserType        string    // config.UserTypeUser or config.UserTypeServiceAccount; empty for env-var auth, legacy profiles, or SSO
	UserID          string    // Harness user uuid from profile; empty for env-var auth, legacy profiles, or service accounts
	TokenKind       TokenKind // pat, sat, jwt, or "" (unknown)

	// Exactly one of these is set depending on AuthType.
	PATToken     string // set when AuthType == AuthTypePAT
	SSOToken     string // set when AuthType == AuthTypeSSO
	RefreshToken string // set when AuthType == AuthTypeSSO

	// Headers are set when pipeline_auth fired (see ResolvePipelineAuth), already
	// evaluated and ready to send. Non-empty Headers takes priority over
	// AuthType/PATToken/SSOToken in SetAuthHeader — the two are never combined.
	Headers map[string]string
}

// SetAuthHeader sets the auth header(s) on req: pipeline_auth's Headers when
// present, otherwise the normal Authorization (SSO) or x-api-key (PAT) header.
func (a *ResolvedAuth) SetAuthHeader(req *http.Request) {
	if len(a.Headers) > 0 {
		for k, v := range a.Headers {
			req.Header.Set(k, v)
		}
		return
	}
	if a.AuthType == AuthTypeSSO {
		req.Header.Set("Authorization", "Bearer "+a.SSOToken)
	} else {
		req.Header.Set("x-api-key", a.PATToken)
	}
}

// Load populates a ResolvedAuth following the resolution order from auth.md:
// --profile, then HARNESS_PROFILE (name or sentinel), then HARNESS_API_KEY,
// then the default profile. It never errors on missing optional fields —
// callers get whatever could be populated. Use Validate to check that the
// result is complete enough to make API calls.
func Load(profileFlag string) (*ResolvedAuth, error) {
	switch selector := EffectiveProfileSelector(profileFlag); selector {
	case ProfileSentinelPipeline:
		return nil, fmt.Errorf("profile %q has no context to resolve here — it only applies to commands run inside a pipeline", selector)
	case ProfileSentinelEnv:
		return loadEnvMode()
	case "":
		if key := os.Getenv(hbase.EnvAPIKey); key != "" {
			return loadEnvMode()
		}
		return resolveProfile("default")
	default:
		r, err := resolveProfile(selector)
		if err != nil {
			return nil, err
		}
		if profileFlag != "" {
			r.ExplicitProfile = profileFlag
		}
		return r, nil
	}
}

// loadEnvMode resolves credentials from HARNESS_API_KEY and friends, no
// config file read. Errors if HARNESS_API_KEY is unset.
func loadEnvMode() (*ResolvedAuth, error) {
	key := os.Getenv(hbase.EnvAPIKey)
	if key == "" {
		return nil, fmt.Errorf("%s is required for env auth mode", hbase.EnvAPIKey)
	}
	apiURL := os.Getenv(hbase.EnvAPIURL)
	if apiURL == "" {
		apiURL = hbase.DefaultAPIURL
	}
	registryURL := os.Getenv(hbase.EnvRegistryURL)
	if registryURL == "" {
		registryURL = hbase.DefaultRegistryURL
	}
	acct := os.Getenv(hbase.EnvAccount)
	if acct == "" {
		acct = AccountIDFromToken(key)
	}
	return &ResolvedAuth{
		Source:      SourceEnv,
		AuthType:    AuthTypePAT,
		PATToken:    key,
		AccountID:   acct,
		OrgID:       os.Getenv(hbase.EnvOrg),
		ProjectID:   os.Getenv(hbase.EnvProject),
		APIUrl:      apiURL,
		RegistryURL: registryURL,
		TokenKind:   TokenType(key),
	}, nil
}

// LoginHint returns the appropriate 'harness auth login...' command for an error hint,
// including --profile <name> when the profile was explicitly set via the flag.
func (r *ResolvedAuth) LoginHint(cmd string) string {
	if r.ExplicitProfile != "" {
		return "harness --profile " + r.ExplicitProfile + " auth " + cmd
	}
	return "harness auth " + cmd
}

// Validate checks that a ResolvedAuth is complete enough to make API calls.
func Validate(r *ResolvedAuth) error {
	if r.AuthType == AuthTypeSSO {
		if r.SSOToken == "" {
			return fmt.Errorf("no token found for profile — run '%s' to re-authenticate", r.LoginHint("login --sso"))
		}
	} else {
		if r.PATToken == "" {
			return fmt.Errorf("no token found for profile — run '%s' to re-authenticate", r.LoginHint("login"))
		}
		if err := ValidatePATFormat(r.PATToken); err != nil {
			if r.Source == SourceEnv {
				return fmt.Errorf("%s is invalid: %w", hbase.EnvAPIKey, err)
			}
			return fmt.Errorf("stored token is invalid — run '%s' to re-authenticate: %w", r.LoginHint("login"), err)
		}
		if tokenAcct := AccountIDFromToken(r.PATToken); tokenAcct != "" && r.AccountID != tokenAcct {
			if r.Source == SourceEnv {
				return fmt.Errorf("%s %q does not match account in token %q", hbase.EnvAccount, r.AccountID, tokenAcct)
			}
			return fmt.Errorf("stored account %q does not match token — run '%s' to re-authenticate", r.AccountID, r.LoginHint("login"))
		}
	}
	if r.OrgID == "" {
		if r.Source == SourceEnv {
			return fmt.Errorf("org is required in env mode — set %s", hbase.EnvOrg)
		}
		return fmt.Errorf("profile has no org — run 'harness auth setscope' to configure it")
	}
	if r.ProjectID == "" {
		if r.Source == SourceEnv {
			return fmt.Errorf("project is required in env mode — set %s", hbase.EnvProject)
		}
		return fmt.Errorf("profile has no project — run 'harness auth setscope' to configure it")
	}
	return nil
}

// Resolve loads and validates credentials. Used by all normal commands.
func Resolve(profileFlag string) (*ResolvedAuth, error) {
	r, err := Load(profileFlag)
	if err != nil {
		return nil, err
	}
	if err := Validate(r); err != nil {
		return nil, err
	}
	return r, nil
}

// ResolveWithOverrides resolves credentials via Resolve, then applies orgOverride/
// projectOverride on top of whatever the resolved profile/env already set. An empty
// override leaves the resolved value untouched.
func ResolveWithOverrides(profileFlag, orgOverride, projectOverride string) (*ResolvedAuth, error) {
	r, err := Resolve(profileFlag)
	if err != nil {
		return nil, err
	}
	if orgOverride != "" {
		r.OrgID = orgOverride
	}
	if projectOverride != "" {
		r.ProjectID = projectOverride
	}
	return r, nil
}

// PipelineAuthConfig is the fully-merged (module default + command-level)
// pipeline_auth for one command. See spec.PipelineAuthSpec — this is pkg/spec's
// shape translated into a plain struct so pkg/auth doesn't import pkg/spec.
type PipelineAuthConfig struct {
	TokenEnvVar       string
	APIURLEnvVar      string
	RegistryURLEnvVar string
	Headers           map[string]string
}

// ResolvePipelineAuth resolves pipeline mode, triggered by hbase.EnvPipelineID.
// When forced is false (implicit auto-detection), it returns (nil, false, nil)
// when not in a pipeline, or when cfg is nil (command has no pipeline_auth
// block, so it falls through to normal auth). When forced is true (the
// explicit "pipeline" --profile/HARNESS_PROFILE sentinel), those same two
// cases are hard errors instead — the caller explicitly asked for pipeline
// mode, so there is no fallthrough. Either way, once applicable, every
// missing piece is a hard error — no fallthrough.
//
// evalHeaders evaluates cfg.Headers's expr-lang expressions against the
// resolved token; pkg/auth cannot import pkg/exprenv (see pkg/registry/
// buildctx.go for the import-cycle reason), so the caller supplies it.
func ResolvePipelineAuth(cfg *PipelineAuthConfig, forced bool, evalHeaders func(headers map[string]string, token string) map[string]string) (*ResolvedAuth, bool, error) {
	if os.Getenv(hbase.EnvPipelineID) == "" {
		if forced {
			return nil, true, fmt.Errorf("%s is unset — not running in a pipeline", hbase.EnvPipelineID)
		}
		return nil, false, nil
	}
	if cfg == nil {
		if forced {
			return nil, true, fmt.Errorf("command does not support pipeline auth")
		}
		return nil, false, nil
	}
	account := os.Getenv(hbase.EnvAccountID)
	if account == "" {
		return nil, true, fmt.Errorf("%s is required in pipeline mode", hbase.EnvAccountID)
	}
	org := os.Getenv(hbase.EnvOrgID)
	if org == "" {
		return nil, true, fmt.Errorf("%s is required in pipeline mode", hbase.EnvOrgID)
	}
	project := os.Getenv(hbase.EnvProjectID)
	if project == "" {
		return nil, true, fmt.Errorf("%s is required in pipeline mode", hbase.EnvProjectID)
	}
	if infra := os.Getenv(hbase.EnvInfra); infra != hbase.InfraVM {
		return nil, true, fmt.Errorf("pipeline auth supports %s infra only (%s=%q)", hbase.InfraVM, hbase.EnvInfra, infra)
	}
	token := os.Getenv(cfg.TokenEnvVar)
	if token == "" {
		return nil, true, fmt.Errorf("%s is required in pipeline mode", cfg.TokenEnvVar)
	}
	return &ResolvedAuth{
		Source:      SourcePipeline,
		AccountID:   account,
		OrgID:       org,
		ProjectID:   project,
		APIUrl:      os.Getenv(cfg.APIURLEnvVar),
		RegistryURL: os.Getenv(cfg.RegistryURLEnvVar),
		Headers:     evalHeaders(cfg.Headers, token),
	}, true, nil
}

func resolveProfile(name string) (*ResolvedAuth, error) {
	cfg, err := config.LoadConfig()
	if err != nil {
		return nil, err
	}
	p, ok := cfg.Profiles[name]
	if !ok {
		if name == "default" {
			return nil, errors.New("not logged in — run 'harness auth login' to get started")
		}
		return nil, fmt.Errorf("profile %q not found", name)
	}
	creds, err := LoadCredentials()
	if err != nil {
		return nil, fmt.Errorf("loading credentials: %w", err)
	}
	authType := p.AuthType
	if authType == "" {
		authType = AuthTypePAT
	}
	profileCreds := creds[name]
	activeToken := ""
	if profileCreds != nil {
		if authType == AuthTypeSSO {
			activeToken = profileCreds.SSOToken
		} else {
			activeToken = profileCreds.Token
		}
	}
	if activeToken == "" {
		return nil, fmt.Errorf("no token found for profile %q — run 'harness auth login' to re-authenticate", name)
	}
	apiURL := p.APIUrl
	if apiURL == "" {
		apiURL = hbase.DefaultAPIURL
	}
	registryURL := p.RegistryURL
	if registryURL == "" {
		registryURL = hbase.DefaultRegistryURL
	}
	r := &ResolvedAuth{
		Source:      "profile:" + name,
		AuthType:    authType,
		APIUrl:      apiURL,
		UIUrl:       p.UIUrl,
		AccountID:   p.AccountID,
		OrgID:       p.OrgID,
		ProjectID:   p.ProjectID,
		RegistryURL: registryURL,
		Email:       p.Email,
		UserType:    p.UserType,
		UserID:      p.UserID,
		TokenKind:   TokenType(activeToken),
	}
	if authType == AuthTypeSSO {
		r.SSOToken = activeToken
		r.RefreshToken = profileCreds.RefreshToken
	} else {
		r.PATToken = activeToken
	}
	return r, nil
}

var hostLabelRE = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9-]*[A-Za-z0-9])?$`)

// NormalizeAPIURL expands a bare label shorthand (e.g. "harness0" → "https://harness0.harness.io")
// and, for anything else that has no scheme, prepends "https://" (e.g. "harness.onefiserv.net" →
// "https://harness.onefiserv.net"). Input that already has a scheme is returned unchanged.
func NormalizeAPIURL(s string) string {
	s = strings.TrimSpace(s)
	if hostLabelRE.MatchString(s) {
		return "https://" + s + ".harness.io"
	}
	if !strings.Contains(s, "://") {
		return "https://" + s
	}
	return s
}

// ValidateAPIURL returns an error if apiURL is not a well-formed https:// URL with a host.
// It no longer restricts the host to *.harness.io, so vanity and on-prem domains are accepted.
func ValidateAPIURL(apiURL string) error {
	u, err := url.Parse(apiURL)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return fmt.Errorf("%q is not a valid URL — expected an https:// URL with a host", apiURL)
	}
	return nil
}

// TokenKind identifies the type of a Harness token.
type TokenKind string

const (
	TokenKindPAT     TokenKind = "pat"
	TokenKindSAT     TokenKind = "sat"
	TokenKindJWT     TokenKind = "jwt"
	TokenKindUnknown TokenKind = ""
)

// jwtSegment matches a single base64url-encoded JWT segment (header, payload, or signature).
var jwtSegment = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// TokenType returns the kind of token (PAT, SAT, JWT, or unknown) by inspecting its structure.
// JWT detection is structural only: exactly 3 non-empty base64url segments separated by dots.
func TokenType(token string) TokenKind {
	switch {
	case strings.HasPrefix(token, "pat."):
		return TokenKindPAT
	case strings.HasPrefix(token, "sat."):
		return TokenKindSAT
	default:
		parts := strings.Split(token, ".")
		if len(parts) == 3 {
			isJWT := true
			for _, p := range parts {
				if len(p) == 0 || !jwtSegment.MatchString(p) {
					isJWT = false
					break
				}
			}
			if isJWT {
				return TokenKindJWT
			}
		}
		return TokenKindUnknown
	}
}

var patSegment = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// ValidatePATFormat returns an error if token does not match pat.<accountId>.<tokenId>.<secret>
// or sat.<accountId>.<tokenId>.<secret>.
func ValidatePATFormat(token string) error {
	parts := strings.SplitN(token, ".", 4)
	if len(parts) != 4 || (parts[0] != "pat" && parts[0] != "sat") {
		return fmt.Errorf("invalid PAT/SAT format — expected pat.<accountId>.<tokenId>.<secret> or sat.<accountId>.<tokenId>.<secret>")
	}
	for _, p := range parts[1:] {
		if !patSegment.MatchString(p) {
			return fmt.Errorf("invalid PAT/SAT format — segments must match [A-Za-z0-9_-]+")
		}
	}
	return nil
}

// AccountIDFromToken extracts the account ID from a valid PAT/SAT of the form pat.{AccountID}.x.y
// or sat.{AccountID}.x.y. Callers must validate the token with ValidatePATFormat before calling this.
func AccountIDFromToken(token string) string {
	if TokenType(token) == TokenKindUnknown {
		return ""
	}
	parts := strings.SplitN(token, ".", 4)
	if len(parts) == 4 {
		return parts[1]
	}
	return ""
}

// MaskedToken returns a display-safe representation of a token.
// For PAT/SAT tokens it reveals <prefix>.<accountID> and masks the remaining segments.
// For anything else it falls back to Masked.
func MaskedToken(s string) string {
	kind := TokenType(s)
	if kind != TokenKindPAT && kind != TokenKindSAT {
		return Masked(s)
	}
	parts := strings.SplitN(s, ".", 4)
	if len(parts) == 4 {
		return parts[0] + "." + parts[1] + "." + strings.Repeat("•", len(parts[2])) + "." + strings.Repeat("•", len(parts[3]))
	}
	return Masked(s)
}

// Masked returns a display-safe representation of an arbitrary secret string
// by replacing every character with a bullet.
func Masked(s string) string {
	return strings.Repeat("•", len(s))
}
