// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package aievals

import (
	"encoding/json"
	"fmt"
	"strings"
)

// ApplyFiles returns the tree to write. Medium values are included only when
// acceptMedium is set. Answered low gaps are included by field name.
func ApplyFiles(inf Inference, acceptMedium bool, answers map[string]string) map[string]string {
	return materialize(inf, !acceptMedium, answers)
}

func materialize(inf Inference, highOnly bool, answers map[string]string) map[string]string {
	usable := map[string]FieldEvidence{}
	for _, ev := range inf.Evidence {
		if ev.Value == "" && answers[ev.Field] == "" {
			continue
		}
		switch ev.Confidence {
		case ConfidenceHigh:
			usable[ev.Field] = withAnswer(ev, answers)
		case ConfidenceMedium:
			if highOnly {
				continue
			}
			usable[ev.Field] = withAnswer(ev, answers)
		case ConfidenceLow:
			if answers[ev.Field] == "" {
				continue
			}
			usable[ev.Field] = withAnswer(ev, answers)
		}
	}
	// Candidate trees (highOnly == false) keep medium values.
	// highOnly here means "drop medium", which is the auto-stage set.
	if _, ok := usable["endpoint_path"]; !ok {
		return nil
	}
	if _, ok := usable["method"]; !ok {
		return nil
	}
	if _, ok := usable["port"]; !ok {
		return nil
	}
	if _, ok := usable["response_path"]; !ok {
		return nil
	}
	agent := inf.AgentID
	if agent == "" {
		agent = "app"
	}
	if ev, ok := usable["agent_id"]; ok && ev.Value != "" {
		agent = ev.Value
	}
	name := displayName(agent)
	endpoint := fmt.Sprintf("http://127.0.0.1:%s%s", usable["port"].Value, usable["endpoint_path"].Value)
	var header strings.Builder
	if envName := usable["auth_env"].Value; envName != "" {
		headerName := usable["auth_header"].Value
		if headerName == "" {
			headerName = "Authorization"
		}
		ref := secretRef(envName)
		fmt.Fprintf(&header, "  headers:\n    %s:\n      source: harness_secret\n      ref: %s\n      env: %s\n", headerName, ref, envName)
	}
	requestField := usable["request_field"].Value
	if requestField == "" {
		requestField = "text"
	}
	agentYAML := fmt.Sprintf(`identifier: %s
name: %s
target:
  method: %s
  response_path: %s
%s  request_template:
    %s: "{{input.%s}}"
environments:
  local:
    endpoint: %s
%s
evals:
  - identifier: %s-eval
    name: %s
    dataset: datasets/%s.jsonl
    metric_set: metric-sets/%s.yaml
suite:
  identifier: %s-suite
  name: %s
  pass_strategy: all_must_pass
  evals:
    - %s-eval
`, agent, name, usable["method"].Value, usable["response_path"].Value, header.String(), requestField, requestField, endpoint, secretsBlock(usable["auth_env"].Value), agent, name, agent, agent, agent, name, agent)

	inputText := usable["test_input_text"].Value
	if inputText == "" {
		inputText = "hello"
	}
	expected := usable["test_expected"].Value
	if expected == "" {
		expected = "ok"
	}
	row, err := json.Marshal(struct {
		ID       string `json:"id"`
		Input    any    `json:"input"`
		Expected string `json:"expected"`
	}{
		ID:       "case-1",
		Input:    map[string]string{requestField: inputText},
		Expected: expected,
	})
	if err != nil {
		return nil
	}
	metric := fmt.Sprintf(`identifier: %s
name: %s
entries:
  - metric:
      identifier: exact-match
      name: Exact Match
      kind: exact_match
      type: heuristic
      dimension: correctness
    threshold: 1.0
    score_name: exact_match
`, agent, name)
	return map[string]string{
		fmt.Sprintf("agents/%s/agent.yaml", agent): agentYAML,
		fmt.Sprintf("datasets/%s.jsonl", agent):    string(row) + "\n",
		fmt.Sprintf("metric-sets/%s.yaml", agent):  metric,
	}
}

func withAnswer(ev FieldEvidence, answers map[string]string) FieldEvidence {
	if answers != nil && answers[ev.Field] != "" {
		ev.Value = answers[ev.Field]
		ev.Confidence = ConfidenceHigh
	}
	return ev
}

func secretsBlock(envName string) string {
	if envName == "" {
		return "    secrets: {}"
	}
	return fmt.Sprintf("    secrets:\n      %s: %s", secretRef(envName), envName)
}

func secretRef(envName string) string {
	return strings.ToLower(strings.ReplaceAll(envName, "_", "-"))
}

func displayName(agent string) string {
	if agent == "" {
		return "App"
	}
	return strings.ToUpper(agent[:1]) + agent[1:]
}
