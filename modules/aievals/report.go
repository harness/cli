// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package aievals

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/harness/cli/v3/pkg/auth"
	"github.com/harness/cli/v3/pkg/httpclient"
)

const (
	reportImportPath  = "/ai-evals/api/v1/orgs/%s/projects/%s/evals/%s/runs/import-report"
	maxReportBytes    = 10 * 1024 * 1024
	reportRetryCount  = 2
	reportKeyPrefix   = "onboarding-report-"
	reportHTTPTimeout = 30 * time.Second
)

var (
	reportIDPattern   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,254}$`)
	reportUUIDPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
)

// PreparedReportImport is the exact request body and identity selected after
// validating a D2 report and correlating it with D3's archive manifest.
type PreparedReportImport struct {
	Body           []byte
	EvalIdentifier string
	EvalID         string
	IdempotencyKey string
	Mapping        []importMetricBinding
}

// ReadPersistReport reads and validates the D2 report before any network call.
// It deliberately returns only safe remediation text and never echoes report
// values, because this path handles files produced by an external runner.
func ReadPersistReport(path string) (map[string]any, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("cannot persist report: cannot read report file; check --persist-report and retry")
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("cannot persist report: --persist-report must name a regular JSON report file")
	}
	if info.Size() > maxReportBytes {
		return nil, fmt.Errorf("cannot persist report: report file exceeds the 10 MiB safety limit")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("cannot persist report: cannot read report file; check permissions and retry")
	}
	value, err := decodeReportJSON(data)
	if err != nil {
		return nil, fmt.Errorf("cannot persist report: report file is not valid JSON; regenerate it with D2 --report-json")
	}
	report, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("cannot persist report: report JSON must be an object emitted by D2 --report-json")
	}
	if err := validatePersistReport(report); err != nil {
		return nil, err
	}
	return report, nil
}

func decodeReportJSON(data []byte) (any, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return nil, fmt.Errorf("report contains trailing JSON")
	}
	return normalizeJSONNumbers(value), nil
}

// PrepareReportImport selects one imported eval, maps every D2 score identity
// through the matching D3 binding, and builds the request without a client
// fingerprint. A missing or duplicate pair is always an error.
func PrepareReportImport(archive []byte, imported importArchiveResponse, requestedEval string, report map[string]any) (PreparedReportImport, error) {
	if err := validatePersistReport(report); err != nil {
		return PreparedReportImport{}, err
	}
	selected, err := selectImportManifest(imported.Manifest, requestedEval)
	if err != nil {
		return PreparedReportImport{}, err
	}
	if err := validateImportedManifest(selected); err != nil {
		return PreparedReportImport{}, err
	}
	if err := remapReportScores(report, selected.MetricBindings); err != nil {
		return PreparedReportImport{}, err
	}

	reportBytes, err := json.Marshal(report)
	if err != nil {
		return PreparedReportImport{}, fmt.Errorf("cannot persist report: normalized report is not safe JSON; regenerate it with D2 --report-json")
	}
	archiveDigest := sha256.Sum256(archive)
	seed := struct {
		ArchiveSHA256  string          `json:"archive_sha256"`
		EvalIdentifier string          `json:"eval_identifier"`
		EvalID         string          `json:"eval_id"`
		Report         json.RawMessage `json:"report"`
	}{
		ArchiveSHA256:  hex.EncodeToString(archiveDigest[:]),
		EvalIdentifier: selected.Eval.Identifier,
		EvalID:         selected.Eval.ID,
		Report:         reportBytes,
	}
	seedBytes, err := json.Marshal(seed)
	if err != nil {
		return PreparedReportImport{}, fmt.Errorf("cannot persist report: could not derive a stable idempotency key")
	}
	requestDigest := sha256.Sum256(seedBytes)
	idempotencyKey := reportKeyPrefix + hex.EncodeToString(requestDigest[:])

	request := map[string]any{
		"schema_version":  SchemaVersionV1,
		"idempotency_key": idempotencyKey,
		"eval": map[string]any{
			"identifier": selected.Eval.Identifier,
			"id":         selected.Eval.ID,
		},
		"report": report,
	}
	body, err := json.Marshal(request)
	if err != nil {
		return PreparedReportImport{}, fmt.Errorf("cannot persist report: import request is not safe JSON; regenerate it with D2 --report-json")
	}
	return PreparedReportImport{
		Body:           body,
		EvalIdentifier: selected.Eval.Identifier,
		EvalID:         selected.Eval.ID,
		IdempotencyKey: idempotencyKey,
		Mapping:        append([]importMetricBinding(nil), selected.MetricBindings...),
	}, nil
}

func selectImportManifest(manifest []importManifest, requested string) (importManifest, error) {
	if requested != "" {
		var matches []importManifest
		for _, candidate := range manifest {
			if candidate.Eval.Identifier == requested {
				matches = append(matches, candidate)
			}
		}
		switch len(matches) {
		case 1:
			return matches[0], nil
		case 0:
			return importManifest{}, fmt.Errorf("cannot persist report: imported eval %q was not found in the archive manifest; choose an eval identifier returned by upload", safeDisplay(requested))
		default:
			return importManifest{}, fmt.Errorf("cannot persist report: imported eval identifier %q is ambiguous in the archive manifest; choose one source eval", safeDisplay(requested))
		}
	}
	if len(manifest) == 1 {
		return manifest[0], nil
	}
	if len(manifest) == 0 {
		return importManifest{}, fmt.Errorf("cannot persist report: archive response has no D3 manifest; update the API/import endpoint before using --persist-report")
	}
	return importManifest{}, fmt.Errorf("cannot persist report: archive contains multiple evals; pass --eval-identifier with the requested source eval")
}

func validateImportedManifest(manifest importManifest) error {
	if !reportIDPattern.MatchString(manifest.Eval.Identifier) || !reportUUIDPattern.MatchString(manifest.Eval.ID) {
		return fmt.Errorf("cannot persist report: archive manifest returned an invalid eval identity; re-upload and retry")
	}
	if !reportIDPattern.MatchString(manifest.Dataset.Identifier) || !reportUUIDPattern.MatchString(manifest.Dataset.ID) {
		return fmt.Errorf("cannot persist report: archive manifest returned an invalid dataset identity; re-upload and retry")
	}
	for _, binding := range manifest.MetricBindings {
		if !reportIDPattern.MatchString(binding.SourceMetricID) ||
			binding.SourceScoreName == "" ||
			len(binding.SourceScoreName) > 100 ||
			!reportUUIDPattern.MatchString(binding.MetricID) ||
			binding.ScoreName == "" ||
			len(binding.ScoreName) > 100 ||
			secretShaped(binding.SourceMetricID) ||
			secretShaped(binding.SourceScoreName) ||
			secretShaped(binding.MetricID) ||
			secretShaped(binding.ScoreName) {
			return fmt.Errorf("cannot persist report: archive manifest contains an unsafe metric mapping; re-upload and retry")
		}
	}
	return nil
}

func remapReportScores(report map[string]any, bindings []importMetricBinding) error {
	byPair := make(map[string][]importMetricBinding)
	for _, binding := range bindings {
		key := binding.SourceMetricID + "\x00" + binding.SourceScoreName
		byPair[key] = append(byPair[key], binding)
	}
	for itemIndex, rawItem := range report["items"].([]any) {
		item, ok := rawItem.(map[string]any)
		if !ok {
			return reportShapeError(fmt.Sprintf("items[%d]", itemIndex))
		}
		scores, _ := item["scores"].([]any)
		for scoreIndex, rawScore := range scores {
			score, ok := rawScore.(map[string]any)
			if !ok {
				return reportShapeError(fmt.Sprintf("items[%d].scores[%d]", itemIndex, scoreIndex))
			}
			if err := remapScore(score, fmt.Sprintf("items[%d].scores[%d]", itemIndex, scoreIndex), byPair); err != nil {
				return err
			}
		}
	}
	for scoreIndex, rawScore := range report["run_scores"].([]any) {
		score, ok := rawScore.(map[string]any)
		if !ok {
			return reportShapeError(fmt.Sprintf("run_scores[%d]", scoreIndex))
		}
		if err := remapScore(score, fmt.Sprintf("run_scores[%d]", scoreIndex), byPair); err != nil {
			return err
		}
	}

	summary, _ := report["summary_scores"].(map[string]any)
	remappedSummary := make(map[string]any, len(summary))
	for sourceName, value := range summary {
		var matches []importMetricBinding
		for _, binding := range bindings {
			if binding.SourceScoreName == sourceName {
				matches = append(matches, binding)
			}
		}
		switch len(matches) {
		case 1:
			if _, exists := remappedSummary[matches[0].ScoreName]; exists {
				return fmt.Errorf("cannot persist report: imported summary score mapping is ambiguous; choose a metric set with unique score names")
			}
			remappedSummary[matches[0].ScoreName] = value
		case 0:
			return fmt.Errorf("cannot persist report: no imported metric mapping for summary score %q; re-run with the matching metric set", safeDisplay(sourceName))
		default:
			return fmt.Errorf("cannot persist report: imported summary score %q has ambiguous metric mappings; choose a metric set with unique score names", safeDisplay(sourceName))
		}
	}
	report["summary_scores"] = remappedSummary
	return nil
}

func remapScore(score map[string]any, path string, byPair map[string][]importMetricBinding) error {
	sourceMetricID, metricOK := score["metric_id"].(string)
	sourceScoreName, scoreOK := score["score_name"].(string)
	if !metricOK || !scoreOK || sourceMetricID == "" || sourceScoreName == "" {
		return fmt.Errorf("cannot persist report: %s is missing a source metric_id or score_name; regenerate the report with D2 --report-json", path)
	}
	matches := byPair[sourceMetricID+"\x00"+sourceScoreName]
	switch len(matches) {
	case 0:
		return fmt.Errorf("cannot persist report: no imported metric mapping for score at %s; re-upload the archive and keep the staged metric set unchanged", path)
	case 1:
		score["metric_id"] = matches[0].MetricID
		score["score_name"] = matches[0].ScoreName
		return nil
	default:
		return fmt.Errorf("cannot persist report: ambiguous imported metric mapping for score at %s; choose an archive with one matching metric binding", path)
	}
}

func validatePersistReport(report map[string]any) error {
	allowed := map[string]bool{
		"schema_version": true,
		"status":         true,
		"items":          true,
		"summary_scores": true,
		"run_scores":     true,
	}
	for key := range report {
		if !allowed[key] {
			return reportUnknownField("report", key)
		}
	}
	if version, ok := report["schema_version"].(string); !ok || version != SchemaVersionV1 {
		return fmt.Errorf("cannot persist report: report schema_version must be %s; regenerate it with D2 --report-json", SchemaVersionV1)
	}
	status, ok := report["status"].(string)
	if !ok || (status != "completed" && status != "failed") {
		return fmt.Errorf("cannot persist report: report status must be completed or failed")
	}
	items, ok := report["items"].([]any)
	if !ok {
		return reportShapeError("items")
	}
	if _, ok := report["summary_scores"]; !ok {
		report["summary_scores"] = map[string]any{}
	}
	if _, ok := report["run_scores"]; !ok {
		report["run_scores"] = []any{}
	}
	summary, ok := report["summary_scores"].(map[string]any)
	if !ok {
		return reportShapeError("summary_scores")
	}
	for name, value := range summary {
		if name == "" || len(name) > 100 || secretShaped(name) || isCredentialName(name) {
			return reportUnknownField("summary_scores", name)
		}
		if !validReportUnitNumber(value) {
			return reportShapeError("summary_scores")
		}
	}
	for index, rawItem := range items {
		if err := validateReportItem(rawItem, index); err != nil {
			return err
		}
	}
	runScores, ok := report["run_scores"].([]any)
	if !ok {
		return reportShapeError("run_scores")
	}
	for index, rawScore := range runScores {
		if err := validateReportScore(rawScore, fmt.Sprintf("run_scores[%d]", index)); err != nil {
			return err
		}
	}
	return nil
}

func validateReportItem(raw any, index int) error {
	path := fmt.Sprintf("items[%d]", index)
	item, ok := raw.(map[string]any)
	if !ok {
		return reportShapeError(path)
	}
	allowed := map[string]bool{
		"external_key": true, "dataset_item_id": true, "dataset_item_snapshot": true,
		"status": true, "output": true, "error_message": true, "scores": true,
	}
	for key := range item {
		if !allowed[key] {
			return reportUnknownField(path, key)
		}
	}
	if err := validateReportID(item, "external_key", path, true); err != nil {
		return err
	}
	if err := validateReportID(item, "dataset_item_id", path, false); err != nil {
		return err
	}
	status, ok := item["status"].(string)
	if !ok || (status != "success" && status != "failed" && status != "error") {
		return reportShapeError(path + ".status")
	}
	if output, exists := item["output"]; exists {
		if output != nil {
			if _, ok := output.(map[string]any); !ok {
				return reportShapeError(path + ".output")
			}
			if err := validateReportUserJSON(output, path+".output"); err != nil {
				return err
			}
		}
	}
	if snapshot, exists := item["dataset_item_snapshot"]; exists && snapshot != nil {
		if _, ok := snapshot.(map[string]any); !ok {
			return reportShapeError(path + ".dataset_item_snapshot")
		}
		if err := validateReportUserJSON(snapshot, path+".dataset_item_snapshot"); err != nil {
			return err
		}
	}
	if text, exists := item["error_message"]; exists && !validReportText(text, 4000) {
		return reportShapeError(path + ".error_message")
	}
	scores, ok := item["scores"].([]any)
	if !ok {
		return reportShapeError(path + ".scores")
	}
	for scoreIndex, rawScore := range scores {
		if err := validateReportScore(rawScore, fmt.Sprintf("%s.scores[%d]", path, scoreIndex)); err != nil {
			return err
		}
	}
	return nil
}

func validateReportScore(raw any, path string) error {
	score, ok := raw.(map[string]any)
	if !ok {
		return reportShapeError(path)
	}
	allowed := map[string]bool{
		"score_name": true, "metric_id": true, "value": true, "threshold": true,
		"passed": true, "reason": true, "metadata": true,
	}
	for key := range score {
		if !allowed[key] {
			return reportUnknownField(path, key)
		}
	}
	name, ok := score["score_name"].(string)
	if !ok || name == "" || len(name) > 100 || secretShaped(name) || isCredentialName(name) {
		return reportShapeError(path + ".score_name")
	}
	if err := validateReportID(score, "metric_id", path, true); err != nil {
		return err
	}
	if !validReportUnitNumber(score["value"]) || !validReportUnitNumber(score["threshold"]) {
		return reportShapeError(path + ".value")
	}
	if passed, ok := score["passed"].(bool); !ok {
		return reportShapeError(path + ".passed")
	} else {
		_ = passed
	}
	if reason, exists := score["reason"]; exists && !validReportText(reason, 4000) {
		return reportShapeError(path + ".reason")
	}
	if metadata, exists := score["metadata"]; exists && metadata != nil {
		if _, ok := metadata.(map[string]any); !ok {
			return reportShapeError(path + ".metadata")
		}
		if err := validateReportUserJSON(metadata, path+".metadata"); err != nil {
			return err
		}
	}
	return nil
}

func validateReportID(fields map[string]any, key, path string, required bool) error {
	value, exists := fields[key]
	if !exists || value == nil {
		if required {
			return reportShapeError(path + "." + key)
		}
		return nil
	}
	text, ok := value.(string)
	if !ok || !reportIDPattern.MatchString(text) || isCredentialName(text) || secretShaped(text) {
		return reportShapeError(path + "." + key)
	}
	return nil
}

func validateReportUserJSON(value any, path string) error {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			if isCredentialName(key) || secretShaped(key) || reportTransportSecretKey(key) {
				return reportUnknownField(path, key)
			}
			if err := validateReportUserJSON(child, path+"."+safeDisplay(key)); err != nil {
				return err
			}
		}
	case []any:
		for index, child := range typed {
			if err := validateReportUserJSON(child, fmt.Sprintf("%s[%d]", path, index)); err != nil {
				return err
			}
		}
	case string:
		if secretShaped(typed) {
			return reportShapeError(path)
		}
	case int, int64, nil, bool:
		return nil
	case float64:
		if math.IsNaN(typed) || math.IsInf(typed, 0) {
			return reportShapeError(path)
		}
	case json.Number:
		if _, err := typed.Float64(); err != nil {
			return reportShapeError(path)
		}
	default:
		return reportShapeError(path)
	}
	return nil
}

func validReportText(value any, max int) bool {
	text, ok := value.(string)
	return ok && len(text) <= max && !secretShaped(text)
}

func validReportUnitNumber(value any) bool {
	switch typed := value.(type) {
	case int:
		return typed >= 0 && typed <= 1
	case int64:
		return typed >= 0 && typed <= 1
	case float64:
		return typed >= 0 && typed <= 1 && !math.IsNaN(typed) && !math.IsInf(typed, 0)
	case json.Number:
		number, err := typed.Float64()
		return err == nil && number >= 0 && number <= 1 && !math.IsNaN(number) && !math.IsInf(number, 0)
	default:
		return false
	}
}

func reportTransportSecretKey(key string) bool {
	normalized := strings.ToLower(strings.ReplaceAll(key, "-", "_"))
	switch normalized {
	case "auth_headers", "authorization", "cookie", "cookies", "env", "environ",
		"environment", "headers", "query_params", "raw_headers", "request_headers",
		"secret", "secrets", "set_cookie":
		return true
	default:
		return false
	}
}

func reportShapeError(path string) error {
	return fmt.Errorf("cannot persist report: report field %s has an unsupported shape; regenerate it with D2 --report-json", path)
}

func reportUnknownField(path, key string) error {
	return fmt.Errorf("cannot persist report: report field %s contains an unsupported field %s; regenerate it with D2 --report-json", path, safeDisplay(key))
}

func safeDisplay(value string) string {
	if isCredentialName(value) || secretShaped(value) || strings.ContainsAny(value, " \t\r\n") {
		return "[redacted]"
	}
	return value
}

func NewReportImportRequest(a *auth.ResolvedAuth, evalID string, body []byte, apiBasePath string) (*http.Request, error) {
	if a == nil {
		return nil, fmt.Errorf("report persistence requires an authenticated profile")
	}
	if a.OrgID == "" || a.ProjectID == "" || evalID == "" {
		return nil, fmt.Errorf("report persistence requires org, project, and imported eval scope")
	}
	route := fmt.Sprintf(reportImportPath, url.PathEscape(a.OrgID), url.PathEscape(a.ProjectID), url.PathEscape(evalID))
	endpoint, err := apiEndpoint(a.APIUrl, apiBasePath, route)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest(http.MethodPost, endpoint, strings.NewReader(string(body)))
	if err != nil {
		return nil, err
	}
	a.SetAuthHeader(req)
	req.Header.Set("Content-Type", "application/json")
	return req, nil
}

type reportImportResponse struct {
	RunID  string `json:"run_id"`
	EvalID string `json:"eval_id"`
	Status string `json:"status"`
	UIPath string `json:"ui_path"`
}

// PersistReport posts one exact request body, retrying only bounded transport
// uncertainty and transient server responses. Every retry carries the same
// body and therefore the same D3 idempotency key.
func PersistReport(a *auth.ResolvedAuth, evalID string, body []byte, apiBasePath string, do func(*http.Request) (*http.Response, error)) (string, error) {
	if do == nil {
		do = func(req *http.Request) (*http.Response, error) {
			ctx, cancel := context.WithTimeout(context.Background(), reportHTTPTimeout)
			defer cancel()
			req = req.WithContext(ctx)
			return httpclient.DoWithProxyCheck(&http.Client{Timeout: reportHTTPTimeout}, req)
		}
	}
	for attempt := 0; attempt < reportRetryCount; attempt++ {
		req, err := NewReportImportRequest(a, evalID, body, apiBasePath)
		if err != nil {
			return "", err
		}
		resp, err := do(req)
		if err != nil {
			if attempt+1 < reportRetryCount {
				continue
			}
			return "", fmt.Errorf("report persistence status is uncertain after one retry; rerun the same --persist-report command")
		}
		respBody, readErr := readReportResponse(resp)
		if readErr != nil {
			if attempt+1 < reportRetryCount {
				continue
			}
			return "", fmt.Errorf("report persistence status is uncertain after one retry; rerun the same --persist-report command")
		}
		if isTransientReportStatus(resp.StatusCode) {
			if attempt+1 < reportRetryCount {
				continue
			}
			return "", fmt.Errorf("report persistence returned a transient server error (%d) after one retry; rerun the same --persist-report command", resp.StatusCode)
		}
		if resp.StatusCode != http.StatusCreated {
			switch resp.StatusCode {
			case http.StatusConflict:
				return "", fmt.Errorf("report persistence conflict (409): the idempotency key already belongs to a different report; keep the key and inspect the existing run")
			case http.StatusNotFound:
				return "", fmt.Errorf("report persistence failed (404): the imported eval, metric, or dataset reference was not found; re-upload and select the matching manifest entry")
			case http.StatusUnprocessableEntity:
				return "", fmt.Errorf("report persistence failed (422): the server rejected the report; check D2 report shape, secret safety, and imported metric mappings")
			default:
				return "", fmt.Errorf("report persistence failed with HTTP %d; inspect the API configuration and retry", resp.StatusCode)
			}
		}
		var parsed reportImportResponse
		if err := json.Unmarshal(respBody, &parsed); err != nil || parsed.RunID == "" || parsed.UIPath == "" {
			return "", fmt.Errorf("report persistence returned an invalid success response; retry the same --persist-report command")
		}
		if !strings.HasPrefix(parsed.UIPath, "/") || strings.ContainsAny(parsed.UIPath, "?#\\") {
			return "", fmt.Errorf("report persistence returned an unsafe result URL; open the run from the Harness UI")
		}
		return fmt.Sprintf("Run %s %s\n", parsed.RunID, resourceURL(a.APIUrl, a.UIUrl, parsed.UIPath)), nil
	}
	return "", fmt.Errorf("report persistence did not complete; retry the same --persist-report command")
}

func readReportResponse(resp *http.Response) ([]byte, error) {
	if resp == nil {
		return nil, io.ErrUnexpectedEOF
	}
	defer resp.Body.Close()
	return io.ReadAll(io.LimitReader(resp.Body, 1<<20))
}

func isTransientReportStatus(status int) bool {
	switch status {
	case http.StatusRequestTimeout, http.StatusTooManyRequests, http.StatusInternalServerError,
		http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	default:
		return false
	}
}
