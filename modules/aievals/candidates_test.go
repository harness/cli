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
			"config":    map[string]any{"options": map[string]any{"case_sensitive": false}},
		}},
	}
	files, err := v1TreeFiles(Candidate{
		Wire: wire,
		Cases: []SampleCase{{
			ID:    "case-1",
			Input: json.RawMessage(`{"text":"hello"}`),
		}},
	}, map[string]MetricDefinition{"contains": {Type: "heuristic", Dimension: "correctness", Supported: true}})
	if err != nil {
		t.Fatalf("v1TreeFiles() error = %v", err)
	}
	metricSet, ok := files["metric-sets/support.yaml"]
	if !ok {
		t.Fatalf("metric-set file missing: %v", files)
	}
	for _, fragment := range []string{"kind: contains", "type: heuristic", "dimension: correctness", "- config:\n        options:\n            case_sensitive: true"} {
		if !strings.Contains(metricSet, fragment) {
			t.Errorf("missing %q from generated metric set:\n%s", fragment, metricSet)
		}
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

func TestStageLegacyJSONPreservesNumericThreshold(t *testing.T) {
	repo, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(repo, "candidate.json")
	body := `{"agent_id":"support","endpoint_url":"https://example.test/chat","method":"POST",
		"request_field":"text","response_path":"output.text",
		"cases":[{"id":"case-1","input":{"text":"hello"}}],
		"metrics":[{"kind":"contains","threshold":0.8}]}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	candidate, err := LoadCandidateFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if code, err := WriteCandidate(WriteOptions{Repo: repo, Candidate: candidate, Confirm: true}); code != 0 || err != nil {
		t.Fatalf("WriteCandidate() = (%d, %v), want success", code, err)
	}
	staged, err := os.ReadFile(filepath.Join(repo, ".harness", "evals", "metric-sets", "support.yaml"))
	if err != nil || !strings.Contains(string(staged), "threshold: 0.8") {
		t.Fatalf("expected a numeric threshold in the staged metric set: %v", err)
	}
}

func TestWriteV1CandidateRejectsConflictingGeneratedPaths(t *testing.T) {
	for _, ref := range []string{"agents/support-agent/agent.yaml", "./agents/support-agent/agent.yaml"} {
		t.Run(ref, func(t *testing.T) {
			repo := t.TempDir()
			wire := CanonicalOnboardingV1Candidate()
			wire["evals"].([]any)[0].(map[string]any)["metric_set"] = ref
			wire["cases"] = []any{map[string]any{"id": "case-1", "input": "hello"}}
			wire["metrics"] = []any{map[string]any{"kind": "contains", "threshold": 0.8}}
			code, err := WriteCandidate(WriteOptions{Repo: repo, Candidate: Candidate{Wire: wire}, Confirm: true})
			if code != 2 || err == nil || !strings.Contains(err.Error(), "metric_set path conflicts") {
				t.Fatalf("WriteCandidate() = (%d, %v), want a path collision", code, err)
			}
			if _, err := os.Stat(filepath.Join(repo, ".harness")); !os.IsNotExist(err) {
				t.Fatalf("path collision left staged files: %v", err)
			}
		})
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

func TestWriteV1CandidateDoesNotCommitBytesRejectedByRunner(t *testing.T) {
	repo := t.TempDir()
	wire := CanonicalOnboardingV1Candidate()
	wire["cases"] = []any{map[string]any{"id": "case-1", "input": map[string]any{"text": "hello"}}}
	wire["metrics"] = []any{map[string]any{"kind": "contains", "threshold": 0.8}}
	code, err := WriteCandidate(WriteOptions{
		Repo: repo, Candidate: Candidate{Wire: wire}, Confirm: true,
		VerifyRunner: func(_ string, _ CommandFunc) (RunnerLayout, error) {
			return RunnerLayout{Python: "test-python"}, nil
		},
		Run: func(name string, args []string, _ []string, _ string) (CommandResult, error) {
			if name != "test-python" || len(args) != 4 || args[0] != "-I" {
				t.Fatalf("unexpected metric validation invocation %q with %d arguments", name, len(args))
			}
			content, err := os.ReadFile(filepath.Join(args[3], "metric-sets", "quality.yaml"))
			if err != nil || !strings.Contains(string(content), "kind: contains") {
				t.Fatalf("validator did not receive the confirmed metric set: %v", err)
			}
			return CommandResult{Code: 1, Stderr: "invalid metric options"}, nil
		},
	})
	if code != 1 || err == nil || !strings.Contains(err.Error(), "invalid metric options") {
		t.Fatalf("WriteCandidate() = (%d, %v), want validator failure", code, err)
	}
	if _, err := os.Stat(filepath.Join(repo, ".harness", "evals")); !os.IsNotExist(err) {
		t.Fatalf("validation failure left a staged tree: %v", err)
	}
}
