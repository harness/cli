// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/harness/cli/pkg/auth"
	hclient "github.com/harness/cli/pkg/client"
	"github.com/harness/cli/pkg/cmdctx"
	"github.com/harness/cli/pkg/config"
	"github.com/harness/cli/pkg/console"
	"github.com/harness/cli/pkg/format"
	"github.com/harness/cli/pkg/hbase"
	"github.com/harness/cli/pkg/hlog"
)

type checkResult struct {
	OK     bool   `json:"ok"`
	Warn   bool   `json:"warn,omitempty"` // soft warning: not OK but not a blocking error
	Name   string `json:"name,omitempty"`
	Error  string `json:"error,omitempty"`  // short message shown in the row
	Detail string `json:"detail,omitempty"` // actionable message shown at the bottom
}

type statusChecks struct {
	Profile checkResult  `json:"Profile"`
	API     checkResult  `json:"API"`
	User    checkResult  `json:"User"`
	Account checkResult  `json:"Account"`
	Org     *checkResult `json:"Org,omitempty"`
	Project *checkResult `json:"Project,omitempty"`
}

type statusResult struct {
	Source             string       `json:"Source"`
	Profile            string       `json:"Profile"`
	ProfileMissing     bool         `json:"-"` // profile has no entry in config at all; StatusHandler returns early on this
	APIUrl             string       `json:"APIUrl"`
	RegistryURL        string       `json:"RegistryURL,omitempty"`
	AccountID          string       `json:"AccountID"`
	OrgID              string       `json:"OrgID,omitempty"`
	ProjectID          string       `json:"ProjectID,omitempty"`
	ProjectURL         string       `json:"ProjectURL,omitempty"`
	IsSAT              bool         `json:"IsSAT,omitempty"`
	TokenType          string       `json:"TokenType,omitempty"`
	SATIdentity        string       `json:"SATIdentity,omitempty"`        // "username (email)" from token/validate
	TokenValidTo       int64        `json:"TokenValidTo,omitempty"`       // epoch ms from token/validate; 0 = no expiry
	RefreshTokenExpiry int64        `json:"RefreshTokenExpiry,omitempty"` // epoch ms; SSO refresh token expiry
	Status             statusChecks `json:"Status"`
	CurrentUser        any          `json:"CurrentUser,omitempty"`
	UserEmail          string       `json:"UserEmail,omitempty"` // SSO: email from JWT claims (no currentUser API call)
}

const apiTimeout = 5 * time.Second

func profileName(source string) string {
	if s, ok := strings.CutPrefix(source, "profile:"); ok {
		return s
	}
	return source
}

func StatusHandler(ctx *cmdctx.Ctx) error {
	profileFlag := cmdctx.GetString(ctx.FlagValues, "profile")
	jsonMode := ctx.FormatFlags.Format == "json"
	tokenStatus := cmdctx.GetBool(ctx.FlagValues, "token-status")

	r := runStatusChecks(profileFlag)

	// A profile that doesn't exist in config at all has nothing to report —
	// skip the status table and return the clean error directly.
	if r.ProfileMissing {
		return errors.New(r.Status.Profile.Error)
	}

	if jsonMode {
		out, _ := json.MarshalIndent(r, "", "  ")
		fmt.Println(string(out))
	} else {
		printStatus(r)
		if tokenStatus {
			printTokenStatus(profileFlag)
		}
	}
	return checkErrors(r)
}

