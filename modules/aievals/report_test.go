// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package aievals

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/harness/cli/v3/pkg/auth"
	"github.com/harness/cli/v3/pkg/cmdctx"
)

func TestPrepareReportImportMapsEveryScoreBySourcePair(t *testing.T) {
	report := onboardingReportFixture()
	archive := []byte("deterministic-archive")
	imported := importArchiveResponse{
		Manifest: []importManifest{{
			Eval:    importedResource{Identifier: "answer-quality", ID: "11111111-1111-4111-8111-111111111111"},
			Dataset: importedResource{Identifier: "answers", ID: "22222222-2222-4222-8222-222222222222"},
			MetricBindings: []importMetricBinding{
				{
					SourceMetricID:  "exact-match",
					SourceScoreName: "exact_match",
					MetricID:        "33333333-3333-4333-8333-333333333333",
					ScoreName:       "Exact Match",
				},
			},
		}},
	}

	prepared, err := PrepareReportImport(archive, imported, "", report)
	if err != nil {
		t.Fatal(err)
	}
	if prepared.EvalIdentifier != "answer-quality" || prepared.EvalID != imported.Manifest[0].Eval.ID {
		t.Fatalf("selected eval = %#v", prepared)
	}
	if prepared.IdempotencyKey == "" {
		t.Fatal("missing idempotency key")
	}
	second, err := PrepareReportImport(archive, imported, "", onboardingReportFixture())
	if err != nil {
		t.Fatal(err)
	}
	if prepared.IdempotencyKey != second.IdempotencyKey {
		t.Fatalf("idempotency key changed across identical preparations: %q != %q", prepared.IdempotencyKey, second.IdempotencyKey)
	}

	var request map[string]any
	if err := json.Unmarshal(prepared.Body, &request); err != nil {
		t.Fatal(err)
	}
	persistedReport := request["report"].(map[string]any)
	item := persistedReport["items"].([]any)[0].(map[string]any)
	score := item["scores"].([]any)[0].(map[string]any)
	if score["metric_id"] != imported.Manifest[0].MetricBindings[0].MetricID {
		t.Fatalf("metric_id = %v", score["metric_id"])
	}
	if score["score_name"] != imported.Manifest[0].MetricBindings[0].ScoreName {
		t.Fatalf("score_name = %v", score["score_name"])
	}
	if item["external_key"] != "case-1" || item["dataset_item_id"] != "case-1" {
		t.Fatalf("item identity was not preserved: %#v", item)
	}
	if got := persistedReport["summary_scores"].(map[string]any)["Exact Match"]; got != float64(1) && got != int64(1) {
		t.Fatalf("summary score = %v", got)
	}
}

func TestPrepareReportImportRejectsMissingAndAmbiguousMappings(t *testing.T) {
	report := onboardingReportFixture()
	imported := importArchiveResponse{
		Manifest: []importManifest{{
			Eval:    importedResource{Identifier: "answer-quality", ID: "11111111-1111-4111-8111-111111111111"},
			Dataset: importedResource{Identifier: "answers", ID: "22222222-2222-4222-8222-222222222222"},
		}},
	}
	if _, err := PrepareReportImport(nil, imported, "", report); err == nil || !strings.Contains(err.Error(), "no imported metric mapping") {
		t.Fatalf("missing mapping error = %v", err)
	}

	imported.Manifest[0].MetricBindings = []importMetricBinding{
		{
			SourceMetricID:  "exact-match",
			SourceScoreName: "exact_match",
			MetricID:        "33333333-3333-4333-8333-333333333333",
			ScoreName:       "Exact Match",
		},
		{
			SourceMetricID:  "exact-match",
			SourceScoreName: "exact_match",
			MetricID:        "44444444-4444-4444-8444-444444444444",
			ScoreName:       "Other Exact Match",
		},
	}
	if _, err := PrepareReportImport(nil, imported, "", report); err == nil || !strings.Contains(err.Error(), "ambiguous imported metric mapping") {
		t.Fatalf("ambiguous mapping error = %v", err)
	}
}

func TestPrepareReportImportRequiresExplicitEvalWhenManifestIsAmbiguous(t *testing.T) {
	imported := importArchiveResponse{
		Manifest: []importManifest{
			{Eval: importedResource{Identifier: "first", ID: "11111111-1111-4111-8111-111111111111"}},
			{Eval: importedResource{Identifier: "second", ID: "22222222-2222-4222-8222-222222222222"}},
		},
	}
	_, err := PrepareReportImport(nil, imported, "", onboardingReportFixture())
	if err == nil || !strings.Contains(err.Error(), "--eval-identifier") {
		t.Fatalf("ambiguous eval error = %v", err)
	}
}

