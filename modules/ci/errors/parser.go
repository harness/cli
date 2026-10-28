// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package errors

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	"go.yaml.in/yaml/v3"
)

var errRuleGroupEnabledDefault = true

func loadYAMLFromPath(path string) ([]byte, error) {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil, fmt.Errorf("errors YAML file not found at path: %s", path)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read errors YAML file: %w", err)
	}
	return content, nil
}

// validateYAML is a syntax-level check: parseable YAML, root is a map,
// and the two required top-level keys are present.
func validateYAML(yamlContent []byte) error {
	if len(yamlContent) == 0 {
		return fmt.Errorf("errors YAML file is empty")
	}

	var genericMap map[string]interface{}
	if err := yaml.Unmarshal(yamlContent, &genericMap); err != nil {
		errStr := err.Error()
		if strings.Contains(errStr, "cannot unmarshal !!seq") {
			trimmed := strings.TrimSpace(string(yamlContent))
			if strings.HasPrefix(trimmed, "-") {
				return fmt.Errorf("invalid YAML structure: file starts with a list item ('-') but root must be a map with 'version' and 'ruleGroups' keys")
			}
			return fmt.Errorf("invalid YAML structure: root element is a list/array but must be a map/object with 'version' and 'ruleGroups' keys")
		}
		return fmt.Errorf("invalid YAML syntax: %w", err)
	}

	if _, exists := genericMap["version"]; !exists {
		return fmt.Errorf("missing required 'version' key at root level of YAML file")
	}
	if _, exists := genericMap["ruleGroups"]; !exists {
		return fmt.Errorf("missing required 'ruleGroups' key at root level of YAML file")
	}
	return nil
}

func parseYAML(yamlPath string) (*rulesConfig, error) {
	yamlContent, err := loadYAMLFromPath(yamlPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load errors YAML file: %w", err)
	}
	if err := validateYAML(yamlContent); err != nil {
		return nil, fmt.Errorf("failed to validate errors YAML: %w", err)
	}

	var config rulesConfig
	if err := yaml.Unmarshal(yamlContent, &config); err != nil {
		return nil, fmt.Errorf("failed to parse errors YAML: %w", err)
	}
	if err := validateParsedRules(&config); err != nil {
		return nil, fmt.Errorf("failed to validate parsed rules: %w", err)
	}
	return &config, nil
}

// validateParsedRules checks structure beyond plain YAML syntax: required
// fields, known enums, action counts, and recurses into every condition tree.
// It also fills in defaults (evaluationMode, a group's Enabled pointer) in place.
func validateParsedRules(config *rulesConfig) error {
	if config == nil {
		return fmt.Errorf("config cannot be nil")
	}
	if config.Version == "" {
		return fmt.Errorf("version is required")
	}

	if config.Settings.EvaluationMode == "" {
		config.Settings.EvaluationMode = evaluationModeFirstMatch
	}
	if config.Settings.EvaluationMode != evaluationModeFirstMatch {
		return fmt.Errorf("settings.evaluationMode must be '%s', got '%s'", evaluationModeFirstMatch, config.Settings.EvaluationMode)
	}

	if len(config.RuleGroups) == 0 {
		return fmt.Errorf("at least one ruleGroup is required")
	}
	if len(config.RuleGroups) > maxRuleGroups {
		config.RuleGroups = config.RuleGroups[:maxRuleGroups]
	}

	for i := range config.RuleGroups {
		group := &config.RuleGroups[i]
		if group.Name == "" {
			return fmt.Errorf("ruleGroup at index %d has empty name", i)
		}
		if group.Enabled == nil {
			group.Enabled = &errRuleGroupEnabledDefault
		}
		if group.ConditionExpression == nil {
			return fmt.Errorf("ruleGroup '%s' at index %d is missing required 'conditionExpression' field", group.Name, i)
		}
		if err := validateConditionExpression(group.ConditionExpression, fmt.Sprintf("ruleGroup '%s'", group.Name), 0); err != nil {
			return err
		}
		if len(group.Actions) == 0 {
			return fmt.Errorf("ruleGroup '%s' at index %d is missing required 'actions' field", group.Name, i)
		}
		for j, action := range group.Actions {
			if action.Type == "" {
				return fmt.Errorf("ruleGroup '%s' action at index %d has empty type", group.Name, j)
			}
			if !validActionTypes[action.Type] {
				return fmt.Errorf("ruleGroup '%s' action at index %d has invalid type '%s', must be one of: %s, %s, %s",
					group.Name, j, action.Type, actionTypeSetErrorCategory, actionTypeSetErrorSubcategory, actionTypeSetErrorMessage)
			}
		}
	}
	return nil
}

