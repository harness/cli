// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package ci

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeYAML(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "errors.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	return path
}

func TestParseErrorsYAMLValid(t *testing.T) {
	path := writeYAML(t, `version: "1.0"
settings:
  evaluationMode: "firstMatch"
ruleGroups:
  - name: "Build Errors"
    enabled: true
    conditionExpression:
      operator: "AND"
      conditions:
        - key: "standardOutput"
          operand: "contains"
          value: "BUILD FAILED"
        - key: "errorCode"
          operand: "is"
          value: 1
    actions:
      - type: "setErrorCategory"
        value: "BuildFailure"
`)

	config, err := parseErrorsYAML(path)
	if err != nil {
		t.Fatalf("parseErrorsYAML: %v", err)
	}
	if config.Version != "1.0" || len(config.RuleGroups) != 1 {
		t.Fatalf("unexpected config: %+v", config)
	}
	if config.RuleGroups[0].Enabled == nil || !*config.RuleGroups[0].Enabled {
		t.Fatalf("expected enabled=true, got %+v", config.RuleGroups[0].Enabled)
	}
}

func TestParseErrorsYAMLDefaultsEnabledAndEvaluationMode(t *testing.T) {
	path := writeYAML(t, `version: "1.0"
ruleGroups:
  - name: "Test Errors"
    conditionExpression:
      key: "standardOutput"
      operand: "regex"
      value: ".*test.*failed.*"
    actions:
      - type: "setErrorCategory"
        value: "TestFailure"
`)

	config, err := parseErrorsYAML(path)
	if err != nil {
		t.Fatalf("parseErrorsYAML: %v", err)
	}
	if config.Settings.EvaluationMode != evaluationModeFirstMatch {
		t.Fatalf("got evaluationMode %q, want default %q", config.Settings.EvaluationMode, evaluationModeFirstMatch)
	}
	if config.RuleGroups[0].Enabled == nil || !*config.RuleGroups[0].Enabled {
		t.Fatalf("expected enabled to default to true, got %+v", config.RuleGroups[0].Enabled)
	}
}

func TestParseErrorsYAMLMissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "does-not-exist.yaml")
	if _, err := parseErrorsYAML(path); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("got %v, want a not-found error", err)
	}
}

func TestParseErrorsYAMLRootIsList(t *testing.T) {
	path := writeYAML(t, `- name: "oops"
`)
	_, err := parseErrorsYAML(path)
	if err == nil || !strings.Contains(err.Error(), "root must be a map") {
		t.Fatalf("got %v, want the friendly list-vs-map error", err)
	}
}

func TestParseErrorsYAMLMissingVersion(t *testing.T) {
	path := writeYAML(t, `ruleGroups:
  - name: "Test"
    conditionExpression:
      key: "standardOutput"
      operand: "contains"
      value: "error"
    actions:
      - type: "setErrorCategory"
        value: "X"
`)
	if _, err := parseErrorsYAML(path); err == nil || !strings.Contains(err.Error(), "missing required 'version'") {
		t.Fatalf("got %v, want a missing-version error", err)
	}
}

func TestParseErrorsYAMLMissingRuleGroups(t *testing.T) {
	path := writeYAML(t, `version: "1.0"
`)
	if _, err := parseErrorsYAML(path); err == nil || !strings.Contains(err.Error(), "missing required 'ruleGroups'") {
		t.Fatalf("got %v, want a missing-ruleGroups error", err)
	}
}

func TestParseErrorsYAMLInvalidFieldKey(t *testing.T) {
	path := writeYAML(t, `version: "1.0"
ruleGroups:
  - name: "Test"
    conditionExpression:
      key: "bogusField"
      operand: "contains"
      value: "x"
    actions:
      - type: "setErrorCategory"
        value: "X"
`)
	if _, err := parseErrorsYAML(path); err == nil || !strings.Contains(err.Error(), "invalid key") {
		t.Fatalf("got %v, want an invalid-key error", err)
	}
}

func TestParseErrorsYAMLInvalidOperand(t *testing.T) {
	path := writeYAML(t, `version: "1.0"
ruleGroups:
  - name: "Test"
    conditionExpression:
      key: "standardOutput"
      operand: "bogusOperand"
      value: "x"
    actions:
      - type: "setErrorCategory"
        value: "X"
`)
	if _, err := parseErrorsYAML(path); err == nil || !strings.Contains(err.Error(), "invalid operand") {
		t.Fatalf("got %v, want an invalid-operand error", err)
	}
}

func TestParseErrorsYAMLInvalidActionType(t *testing.T) {
	path := writeYAML(t, `version: "1.0"
ruleGroups:
  - name: "Test"
    conditionExpression:
      key: "standardOutput"
      operand: "contains"
      value: "x"
    actions:
      - type: "bogusAction"
        value: "X"
`)
	if _, err := parseErrorsYAML(path); err == nil || !strings.Contains(err.Error(), "invalid type") {
		t.Fatalf("got %v, want an invalid-action-type error", err)
	}
}

func TestParseErrorsYAMLRegexTooLong(t *testing.T) {
	longPattern := strings.Repeat("a", maxRegexPatternLength+1)
	path := writeYAML(t, `version: "1.0"
ruleGroups:
  - name: "Test"
    conditionExpression:
      key: "standardOutput"
      operand: "regex"
      value: "`+longPattern+`"
    actions:
      - type: "setErrorCategory"
        value: "X"
`)
	if _, err := parseErrorsYAML(path); err == nil || !strings.Contains(err.Error(), "exceeds maximum") {
		t.Fatalf("got %v, want a regex-length error", err)
	}
}

func TestParseErrorsYAMLInvalidRegex(t *testing.T) {
	path := writeYAML(t, `version: "1.0"
ruleGroups:
  - name: "Test"
    conditionExpression:
      key: "standardOutput"
      operand: "regex"
      value: "("
    actions:
      - type: "setErrorCategory"
        value: "X"
`)
	if _, err := parseErrorsYAML(path); err == nil || !strings.Contains(err.Error(), "invalid regex pattern") {
		t.Fatalf("got %v, want an invalid-regex error", err)
	}
}

func TestValidateConditionExpressionDepthExceeded(t *testing.T) {
	expr := &errorConditionExpr{Key: fieldKeyStandardOutput, Operand: operandContains, Value: "x"}
	for i := 0; i <= maxConditionDepth+1; i++ {
		expr = &errorConditionExpr{Operator: operatorAND, Conditions: []errorConditionExpr{*expr}}
	}
	if err := validateConditionExpression(expr, "root", 0); err == nil || !strings.Contains(err.Error(), "exceeds maximum condition depth") {
		t.Fatalf("got %v, want a depth-exceeded error", err)
	}
}

func TestValidateConditionErrorCodeRequiresNumericValue(t *testing.T) {
	if err := validateCondition(fieldKeyErrorCode, operandIs, "not-a-number", "root"); err == nil || !strings.Contains(err.Error(), "must have numeric value") {
		t.Fatalf("got %v, want a numeric-value error", err)
	}
	if err := validateCondition(fieldKeyErrorCode, operandIs, 1, "root"); err != nil {
		t.Fatalf("expected a numeric errorCode value to be valid, got %v", err)
	}
}

func TestValidateErrorsYAMLEmpty(t *testing.T) {
	if err := validateErrorsYAML(nil); err == nil || !strings.Contains(err.Error(), "empty") {
		t.Fatalf("got %v, want an empty-file error", err)
	}
}