func TestPersistReportRetriesExactRequestAndIdempotencyKey(t *testing.T) {
	authProfile := reportAuth()
	body := []byte(`{"schema_version":"onboarding/v1","idempotency_key":"onboarding-report-abc","eval":{"identifier":"answer-quality","id":"11111111-1111-4111-8111-111111111111"},"report":{"schema_version":"onboarding/v1","status":"completed","items":[],"summary_scores":{},"run_scores":[]}}`)
	var requests [][]byte
	var keys []string
	attempts := 0
	result, err := PersistReport(authProfile, "11111111-1111-4111-8111-111111111111", body, "/", func(req *http.Request) (*http.Response, error) {
		attempts++
		raw, readErr := io.ReadAll(req.Body)
		if readErr != nil {
			t.Fatal(readErr)
		}
		requests = append(requests, raw)
		var decoded map[string]any
		if err := json.Unmarshal(raw, &decoded); err != nil {
			t.Fatal(err)
		}
		keys = append(keys, decoded["idempotency_key"].(string))
		if attempts == 1 {
			return nil, errors.New("connection reset after request")
		}
		return jsonResponse(http.StatusCreated, `{"schema_version":"onboarding/v1","idempotency_key":"onboarding-report-abc","run_id":"55555555-5555-4555-8555-555555555555","eval_id":"11111111-1111-4111-8111-111111111111","status":"completed","ui_path":"/account/acct/all/ai-evals/orgs/org/projects/project/runs/55555555-5555-4555-8555-555555555555"}`), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if attempts != 2 || !bytes.Equal(requests[0], requests[1]) || keys[0] != keys[1] {
		t.Fatalf("retry requests=%q keys=%q attempts=%d", requests, keys, attempts)
	}
	if !strings.Contains(result, "/runs/55555555-5555-4555-8555-555555555555") {
		t.Fatalf("result URL missing: %s", result)
	}
}

func TestPersistReportResponseHandlingIsSafeAndActionable(t *testing.T) {
	for _, test := range []struct {
		status int
		want   string
	}{
		{http.StatusConflict, "idempotency key already belongs to a different report"},
		{http.StatusNotFound, "imported eval, metric, or dataset reference was not found"},
		{http.StatusUnprocessableEntity, "server rejected the report"},
	} {
		t.Run(http.StatusText(test.status), func(t *testing.T) {
			_, err := PersistReport(reportAuth(), "11111111-1111-4111-8111-111111111111", []byte(`{"idempotency_key":"onboarding-report-abc"}`), "/", func(*http.Request) (*http.Response, error) {
				return jsonResponse(test.status, `{"message":"Bearer secret-must-not-escape"}`), nil
			})
			if err == nil || !strings.Contains(err.Error(), test.want) || strings.Contains(err.Error(), "Bearer") {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestReportURLNormalizationSupportsGatewayAndRawAPI(t *testing.T) {
	for _, test := range []struct {
		name   string
		apiURL string
		prefix string
		want   string
	}{
		{"hosted default", "https://app.harness.io", "", "/gateway/ai-evals/api/v1/orgs/org/projects/project/evals/import-archive"},
		{"hosted gateway URL", "https://app.harness.io/gateway", "", "/gateway/ai-evals/api/v1/orgs/org/projects/project/evals/import-archive"},
		{"raw local", "http://localhost:8123", "/", "/ai-evals/api/v1/orgs/org/projects/project/evals/import-archive"},
	} {
		t.Run(test.name, func(t *testing.T) {
			a := reportAuth()
			a.APIUrl = test.apiURL
			req, err := NewArchiveRequestWithBasePath(a, []byte("zip"), test.prefix)
			if err != nil {
				t.Fatal(err)
			}
			if req.URL.Path != test.want {
				t.Fatalf("path = %s, want %s", req.URL.Path, test.want)
			}
			reportReq, err := NewReportImportRequest(
				a,
				"11111111-1111-4111-8111-111111111111",
				[]byte(`{"idempotency_key":"onboarding-report-abc"}`),
				test.prefix,
			)
			if err != nil {
				t.Fatal(err)
			}
			wantReport := strings.Replace(test.want, "/evals/import-archive", "/evals/11111111-1111-4111-8111-111111111111/runs/import-report", 1)
			if reportReq.URL.Path != wantReport {
				t.Fatalf("report path = %s, want %s", reportReq.URL.Path, wantReport)
			}
		})
	}
}

func TestReadPersistReportRejectsSecretBeforeNetwork(t *testing.T) {
	path := t.TempDir() + "/report.json"
	if err := writeJSON(path, map[string]any{
		"schema_version": "onboarding/v1",
		"status":         "completed",
		"items": []any{map[string]any{
			"external_key": "case-1",
			"status":       "success",
			"output":       map[string]any{"Authorization": "Bearer secret"},
			"scores":       []any{},
		}},
		"summary_scores": map[string]any{},
		"run_scores":     []any{},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadPersistReport(path); err == nil || strings.Contains(err.Error(), "Bearer secret") {
		t.Fatalf("secret report error = %v", err)
	}
}

func TestUploadWithoutPersistReportDoesNotPostReport(t *testing.T) {
	// The archive-only workflow deliberately never reads a report or calls the
	// report endpoint; this regression stays at the upload boundary.
	repo := t.TempDir()
	if err := writeUploadFixture(repo); err != nil {
		t.Fatal(err)
	}
	reportPath := repo + "/report.json"
	if err := writeJSON(reportPath, map[string]any{"not": "a D2 report"}); err != nil {
		t.Fatal(err)
	}
	reportPosts := 0
	server := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/import-report") {
			reportPosts++
			t.Errorf("report endpoint called without --persist-report")
		}
		_, _ = io.WriteString(w, `{"agents":[{"identifier":"support","id":"11111111-1111-4111-8111-111111111111","ui_path":"/agent"}],"evals":[{"identifier":"answer-quality","id":"22222222-2222-4222-8222-222222222222","ui_path":"/eval"}],"datasets":[],"metric_sets":[],"suites":[]}`)
	})
	defer server.Close()
	a := reportAuth()
	a.APIUrl = server.URL
	ctx := &cmdctx.Ctx{
		Auth: a,
		FlagValues: map[string]any{
			"root":          filepath.Join(repo, ".harness", "evals"),
			"api-base-path": "/",
		},
	}
	if err := uploadWorkflow(ctx); err != nil {
		t.Fatal(err)
	}
	if reportPosts != 0 {
		t.Fatalf("report posts = %d", reportPosts)
	}
}

func TestUploadWorkflowPersistsOnlyAfterArchiveAndMapsReport(t *testing.T) {
	repo := t.TempDir()
	if err := writeUploadFixture(repo); err != nil {
		t.Fatal(err)
	}
	reportPath := filepath.Join(repo, "report.json")
	if err := writeJSON(reportPath, onboardingReportFixture()); err != nil {
		t.Fatal(err)
	}
	reportPosts := 0
	var reportBody []byte
	server := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/import-archive"):
			_, _ = io.WriteString(w, `{"agents":[{"identifier":"support","id":"11111111-1111-4111-8111-111111111111","ui_path":"/agent"}],"evals":[{"identifier":"answer-quality","id":"22222222-2222-4222-8222-222222222222","ui_path":"/eval"}],"datasets":[],"metric_sets":[],"suites":[],"manifest":[{"eval":{"identifier":"answer-quality","id":"22222222-2222-4222-8222-222222222222"},"dataset":{"identifier":"answers","id":"33333333-3333-4333-8333-333333333333"},"metric_bindings":[{"source_metric_id":"exact-match","source_score_name":"exact_match","metric_id":"44444444-4444-4444-8444-444444444444","score_name":"Exact Match"}]}]}`)
		case strings.HasSuffix(r.URL.Path, "/import-report"):
			reportPosts++
			reportBody, _ = io.ReadAll(r.Body)
			w.WriteHeader(http.StatusCreated)
			_, _ = io.WriteString(w, `{"schema_version":"onboarding/v1","idempotency_key":"onboarding-report-abc","run_id":"55555555-5555-4555-8555-555555555555","eval_id":"22222222-2222-4222-8222-222222222222","status":"completed","ui_path":"/account/acct/all/ai-evals/orgs/org/projects/project/runs/55555555-5555-4555-8555-555555555555"}`)
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			http.NotFound(w, r)
		}
	})
	defer server.Close()
	a := reportAuth()
	a.APIUrl = server.URL
	ctx := &cmdctx.Ctx{
		Auth: a,
		FlagValues: map[string]any{
			"root":           filepath.Join(repo, ".harness", "evals"),
			"persist-report": reportPath,
			"api-base-path":  "/",
		},
	}
	if err := uploadWorkflow(ctx); err != nil {
		t.Fatal(err)
	}
	if reportPosts != 1 {
		t.Fatalf("report posts = %d, want 1", reportPosts)
	}
	var body map[string]any
	if err := json.Unmarshal(reportBody, &body); err != nil {
		t.Fatal(err)
	}
	if body["eval"].(map[string]any)["id"] != "22222222-2222-4222-8222-222222222222" {
		t.Fatalf("eval identity = %#v", body["eval"])
	}
	score := body["report"].(map[string]any)["items"].([]any)[0].(map[string]any)["scores"].([]any)[0].(map[string]any)
	if score["metric_id"] != "44444444-4444-4444-8444-444444444444" || score["score_name"] != "Exact Match" {
		t.Fatalf("mapped score = %#v", score)
	}
}

func TestUploadWorkflowDoesNotPostWhenMappingIsMissing(t *testing.T) {
	repo := t.TempDir()
	if err := writeUploadFixture(repo); err != nil {
		t.Fatal(err)
	}
	reportPath := filepath.Join(repo, "report.json")
	if err := writeJSON(reportPath, onboardingReportFixture()); err != nil {
		t.Fatal(err)
	}
	reportPosts := 0
	server := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/import-report") {
			reportPosts++
		}
		_, _ = io.WriteString(w, `{"agents":[{"identifier":"support","id":"11111111-1111-4111-8111-111111111111","ui_path":"/agent"}],"evals":[{"identifier":"answer-quality","id":"22222222-2222-4222-8222-222222222222","ui_path":"/eval"}],"datasets":[],"metric_sets":[],"suites":[],"manifest":[{"eval":{"identifier":"answer-quality","id":"22222222-2222-4222-8222-222222222222"},"dataset":{"identifier":"answers","id":"33333333-3333-4333-8333-333333333333"},"metric_bindings":[]}]}`)
	})
	defer server.Close()
	a := reportAuth()
	a.APIUrl = server.URL
	ctx := &cmdctx.Ctx{
		Auth: a,
		FlagValues: map[string]any{
			"root":           filepath.Join(repo, ".harness", "evals"),
			"persist-report": reportPath,
			"api-base-path":  "/",
		},
	}
	if err := uploadWorkflow(ctx); err == nil || !strings.Contains(err.Error(), "no imported metric mapping") {
		t.Fatalf("mapping error = %v", err)
	}
	if reportPosts != 0 {
		t.Fatalf("report posts = %d, want 0", reportPosts)
	}
}

