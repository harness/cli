// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package ci

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/harness/cli/v3/pkg/client"
	"github.com/harness/cli/v3/pkg/cmdctx"
	"github.com/harness/cli/v3/pkg/hlog"
)

const (
	coverageDefaultWaitTimeout  = 5 * time.Minute
	coverageDefaultWaitInterval = 10 * time.Second
)

// coverageWaitUntilFlags holds the raw --flag values for `execute coverage:wait-until`.
type coverageWaitUntilFlags struct {
	accountID   string
	executionID string
	gt          float64
	gte         float64
	patchGt     float64
	patchGte    float64
	timeout     string
	interval    string
}

type coveragePatchSummary struct {
	Lines    int64   `json:"lines"`
	Hits     int64   `json:"hits"`
	Coverage float64 `json:"coverage"`
}

type coverageSummary struct {
	Lines       int64                 `json:"lines"`
	Hits        int64                 `json:"hits"`
	Coverage    float64               `json:"coverage"`
	ReportCount int                   `json:"report_count"`
	Patch       *coveragePatchSummary `json:"patch"`
}

type coverageStatusResponse struct {
	State   string           `json:"state"`
	Summary *coverageSummary `json:"summary"`
}

type coverageTreeResponse struct {
	Summary *coverageSummary `json:"summary"`
}

// coverageHTTPError carries the status code of a non-2xx coverage-service
// response, so the polling loop can tell a 404 (fall back to the tree
// endpoint) from any other 4xx (fail fast, do not keep polling).
type coverageHTTPError struct {
	StatusCode int
	Body       string
}

func (e *coverageHTTPError) Error() string {
	return fmt.Sprintf("HTTP %d: %s", e.StatusCode, e.Body)
}

func (e *coverageHTTPError) IsNotFound() bool {
	return e.StatusCode == http.StatusNotFound
}

func (e *coverageHTTPError) IsClientError() bool {
	return e.StatusCode >= 400 && e.StatusCode < 500
}

func coverageStatusPath(accountID, executionID string) string {
	return fmt.Sprintf(coverageAPIPrefix+"/accounts/%s/executions/%s/status", accountID, executionID)
}

func coverageTreePath(accountID, executionID string) string {
	return fmt.Sprintf(coverageAPIPrefix+"/accounts/%s/executions/%s/tree/", accountID, executionID)
}

// coverageWaitUntil polls the coverage service until all reports for the
// execution are processed, then evaluates the result against the given
// thresholds. The legacy TI-service dual-write fallback is deliberately not
// ported (see plan Decision 5): only the primary coverage-service path runs.
func coverageWaitUntil(ctx *cmdctx.Ctx, f coverageWaitUntilFlags) error {
	if f.gt == 0 && f.gte == 0 && f.patchGt == 0 && f.patchGte == 0 {
		return fmt.Errorf("at least one threshold flag must be specified (--gt, --gte, --patch-gt, --patch-gte)")
	}

	vendor, err := coverageInfo()
	if err != nil {
		return fmt.Errorf("failed to get CI vendor info: %v", err)
	}

	accountID := f.accountID
	if accountID == "" {
		accountID, err = vendor.AccountID()
		if err != nil {
			return fmt.Errorf("failed to get account ID: %w", err)
		}
	}

	executionID := f.executionID
	if executionID == "" {
		executionID = vendor.Execution()
		if executionID == "" {
			return fmt.Errorf("failed to get execution ID")
		}
	}

	timeout, err := coverageParseWaitDuration(f.timeout, coverageDefaultWaitTimeout, "--wait-timeout")
	if err != nil {
		return err
	}
	interval, err := coverageParseWaitDuration(f.interval, coverageDefaultWaitInterval, "--interval")
	if err != nil {
		return err
	}

	waitCtx := *ctx
	timeoutContext, cancel := context.WithTimeout(ctx.Context, timeout)
	defer cancel()
	waitCtx.Context = timeoutContext

	hlog.Info("waiting for coverage reports", "timeout", timeout.String())

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	var lastSummary *coverageSummary
	for {
		select {
		case <-timeoutContext.Done():
			if lastSummary == nil {
				return fmt.Errorf("timeout: no coverage data received within %s", timeout)
			}
			return coverageEvaluateThresholds(f, lastSummary)
		case <-ticker.C:
			summary, complete, err := coverageGetSummary(&waitCtx, f, accountID, executionID)
			if err != nil {
				var httpErr *coverageHTTPError
				if errors.As(err, &httpErr) && httpErr.IsClientError() {
					return err
				}
				hlog.Warn("failed to get coverage, retrying", "err", err)
				continue
			}

			if summary != nil {
				lastSummary = summary
				hlog.Debug("coverage status", "coverage", summary.Coverage, "reports", summary.ReportCount)
			} else {
				hlog.Debug("waiting for coverage data")
			}

			if complete {
				hlog.Info("coverage processing complete")
				if lastSummary == nil {
					return fmt.Errorf("state is complete but no coverage data available")
				}
				return coverageEvaluateThresholds(f, lastSummary)
			}
		}
	}
}

func coverageParseWaitDuration(raw string, def time.Duration, flagName string) (time.Duration, error) {
	if raw == "" {
		return def, nil
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("%s %q is not a duration such as 10s or 5m", flagName, raw)
	}
	if d <= 0 {
		return 0, fmt.Errorf("%s must be greater than zero, got %s", flagName, d)
	}
	return d, nil
}

