// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package aievals

import (
	"encoding/json"
	"fmt"
	"strconv"
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
	endpoint := fmt.Sprintf("http://127.0.0.1:%s%s", usable["port"].Value, ensureSlash(usable["endpoint_path"].Value))
	var header strings.Builder
	headerName := usable["auth_header"].Value
	envName := usable["auth_env"].Value
	if headerName != "" && envName != "" {
		ref := secretRef(envName)
		fmt.Fprintf(&header, "  headers:\n    %s:\n      source: harness_secret\n      ref: %s\n      env: %s\n", headerName, ref, envName)
	}
	fields := stagedRequestFields(usable)
	var requestBlock strings.Builder
	requestBlock.WriteString("  request_template:\n")
	for _, field := range fields {
		fmt.Fprintf(&requestBlock, "    %s: \"{{input.%s}}\"\n", field, field)
	}
	agentYAML := fmt.Sprintf(`identifier: %s
name: %s
target:
  method: %s
  response_path: %s
%s%senvironments:
  local:
    endpoint: %s
%s
evals:
  - identifier: %s
    name: %s
    dataset: %s
    metric_set: %s
suite:
  identifier: %s
  name: %s
  pass_strategy: all_must_pass
  evals:
    - %s
`, yamlString(agent), yamlString(name), yamlString(usable["method"].Value), yamlString(usable["response_path"].Value), header.String(), requestBlock.String(), yamlString(endpoint), secretsBlock(envName), yamlString(agent+"-eval"), yamlString(name), yamlString("datasets/"+agent+".jsonl"), yamlString("metric-sets/"+agent+".yaml"), yamlString(agent+"-suite"), yamlString(name), yamlString(agent+"-eval"))

	inputText := usable["test_input_text"].Value
	primary := usable["request_field"].Value
	input := map[string]string{}
	for _, field := range fields {
		if inputText != "" && (field == primary || (primary == "" && len(fields) == 1)) {
			input[field] = inputText
		} else if inputText != "" && len(fields) == 1 {
			input[field] = inputText
		} else if inputText != "" && primary == "" {
			input[field] = inputText
		} else {
			input[field] = ""
		}
	}
	if len(input) == 0 {
		input["text"] = inputText
	}
	expected := usable["test_expected"].Value
	row, err := json.Marshal(struct {
		ID       string `json:"id"`
		Input    any    `json:"input"`
		Expected string `json:"expected"`
	}{
		ID:       "case-1",
		Input:    input,
		Expected: expected,
	})
	if err != nil {
		return nil
	}
	dataset := string(row) + "\n"
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
`, yamlString(agent), yamlString(name))
	return map[string]string{
		fmt.Sprintf("agents/%s/agent.yaml", agent): agentYAML,
		fmt.Sprintf("datasets/%s.jsonl", agent):    dataset,
		fmt.Sprintf("metric-sets/%s.yaml", agent):  metric,
	}
}

func stagedRequestFields(usable map[string]FieldEvidence) []string {
	if raw := usable["request_fields"].Value; raw != "" {
		var out []string
		for _, part := range strings.Split(raw, ",") {
			part = strings.TrimSpace(part)
			if part != "" {
				out = append(out, part)
			}
		}
		if len(out) > 0 {
			return out
		}
	}
	field := usable["request_field"].Value
	if field == "" {
		field = "text"
	}
	return []string{field}
}

func ensureSlash(path string) string {
	if path == "" {
		return "/"
	}
	if strings.HasPrefix(path, "/") {
		return path
	}
	return "/" + path
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

func yamlString(value string) string {
	return strconv.Quote(value)
}
