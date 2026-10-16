// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package har

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/harness/cli/modules/har/pkg/har/genapi/ar_v3"
)

const (
	firewallMaxBatchSize = 50
	firewallPollInterval = 2 * time.Second
	firewallMaxPolls     = 120
)

type firewallClient interface {
	BulkScanEvaluateSyncWithResponse(
		context.Context,
		*ar_v3.BulkScanEvaluateSyncParams,
		ar_v3.BulkScanEvaluateSyncJSONRequestBody,
		...ar_v3.RequestEditorFn,
	) (*ar_v3.BulkScanEvaluateSyncResp, error)
	InitiateBulkScanEvaluationWithResponse(
		context.Context,
		*ar_v3.InitiateBulkScanEvaluationParams,
		ar_v3.InitiateBulkScanEvaluationJSONRequestBody,
		...ar_v3.RequestEditorFn,
	) (*ar_v3.InitiateBulkScanEvaluationResp, error)
	GetBulkScanEvaluationStatusWithResponse(
		context.Context,
		ar_v3.EvaluationIdParam,
		*ar_v3.GetBulkScanEvaluationStatusParams,
		...ar_v3.RequestEditorFn,
	) (*ar_v3.GetBulkScanEvaluationStatusResp, error)
}

type firewallEvaluator interface {
	Evaluate(context.Context, []dependency) (firewallBatchResult, error)
	Mode() string
}

type firewallBatchResult struct {
	results []auditScanResult
	details []*ar_v3.ArtifactScanDetailsV3
}

type firewallEvaluatorParams struct {
	client       firewallClient
	accountID    string
	orgID        string
	projectID    string
	registryUUID uuid.UUID
	skipCache    bool
}

func newFirewallEvaluator(params firewallEvaluatorParams, useAsync bool) firewallEvaluator {
	if useAsync {
		return &asyncFirewallEvaluator{params: params}
	}
	return &syncFirewallEvaluator{params: params}
}

type syncFirewallEvaluator struct {
	params firewallEvaluatorParams
}

func (*syncFirewallEvaluator) Mode() string { return "sync" }

func (e *syncFirewallEvaluator) Evaluate(ctx context.Context, batch []dependency) (firewallBatchResult, error) {
	artifacts := make([]ar_v3.ArtifactScanInput, 0, len(batch))
	for _, dep := range batch {
		artifacts = append(artifacts, ar_v3.ArtifactScanInput{
			PackageName: dep.Name,
			Version:     dep.Version,
		})
	}

	skipCache := e.params.skipCache
	resp, err := e.params.client.BulkScanEvaluateSyncWithResponse(
		ctx,
		&ar_v3.BulkScanEvaluateSyncParams{
			AccountIdentifier: e.params.accountID,
			OrgIdentifier:     optionalStrPtr(e.params.orgID),
			ProjectIdentifier: optionalStrPtr(e.params.projectID),
		},
		ar_v3.BulkScanEvaluateSyncJSONRequestBody{
			RegistryId: e.params.registryUUID,
			Artifacts:  artifacts,
			SkipCache:  &skipCache,
		},
	)
	if err != nil {
		return firewallBatchResult{}, fmt.Errorf("sync firewall evaluation: %w", err)
	}
	if resp.StatusCode() != http.StatusOK {
		return firewallBatchResult{}, fmt.Errorf(
			"sync firewall evaluation returned %s: %s",
			resp.Status(),
			firewallResponseMessage(resp.Body),
		)
	}
	if resp.JSON200 == nil || resp.JSON200.Data == nil {
		return firewallBatchResult{}, fmt.Errorf("sync firewall evaluation returned an empty response")
	}

	type coordinate struct {
		name    string
		version string
	}
	byCoordinate := make(map[coordinate]*ar_v3.ArtifactScanDetailsV3, len(*resp.JSON200.Data))
	for i := range *resp.JSON200.Data {
		item := &(*resp.JSON200.Data)[i]
		byCoordinate[coordinate{name: item.PackageName, version: item.Version}] = item
	}

	result := firewallBatchResult{
		results: make([]auditScanResult, 0, len(batch)),
		details: make([]*ar_v3.ArtifactScanDetailsV3, 0, len(batch)),
	}
	for _, dep := range batch {
		item := byCoordinate[coordinate{name: dep.Name, version: dep.Version}]
		if item == nil {
			result.results = append(result.results, auditScanResult{
				PackageName: dep.Name,
				Version:     dep.Version,
				ScanStatus:  "UNKNOWN",
			})
			result.details = append(result.details, nil)
			continue
		}

		scanID := ""
		if item.Id != nil {
			scanID = item.Id.String()
		}
		result.results = append(result.results, auditScanResult{
			PackageName: item.PackageName,
			Version:     item.Version,
			ScanID:      scanID,
			ScanStatus:  string(item.ScanStatus),
		})
		result.details = append(result.details, item)
	}
	return result, nil
}