func runStatusChecks(profileFlag string) statusResult {
	// Determine the source before resolution so error display is correct.
	var anticipatedSource string
	if profileFlag != "" {
		anticipatedSource = "profile:" + profileFlag
	} else if os.Getenv(hbase.EnvAPIKey) != "" {
		anticipatedSource = auth.SourceEnv
	} else if env := os.Getenv(hbase.EnvProfile); env != "" {
		anticipatedSource = "profile:" + env
	} else {
		anticipatedSource = "profile:default"
	}

	skip := checkResult{OK: false, Error: "skipped"}
	r := statusResult{Source: anticipatedSource, Profile: profileName(anticipatedSource)}

	resolved, loadErr := auth.Load(profileFlag)
	if loadErr != nil {
		r.Status.Profile = checkResult{OK: false, Error: loadErr.Error()}
		populateKnownProfileFields(&r, profileName(anticipatedSource))

		// APIUrl needs no credential, so check it independently even though
		// auth failed — it's real signal, not something we're skipping.
		if r.APIUrl != "" {
			overridden := os.Getenv(hbase.EnvSSOBaseURL) != ""
			r.Status.API = checkAPIUrl(r.APIUrl, overridden)
		} else {
			r.Status.API = skip
		}
		r.Status.User = skip
		r.Status.Account = notVerifiedResult()
		if r.OrgID != "" {
			org := notVerifiedResult()
			r.Status.Org = &org
		} else {
			org := notSetResult(anticipatedSource, hbase.EnvOrg, "org")
			r.Status.Org = &org
		}
		if r.ProjectID != "" {
			proj := notVerifiedResult()
			r.Status.Project = &proj
		} else {
			proj := notSetResult(anticipatedSource, hbase.EnvProject, "project")
			r.Status.Project = &proj
		}
		return r
	}
	r.Source = resolved.Source
	r.Profile = profileName(resolved.Source)
	r.APIUrl = resolved.APIUrl
	r.RegistryURL = resolved.RegistryURL
	r.AccountID = resolved.AccountID
	r.OrgID = resolved.OrgID
	r.ProjectID = resolved.ProjectID
	r.IsSAT = auth.TokenType(resolved.PATToken) == auth.TokenKindSAT
	switch {
	case resolved.AuthType == auth.AuthTypeSSO:
		r.TokenType = "SSO"
		if exp, err := auth.AccessTokenExpiry(resolved.RefreshToken); err == nil {
			r.RefreshTokenExpiry = exp.UnixMilli()
		}
	case auth.TokenType(resolved.PATToken) == auth.TokenKindSAT:
		r.TokenType = "SAT"
	default:
		r.TokenType = "PAT"
	}
	if err := auth.CheckAndUpdateAccessToken(resolved, time.Now()); err != nil {
		r.Status.Profile = checkResult{OK: false, Error: err.Error()}
		r.Status.API = skip
		r.Status.User = skip
		r.Status.Account = skip
		r.Status.Org = &skip
		r.Status.Project = &skip
		return r
	}
	r.Status.Profile = checkResult{OK: true}

	// An explicit HARNESS_SSO_BASE_URL override (present during SSO login) means the
	// value was deliberate, so a format mismatch is not even a warning.
	overridden := os.Getenv(hbase.EnvSSOBaseURL) != ""
	apiCheck := checkAPIUrl(resolved.APIUrl, overridden)
	r.Status.API = apiCheck
	if !apiCheck.OK && !apiCheck.Warn {
		r.Status.User = skip
		r.Status.Account = skip
		r.Status.Org = &skip
		r.Status.Project = &skip
		return r
	}

	if resolved.AuthType != auth.AuthTypeSSO {
		if err := auth.ValidatePATFormat(resolved.PATToken); err != nil {
			r.Status.User = checkResult{OK: false, Error: err.Error()}
			r.Status.Account = skip
			r.Status.Org = &skip
			r.Status.Project = &skip
			return r
		}
	}

	isSAT := auth.TokenType(resolved.PATToken) == auth.TokenKindSAT
	var resolvedIdentity tokenIdentity // identity discovered during user check
	if isSAT {
		identity, validTo, err := validateSATToken(resolved)
		if err != nil {
			r.Status.User = checkResult{OK: false, Error: err.Error()}
			r.Status.Account = skip
			r.Status.Org = &skip
			r.Status.Project = &skip
			return r
		}
		r.SATIdentity = identity
		r.TokenValidTo = validTo
		r.Status.User = checkResult{OK: true}
		// Resolve the service account's identity for profile update below.
		resolvedIdentity = fetchTokenIdentity(resolved.APIUrl, resolved.PATToken, resolved.AccountID)
	} else if resolved.AuthType == auth.AuthTypeSSO {
		// For SSO, email comes from JWT claims — parse it from the stored token.
		if claims, cerr := parseJWT(resolved.SSOToken); cerr == nil {
			resolvedIdentity.Email = claims.Email
			r.UserEmail = claims.Email
		}
		r.Status.User = checkResult{OK: true}
		// The JWT's sub claim is a Keycloak uuid, not the Harness one, so the user
		// uuid needs its own lookup. Best-effort — a failure here must not affect
		// the already-OK auth status.
		if currentUser, cerr := fetchCurrentUser(resolved); cerr != nil {
			hlog.Debug("fetching current user for SSO status failed", "error", cerr)
		} else if _, uuid := currentUserFields(currentUser); uuid != "" {
			resolvedIdentity.UserType = config.UserTypeUser
			resolvedIdentity.UserID = uuid
		}
	} else {
		// Validate the PAT token first — this is authoritative for auth failure.
		validTo, err := fetchTokenValidTo(resolved)
		if err != nil {
			r.Status.User = checkResult{OK: false, Error: err.Error()}
			r.Status.Account = skip
			r.Status.Org = &skip
			r.Status.Project = &skip
			return r
		}
		r.TokenValidTo = validTo
		r.Status.User = checkResult{OK: true}
		// Fetch user details for display (best-effort, not auth-critical).
		if currentUser, cerr := fetchCurrentUser(resolved); cerr != nil {
			hlog.Debug("fetching current user for status failed", "error", cerr)
		} else {
			r.CurrentUser = currentUser
			email, uuid := currentUserFields(currentUser)
			resolvedIdentity = tokenIdentity{Email: email, UserType: config.UserTypeUser, UserID: uuid}
		}
	}

	persistResolvedIdentity(resolved, resolvedIdentity)

	// softErr wraps a 403 as a warning for SAT tokens — the SA may lack enumeration
	// permissions but still have resource-level access.
	softErr := func(err error) checkResult {
		if isSAT && err != nil && strings.Contains(err.Error(), "403") {
			return checkResult{Warn: true, Error: "access denied (403) — SA may lack enumeration permissions"}
		}
		if err != nil {
			return checkResult{OK: false, Error: err.Error()}
		}
		return checkResult{}
	}

	accountName, err := checkAccount(resolved)
	if err != nil {
		cr := softErr(err)
		if cr.Warn {
			r.Status.Account = cr
		} else {
			r.Status.Account = cr
			r.Status.Org = &skip
			r.Status.Project = &skip
			return r
		}
	} else {
		r.Status.Account = checkResult{OK: true, Name: accountName}
	}

	if resolved.OrgID == "" {
		orgResult := notSetResult(resolved.Source, hbase.EnvOrg, "org")
		projectResult := notSetResult(resolved.Source, hbase.EnvProject, "project")
		r.Status.Org = &orgResult
		r.Status.Project = &projectResult
		return r
	}
	orgName, err := checkOrg(resolved)
	if err != nil {
		cr := softErr(err)
		if cr.Warn {
			orgWarn := cr
			r.Status.Org = &orgWarn
			projWarn := checkResult{Warn: true, Error: "access denied (403) — SA may lack enumeration permissions"}
			r.Status.Project = &projWarn
			return r
		}
		r.Status.Org = &checkResult{OK: false, Error: err.Error()}
		r.Status.Project = &skip
		return r
	}
	r.Status.Org = &checkResult{OK: true, Name: orgName}

	if resolved.ProjectID == "" {
		projectResult := notSetResult(resolved.Source, hbase.EnvProject, "project")
		r.Status.Project = &projectResult
		return r
	}
	projectName, err := checkProject(resolved)
	if err != nil {
		cr := softErr(err)
		if cr.Warn {
			projWarn := cr
			r.Status.Project = &projWarn
			return r
		}
		r.Status.Project = &checkResult{OK: false, Error: err.Error()}
		return r
	}
	r.Status.Project = &checkResult{OK: true, Name: projectName}
	uiBase := resolved.UIUrl
	if uiBase == "" {
		uiBase = resolved.APIUrl
	}
	r.ProjectURL = fmt.Sprintf("%s/ng/account/%s/all/orgs/%s/projects/%s/overview",
		uiBase, resolved.AccountID, resolved.OrgID, resolved.ProjectID)

	return r
}

