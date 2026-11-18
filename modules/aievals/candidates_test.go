// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package aievals

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSampleCasePreservesLargeExpectedToolCallInteger(t *testing.T) {
	var item SampleCase
	if err := json.Unmarshal([]byte(`{
		"id":"case-1",
		"input":{"text":"hello"},
		"expected_tool_calls":[{"arguments":{"id":12345678901234567890}}]
	}`), &item); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	encoded, err := json.Marshal(item)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	if !strings.Contains(string(encoded), "12345678901234567890") {
		t.Fatalf("large tool-call integer was changed: %s", encoded)
	}
}

func TestV1TreeFilesWritesConfirmedMetrics(t *testing.T) {
	wire := map[string]any{
		"identifier": "support",
		"evals": []any{map[string]any{
			"dataset":    "datasets/support.jsonl",
			"metric_set": "metric-sets/support.yaml",
		}},
		"metrics": []any{map[string]any{
			"kind":      "contains",
			"threshold": 0.8,
			"options":   map[string]any{"case_sensitive": true},
		}},
	}
	files, err := v1TreeFiles(Candidate{
		Wire: wire,
		Cases: []SampleCase{{
			ID:    "case-1",
			Input: json.RawMessage(`{"text":"hello"}`),
		}},
	})
	if err != nil {
		t.Fatalf("v1TreeFiles() error = %v", err)
	}
	metricSet, ok := files["metric-sets/support.yaml"]
	if !ok {
		t.Fatalf("metric-set file missing: %v", files)
	}
	if !strings.Contains(metricSet, "kind: contains") {
		t.Fatalf("metric kind missing from generated metric set: %s", metricSet)
	}
}

func TestLegacyCandidateDocumentIncludesMetricSetReference(t *testing.T) {
	document, err := legacyCandidateDocument("/tmp/support", Candidate{
		AgentID:      "support",
		EndpointURL:  "https://example.test/chat",
		Method:       "POST",
		RequestField: "text",
		ResponsePath: "output.text",
		Metrics:      []any{map[string]any{"kind": "contains", "threshold": 0.8}},
	})
	if err != nil {
		t.Fatalf("legacyCandidateDocument() error = %v", err)
	}
	evals := document["evals"].([]any)
	eval := evals[0].(map[string]any)
	if eval["metric_set"] != "metric-sets/support.yaml" {
		t.Fatalf("metric_set = %v", eval["metric_set"])
	}
}

func TestWriteV1CandidateRequiresConfirmationBeforeWriting(t *testing.T) {
	repo := t.TempDir()
	wire := CanonicalOnboardingV1Candidate()
	wire["cases"] = []any{map[string]any{
		"id":    "case-1",
		"input": map[string]any{"text": "hello"},
	}}
	wire["metrics"] = []any{map[string]any{"kind": "contains", "threshold": 0.8}}
	code, err := writeV1Candidate(WriteOptions{Repo: repo, Candidate: Candidate{Wire: wire}})
	if code != 2 || err == nil {
		t.Fatalf("writeV1Candidate() = (%d, %v), want confirmation error", code, err)
	}
	if _, statErr := os.Stat(filepath.Join(repo, ".harness", "evals")); !os.IsNotExist(statErr) {
		t.Fatalf("confirmation failure created staging tree: %v", statErr)
	}
}
