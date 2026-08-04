// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package har

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/harness/cli/modules/har/pkg/har/migrate/util"
	"github.com/harness/cli/pkg/cmdctx"
	"github.com/pterm/pterm"
)

const bulkDeleteArtifactHandlerID = "bulk_delete_artifact"

// Gap 7 fix: Force and DryRun are *bool with omitempty to match generated legacy client struct.
type bulkDeleteRequest struct {
	DryRun   *bool  `json:"dryRun,omitempty"`
	Force    *bool  `json:"force,omitempty"`
	Packages string `json:"packages"`
	Registry string `json:"registry"`
	Versions string `json:"versions"`
}

type bulkDeleteResponse struct {
	DryRun          bool     `json:"dryRun"`
	Failed          int      `json:"failed"`
	FailedPackages  []string `json:"failedPackages"`
	Force           bool     `json:"force"`
	Message         string   `json:"message"`
	Pattern         string   `json:"pattern"`
	Registry        string   `json:"registry"`
	Success         int      `json:"success"`
	SuccessPackages []string `json:"successPackages"`
	Total           int      `json:"total"`
	VersionPattern  string   `json:"versionPattern"`
}

func bulkDeleteArtifactHandler(ctx *cmdctx.Ctx) error {
	if len(ctx.Args) == 0 {
		return fmt.Errorf("artifact pattern is required (e.g. 'express*' or 'myapp')")
	}
	pattern := ctx.Args[0]
	registry := cmdctx.GetString(ctx.FlagValues, "registry")
	// Gap 1 fix: version defaults to "" (empty), not "*".
	version := cmdctx.GetString(ctx.FlagValues, "version")
	// dry-run defaults to true unless explicitly set to "false".
	dryRun := cmdctx.GetString(ctx.FlagValues, "dry-run") != "false"
	force := cmdctx.GetBool(ctx.FlagValues, "force")

	// Gap 2 fix: determine impactType from input flag, not from response.
	impactType := "Packages"
	if version != "" {
		impactType = "Versions"
	}

	// Validate artifact pattern.
	if _, err := util.IsWildCardExpression(pattern); err != nil {
		return fmt.Errorf("invalid package expression: %w", err)
	}

	// Gap 3 fix: only validate version if flag was explicitly set.
	if version != "" {
		if _, err := util.IsWildCardExpression(version); err != nil {
			return fmt.Errorf("invalid version expression: %w", err)
		}
	}

	// Gap 4 fix: force prompt includes warning + tip lines before the confirmation.
	if force {
		if err := confirmForceDelete(); err != nil {
			return err
		}
	}

	a := ctx.Auth
	apiURL := buildBulkDeleteURL(a.APIUrl, a.AccountID, a.OrgID, a.ProjectID)

	body, err := callBulkDelete(apiURL, ctx, newBulkDeleteRequest(pattern, version, registry, dryRun, force))
	if err != nil {
		return fmt.Errorf("bulk delete failed: %w", err)
	}

	var parsed bulkDeleteResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return fmt.Errorf("failed to parse bulk delete response: %w", err)
	}

	printDryRunSummary(parsed, impactType)

	// Gap 6 fix: if nothing to delete, exit early without second prompt.
	if len(parsed.SuccessPackages) == 0 {
		pterm.Info.Println("No package/Version found to be deleted matching given pattern")
		return nil
	}

	// Two-phase: if the first call was a dry-run, prompt then execute for real.
	if parsed.DryRun {
		prompt := fmt.Sprintf("Above %s will be soft deleted. Do you want to proceed? (y/N): ", impactType)
		if err := confirmPrompt(prompt); err != nil {
			return err
		}

		body, err = callBulkDelete(apiURL, ctx, newBulkDeleteRequest(pattern, version, registry, false, force))
		if err != nil {
			return fmt.Errorf("bulk delete execution failed: %w", err)
		}

		var final bulkDeleteResponse
		if err := json.Unmarshal(body, &final); err != nil {
			return fmt.Errorf("failed to parse actual bulk delete response: %w", err)
		}
		printRealRunSummary(final, impactType)
	}

	return nil
}

