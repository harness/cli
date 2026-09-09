// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package har

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/harness/cli/v3/pkg/auth"
	"github.com/harness/cli/v3/pkg/cmdctx"
)

// pkgmgrExecute is the shared 4-phase flow for all package manager wrappers:
//  1. Detect HAR registry from config files written by `configure registry`
//  2. Resolve registry UUID via HAR API
//  3. Run the native command; stream output live
//  4. On 403: resolve deps → bulk firewall evaluation → display results
func pkgmgrExecute(cmdCtx *cmdctx.Ctx, client pkgmgrClient, subcommand string, nativeArgs []string) error {
	a := cmdCtx.Auth
	explicitRegistry := cmdCtx.Id

	// Phase 1 — detect registry
	fmt.Fprintf(os.Stderr, "Detecting HAR registry...\n")
	regInfo, err := client.DetectRegistry(explicitRegistry)
	if err != nil {
		return fmt.Errorf("detecting HAR registry: %w", err)
	}
	fmt.Fprintf(os.Stderr, "Found HAR registry: %s\n", regInfo.RegistryIdentifier)

	// Phase 2 — resolve UUID; prefer org/project from configure-saved config
	// so that pipeline env vars (ORG_IDENTIFIER, PROJECT_IDENTIFIER) cannot
	// silently redirect an account-level registry lookup to the wrong scope.
	fmt.Fprintf(os.Stderr, "Resolving registry details...\n")
	ctx := context.Background()
	hc := newHTTPClient()
	scopedAuth := *a
	if regInfo.OrgID != "" {
		scopedAuth.OrgID = regInfo.OrgID
		scopedAuth.ProjectID = regInfo.ProjectID
	}
	registryUUID, err := getRegistryUUID(ctx, hc, &scopedAuth, regInfo.RegistryIdentifier)
	if err != nil {
		return fmt.Errorf("resolving registry UUID: %w", err)
	}
	fmt.Fprintf(os.Stderr, "Registry UUID: %s\n", registryUUID)

	// Phase 3 — run native command
	fmt.Fprintf(os.Stderr, "Running %s %s...\n", client.Name(), subcommand)
	result, err := client.RunCommand(subcommand, nativeArgs)
	if err != nil && result == nil {
		return fmt.Errorf("%s %s failed: %w", client.Name(), subcommand, err)
	}

	if result.Status == "SUCCESS" {
		return nil
	}

	// Phase 4 — 403 / firewall block detected?
	if !client.DetectFirewallError(result.Stderr) {
		return fmt.Errorf("%s %s failed", client.Name(), subcommand)
	}

	fmt.Fprintf(os.Stderr, "\n%s %s failed — firewall may have blocked packages\n\n", client.Name(), subcommand)

	fmt.Fprintf(os.Stderr, "Resolving complete dependency list...\n")
	deps, cleanup, err := client.ResolveDeps()
	if cleanup != nil {
		defer cleanup()
	}
	if err != nil {
		return fmt.Errorf("%s %s failed: dependency resolution: %w", client.Name(), subcommand, err)
	}
	if len(deps) == 0 {
		return fmt.Errorf("%s %s failed: no dependencies found to evaluate", client.Name(), subcommand)
	}
	fmt.Fprintf(os.Stderr, "Resolved %d dependencies (including transitive)\n", len(deps))

	pkgmgrSaveBuildInfo(client.Name(), subcommand, regInfo.RegistryIdentifier, deps)

	artifacts := make([]artifactScanInput, 0, len(deps))
	for _, d := range deps {
		artifacts = append(artifacts, artifactScanInput{PackageName: d.Name, Version: d.Version})
	}

	fmt.Fprintf(os.Stderr, "Fetching firewall evaluation...\n")
	if _, evalErr := pkgmgrBulkEvalAndDisplay(ctx, hc, &scopedAuth, registryUUID, artifacts); evalErr != nil {
		fmt.Fprintf(os.Stderr, "Firewall evaluation failed: %v\n", evalErr)
	}

	return fmt.Errorf("%s %s failed", client.Name(), subcommand)
}

const (
	pkgmgrMaxRetries     = 3
	pkgmgrRetryInterval  = 30 * time.Second
)

