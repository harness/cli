// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package aievals

import (
	"encoding/json"
	"testing"
)

func adviceCandidate(t *testing.T, expected bool) Candidate {
	t.Helper()
	input := json.RawMessage(`{"text":"hello"}`)
	item := SampleCase{ID: "case-1", Input: input}
	if expected {
		item.Expected = "world"
		item.expectedPresent = true
	}
	return Candidate{Wire: map[string]any{"target": map[string]any{"type": "agent"}}, Cases: []SampleCase{item}}
}

func testMetric(kind, metricType string, required ...string) MetricCatalogEntry {
	return MetricCatalogEntry{
		Kind:             kind,
		Name:             kind,
		Dimension:        "correctness",
		Category:         "deterministic",
		MetricType:       metricType,
		Supported:        true,
		ReviewedContract: true,
		ExecutionScope:   "case",
		EvalInputs:       MetricInputs{Required: required},
	}
}

func TestAdviseMetricsBlocksMissingEvidenceAndUnreviewedContracts(t *testing.T) {
	candidate := adviceCandidate(t, false)
	catalog := []MetricCatalogEntry{
		testMetric("contains", "heuristic", "output"),
		testMetric("json_diff", "heuristic", "output", "expected"),
		{Kind: "plan_quality", Supported: true, ReviewedContract: false},
	}
	report := AdviseMetrics(candidate, catalog, MetricAdviceOptions{})
	if len(report.Recommendations) != 1 || report.Recommendations[0].Kind != "contains" {
		t.Fatalf("recommendations=%#v", report.Recommendations)
	}
	if len(report.MissingPrerequisites) != 1 || report.MissingPrerequisites[0].Kind != "json_diff" {
		t.Fatalf("missing=%#v", report.MissingPrerequisites)
	}
	if len(report.Blocked) != 1 || report.Blocked[0].Kind != "plan_quality" {
		t.Fatalf("blocked=%#v", report.Blocked)
	}
}

func TestAdviseMetricsRequiresProviderConsentAndOptions(t *testing.T) {
	candidate := adviceCandidate(t, true)
	judge := testMetric("rubric_judge", "llm", "input", "output")
	judge.RequiresLLM = true
	schema := testMetric("schema_validation", "heuristic", "output")
	schema.OptionsSchema = map[string]any{"required": []any{"schema"}}

	report := AdviseMetrics(candidate, []MetricCatalogEntry{judge, schema}, MetricAdviceOptions{})
	if !report.NoEligibleMetrics || len(report.Recommendations) != 0 {
		t.Fatalf("expected no eligible metrics: %#v", report)
	}
	if len(report.MissingPrerequisites) != 2 {
		t.Fatalf("missing=%#v", report.MissingPrerequisites)
	}

	report = AdviseMetrics(candidate, []MetricCatalogEntry{judge}, MetricAdviceOptions{AllowLLM: true})
	if len(report.Recommendations) != 1 || report.Recommendations[0].Kind != "rubric_judge" {
		t.Fatalf("LLM consent did not enable judge: %#v", report)
	}
}

func TestAdviseMetricsBlocksUnsupportedExecutionScope(t *testing.T) {
	candidate := adviceCandidate(t, true)
	repeated := testMetric("outcome_consistency", "heuristic", "runs")
	repeated.ExecutionScope = "dataset"
	report := AdviseMetrics(candidate, []MetricCatalogEntry{repeated}, MetricAdviceOptions{})
	if !report.NoEligibleMetrics || len(report.MissingPrerequisites) != 1 {
		t.Fatalf("report=%#v", report)
	}
}

func TestAdviseMetricsDoesNotLeadWithExactMatchForFreeTextExpected(t *testing.T) {
	candidate := adviceCandidate(t, true)
	exact := testMetric("exact_match", "heuristic", "output", "expected")
	similarity := testMetric("levenshtein", "heuristic", "output", "expected")
	report := AdviseMetrics(candidate, []MetricCatalogEntry{exact, similarity}, MetricAdviceOptions{Goals: []string{"correctness"}})
	if len(report.Recommendations) != 2 {
		t.Fatalf("recommendations=%#v", report.Recommendations)
	}
	if report.Recommendations[0].Kind != "levenshtein" {
		t.Fatalf("free-text expected values should not default to exact match: %#v", report.Recommendations)
	}
}
