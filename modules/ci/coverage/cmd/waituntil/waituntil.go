// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package waituntil

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	covclient "github.com/harness/cli/v3/modules/ci/coverage/client"
	"github.com/harness/cli/v3/modules/ci/coverage/env"
	"github.com/harness/cli/v3/pkg/client"
	"github.com/harness/cli/v3/pkg/cmdctx"
	"github.com/harness/cli/v3/pkg/hlog"
)

const (
	defaultWaitTimeout  = 5 * time.Minute
	defaultWaitInterval = 10 * time.Second
)

// waitUntilFlags holds the raw --flag values for `execute coverage:wait-until`.
type waitUntilFlags struct {
	accountID   string
	executionID string
	gt          float64
	gte         float64
	patchGt     float64
	patchGte    float64
	timeout     string
	interval    string
}

type patchSummary struct {
	Lines    int64   `json:"lines"`
	Hits     int64   `json:"hits"`
	Coverage float64 `json:"coverage"`
}

type reportSummary struct {
	Lines       int64         `json:"lines"`
	Hits        int64         `json:"hits"`
	Coverage    float64       `json:"coverage"`
	ReportCount int           `json:"report_count"`
	Patch       *patchSummary `json:"patch"`
}

type statusResponse struct {
	State   string         `json:"state"`
	Summary *reportSummary `json:"summary"`
}

type treeResponse struct {
	Summary *reportSummary `json:"summary"`
}

// hTTPError carries the status code of a non-2xx coverage-service
// response, so the polling loop can tell a 404 (fall back to the tree
// endpoint) from any other 4xx (fail fast, do not keep polling).
type hTTPError struct {
	StatusCode int
	Body       string
}

func (e *hTTPError) Error() string {
	return fmt.Sprintf("HTTP %d: %s", e.StatusCode, e.Body)
}

func (e *hTTPError) IsNotFound() bool {
	return e.StatusCode == http.StatusNotFound
}

func (e *hTTPError) IsClientError() bool {
	return e.StatusCode >= 400 && e.StatusCode < 500
}

func statusPath(accountID, executionID string) string {
	return fmt.Sprintf(covclient.APIPrefix+"/accounts/%s/executions/%s/status", accountID, executionID)
}

func treePath(accountID, executionID string) string {
	return fmt.Sprintf(covclient.APIPrefix+"/accounts/%s/executions/%s/tree/", accountID, executionID)
}

// waitUntil polls the coverage service until all reports for the
// execution are processed, then evaluates the result against the given
// thresholds. The legacy TI-service dual-write fallback is deliberately not
// ported (see plan Decision 5): only the primary coverage-service path runs.
func waitUntil(ctx *cmdctx.Ctx, f waitUntilFlags) error {
	if f.gt == 0 && f.gte == 0 && f.patchGt == 0 && f.patchGte == 0 {
		return fmt.Errorf("at least one threshold flag must be specified (--gt, --gte, --patch-gt, --patch-gte)")
	}

	vendor, err := env.Info()
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

	timeout, err := parseWaitDuration(f.timeout, defaultWaitTimeout, "--wait-timeout")
	if err != nil {
		return err
	}
	interval, err := parseWaitDuration(f.interval, defaultWaitInterval, "--interval")
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

	var lastSummary *reportSummary
	for {
		select {
		case <-timeoutContext.Done():
			if lastSummary == nil {
				return fmt.Errorf("timeout: no coverage data received within %s", timeout)
			}
			return evaluateThresholds(f, lastSummary)
		case <-ticker.C:
			summary, complete, err := getSummary(&waitCtx, f, accountID, executionID)
			if err != nil {
				var httpErr *hTTPError
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
				return evaluateThresholds(f, lastSummary)
			}
		}
	}
}

func parseWaitDuration(raw string, def time.Duration, flagName string) (time.Duration, error) {
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

// getSummary fetches the latest coverage summary and whether
// processing is complete. The status endpoint is tried first; a 404 falls
// back to the tree endpoint (older executions / before status existed), and
// so does a status response with no patch data when a patch threshold is in
// use (the tree endpoint computes patch coverage, status does not always).
func getSummary(
	ctx *cmdctx.Ctx, f waitUntilFlags, accountID, executionID string,
) (*reportSummary, bool, error) {
	statusResp, err := fetchStatus(ctx, accountID, executionID)
	if err == nil {
		if statusResp == nil {
			return nil, false, nil
		}
		complete := statusResp.State == "complete"
		summary := statusResp.Summary
		patchRequired := f.patchGt > 0 || f.patchGte > 0
		if patchRequired && summary != nil && summary.Patch == nil {
			hlog.Debug("status endpoint returned no patch data, falling back to tree endpoint")
			treeSummary, treeErr := fetchTree(ctx, accountID, executionID)
			if treeErr != nil {
				return nil, complete, fmt.Errorf("tree endpoint failed: %w", treeErr)
			}
			return treeSummary, complete, nil
		}
		return summary, complete, nil
	}

	var httpErr *hTTPError
	if errors.As(err, &httpErr) && httpErr.IsNotFound() {
		hlog.Debug("status endpoint not available, falling back to tree endpoint")
		summary, treeErr := fetchTree(ctx, accountID, executionID)
		if treeErr != nil {
			return nil, false, fmt.Errorf("tree endpoint failed: %w", treeErr)
		}
		return summary, false, nil
	}
	return nil, false, fmt.Errorf("status endpoint failed: %w", err)
}

func fetchStatus(ctx *cmdctx.Ctx, accountID, executionID string) (*statusResponse, error) {
	body, err := doWaitRequest(ctx, accountID, statusPath(accountID, executionID))
	if err != nil {
		return nil, err
	}
	var status statusResponse
	if err := json.Unmarshal(body, &status); err != nil {
		return nil, fmt.Errorf("decoding coverage status response: %w", err)
	}
	return &status, nil
}

func fetchTree(ctx *cmdctx.Ctx, accountID, executionID string) (*reportSummary, error) {
	body, err := doWaitRequest(ctx, accountID, treePath(accountID, executionID))
	if err != nil {
		return nil, err
	}
	var tree treeResponse
	if err := json.Unmarshal(body, &tree); err != nil {
		return nil, fmt.Errorf("decoding coverage tree response: %w", err)
	}
	return tree.Summary, nil
}

// doWaitRequest is shared by the status and tree GETs: both need the
// raw status code (via DoRaw) to distinguish a 404 from other errors, which
// client.Client's DoRequest does not expose.
func doWaitRequest(ctx *cmdctx.Ctx, accountID, path string) ([]byte, error) {
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
		return nil, &hTTPError{StatusCode: resp.StatusCode, Body: string(body)}
	}
	return body, nil
}

func evaluateThresholds(f waitUntilFlags, summary *reportSummary) error {
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
