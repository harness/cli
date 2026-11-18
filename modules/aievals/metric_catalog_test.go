// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package aievals

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/harness/cli/v3/pkg/hbase"
	"go.yaml.in/yaml/v3"
)

func metricTestPython(t *testing.T) string {
	t.Helper()
	if os.Getenv(hbase.EnvCLIHome) == "" {
		t.Skipf("set %s to an isolated 0.7.10 runner home for SDK catalog tests", hbase.EnvCLIHome)
	}
	layout, err := VerifyRunner(RunnerHome(), nil)
	if err != nil {
		t.Fatal(err)
	}
	return layout.Python
}

func TestMetricCatalogMatchesInstalledSDK(t *testing.T) {
	python := metricTestPython(t)
	shadow := t.TempDir()
	if err := os.WriteFile(filepath.Join(shadow, "harness_evals.py"), []byte("raise RuntimeError('repository code must not execute during discovery')\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PYTHONPATH", shadow)
	entries, err := ReadMetricCatalog(python, func(name string, args, env []string, _ string) (CommandResult, error) {
		return defaultCommand(name, args, env, shadow)
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := defaultCommand(python, []string{"-I", "-c", "import json; from harness_evals import catalog; print(json.dumps([item.kind for item in catalog()]))"}, nil, "")
	if err != nil || result.Code != 0 {
		t.Fatalf("SDK catalog failed: %v", err)
	}
	var want []string
	if err := json.Unmarshal([]byte(result.Stdout), &want); err != nil {
		t.Fatal(err)
	}
	var got []string
	byKind := map[string]MetricCatalogEntry{}
	for _, entry := range entries {
		got = append(got, entry.Kind)
		byKind[entry.Kind] = entry
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("catalog omitted or invented SDK kinds: got %v, want %v", got, want)
	}
	for kind, supported := range map[string]bool{"schema_validation": true, "geval": true, "answer_correctness": true, "embedding_similarity": true, "decision_choice": false, "composite": false, "dag": false, "mcp_trace_completeness": false} {
		entry, ok := byKind[kind]
		if !ok || entry.Supported != supported || (!supported && entry.UnsupportedReason == "") {
			t.Errorf("unexpected support metadata for %s: %+v", kind, entry)
		}
	}
	if !byKind["answer_correctness"].RequiresLLM || !byKind["answer_correctness"].RequiresEmbedding {
		t.Fatal("lost combined LLM and embedding requirements")
	}
	if !strings.Contains(byKind["faithfulness"].Description, "context") {
		t.Fatal("catalog lost metric input documentation")
	}
	if !reflect.DeepEqual(byKind["schema_validation"].OptionsSchema["required"], []any{"schema"}) {
		t.Fatal("catalog lost a required constructor option")
	}
	if len(byKind["turn_faithfulness"].OptionsSchema["required"].([]any)) != 0 {
		t.Fatal("inherited constructor arguments were incorrectly reported as required")
	}
}

func TestSelectedMetricsAndCaseDataSurviveStaging(t *testing.T) {
	python := metricTestPython(t)
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("ANTHROPIC_API_KEY", "")
	repo := t.TempDir()
	path := filepath.Join(t.TempDir(), "candidate.json")
	body := `{
  "agent_id":"incident-helper", "endpoint_url":"https://localhost:9443/api/analyze?mode=eval",
  "method":"POST", "request_field":"incident", "response_path":"result",
  "cases":[{"id":"outage-1","input":{"incident":"Service unavailable"},
    "expected":{"cause":"deployment","sequence":9007199254740993},
    "context":["Errors began after a deployment"], "expected_tools":["search"],
    "expected_tool_calls":[{"name":"search","arguments":{"service":"checkout"}}],
    "tags":["regression"], "metadata":{"severity":"high"}}],
  "metrics":[
    {"kind":"schema_validation","options":{"schema":{"type":"object","required":["cause"]}}},
    {"kind":"geval","score_name":"root-cause","threshold":0,"options":{"evaluation_steps":["Check the evidence"]},"config":{"criteria":"Explain a supported cause and next action","metadata":{"provider":"openai","model":"judge-model"}}},
    {"kind":"embedding_similarity","threshold":0.75,"config":{"metadata":{"embedding_model":"embedding-model"}}},
    {"kind":"answer_correctness","threshold":0.7}
  ]
}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	candidate, err := LoadCandidateFile(path)
	if err != nil {
		t.Fatal(err)
	}
	code, err := WriteCandidate(WriteOptions{Repo: repo, Candidate: candidate, Confirm: true, Python: python, Stdout: io.Discard, Stderr: io.Discard})
	if err != nil || code != 0 {
		t.Fatalf("stage selected metrics: code=%d err=%v", code, err)
	}
	data, err := os.ReadFile(filepath.Join(repo, ".harness/evals/datasets/incident-helper.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	var row SampleCase
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.UseNumber()
	if err := decoder.Decode(&row); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(row, candidate.Cases[0]) {
		t.Fatalf("case data changed: got %+v, want %+v", row, candidate.Cases[0])
	}
	metricData, err := os.ReadFile(filepath.Join(repo, ".harness/evals/metric-sets/incident-helper.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var metricSet struct {
		Entries []struct {
			Metric    struct{ Kind, Type, Dimension string }
			Threshold float64
			ScoreName string `yaml:"score_name"`
			Config    map[string]any
		}
	}
	if err := yaml.Unmarshal(metricData, &metricSet); err != nil {
		t.Fatal(err)
	}
	if len(metricSet.Entries) != 4 || strings.Contains(string(metricData), "exact_match") {
		t.Fatalf("selected metrics were replaced or lost: %s", metricData)
	}
	judge := metricSet.Entries[1]
	if judge.Metric.Kind != "geval" || judge.Metric.Type != "llm" || judge.Metric.Dimension != "correctness" || judge.Threshold != 0 || judge.ScoreName != "root-cause" {
		t.Fatalf("judge configuration changed: %+v", judge)
	}
	if judge.Config["criteria"] != "Explain a supported cause and next action" || judge.Config["metadata"].(map[string]any)["model"] != "judge-model" {
		t.Fatalf("judge config was lost: %+v", judge.Config)
	}
	if metricSet.Entries[0].Threshold != 1 || metricSet.Entries[2].Metric.Type != "embedding" {
		t.Fatal("SDK default threshold or embedding metric type was lost")
	}
	agentData, err := os.ReadFile(filepath.Join(repo, ".harness/evals/agents/incident-helper/agent.yaml"))
	if err != nil || !strings.Contains(string(agentData), candidate.EndpointURL) {
		t.Fatalf("full endpoint was not preserved: %v", err)
	}
	script := `import sys
from ai_evals.onboarding.load import load_onboarding_tree
from ai_evals.onboarding.translate import translate
tree = load_onboarding_tree(sys.argv[1])
translation = translate(tree, "incident-helper")
item = translation.items_by_eval["incident-helper-eval"][0]
golden = item.to_golden()
assert golden.expected == {"cause": "deployment", "sequence": 9007199254740993}
assert golden.context == ["Errors began after a deployment"]
assert golden.expected_tools == ["search"]
assert item.expected_tool_calls == [{"name": "search", "arguments": {"service": "checkout"}}]
assert golden.expected_tool_calls[0].name == "search"
assert golden.metadata["severity"] == "high"
assert golden.tags == {"regression": "true"}
`
	result, err := defaultCommand(python, []string{"-I", "-c", script, filepath.Join(repo, ".harness/evals")}, nil, "")
	if err != nil || result.Code != 0 {
		t.Fatalf("runner translation lost case fields: %v %s", err, result.Stderr)
	}
}

func TestSelectedMetricErrorsDoNotWriteFiles(t *testing.T) {
	python := metricTestPython(t)
	for _, tc := range []struct{ name, metrics, message string }{
		{"unknown kind", `[{"kind":"made_up"}]`, "not in the installed SDK catalog"},
		{"unsupported provider", `[{"kind":"decision_choice"}]`, "unavailable for onboarding"},
		{"runtime object", `[{"kind":"dag"}]`, "persisted JSON representation"},
		{"missing option", `[{"kind":"schema_validation"}]`, "schema"},
		{"unknown option", `[{"kind":"contains","options":{"case_sensitve":false}}]`, "invalid or missing constructor options"},
		{"wrong option type", `[{"kind":"contains","options":{"case_sensitive":"false"}}]`, "invalid or missing constructor options"},
		{"missing judge criteria", `[{"kind":"conversational_geval"}]`, "invalid or missing constructor options"},
		{"duplicate score", `[{"kind":"contains"},{"kind":"contains"}]`, "unique stable identifier"},
		{"reserved config", `[{"kind":"contains","config":{"kind":"exact_match"}}]`, "unknown_field"},
		{"credential URL", `[{"kind":"geval","config":{"metadata":{"base_url":"https://user:password@example.invalid/api"}}}]`, "without credentials"},
		{"unknown provider field", `[{"kind":"geval","config":{"metadata":{"token_value":"test-value"}}}]`, "credential field"},
		{"empty selection", `[]`, "non-empty list"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var metrics []MetricSelection
			if err := json.Unmarshal([]byte(tc.metrics), &metrics); err != nil {
				t.Fatal(err)
			}
			repo := t.TempDir()
			candidate := Candidate{AgentID: "chat", EndpointURL: "http://localhost:8080/chat", Method: "POST", RequestField: "text", ResponsePath: "reply", Cases: validSampleCases(), Metrics: metrics}
			code, err := WriteCandidate(WriteOptions{Repo: repo, Candidate: candidate, Confirm: true, Python: python, Stdout: io.Discard, Stderr: io.Discard})
			if code == 0 || err == nil || !strings.Contains(err.Error(), tc.message) {
				t.Fatalf("code=%d err=%v, want %s", code, err, tc.message)
			}
			assertNoEvalFiles(t, repo)
		})
	}
}

func TestCandidateRejectsNestedMetricSecretsAndMisspelledFields(t *testing.T) {
	for _, metrics := range []string{
		`[{"kind":"geval","config":{"metadata":{"api_key":"test-value"}}}]`,
		`[{"kind":"geval","options":{"nested":[{"embedding_api_key":"test-value"}]}}]`,
		`[{"kind":"contains","options":{"module_path":"untrusted.py"}}]`,
		`[{"kind":"contains","optionz":{"case_sensitive":false}}]`,
		`null`,
	} {
		path := filepath.Join(t.TempDir(), "candidate.json")
		if err := os.WriteFile(path, []byte(`{"metrics":`+metrics+`}`), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadCandidateFile(path); err == nil || strings.Contains(err.Error(), "test-value") {
			t.Fatalf("unsafe or misspelled metric config was accepted or echoed: %v", err)
		}
	}
}

func TestStagedMetricsUseSelectedOptionsWhenScoring(t *testing.T) {
	python := metricTestPython(t)
	repo := t.TempDir()
	candidate := Candidate{
		AgentID: "chat", EndpointURL: "http://localhost:8080/chat", Method: "POST", RequestField: "text", ResponsePath: "reply",
		Cases: []SampleCase{{
			ID: "case-1", Input: json.RawMessage(`{"text":"Check service health"}`), Expected: "service=healthy",
		}},
		InputText: "Check service health", Expected: "service=healthy",
		Metrics: []MetricSelection{
			{Kind: "contains", ScoreName: "required-text", Options: map[string]any{"case_sensitive": false}},
			{Kind: "contains", ScoreName: "no-leak", Options: map[string]any{"case_sensitive": false, "negate": true, "forbidden": "internal-only"}},
		},
	}
	code, err := WriteCandidate(WriteOptions{Repo: repo, Candidate: candidate, Confirm: true, Python: python, Stdout: io.Discard, Stderr: io.Discard})
	if err != nil || code != 0 {
		t.Fatalf("code=%d err=%v", code, err)
	}
	script := `import json, sys
from pathlib import Path
import yaml
from harness_evals import EvalCase
from harness_evals.metrics.factory import build_metric
root = Path(sys.argv[1])
document = yaml.safe_load((root / "metric-sets" / "chat.yaml").read_text())
metrics = [build_metric(entry["metric"]["type"], {**entry.get("config", {}), "kind": entry["metric"]["kind"]},
                       score_name=entry["score_name"], threshold=entry["threshold"], allow_code_loading=False)
           for entry in document["entries"]]
row = json.loads((root / "datasets" / "chat.jsonl").read_text().splitlines()[0])
case = EvalCase(input=row["input"], expected=row["expected"], output="Service=healthy (verified)")
assert [metric.measure(case).value for metric in metrics] == [1.0, 1.0]
case.output = "Service=healthy INTERNAL-ONLY"
assert [metric.measure(case).value for metric in metrics] == [1.0, 0.0]
assert [metric.name for metric in metrics] == ["required-text", "no-leak"]
`
	result, err := defaultCommand(python, []string{"-I", "-c", script, filepath.Join(repo, ".harness/evals")}, nil, "")
	if err != nil || result.Code != 0 {
		t.Fatalf("scoring staged metrics failed: %v %s", err, result.Stderr)
	}
}

func TestMetricCatalogFailureDoesNotFallBackToExactMatch(t *testing.T) {
	for _, result := range []CommandResult{{Code: 1}, {Stdout: "[]"}, {Stdout: "not-json"}} {
		repo := t.TempDir()
		candidate := Candidate{AgentID: "chat", EndpointURL: "http://localhost:8080/chat", Method: "POST", RequestField: "text", ResponsePath: "reply", Cases: validSampleCases(), Metrics: []MetricSelection{{Kind: "contains"}}}
		calls := 0
		code, err := WriteCandidate(WriteOptions{
			Repo: repo, Candidate: candidate, Confirm: true, Python: "python-test", Stdout: io.Discard, Stderr: io.Discard,
			Run: func(string, []string, []string, string) (CommandResult, error) {
				calls++
				return result, nil
			},
		})
		if code == 0 || err == nil || calls != 1 {
			t.Fatalf("catalog failure reached staging or silently fell back: code=%d calls=%d err=%v", code, calls, err)
		}
		assertNoEvalFiles(t, repo)
	}
}
