// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package har

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/harness/cli/modules/har/pkg/har/genapi/ar_v3"
)

type fakeFirewallClient struct {
	syncResp   *ar_v3.BulkScanEvaluateSyncResp
	syncErr    error
	syncCalls  int
	syncBodies []ar_v3.BulkScanEvaluateSyncJSONRequestBody

	initResp  *ar_v3.InitiateBulkScanEvaluationResp
	initErr   error
	initCalls int

	statusResp  *ar_v3.GetBulkScanEvaluationStatusResp
	statusErr   error
	statusCalls int
}

func (f *fakeFirewallClient) BulkScanEvaluateSyncWithResponse(
	_ context.Context,
	_ *ar_v3.BulkScanEvaluateSyncParams,
	body ar_v3.BulkScanEvaluateSyncJSONRequestBody,
	_ ...ar_v3.RequestEditorFn,
) (*ar_v3.BulkScanEvaluateSyncResp, error) {
	f.syncCalls++
	f.syncBodies = append(f.syncBodies, body)
	return f.syncResp, f.syncErr
}

func (f *fakeFirewallClient) InitiateBulkScanEvaluationWithResponse(
	_ context.Context,
	_ *ar_v3.InitiateBulkScanEvaluationParams,
	_ ar_v3.InitiateBulkScanEvaluationJSONRequestBody,
	_ ...ar_v3.RequestEditorFn,
) (*ar_v3.InitiateBulkScanEvaluationResp, error) {
	f.initCalls++
	return f.initResp, f.initErr
}

func (f *fakeFirewallClient) GetBulkScanEvaluationStatusWithResponse(
	_ context.Context,
	_ ar_v3.EvaluationIdParam,
	_ *ar_v3.GetBulkScanEvaluationStatusParams,
	_ ...ar_v3.RequestEditorFn,
) (*ar_v3.GetBulkScanEvaluationStatusResp, error) {
	f.statusCalls++
	return f.statusResp, f.statusErr
}

func TestSyncFirewallEvaluator(t *testing.T) {
	t.Parallel()

	scanID := uuid.Nil
	items := []ar_v3.ArtifactScanDetailsV3{{
		Id:          &scanID,
		PackageName: "allowed",
		Version:     "1.0.0",
		ScanStatus:  ar_v3.ArtifactScanDetailsV3ScanStatusALLOWED,
	}}
	fake := &fakeFirewallClient{
		syncResp: &ar_v3.BulkScanEvaluateSyncResp{
			HTTPResponse: &http.Response{StatusCode: http.StatusOK, Status: "200 OK"},
			JSON200:      &ar_v3.BulkScanEvaluateSyncResponse{Data: &items},
		},
	}

	evaluator := newFirewallEvaluator(firewallEvaluatorParams{
		client:       fake,
		accountID:    "account",
		registryUUID: uuid.New(),
		skipCache:    true,
	}, false)
	result, err := evaluator.Evaluate(context.Background(), []dependency{
		{Name: "allowed", Version: "1.0.0"},
		{Name: "missing", Version: "2.0.0"},
	})

	require.NoError(t, err)
	require.Equal(t, "sync", evaluator.Mode())
	require.Equal(t, 1, fake.syncCalls)
	require.Zero(t, fake.initCalls)
	require.NotNil(t, fake.syncBodies[0].SkipCache)
	require.True(t, *fake.syncBodies[0].SkipCache)
	require.Equal(t, []auditScanResult{
		{
			PackageName: "allowed",
			Version:     "1.0.0",
			ScanID:      uuid.Nil.String(),
			ScanStatus:  "ALLOWED",
		},
		{
			PackageName: "missing",
			Version:     "2.0.0",
			ScanStatus:  "UNKNOWN",
		},
	}, result.results)
	require.Len(t, result.details, 2)
	require.NotNil(t, result.details[0])
	require.Nil(t, result.details[1])
}

func TestAsyncFirewallEvaluator(t *testing.T) {
	t.Parallel()

	evaluationID := "evaluation-id"
	status := ar_v3.SUCCESS
	name := "blocked"
	version := "1.0.0"
	scanStatus := ar_v3.BulkScanResultItemScanStatusBLOCKED
	scanID := uuid.New()
	scans := []ar_v3.BulkScanResultItem{{
		PackageName: &name,
		Version:     &version,
		ScanId:      &scanID,
		ScanStatus:  &scanStatus,
	}}
	fake := &fakeFirewallClient{
		initResp: &ar_v3.InitiateBulkScanEvaluationResp{
			HTTPResponse: &http.Response{StatusCode: http.StatusAccepted, Status: "202 Accepted"},
			JSON202: &ar_v3.BulkScanEvaluationAccepted{
				Data: &ar_v3.BulkScanEvaluationAcceptedData{EvaluationId: &evaluationID},
			},
		},
		statusResp: &ar_v3.GetBulkScanEvaluationStatusResp{
			HTTPResponse: &http.Response{StatusCode: http.StatusOK, Status: "200 OK"},
			JSON200: &ar_v3.BulkScanEvaluationStatusResponse{
				Data: &ar_v3.BulkScanEvaluationStatusData{Status: &status, Scans: &scans},
			},
		},
	}

	evaluator := newFirewallEvaluator(firewallEvaluatorParams{
		client:       fake,
		accountID:    "account",
		registryUUID: uuid.New(),
	}, true)
	result, err := evaluator.Evaluate(context.Background(), []dependency{{Name: name, Version: version}})

	require.NoError(t, err)
	require.Equal(t, "async", evaluator.Mode())
	require.Zero(t, fake.syncCalls)
	require.Equal(t, 1, fake.initCalls)
	require.Equal(t, 1, fake.statusCalls)
	require.Equal(t, []auditScanResult{{
		PackageName: name,
		Version:     version,
		ScanID:      scanID.String(),
		ScanStatus:  "BLOCKED",
	}}, result.results)
}

func TestEvaluateFirewallBatchesPreservesFailedRows(t *testing.T) {
	t.Parallel()

	evaluator := &stubFirewallEvaluator{err: errors.New("evaluation unavailable")}
	deps := []dependency{{Name: "one", Version: "1"}, {Name: "two", Version: "2"}}

	results, err := evaluateFirewallBatches(context.Background(), evaluator, deps, 1)

	require.ErrorContains(t, err, "evaluation unavailable")
	require.ElementsMatch(t, []auditScanResult{
		{PackageName: "one", Version: "1", ScanStatus: "UNKNOWN"},
		{PackageName: "two", Version: "2", ScanStatus: "UNKNOWN"},
	}, results)
}

type stubFirewallEvaluator struct {
	err error
}

func (*stubFirewallEvaluator) Mode() string { return "sync" }

func (e *stubFirewallEvaluator) Evaluate(context.Context, []dependency) (firewallBatchResult, error) {
	return firewallBatchResult{}, e.err
}