// persistResolvedIdentity writes identity fields discovered during a status/login check
// back to the profile on disk, if any of them are new or changed. No-op for env-var auth,
// which has no profile to update.
func persistResolvedIdentity(resolved *auth.ResolvedAuth, identity tokenIdentity) {
	if identity.Email == "" || resolved.Source == auth.SourceEnv {
		return
	}
	pName := profileName(resolved.Source)
	cfg, err := config.LoadConfig()
	if err != nil {
		return
	}
	p, ok := cfg.Profiles[pName]
	if !ok {
		return
	}
	changed := false
	if p.Email != identity.Email {
		p.Email = identity.Email
		changed = true
	}
	if identity.UserType != "" && p.UserType != identity.UserType {
		p.UserType = identity.UserType
		changed = true
	}
	if identity.UserID != "" && p.UserID != identity.UserID {
		p.UserID = identity.UserID
		changed = true
	}
	if identity.ServiceAccountID != "" && p.ServiceAccountID != identity.ServiceAccountID {
		p.ServiceAccountID = identity.ServiceAccountID
		changed = true
	}
	if changed {
		config.SaveConfig(cfg) //nolint:errcheck — best-effort
	}
}

// populateKnownProfileFields fills in whatever profile fields we already know from
// config even though auth.Load failed — the profile's own APIUrl/AccountID/OrgID/
// ProjectID don't require a valid token to read, so a bad/missing credential
// shouldn't blank them out on the status display.
func populateKnownProfileFields(r *statusResult, pName string) {
	cfg, err := config.LoadConfig()
	if err != nil {
		return
	}
	p, ok := cfg.Profiles[pName]
	if !ok {
		// Env-var auth has no profile to look up; only a named profile can be "missing".
		r.ProfileMissing = r.Source != auth.SourceEnv
		return
	}
	r.APIUrl = p.APIUrl
	r.RegistryURL = p.RegistryURL
	r.AccountID = p.AccountID
	r.OrgID = p.OrgID
	r.ProjectID = p.ProjectID
	if p.AuthType == auth.AuthTypeSSO {
		r.TokenType = "SSO"
	} else {
		r.TokenType = "PAT"
	}
}