type asyncFirewallEvaluator struct {
	params firewallEvaluatorParams
}

func (*asyncFirewallEvaluator) Mode() string { return "async" }

func (e *asyncFirewallEvaluator) Evaluate(ctx context.Context, batch []dependency) (firewallBatchResult, error) {
	artifacts := make([]ar_v3.ArtifactScanInput, 0, len(batch))
	for _, dep := range batch {
		artifacts = append(artifacts, ar_v3.ArtifactScanInput{
			PackageName: dep.Name,
			Version:     dep.Version,
		})
	}

	initResp, err := e.params.client.InitiateBulkScanEvaluationWithResponse(
		ctx,
		&ar_v3.InitiateBulkScanEvaluationParams{
			AccountIdentifier: e.params.accountID,
			OrgIdentifier:     optionalStrPtr(e.params.orgID),
			ProjectIdentifier: optionalStrPtr(e.params.projectID),
		},
		ar_v3.InitiateBulkScanEvaluationJSONRequestBody{
			RegistryId: e.params.registryUUID,
			Artifacts:  artifacts,
		},
	)
	if err != nil {
		return firewallBatchResult{}, fmt.Errorf("initiating firewall evaluation: %w", err)
	}
	if initResp.StatusCode() != http.StatusAccepted ||
		initResp.JSON202 == nil ||
		initResp.JSON202.Data == nil ||
		initResp.JSON202.Data.EvaluationId == nil {
		return firewallBatchResult{}, fmt.Errorf(
			"initiating firewall evaluation returned %s: %s",
			initResp.Status(),
			firewallResponseMessage(initResp.Body),
		)
	}
	evaluationID := *initResp.JSON202.Data.EvaluationId

	for poll := 0; poll < firewallMaxPolls; poll++ {
		statusResp, err := e.params.client.GetBulkScanEvaluationStatusWithResponse(
			ctx,
			evaluationID,
			&ar_v3.GetBulkScanEvaluationStatusParams{
				AccountIdentifier: e.params.accountID,
				OrgIdentifier:     optionalStrPtr(e.params.orgID),
				ProjectIdentifier: optionalStrPtr(e.params.projectID),
			},
		)
		if err != nil {
			return firewallBatchResult{}, fmt.Errorf("polling firewall evaluation: %w", err)
		}
		if statusResp.StatusCode() != http.StatusOK ||
			statusResp.JSON200 == nil ||
			statusResp.JSON200.Data == nil ||
			statusResp.JSON200.Data.Status == nil {
			return firewallBatchResult{}, fmt.Errorf(
				"polling firewall evaluation returned %s: %s",
				statusResp.Status(),
				firewallResponseMessage(statusResp.Body),
			)
		}

		switch *statusResp.JSON200.Data.Status {
		case ar_v3.SUCCESS:
			return firewallBatchResult{results: asyncScanResults(statusResp.JSON200.Data, batch)}, nil
		case ar_v3.FAILURE:
			message := "firewall evaluation failed"
			if statusResp.JSON200.Data.Error != nil {
				message = *statusResp.JSON200.Data.Error
			}
			return firewallBatchResult{}, fmt.Errorf("%s", message)
		}

		select {
		case <-ctx.Done():
			return firewallBatchResult{}, context.Cause(ctx)
		case <-time.After(firewallPollInterval):
		}
	}
	return firewallBatchResult{}, fmt.Errorf(
		"timeout waiting for firewall evaluation after %d polls",
		firewallMaxPolls,
	)
}

func asyncScanResults(data *ar_v3.BulkScanEvaluationStatusData, batch []dependency) []auditScanResult {
	type coordinate struct {
		name    string
		version string
	}
	byCoordinate := make(map[coordinate]auditScanResult)
	if data.Scans != nil {
		for _, scan := range *data.Scans {
			result := auditScanResult{ScanStatus: "UNKNOWN"}
			if scan.PackageName != nil {
				result.PackageName = *scan.PackageName
			}
			if scan.Version != nil {
				result.Version = *scan.Version
			}
			if scan.ScanId != nil {
				result.ScanID = scan.ScanId.String()
			}
			if scan.ScanStatus != nil {
				result.ScanStatus = string(*scan.ScanStatus)
			}
			byCoordinate[coordinate{name: result.PackageName, version: result.Version}] = result
		}
	}

	results := make([]auditScanResult, 0, len(batch))
	for _, dep := range batch {
		key := coordinate{name: dep.Name, version: dep.Version}
		result, ok := byCoordinate[key]
		if !ok {
			result = auditScanResult{
				PackageName: dep.Name,
				Version:     dep.Version,
				ScanStatus:  "UNKNOWN",
			}
		}
		results = append(results, result)
	}
	return results
}

func firewallResponseMessage(body []byte) string {
	message := string(body)
	if message == "" {
		return "empty response body"
	}
	return message
}