// pkgmgrBulkEvalAndDisplay batches artifacts into ≤50-item chunks, polls each
// evaluation to completion, and prints BLOCKED/WARN/ALLOWED results with scan details.
func pkgmgrBulkEvalAndDisplay(ctx context.Context, hc *http.Client, a *auth.ResolvedAuth, registryUUID string, artifacts []artifactScanInput) (int, error) {
	const batchSize = 50
	totalBatches := (len(artifacts) + batchSize - 1) / batchSize
	var allScans []bulkScanItem

	for i := 0; i < totalBatches; i++ {
		start := i * batchSize
		end := start + batchSize
		if end > len(artifacts) {
			end = len(artifacts)
		}
		batch := artifacts[start:end]

		if totalBatches > 1 {
			fmt.Fprintf(os.Stderr, "Evaluating batch %d/%d (%d packages)...\n", i+1, totalBatches, len(batch))
		}

		scans, err := pkgmgrRunBatch(ctx, hc, a, registryUUID, batch, i+1)
		if err != nil {
			return 0, err
		}
		allScans = append(allScans, scans...)
	}

	results := make([]auditScanResult, 0, len(allScans))
	for _, s := range allScans {
		r := auditScanResult{}
		if s.PackageName != nil {
			r.PackageName = *s.PackageName
		}
		if s.Version != nil {
			r.Version = *s.Version
		}
		if s.ScanId != nil {
			r.ScanID = *s.ScanId
		}
		if s.ScanStatus != nil {
			r.ScanStatus = *s.ScanStatus
		}
		results = append(results, r)
	}

	fmt.Printf("\nFirewall evaluation: %d package(s) evaluated\n", len(results))
	for _, r := range results {
		switch r.ScanStatus {
		case "BLOCKED":
			fmt.Printf("  BLOCKED  %s@%s\n", r.PackageName, r.Version)
		case "WARN":
			fmt.Printf("  WARN     %s@%s\n", r.PackageName, r.Version)
		case "ALLOWED":
			fmt.Printf("  ALLOWED  %s@%s\n", r.PackageName, r.Version)
			continue
		default:
			fmt.Printf("  %-7s  %s@%s\n", r.ScanStatus, r.PackageName, r.Version)
			continue
		}
		// Fetch and print policy-set violation details for BLOCKED/WARN.
		if r.ScanID != "" {
			detailURL, err := buildScanDetailsURL(a.APIUrl, r.ScanID, a.AccountID)
			if err != nil {
				continue
			}
			var detailResp scanDetailsResp
			if err := doHAR(ctx, hc, a, detailURL, "GET", nil, &detailResp); err == nil && detailResp.Data != nil {
				printScanDetails(detailResp.Data)
			}
		}
	}

	return len(results), nil
}

// pkgmgrRunBatch initiates a single bulk eval batch and polls to completion with retry/backoff.
func pkgmgrRunBatch(ctx context.Context, hc *http.Client, a *auth.ResolvedAuth, registryUUID string, batch []artifactScanInput, batchNum int) ([]bulkScanItem, error) {
	var lastErr error
	for attempt := 1; attempt <= pkgmgrMaxRetries; attempt++ {
		scans, err := pkgmgrInitiateAndPoll(ctx, hc, a, registryUUID, batch, batchNum)
		if err == nil {
			return scans, nil
		}
		lastErr = err
		if attempt < pkgmgrMaxRetries {
			fmt.Fprintf(os.Stderr, "batch %d: evaluation failed (attempt %d/%d), retrying in %ds: %v\n",
				batchNum, attempt, pkgmgrMaxRetries, int(pkgmgrRetryInterval.Seconds()), err)
			time.Sleep(pkgmgrRetryInterval)
		}
	}
	return nil, fmt.Errorf("batch %d: evaluation failed after %d attempts: %w", batchNum, pkgmgrMaxRetries, lastErr)
}