// notVerifiedResult marks a value we have on file but couldn't confirm against the
// API because an earlier check (token/API reachability) failed first.
func notVerifiedResult() checkResult {
	return checkResult{OK: false, Error: "not verified"}
}

func notSetResult(source, envVar, noun string) checkResult {
	if source == auth.SourceEnv {
		return checkResult{
			OK:     false,
			Error:  "not set",
			Detail: fmt.Sprintf("%s is not set", envVar),
		}
	}
	return checkResult{
		OK:     false,
		Error:  "not set",
		Detail: fmt.Sprintf("profile has no %s — run 'harness auth setscope' to configure it", noun),
	}
}

func statusValue(ok, warn bool, value, errMsg string) string {
	var icon string
	switch {
	case ok:
		icon = console.GreenCheck()
	case warn:
		icon = console.YellowWarning()
	default:
		icon = console.RedX()
	}
	if value == "" {
		return fmt.Sprintf("%s %s", icon, errMsg)
	}
	if errMsg == "" {
		return fmt.Sprintf("%s %s", icon, value)
	}
	return fmt.Sprintf("%s %s — %s", icon, value, errMsg)
}

func printStatus(r statusResult) {
	var rows []format.LabeledValue
	add := func(label, value string) {
		rows = append(rows, format.LabeledValue{Label: label, Value: value})
	}

	sv := func(c checkResult, value string) string {
		return statusValue(c.OK, c.Warn, value, c.Error)
	}

	profileSuffix := ""
	if r.TokenType != "" {
		profileSuffix = fmt.Sprintf(" (%s)", r.TokenType)
	}
	if r.Source == auth.SourceEnv {
		add("Mode", sv(r.Status.Profile, "env vars"+profileSuffix))
	} else {
		add("Profile", sv(r.Status.Profile, r.Profile+profileSuffix))
	}
	add("APIUrl", sv(r.Status.API, r.APIUrl))
	if r.RegistryURL != "" {
		add("RegistryUrl", fmt.Sprintf("%s %s", console.GreenCheck(), r.RegistryURL))
	}

	userLabel := "User"
	userVal := ""
	if r.IsSAT {
		userLabel = "Token"
		if r.Status.User.OK {
			userVal = r.SATIdentity
		}
	} else if email, uuid := currentUserFields(r.CurrentUser); email != "" {
		userVal = fmt.Sprintf("%s (%s)", email, uuid)
	} else if r.UserEmail != "" {
		// SSO: no currentUser call — email comes straight from the JWT claims.
		userVal = r.UserEmail
	}
	add(userLabel, sv(r.Status.User, userVal))
	switch {
	case r.TokenType == "SSO" && r.RefreshTokenExpiry != 0:
		add("Expires", formatTokenValidTo(r.RefreshTokenExpiry))
	case r.TokenType != "SSO" && r.Status.User.OK:
		add("Expires", formatTokenValidTo(r.TokenValidTo))
	}
	add("Account", sv(r.Status.Account, func() string {
		if r.Status.Account.OK {
			return fmt.Sprintf("%s (%s)", r.Status.Account.Name, r.AccountID)
		}
		return r.AccountID
	}()))
	if r.OrgID != "" || r.Status.Org != nil {
		org := r.Status.Org
		if org == nil {
			org = &checkResult{OK: false, Error: "skipped"}
		}
		add("Org", sv(*org, func() string {
			if org.OK {
				return fmt.Sprintf("%s (%s)", org.Name, r.OrgID)
			}
			return r.OrgID
		}()))
	}
	if r.ProjectID != "" || r.Status.Project != nil {
		proj := r.Status.Project
		if proj == nil {
			proj = &checkResult{OK: false, Error: "skipped"}
		}
		add("Project", sv(*proj, func() string {
			if proj.OK {
				return fmt.Sprintf("%s (%s)", proj.Name, r.ProjectID)
			}
			return r.ProjectID
		}()))
		if r.ProjectURL != "" {
			add("ProjectURL", r.ProjectURL)
		}
	}

	format.WriteLabeledValues(os.Stdout, rows)
}