// newBulkDeleteRequest builds the request body with pointer fields.
func newBulkDeleteRequest(packages, versions, registry string, dryRun, force bool) bulkDeleteRequest {
	return bulkDeleteRequest{
		DryRun:   &dryRun,
		Force:    &force,
		Packages: packages,
		Registry: registry,
		Versions: versions,
	}
}

func callBulkDelete(apiURL string, ctx *cmdctx.Ctx, reqBody bulkDeleteRequest) ([]byte, error) {
	data, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := newJSONPostRequest(apiURL, data)
	if err != nil {
		return nil, err
	}
	setAuthHeader(req, ctx.Auth)
	return doRequest(newHTTPClient(), req)
}

func buildBulkDeleteURL(apiURL, accountID, orgID, projectID string) string {
	base, err := url.Parse(harV3URL(apiURL, "/bulkdelete"))
	if err != nil {
		return harV3URL(apiURL, "/bulkdelete")
	}
	q := base.Query()
	q.Set("account_identifier", accountID)
	if orgID != "" {
		q.Set("org_identifier", orgID)
	}
	if projectID != "" {
		q.Set("project_identifier", projectID)
	}
	base.RawQuery = q.Encode()
	return base.String()
}

// Gap 5 fix: dry-run summary matches legacy table layout.
func printDryRunSummary(r bulkDeleteResponse, impactType string) {
	if r.Message != "" {
		pterm.Info.Println(r.Message)
	}
	fmt.Printf("Registry        : %s\n", r.Registry)
	fmt.Printf("Version pattern : %s\n", r.VersionPattern)
	fmt.Printf("Dry-run         : %t\n", r.DryRun)
	fmt.Printf("Force           : %t\n", r.Force)
	fmt.Printf("Total impacted  : %d (success: %d, failed: %d)\n", r.Total, r.Success, r.Failed)

	if len(r.SuccessPackages) > 0 {
		fmt.Println("\nImpacted package/Version")
		for _, p := range r.SuccessPackages {
			fmt.Println(p)
		}
		if extra := r.Success - len(r.SuccessPackages); extra > 0 {
			fmt.Printf("... and %d more %s, will be impacted (not listed above)\n", extra, impactType)
		}
	}

	for _, p := range r.FailedPackages {
		pterm.Error.Println(p)
	}
}

// Gap 5 fix: real-run summary matches legacy layout.
func printRealRunSummary(r bulkDeleteResponse, impactType string) {
	if r.Message != "" {
		pterm.Info.Println(r.Message)
	}
	fmt.Printf("Deleted        : %d / %d (failed: %d)\n", r.Success, r.Total, r.Failed)

	if len(r.SuccessPackages) > 0 {
		fmt.Println()
		for _, p := range r.SuccessPackages {
			fmt.Println(p)
		}
		if extra := r.Success - len(r.SuccessPackages); extra > 0 {
			fmt.Printf("... and %d more %s, is deleted\n", extra, impactType)
		}
	}

	for _, p := range r.FailedPackages {
		pterm.Error.Println(p)
	}
}

// Gap 4 fix: prints irreversible warning + tip before asking for confirmation.
func confirmForceDelete() error {
	pterm.Error.Println("Warning :: Force (hard) delete is enabled. This action is irreversible")
	pterm.Info.Println("Tip: run with --dry-run first to preview impacted packages/versions.")
	return confirmPrompt("Are you sure you want to proceed with force delete ? (y/N): ")
}

func confirmPrompt(prompt string) error {
	fmt.Fprint(os.Stderr, prompt)
	scanner := bufio.NewScanner(os.Stdin)
	if !scanner.Scan() {
		return fmt.Errorf("failed to read confirmation: %w", scanner.Err())
	}
	answer := strings.TrimSpace(scanner.Text())
	if answer != "y" && answer != "Y" {
		return fmt.Errorf("bulk delete cancelled by user")
	}
	return nil
}

func newJSONPostRequest(rawURL string, body []byte) (*http.Request, error) {
	req, err := http.NewRequest(http.MethodPost, rawURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	return req, nil
}