func validateConditionExpression(expr *conditionExpr, context string, depth int) error {
	if depth > maxConditionDepth {
		return fmt.Errorf("%s exceeds maximum condition depth of %d", context, maxConditionDepth)
	}
	if expr == nil {
		return fmt.Errorf("%s conditionExpression is nil", context)
	}

	isLeaf := expr.Key != ""
	isNode := expr.Operator != "" && len(expr.Conditions) > 0

	if !isLeaf && !isNode {
		return fmt.Errorf("%s conditionExpression must be either a leaf condition (with key/operand/value) or a node (with operator and nested conditions)", context)
	}

	if isLeaf {
		if expr.Operator != "" {
			return fmt.Errorf("%s leaf condition cannot have operator", context)
		}
		if len(expr.Conditions) > 0 {
			return fmt.Errorf("%s leaf condition cannot have nested conditions", context)
		}
		return validateCondition(expr.Key, expr.Operand, expr.Value, context)
	}

	if expr.Operator != operatorAND && expr.Operator != operatorOR {
		return fmt.Errorf("%s conditionExpression has invalid operator '%s', must be '%s' or '%s'", context, expr.Operator, operatorAND, operatorOR)
	}
	if len(expr.Conditions) == 0 {
		return fmt.Errorf("%s conditionExpression with operator '%s' must have at least one nested condition", context, expr.Operator)
	}

	for i := range expr.Conditions {
		if err := validateConditionExpression(&expr.Conditions[i], fmt.Sprintf("%s nested condition at index %d", context, i), depth+1); err != nil {
			return err
		}
	}
	return nil
}

func validateCondition(key, operand string, value interface{}, context string) error {
	if key == "" {
		return fmt.Errorf("%s has empty key", context)
	}
	if !validFieldKeys[key] {
		return fmt.Errorf("%s has invalid key '%s', must be one of: %s, %s, %s, %s, %s, %s", context, key,
			fieldKeyStandardOutput, fieldKeyStandardErrorOutput, fieldKeyErrorCode, fieldKeyStepId, fieldKeyStageId, fieldKeyPipelineId)
	}
	if operand == "" {
		return fmt.Errorf("%s has empty operand", context)
	}
	if !validOperands[operand] {
		return fmt.Errorf("%s has invalid operand '%s', must be one of: %s, %s, %s, %s, %s", context, operand,
			operandContains, operandIs, operandRegex, operandIsNot, operandDoesNotMatch)
	}
	if value == nil {
		return fmt.Errorf("%s has nil value", context)
	}

	if operand == operandRegex {
		pattern, ok := value.(string)
		if !ok {
			return fmt.Errorf("%s with operand 'regex' must have string value, got %T", context, value)
		}
		if pattern == "" {
			return fmt.Errorf("%s with operand 'regex' must have non-empty pattern", context)
		}
		if len(pattern) > maxRegexPatternLength {
			return fmt.Errorf("%s regex pattern length %d exceeds maximum %d", context, len(pattern), maxRegexPatternLength)
		}
		if _, err := regexp.Compile(pattern); err != nil {
			return fmt.Errorf("%s has invalid regex pattern '%s': %w", context, pattern, err)
		}
	}

	if key == fieldKeyErrorCode {
		switch value.(type) {
		case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, float32, float64:
		default:
			return fmt.Errorf("%s with key '%s' must have numeric value, got %T", context, key, value)
		}
	} else {
		if _, ok := value.(string); !ok {
			return fmt.Errorf("%s with key '%s' must have string value, got %T", context, key, value)
		}
	}
	return nil
}