func printTokenStatus(profileFlag string) {
	resolved, err := auth.Load(profileFlag)
	if err != nil || resolved.AuthType != auth.AuthTypeSSO {
		return
	}
	fmt.Println()
	printTokenExpiry(resolved.SSOToken, resolved.RefreshToken)
}

func checkErrors(r statusResult) error {
	failed := func(c checkResult) bool { return !c.OK && !c.Warn && c.Error != "skipped" }
	msg := func(c checkResult) string {
		if c.Detail != "" {
			return c.Detail
		}
		return c.Error
	}
	if failed(r.Status.Profile) {
		return fmt.Errorf("\nError: %s", maybeAppendProfileHint(msg(r.Status.Profile)))
	}
	if failed(r.Status.API) {
		return fmt.Errorf("\nError: %s", maybeAppendProfileHint(msg(r.Status.API)))
	}
	if failed(r.Status.User) {
		return fmt.Errorf("\nError: %s", maybeAppendProfileHint(msg(r.Status.User)))
	}
	if failed(r.Status.Account) {
		return fmt.Errorf("\nError: %s", maybeAppendProfileHint(msg(r.Status.Account)))
	}
	if r.Status.Org != nil && failed(*r.Status.Org) {
		return fmt.Errorf("\nError: %s", maybeAppendProfileHint(msg(*r.Status.Org)))
	}
	if r.Status.Project != nil && failed(*r.Status.Project) {
		return fmt.Errorf("\nError: %s", maybeAppendProfileHint(msg(*r.Status.Project)))
	}
	return nil
}

// maybeAppendProfileHint appends a hint pointing the user to 'harness auth profiles'
// when the failure looks like a rejected/invalid token (401), so they have an
// actionable next step instead of a dead-end error.
func maybeAppendProfileHint(msg string) string {
	if strings.Contains(msg, "401") {
		return msg + "\n\nTip: run 'harness auth profiles' to see available profiles, then retry with --profile <name>"
	}
	return msg
}

func currentUserFields(u any) (email, uuid string) {
	m, ok := u.(map[string]any)
	if !ok {
		return
	}
	data, ok := m["data"].(map[string]any)
	if !ok {
		return
	}
	email, _ = data["email"].(string)
	uuid, _ = data["uuid"].(string)
	return
}

