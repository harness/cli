// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package har

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/harness/cli/modules/har/pkg/har/genapi/ar_v3"

	"github.com/harness/cli/v3/pkg/auth"
	"github.com/harness/cli/v3/pkg/cmdctx"
)

// pkgmgrExecute is the shared 4-phase flow for all package manager wrappers:
//  1. Detect HAR registry from config files written by `configure registry`
//  2. Resolve registry UUID via HAR API
//  3. Run the native command; stream output live
//  4. On 403: resolve deps → bulk firewall evaluation → display results → upload build info
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
	registryUUIDParsed, err := uuid.Parse(registryUUID)
	if err != nil {
		return fmt.Errorf("parsing registry UUID %q: %w", registryUUID, err)
	}

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

	artifacts := make([]ar_v3.ArtifactScanInput, 0, len(deps))
	for _, d := range deps {
		artifacts = append(artifacts, ar_v3.ArtifactScanInput{PackageName: d.Name, Version: d.Version})
	}

	fwClient, err := newFirewallClient(hc, &scopedAuth)
	if err != nil {
		return fmt.Errorf("building firewall client: %w", err)
	}

	fmt.Fprintf(os.Stderr, "Fetching firewall evaluation...\n")
	counts, evalErr := pkgmgrBulkEvalAndDisplay(ctx, fwClient, &scopedAuth, registryUUIDParsed, artifacts)
	if evalErr != nil {
		fmt.Fprintf(os.Stderr, "Firewall evaluation failed: %v\n", evalErr)
	}
	if counts.total() > 0 {
		uploader, accountID := pkgmgrBuildInfoClient(hc, &scopedAuth, fwClient)
		pkgmgrUploadBuildInfo(ctx, uploader, accountID, client.PackageType(), registryUUIDParsed, deps)
	}

	return pkgmgrFailure(client.Name(), subcommand, counts)
}

func pkgmgrFailure(clientName, subcommand string, counts scanStatusCounts) error {
	if counts.Blocked > 0 {
		return fmt.Errorf("%s %s failed: %d package(s) blocked by firewall", clientName, subcommand, counts.Blocked)
	}
	return fmt.Errorf("%s %s failed", clientName, subcommand)
}

// Variables so tests can exercise retries without sleeping.
var (
	pkgmgrMaxRetries    = 3
	pkgmgrRetryInterval = 30 * time.Second
)

const pkgmgrBatchSize = 50

type scanStatusCounts struct {
	Allowed int
	Warn    int
	Blocked int
	Unknown int
}

func (c scanStatusCounts) total() int {
	return c.Allowed + c.Warn + c.Blocked + c.Unknown
}

// pkgmgrBulkEvalAndDisplay evaluates artifacts in batches, prints each BLOCKED
// package with its policy details, and ends with a status summary. When a later
// batch fails, the batches already evaluated are still displayed and counted.
func pkgmgrBulkEvalAndDisplay(ctx context.Context, client *ar_v3.ClientWithResponses, a *auth.ResolvedAuth, registryUUID uuid.UUID, artifacts []ar_v3.ArtifactScanInput) (scanStatusCounts, error) {
	scans, err := pkgmgrEvaluateBatches(artifacts, pkgmgrBatchSize, func(batch []ar_v3.ArtifactScanInput, batchNum int) ([]ar_v3.BulkScanResultItem, error) {
		return pkgmgrRunBatch(ctx, client, a, registryUUID, batch, batchNum)
	})
	if err != nil && len(scans) == 0 {
		return scanStatusCounts{}, err
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v; showing %d package(s) evaluated so far\n", err, len(scans))
	}

	fmt.Println()
	fmt.Println(strings.Repeat("=", 60))
	fmt.Printf("FIREWALL EVALUATION: %d package(s) evaluated\n", len(scans))
	fmt.Println(strings.Repeat("=", 60))
	for _, s := range scans {
		if s.ScanStatus == nil || *s.ScanStatus != ar_v3.BulkScanResultItemScanStatusBLOCKED {
			continue
		}
		fmt.Printf("\nBLOCKED  %s@%s\n", derefStr(s.PackageName), derefStr(s.Version))
		if s.ScanId == nil {
			continue
		}
		detailResp, detailErr := client.GetArtifactScanDetailsWithResponse(ctx, s.ScanId.String(), &ar_v3.GetArtifactScanDetailsParams{
			AccountIdentifier: a.AccountID,
		})
		if detailErr == nil && detailResp.JSON200 != nil && detailResp.JSON200.Data != nil {
			printScanDetails(detailResp.JSON200.Data)
		}
	}

	counts := countScanStatuses(scans)
	writeFirewallSummary(os.Stdout, counts, len(artifacts), err != nil)
	return counts, err
}

// pkgmgrEvaluateBatches stops at the first failing batch and returns the scans
// gathered from the batches before it alongside the error.
func pkgmgrEvaluateBatches(
	artifacts []ar_v3.ArtifactScanInput,
	batchSize int,
	run func(batch []ar_v3.ArtifactScanInput, batchNum int) ([]ar_v3.BulkScanResultItem, error),
) ([]ar_v3.BulkScanResultItem, error) {
	totalBatches := (len(artifacts) + batchSize - 1) / batchSize
	var allScans []ar_v3.BulkScanResultItem
	for i := 0; i < totalBatches; i++ {
		start := i * batchSize
		end := min(start+batchSize, len(artifacts))
		batch := artifacts[start:end]
		if totalBatches > 1 {
			fmt.Fprintf(os.Stderr, "Evaluating batch %d/%d (%d packages)...\n", i+1, totalBatches, len(batch))
		}
		scans, err := run(batch, i+1)
		if err != nil {
			return allScans, err
		}
		allScans = append(allScans, scans...)
	}
	return allScans, nil
}

func derefStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func countScanStatuses(scans []ar_v3.BulkScanResultItem) scanStatusCounts {
	var counts scanStatusCounts
	for _, s := range scans {
		if s.ScanStatus == nil {
			counts.Unknown++
			continue
		}
		switch *s.ScanStatus {
		case ar_v3.BulkScanResultItemScanStatusBLOCKED:
			counts.Blocked++
		case ar_v3.BulkScanResultItemScanStatusWARN:
			counts.Warn++
		case ar_v3.BulkScanResultItemScanStatusALLOWED:
			counts.Allowed++
		default:
			counts.Unknown++
		}
	}
	return counts
}

// writeFirewallSummary: resolved is the number of dependencies submitted, which
// exceeds the evaluated total when a batch failed.
func writeFirewallSummary(w io.Writer, counts scanStatusCounts, resolved int, partial bool) {
	fmt.Fprintln(w)
	fmt.Fprintln(w, strings.Repeat("=", 60))
	fmt.Fprintln(w, "FIREWALL EVALUATION SUMMARY")
	if evaluated := counts.total(); resolved > 0 && evaluated != resolved {
		fmt.Fprintf(w, "  Evaluated: %d of %d resolved dependencies\n", evaluated, resolved)
	} else {
		fmt.Fprintf(w, "  Evaluated: %d\n", evaluated)
	}
	if partial {
		fmt.Fprintln(w, "  (incomplete — some batches failed)")
	}
	fmt.Fprintf(w, "  Allowed: %d\n", counts.Allowed)
	fmt.Fprintf(w, "  Warn: %d\n", counts.Warn)
	fmt.Fprintf(w, "  Blocked: %d\n", counts.Blocked)
	if counts.Unknown > 0 {
		fmt.Fprintf(w, "  Unknown: %d\n", counts.Unknown)
	}
	fmt.Fprintln(w, strings.Repeat("=", 60))
}

// pkgmgrRunBatch initiates a single bulk eval batch and polls to completion with retry/backoff.
func pkgmgrRunBatch(ctx context.Context, client *ar_v3.ClientWithResponses, a *auth.ResolvedAuth, registryUUID uuid.UUID, batch []ar_v3.ArtifactScanInput, batchNum int) ([]ar_v3.BulkScanResultItem, error) {
	var lastErr error
	for attempt := 1; attempt <= pkgmgrMaxRetries; attempt++ {
		scans, err := pkgmgrInitiateAndPoll(ctx, client, a, registryUUID, batch, batchNum)
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
func pkgmgrInitiateAndPoll(ctx context.Context, client *ar_v3.ClientWithResponses, a *auth.ResolvedAuth, registryUUID uuid.UUID, batch []ar_v3.ArtifactScanInput, batchNum int) ([]ar_v3.BulkScanResultItem, error) {
	initResp, err := client.InitiateBulkScanEvaluationWithResponse(ctx, &ar_v3.InitiateBulkScanEvaluationParams{
		AccountIdentifier: a.AccountID,
		OrgIdentifier:     optionalStrPtr(a.OrgID),
		ProjectIdentifier: optionalStrPtr(a.ProjectID),
	}, ar_v3.InitiateBulkScanEvaluationJSONRequestBody{
		RegistryId: registryUUID,
		Artifacts:  batch,
	})
	if err != nil {
		return nil, fmt.Errorf("initiating evaluation: %w", err)
	}
	if initResp.JSON202 == nil || initResp.JSON202.Data == nil || initResp.JSON202.Data.EvaluationId == nil {
		return nil, fmt.Errorf("missing evaluationId in response")
	}
	evaluationID := *initResp.JSON202.Data.EvaluationId

	pollRetries := 0
	for poll := 0; poll < 120; poll++ {
		statusResp, err := client.GetBulkScanEvaluationStatusWithResponse(ctx, evaluationID, &ar_v3.GetBulkScanEvaluationStatusParams{
			AccountIdentifier: a.AccountID,
			OrgIdentifier:     optionalStrPtr(a.OrgID),
			ProjectIdentifier: optionalStrPtr(a.ProjectID),
		})
		if err != nil {
			pollRetries++
			if pollRetries <= pkgmgrMaxRetries {
				fmt.Fprintf(os.Stderr, "batch %d: poll failed (attempt %d/%d), retrying in %ds: %v\n",
					batchNum, pollRetries, pkgmgrMaxRetries, int(pkgmgrRetryInterval.Seconds()), err)
				time.Sleep(pkgmgrRetryInterval)
				continue
			}
			return nil, fmt.Errorf("polling status failed after retries: %w", err)
		}
		if statusResp.JSON200 == nil || statusResp.JSON200.Data == nil || statusResp.JSON200.Data.Status == nil {
			pollRetries++
			if pollRetries <= pkgmgrMaxRetries {
				time.Sleep(pkgmgrRetryInterval)
				continue
			}
			return nil, fmt.Errorf("invalid status response after retries")
		}
		pollRetries = 0
		switch *statusResp.JSON200.Data.Status {
		case ar_v3.SUCCESS:
			if statusResp.JSON200.Data.Scans != nil {
				return *statusResp.JSON200.Data.Scans, nil
			}
			return nil, nil
		case ar_v3.FAILURE:
			msg := "evaluation failed"
			if statusResp.JSON200.Data.Error != nil {
				msg = *statusResp.JSON200.Data.Error
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