func TestUploadWorkflowValidatesReportBeforeArchiveNetwork(t *testing.T) {
	repo := t.TempDir()
	if err := writeUploadFixture(repo); err != nil {
		t.Fatal(err)
	}
	reportPath := filepath.Join(repo, "report.json")
	if err := writeJSON(reportPath, map[string]any{
		"schema_version": "onboarding/v1",
		"status":         "completed",
		"items": []any{map[string]any{
			"external_key": "case-1",
			"status":       "success",
			"output":       map[string]any{"headers": map[string]any{"Authorization": "Bearer secret"}},
			"scores":       []any{},
		}},
		"summary_scores": map[string]any{},
		"run_scores":     []any{},
	}); err != nil {
		t.Fatal(err)
	}
	archivePosts := 0
	server := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		archivePosts++
	})
	defer server.Close()
	a := reportAuth()
	a.APIUrl = server.URL
	ctx := &cmdctx.Ctx{
		Auth: a,
		FlagValues: map[string]any{
			"root":           filepath.Join(repo, ".harness", "evals"),
			"persist-report": reportPath,
			"api-base-path":  "/",
		},
	}
	err := uploadWorkflow(ctx)
	if err == nil || strings.Contains(err.Error(), "Bearer secret") {
		t.Fatalf("secret validation error = %v", err)
	}
	if archivePosts != 0 {
		t.Fatalf("archive posts = %d, want 0", archivePosts)
	}
}

