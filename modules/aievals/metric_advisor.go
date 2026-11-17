// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package aievals

import (
	"sort"
	"strings"
)

// MetricAdvice explains whether a catalog metric is meaningful for the
// authored candidate. It is deliberately provider-free: choosing a judge is a
// later, user-confirmed stage in the skill.
type MetricAdvice struct {
	MetricCatalogEntry
	Reasons     []string `json:"reasons,omitempty"`
	Missing     []string `json:"missing_prerequisites,omitempty"`
	Rank        int      `json:"rank,omitempty"`
	Recommended bool     `json:"recommended"`
}

type MetricAdviceReport struct {
	Schema               string         `json:"schema"`
	Eligible             []MetricAdvice `json:"eligible"`
	Blocked              []MetricAdvice `json:"blocked"`
	MissingPrerequisites []MetricAdvice `json:"missing_prerequisites"`
	Recommendations      []MetricAdvice `json:"recommendations"`
	NoEligibleMetrics    bool           `json:"no_eligible_metrics"`
}

type MetricAdviceOptions struct {
	AllowLLM       bool
	AllowEmbedding bool
	Goals          []string
}

// AdviseMetrics is a hard eligibility filter followed by an explainable rank.
// It never creates a provider or calls a judge, so the skill can present its
// result before the user accepts any cost or data-sharing boundary.
func AdviseMetrics(candidate Candidate, catalog []MetricCatalogEntry, opts MetricAdviceOptions) MetricAdviceReport {
	evidence := candidateEvidence(candidate)
	report := MetricAdviceReport{Schema: SchemaVersion, Eligible: []MetricAdvice{}, Blocked: []MetricAdvice{}, MissingPrerequisites: []MetricAdvice{}, Recommendations: []MetricAdvice{}}
	for _, entry := range catalog {
		advice := MetricAdvice{MetricCatalogEntry: entry}
		if !entry.Supported || !entry.ReviewedContract {
			advice.Reasons = append(advice.Reasons, firstNonEmpty(entry.UnsupportedReason, "The SDK has no reviewed configured evidence contract."))
			report.Blocked = append(report.Blocked, advice)
			continue
		}
		if entry.ExecutionScope != "" && entry.ExecutionScope != "item" && entry.ExecutionScope != "case" {
			advice.Missing = append(advice.Missing, "requires "+entry.ExecutionScope+" execution, which an ordinary HTTP case does not provide")
		}
		if entry.RequiresLLM && !opts.AllowLLM {
			advice.Missing = append(advice.Missing, "requires explicit LLM judge consent, provider configuration, and paid data-sharing approval")
		}
		if entry.RequiresEmbedding && !opts.AllowEmbedding {
			advice.Missing = append(advice.Missing, "requires explicit embedding provider consent and configuration")
		}
		for _, input := range entry.EvalInputs.Required {
			if !evidence[input] {
				advice.Missing = append(advice.Missing, "missing "+input+" evidence")
			}
		}
		if required := requiredOptions(entry.OptionsSchema); len(required) > 0 {
			advice.Missing = append(advice.Missing, "requires configured option(s): "+strings.Join(required, ", "))
		}
		if len(advice.Missing) > 0 {
			report.MissingPrerequisites = append(report.MissingPrerequisites, advice)
			continue
		}
		advice.Reasons = append(advice.Reasons, "uses available "+strings.Join(entry.EvalInputs.Required, ", ")+" evidence")
		report.Eligible = append(report.Eligible, advice)
	}
	sort.SliceStable(report.Eligible, func(i, j int) bool {
		return adviceScore(report.Eligible[i], opts, evidence) > adviceScore(report.Eligible[j], opts, evidence)
	})
	for i := range report.Eligible {
		report.Eligible[i].Rank = i + 1
		if i < 4 {
			report.Eligible[i].Recommended = true
			report.Recommendations = append(report.Recommendations, report.Eligible[i])
		}
	}
	report.NoEligibleMetrics = len(report.Eligible) == 0
	return report
}

func candidateEvidence(candidate Candidate) map[string]bool {
	evidence := map[string]bool{"output": true}
	for _, item := range candidate.Cases {
		if len(item.Input) > 0 {
			evidence["input"] = true
		}
		if item.expectedPresent || item.expectedOutputPresent {
			evidence["expected"] = true
		}
		if item.Context != nil {
			evidence["context"] = true
		}
		if len(item.ExpectedTools) > 0 {
			evidence["expected_tools"] = true
		}
	}
	// HTTP onboarding measures request latency, but precomputed targets do not
	// invoke a target and therefore cannot provide latency evidence.
	if target, ok := candidate.Wire["target"].(map[string]any); !ok || target["type"] != "precomputed" {
		evidence["latency_ms"] = true
	}
	return evidence
}

func requiredOptions(schema map[string]any) []string {
	raw, ok := schema["required"].([]any)
	if !ok {
		if values, ok := schema["required"].([]string); ok {
			return values
		}
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, value := range raw {
		if name, ok := value.(string); ok {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

func adviceScore(advice MetricAdvice, opts MetricAdviceOptions, evidence map[string]bool) int {
	score := 100
	if advice.MetricType == "heuristic" {
		score += 30
	}
	if advice.RequiresLLM || advice.RequiresEmbedding {
		score -= 20
	}
	goals := strings.ToLower(strings.Join(opts.Goals, " "))
	if goals != "" && (strings.Contains(goals, strings.ToLower(advice.Dimension)) || strings.Contains(goals, strings.ToLower(advice.Category)) || strings.Contains(goals, strings.ToLower(advice.Kind))) {
		score += 20
	}
	if evidence["expected"] {
		switch advice.Kind {
		case "levenshtein", "rouge", "bleu", "answer_similarity":
			score += 15
		case "exact_match", "contains", "list_contains", "regex":
			// These are useful for stable protocols, but a plain text expected
			// value alone is not evidence that exact wording is the user's goal.
			score -= 25
		}
	}
	return score
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