// coverageGetSummary fetches the latest coverage summary and whether
// processing is complete. The status endpoint is tried first; a 404 falls
// back to the tree endpoint (older executions / before status existed), and
// so does a status response with no patch data when a patch threshold is in
// use (the tree endpoint computes patch coverage, status does not always).
func coverageGetSummary(
	ctx *cmdctx.Ctx, f coverageWaitUntilFlags, accountID, executionID string,
) (*coverageSummary, bool, error) {
	statusResp, err := coverageFetchStatus(ctx, accountID, executionID)
	if err == nil {
		if statusResp == nil {
			return nil, false, nil
		}
		complete := statusResp.State == "complete"
		summary := statusResp.Summary
		patchRequired := f.patchGt > 0 || f.patchGte > 0
		if patchRequired && summary != nil && summary.Patch == nil {
			hlog.Debug("status endpoint returned no patch data, falling back to tree endpoint")
			treeSummary, treeErr := coverageFetchTree(ctx, accountID, executionID)
			if treeErr != nil {
				return nil, complete, fmt.Errorf("tree endpoint failed: %w", treeErr)
			}
			return treeSummary, complete, nil
		}
		return summary, complete, nil
	}

	var httpErr *coverageHTTPError
	if errors.As(err, &httpErr) && httpErr.IsNotFound() {
		hlog.Debug("status endpoint not available, falling back to tree endpoint")
		summary, treeErr := coverageFetchTree(ctx, accountID, executionID)
		if treeErr != nil {
			return nil, false, fmt.Errorf("tree endpoint failed: %w", treeErr)
		}
		return summary, false, nil
	}
	return nil, false, fmt.Errorf("status endpoint failed: %w", err)
}

func coverageFetchStatus(ctx *cmdctx.Ctx, accountID, executionID string) (*coverageStatusResponse, error) {
	body, err := coverageDoWaitRequest(ctx, accountID, coverageStatusPath(accountID, executionID))
	if err != nil {
		return nil, err
	}
	var status coverageStatusResponse
	if err := json.Unmarshal(body, &status); err != nil {
		return nil, fmt.Errorf("decoding coverage status response: %w", err)
	}
	return &status, nil
}

func coverageFetchTree(ctx *cmdctx.Ctx, accountID, executionID string) (*coverageSummary, error) {
	body, err := coverageDoWaitRequest(ctx, accountID, coverageTreePath(accountID, executionID))
	if err != nil {
		return nil, err
	}
	var tree coverageTreeResponse
	if err := json.Unmarshal(body, &tree); err != nil {
		return nil, fmt.Errorf("decoding coverage tree response: %w", err)
	}
	return tree.Summary, nil
}

// coverageDoWaitRequest is shared by the status and tree GETs: both need the
// raw status code (via DoRaw) to distinguish a 404 from other errors, which
// client.Client's DoRequest does not expose.
func coverageDoWaitRequest(ctx *cmdctx.Ctx, accountID, path string) ([]byte, error) {
	resp, err := client.New(ctx).DoRaw(client.Request{
		Method: "GET",
		Path:   path,
		QueryParams: map[string]string{
			"routingId": accountID,
			"accountId": accountID,
		},
		NoAccountID: true,
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading coverage response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, &coverageHTTPError{StatusCode: resp.StatusCode, Body: string(body)}
	}
	return body, nil
}

func coverageEvaluateThresholds(f coverageWaitUntilFlags, summary *coverageSummary) error {
	if summary == nil {
		return fmt.Errorf("no coverage data available")
	}

	var failures []string

	if f.gt > 0 && summary.Coverage <= f.gt {
		failures = append(failures, fmt.Sprintf("coverage %.2f%% is not > %.2f%%", summary.Coverage, f.gt))
	}
	if f.gte > 0 && summary.Coverage < f.gte {
		failures = append(failures, fmt.Sprintf("coverage %.2f%% is not >= %.2f%%", summary.Coverage, f.gte))
	}

	if f.patchGt > 0 || f.patchGte > 0 {
		if summary.Patch == nil {
			hlog.Info("no patch coverage data available, skipping patch threshold check")
		} else {
			if f.patchGt > 0 && summary.Patch.Coverage <= f.patchGt {
				failures = append(failures, fmt.Sprintf(
					"patch coverage %.2f%% is not > %.2f%%", summary.Patch.Coverage, f.patchGt))
			}
			if f.patchGte > 0 && summary.Patch.Coverage < f.patchGte {
				failures = append(failures, fmt.Sprintf(
					"patch coverage %.2f%% is not >= %.2f%%", summary.Patch.Coverage, f.patchGte))
			}
		}
	}

	hlog.Info("coverage", "percent", fmt.Sprintf("%.2f%%", summary.Coverage), "lines", fmt.Sprintf("%d/%d", summary.Hits, summary.Lines))
	if summary.Patch != nil {
		hlog.Info("patch coverage", "percent", fmt.Sprintf("%.2f%%", summary.Patch.Coverage), "lines", fmt.Sprintf("%d/%d", summary.Patch.Hits, summary.Patch.Lines))
	}

	if len(failures) > 0 {
		hlog.Error("quality gate failed")
		for _, failure := range failures {
			hlog.Error("  - " + failure)
		}
		return fmt.Errorf("quality gate failed: %d threshold(s) not met", len(failures))
	}

	hlog.Info("quality gate passed")
	return nil
}