func onboardingReportFixture() map[string]any {
	return map[string]any{
		"schema_version": "onboarding/v1",
		"status":         "completed",
		"items": []any{
			map[string]any{
				"external_key":    "case-1",
				"dataset_item_id": "case-1",
				"status":          "success",
				"output":          map[string]any{"text": "answer"},
				"scores": []any{
					map[string]any{
						"score_name": "exact_match",
						"metric_id":  "exact-match",
						"value":      1.0,
						"threshold":  0.8,
						"passed":     true,
					},
				},
			},
		},
		"summary_scores": map[string]any{"exact_match": 1.0},
		"run_scores":     []any{},
	}
}

func reportAuth() *auth.ResolvedAuth {
	return &auth.ResolvedAuth{
		AuthType:  auth.AuthTypePAT,
		PATToken:  "pat.acct.id.secret",
		APIUrl:    "https://app.harness.io",
		AccountID: "acct",
		OrgID:     "org",
		ProjectID: "project",
	}
}

func jsonResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     make(http.Header),
	}
}

func writeJSON(path string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

func writeUploadFixture(repo string) error {
	root := filepath.Join(repo, ".harness", "evals", "agents", "support")
	if err := os.MkdirAll(root, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(root, "agent.yaml"), []byte("identifier: support\n"), 0o600)
}

func testServer(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	return httptest.NewServer(handler)
}