// checkAPIUrl validates the API URL's format and reachability. Reachability is the
// authoritative gate: an unreachable host is a hard failure that cascades. A malformed
// but reachable URL (e.g. missing scheme, from a hand-edited HARNESS_API_URL) is a soft
// warning so downstream checks still run — unless overridden is set (an explicit
// HARNESS_SSO_BASE_URL override), in which case the deliberate value passes cleanly.
func checkAPIUrl(apiURL string, overridden bool) checkResult {
	formatErr := auth.ValidateAPIURL(apiURL)

	u, err := url.Parse(apiURL)
	if err != nil || u.Hostname() == "" {
		return checkResult{OK: false, Error: fmt.Sprintf("%q is not a valid URL — expected an https:// URL with a host", apiURL)}
	}
	if _, err := net.DialTimeout("tcp", u.Hostname()+":443", 5*time.Second); err != nil {
		if _, ok := errors.AsType[*net.DNSError](err); ok {
			return checkResult{OK: false, Error: fmt.Sprintf("cannot resolve host %q — check your API URL", u.Hostname())}
		}
		return checkResult{OK: false, Error: fmt.Sprintf("cannot reach %q — %s", u.Hostname(), err)}
	}

	if formatErr != nil && !overridden {
		return checkResult{Warn: true, Error: "malformed API URL"}
	}
	return checkResult{OK: true}
}

// validateSATToken calls POST /ng/api/token/validate and returns a display identity
// string of the form "username (email)" parsed from the response, plus the validTo epoch ms.
func validateSATToken(a *auth.ResolvedAuth) (identity string, validTo int64, err error) {
	ctx, cancel := context.WithTimeout(context.Background(), apiTimeout)
	defer cancel()
	result, _, err := hclient.NewWithAuth(ctx, a).PostRaw("/ng/api/token/validate", nil, a.PATToken, "text/plain")
	if err != nil {
		if strings.Contains(err.Error(), "401") {
			return "", 0, fmt.Errorf("token rejected (401)")
		}
		if strings.Contains(err.Error(), "403") {
			return "", 0, fmt.Errorf("access denied (403)")
		}
		return "", 0, err
	}
	u := jsonAnyAt(result, "data", "username")
	e := jsonAnyAt(result, "data", "email")
	validTo = jsonInt64At(result, "data", "validTo")
	if u != "" && e != "" {
		return fmt.Sprintf("%s (%s)", u, e), validTo, nil
	}
	return u, validTo, nil
}

// fetchTokenValidTo calls POST /ng/api/token/validate and returns the validTo epoch ms.
// Returns an error if the token is rejected; 0 validTo means no expiry.
func fetchTokenValidTo(a *auth.ResolvedAuth) (int64, error) {
	ctx, cancel := context.WithTimeout(context.Background(), apiTimeout)
	defer cancel()
	result, _, err := hclient.NewWithAuth(ctx, a).PostRaw("/ng/api/token/validate", nil, a.PATToken, "text/plain")
	if err != nil {
		if strings.Contains(err.Error(), "401") {
			return 0, fmt.Errorf("token rejected (401)")
		}
		if strings.Contains(err.Error(), "403") {
			return 0, fmt.Errorf("access denied (403)")
		}
		return 0, err
	}
	return jsonInt64At(result, "data", "validTo"), nil
}

// tokenIdentity is the principal resolved for a token during login/status — either a
// Harness USER (PAT/SSO), identified by user uuid, or a SERVICE_ACCOUNT (SAT), which
// has no uuid and is identified by its own identifier instead.
type tokenIdentity struct {
	Email            string
	UserType         string // config.UserTypeUser or config.UserTypeServiceAccount
	UserID           string // set when UserType == config.UserTypeUser
	ServiceAccountID string // set when UserType == config.UserTypeServiceAccount
}

