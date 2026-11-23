// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package errors

import "time"

// Field keys for condition matching.
const (
	fieldKeyStandardOutput      = "standardOutput"      // Step stdout
	fieldKeyStandardErrorOutput = "standardErrorOutput" // Step stderr
	fieldKeyErrorCode           = "errorCode"           // Exit code
	fieldKeyStepId              = "stepId"              // Step identifier
	fieldKeyStageId             = "stageId"             // Stage identifier
	fieldKeyPipelineId          = "pipelineId"          // Pipeline identifier
)

// Operands for condition matching.
const (
	operandContains     = "contains"     // Case-insensitive substring match
	operandIs           = "is"           // Exact match (case-sensitive)
	operandRegex        = "regex"        // Regular expression match
	operandIsNot        = "isNot"        // Negation of is (case-sensitive)
	operandDoesNotMatch = "doesNotMatch" // Negation of contains (case-insensitive)
)

// Operators for condition expressions.
const (
	operatorAND = "AND"
	operatorOR  = "OR"
)

// Action types for rule actions.
const (
	actionTypeSetErrorCategory    = "setErrorCategory"
	actionTypeSetErrorSubcategory = "setErrorSubcategory"
	actionTypeSetErrorMessage     = "setErrorMessage"
)

const evaluationModeFirstMatch = "firstMatch"

const sourceCustom = "custom" // User-defined rules from errors.yaml

// Guardrail constants for error categorization — these keep a customer-authored
// errors.yaml from becoming a ReDoS/OOM/hang vector during evaluate.
const (
	evaluationTimeout     = 4 * time.Second
	maxRuleGroups         = 100
	maxConditionDepth     = 20
	maxRegexPatternLength = 1000
	maxLineSize           = 64 * 1024
	maxScannerBufSize     = 512 * 1024
	maxLogFileSize        = 25 * 1024 * 1024
	timeoutCheckInterval  = 10000
)

var validFieldKeys = map[string]bool{
	fieldKeyStandardOutput:      true,
	fieldKeyStandardErrorOutput: true,
	fieldKeyErrorCode:           true,
	fieldKeyStepId:              true,
	fieldKeyStageId:             true,
	fieldKeyPipelineId:          true,
}

var validOperands = map[string]bool{
	operandContains:     true,
	operandIs:           true,
	operandRegex:        true,
	operandIsNot:        true,
	operandDoesNotMatch: true,
}

var validActionTypes = map[string]bool{
	actionTypeSetErrorCategory:    true,
	actionTypeSetErrorSubcategory: true,
	actionTypeSetErrorMessage:     true,
}

// rulesConfig is the root structure of an errors.yaml file.
type rulesConfig struct {
	Version    string        `yaml:"version" json:"version"`
	Settings   rulesSettings `yaml:"settings,omitempty" json:"settings,omitempty"`
	RuleGroups []ruleGroup   `yaml:"ruleGroups" json:"ruleGroups"`
}

type rulesSettings struct {
	EvaluationMode string `yaml:"evaluationMode,omitempty" json:"evaluationMode,omitempty"`
}

type ruleGroup struct {
	Name                string         `yaml:"name" json:"name"`
	Enabled             *bool          `yaml:"enabled,omitempty" json:"enabled,omitempty"`
	ConditionExpression *conditionExpr `yaml:"conditionExpression" json:"conditionExpression"`
	Actions             []ruleAction   `yaml:"actions" json:"actions"`
}

// conditionExpr is a recursive AND/OR tree: a node has Operator+Conditions,
// a leaf has Key/Operand/Value. Never both on the same node.
type conditionExpr struct {
	Operator   string          `yaml:"operator" json:"operator,omitempty"`
	Conditions []conditionExpr `yaml:"conditions" json:"conditions,omitempty"`
	Key        string          `yaml:"key,omitempty" json:"key,omitempty"`
	Operand    string          `yaml:"operand,omitempty" json:"operand,omitempty"`
	Value      interface{}     `yaml:"value,omitempty" json:"value,omitempty"`
}

type ruleAction struct {
	Type  string `yaml:"type" json:"type"`
	Value string `yaml:"value" json:"value"`
}

// stepContext is the execution context an evaluate call checks rules against.
type stepContext struct {
	StdoutPath string
	StderrPath string
	ExitCode   int
	StepID     string
	StageID    string
	PipelineID string
}

// categorization is the internal result of a matched rule group.
type categorization struct {
	Category    string
	Subcategory string
	Message     string
	MatchedRule string
	Source      string
}

// evaluationResult is the JSON printed by `execute error:evaluate`.
type evaluationResult struct {
	Matched     bool   `json:"matched"`
	Category    string `json:"category,omitempty"`
	Subcategory string `json:"subcategory,omitempty"`
	Message     string `json:"message,omitempty"`
	MatchedRule string `json:"matched_rule,omitempty"`
	Source      string `json:"source,omitempty"`
	RuleCount   int    `json:"rule_count,omitempty"`
	TimedOut    bool   `json:"timed_out,omitempty"`
	Error       string `json:"error,omitempty"`
}