// pkgmgrInitiateAndPoll initiates a bulk eval and polls until SUCCESS or FAILURE.
func pkgmgrInitiateAndPoll(ctx context.Context, hc *http.Client, a *auth.ResolvedAuth, registryUUID string, batch []artifactScanInput, batchNum int) ([]bulkScanItem, error) {
	evalURL, err := buildEvalURL(a.APIUrl, a.AccountID, a.OrgID, a.ProjectID)
	if err != nil {
		return nil, fmt.Errorf("building evaluation URL: %w", err)
	}
	var initResp bulkEvalAcceptedResp
	if err := doHAR(ctx, hc, a, evalURL, "POST", bulkEvalRequest{
		RegistryId: registryUUID,
		Artifacts:  batch,
	}, &initResp); err != nil {
		return nil, fmt.Errorf("initiating evaluation: %w", err)
	}
	if initResp.Data == nil || initResp.Data.EvaluationId == nil {
		return nil, fmt.Errorf("missing evaluationId in response")
	}
	evaluationID := *initResp.Data.EvaluationId

	statusURL, err := buildEvalStatusURL(a.APIUrl, evaluationID, a.AccountID, a.OrgID, a.ProjectID)
	if err != nil {
		return nil, fmt.Errorf("building status URL: %w", err)
	}
	pollRetries := 0
	for poll := 0; poll < 120; poll++ {
		var statusResp bulkEvalStatusResp
		if err := doHAR(ctx, hc, a, statusURL, "GET", nil, &statusResp); err != nil {
			pollRetries++
			if pollRetries <= pkgmgrMaxRetries {
				fmt.Fprintf(os.Stderr, "batch %d: poll failed (attempt %d/%d), retrying in %ds: %v\n",
					batchNum, pollRetries, pkgmgrMaxRetries, int(pkgmgrRetryInterval.Seconds()), err)
				time.Sleep(pkgmgrRetryInterval)
				continue
			}
			return nil, fmt.Errorf("polling status failed after retries: %w", err)
		}
		if statusResp.Data == nil || statusResp.Data.Status == nil {
			pollRetries++
			if pollRetries <= pkgmgrMaxRetries {
				time.Sleep(pkgmgrRetryInterval)
				continue
			}
			return nil, fmt.Errorf("invalid status response after retries")
		}
		pollRetries = 0
		switch *statusResp.Data.Status {
		case "SUCCESS":
			if statusResp.Data.Scans != nil {
				return *statusResp.Data.Scans, nil
			}
			return nil, nil
		case "FAILURE":
			msg := "evaluation failed"
			if statusResp.Data.Error != nil {
				msg = *statusResp.Data.Error
			}
			return nil, fmt.Errorf("%s", msg)
		}
		time.Sleep(2 * time.Second)
	}
	return nil, fmt.Errorf("timeout waiting for evaluation")
}

// pkgmgrSaveBuildInfo writes the resolved dependency list to .harness/build-info.json.
func pkgmgrSaveBuildInfo(clientName, command, registry string, deps []dependency) {
	type entry struct {
		Client       string       `json:"client"`
		Command      string       `json:"command"`
		Registry     string       `json:"registry"`
		Timestamp    string       `json:"timestamp"`
		Dependencies []dependency `json:"dependencies"`
	}
	data, err := json.MarshalIndent(entry{
		Client:       clientName,
		Command:      command,
		Registry:     registry,
		Timestamp:    time.Now().UTC().Format(time.RFC3339),
		Dependencies: deps,
	}, "", "  ")
	if err != nil {
		return
	}
	_ = os.MkdirAll(".harness", 0755)
	_ = os.WriteFile(filepath.Join(".harness", "build-info.json"), data, 0644)
}

// pkgmgrParseArgs splits args at "--" and extracts an optional --registry value.
// Everything before "--" is parsed for --registry; everything after is passed through.
func pkgmgrParseArgs(args []string) (registry string, nativeArgs []string) {
	sepIdx := -1
	for i, a := range args {
		if a == "--" {
			sepIdx = i
			break
		}
	}

	var harArgs []string
	if sepIdx >= 0 {
		harArgs = args[:sepIdx]
		nativeArgs = args[sepIdx+1:]
	} else {
		harArgs = args
	}

	for i := 0; i < len(harArgs); i++ {
		switch {
		case harArgs[i] == "--registry" && i+1 < len(harArgs):
			registry = harArgs[i+1]
			i++
		case strings.HasPrefix(harArgs[i], "--registry="):
			registry = strings.TrimPrefix(harArgs[i], "--registry=")
		default:
			nativeArgs = append(nativeArgs, harArgs[i])
		}
	}

	return registry, nativeArgs
}