// fetchTokenIdentity resolves the identity behind a PAT or SAT token.
// SAT: calls token/validate — service accounts have no uuid, so ServiceAccountID
// comes from parentIdentifier (the SA's own identifier) instead.
// PAT: calls currentUser to get the real Harness user uuid.
// Returns a zero-value tokenIdentity on any error — callers treat this as best-effort.
func fetchTokenIdentity(apiURL, token, accountID string) tokenIdentity {
	ctx, cancel := context.WithTimeout(context.Background(), apiTimeout)
	defer cancel()
	a := &auth.ResolvedAuth{APIUrl: apiURL, AccountID: accountID, PATToken: token}
	cl := hclient.NewWithAuth(ctx, a)
	if auth.TokenType(token) == auth.TokenKindSAT {
		result, _, err := cl.PostRaw("/ng/api/token/validate", nil, token, "text/plain")
		if err != nil {
			hlog.Debug("fetching token identity for SAT failed", "error", err)
			return tokenIdentity{}
		}
		return tokenIdentity{
			Email:            jsonAnyAt(result, "data", "email"),
			UserType:         config.UserTypeServiceAccount,
			ServiceAccountID: jsonAnyAt(result, "data", "parentIdentifier"),
		}
	}
	result, _, err := cl.Get("/ng/api/user/currentUser", nil)
	if err != nil {
		hlog.Debug("fetching token identity for PAT failed", "error", err)
		return tokenIdentity{}
	}
	email, uuid := currentUserFields(result)
	return tokenIdentity{Email: email, UserType: config.UserTypeUser, UserID: uuid}
}

func fetchCurrentUser(a *auth.ResolvedAuth) (any, error) {
	ctx, cancel := context.WithTimeout(context.Background(), apiTimeout)
	defer cancel()
	result, _, err := hclient.NewWithAuth(ctx, a).Get("/ng/api/user/currentUser", nil)
	return result, err
}

func checkAccount(a *auth.ResolvedAuth) (string, error) {
	return checkResource(a, "/ng/api/accounts/"+a.AccountID, nil, "account", a.AccountID, "access denied (403) — check account ID or RBAC permissions", "data", "name")
}

func checkOrg(a *auth.ResolvedAuth) (string, error) {
	return checkResource(a, "/ng/api/organizations/"+a.OrgID, nil, "org", a.OrgID, "access denied (403)", "data", "organization", "name")
}

func checkProject(a *auth.ResolvedAuth) (string, error) {
	return checkResource(a, "/ng/api/projects/"+a.ProjectID, map[string]string{"orgIdentifier": a.OrgID}, "project", a.ProjectID, "access denied (403)", "data", "project", "name")
}

func checkResource(a *auth.ResolvedAuth, path string, params map[string]string, entityType, entityID, forbidden string, jsonPath ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), apiTimeout)
	defer cancel()
	result, _, err := hclient.NewWithAuth(ctx, a).Get(path, params)
	if err != nil {
		if strings.Contains(err.Error(), "403") {
			return "", fmt.Errorf("%s", forbidden)
		}
		if strings.Contains(err.Error(), "404") {
			return "", fmt.Errorf("%s %q not found (404)", entityType, entityID)
		}
		return "", err
	}
	name := jsonAnyAt(result, jsonPath...)
	if name == "" {
		name = entityID
	}
	return name, nil
}

func formatTokenValidTo(validTo int64) string {
	if validTo == 0 {
		return "no expiry"
	}
	exp := time.UnixMilli(validTo).Local()
	remaining := time.Until(exp)
	date := exp.Format("Jan 2, 2006 15:04")
	if remaining <= 0 {
		return fmt.Sprintf("%s %s (expired %s ago)", console.RedX(), date, roughDuration(-remaining))
	}
	return fmt.Sprintf("%s %s (%s)", console.GreenCheck(), date, roughDuration(remaining))
}

// roughDuration formats a duration as "Xd Yh", dropping smaller units.
func roughDuration(d time.Duration) string {
	d = d.Round(time.Hour)
	days := int(d.Hours()) / 24
	hours := int(d.Hours()) % 24
	if days > 0 {
		return fmt.Sprintf("%dd %dh", days, hours)
	}
	return fmt.Sprintf("%dh", hours)
}

func jsonAnyAt(v any, keys ...string) string {
	for _, k := range keys {
		m, ok := v.(map[string]any)
		if !ok {
			return ""
		}
		v = m[k]
	}
	s, _ := v.(string)
	return s
}

func jsonInt64At(v any, keys ...string) int64 {
	for _, k := range keys {
		m, ok := v.(map[string]any)
		if !ok {
			return 0
		}
		v = m[k]
	}
	switch n := v.(type) {
	case float64:
		return int64(n)
	case int64:
		return n
	}
	return 0
}
