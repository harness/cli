// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package ci

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeLogFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "log.txt")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	return path
}

func TestEvaluateErrorRulesANDMatchesOnlyWhenBothTrue(t *testing.T) {
	stdoutPath := writeLogFile(t, "line one\nBUILD FAILED\nline three\n")
	config := &errorRulesConfig{RuleGroups: []errorRuleGroup{
		{
			Name:    "Build Errors",
			Enabled: &errRuleGroupEnabledDefault,
			ConditionExpression: &errorConditionExpr{
				Operator: operatorAND,
				Conditions: []errorConditionExpr{
					{Key: fieldKeyStandardOutput, Operand: operandContains, Value: "BUILD FAILED"},
					{Key: fieldKeyErrorCode, Operand: operandIs, Value: 1},
				},
			},
			Actions: []errorAction{{Type: actionTypeSetErrorCategory, Value: "BuildFailure"}},
		},
	}}

	stepCtx := &errorStepContext{StdoutPath: stdoutPath, ExitCode: 1}
	cat, err := evaluateErrorRules(context.Background(), config, stepCtx)
	if err != nil {
		t.Fatalf("evaluateErrorRules: %v", err)
	}
	if cat == nil || cat.Category != "BuildFailure" || cat.MatchedRule != "Build Errors" {
		t.Fatalf("got %+v, want a BuildFailure match", cat)
	}

	// Same stdout but a different exit code: AND must fail.
	stepCtx2 := &errorStepContext{StdoutPath: stdoutPath, ExitCode: 2}
	cat2, err := evaluateErrorRules(context.Background(), config, stepCtx2)
	if err != nil {
		t.Fatalf("evaluateErrorRules: %v", err)
	}
	if cat2 != nil {
		t.Fatalf("got a match %+v, want no match when exit code differs", cat2)
	}
}

func TestEvaluateErrorRulesORMatchesOnEither(t *testing.T) {
	stdoutPath := writeLogFile(t, "nothing interesting\n")
	config := &errorRulesConfig{RuleGroups: []errorRuleGroup{
		{
			Name:    "OOM or Timeout",
			Enabled: &errRuleGroupEnabledDefault,
			ConditionExpression: &errorConditionExpr{
				Operator: operatorOR,
				Conditions: []errorConditionExpr{
					{Key: fieldKeyStandardOutput, Operand: operandContains, Value: "out of memory"},
					{Key: fieldKeyErrorCode, Operand: operandIs, Value: 137},
				},
			},
			Actions: []errorAction{{Type: actionTypeSetErrorCategory, Value: "Infra"}},
		},
	}}

	stepCtx := &errorStepContext{StdoutPath: stdoutPath, ExitCode: 137}
	cat, err := evaluateErrorRules(context.Background(), config, stepCtx)
	if err != nil {
		t.Fatalf("evaluateErrorRules: %v", err)
	}
	if cat == nil || cat.Category != "Infra" {
		t.Fatalf("got %+v, want an Infra match via the OR's errorCode branch", cat)
	}
}

func TestEvaluateErrorRulesSkipsDisabledGroup(t *testing.T) {
	stdoutPath := writeLogFile(t, "BUILD FAILED\n")
	disabled := false
	config := &errorRulesConfig{RuleGroups: []errorRuleGroup{
		{
			Name:                "Disabled",
			Enabled:             &disabled,
			ConditionExpression: &errorConditionExpr{Key: fieldKeyStandardOutput, Operand: operandContains, Value: "BUILD FAILED"},
			Actions:             []errorAction{{Type: actionTypeSetErrorCategory, Value: "ShouldNotMatch"}},
		},
	}}

	cat, err := evaluateErrorRules(context.Background(), config, &errorStepContext{StdoutPath: stdoutPath})
	if err != nil {
		t.Fatalf("evaluateErrorRules: %v", err)
	}
	if cat != nil {
		t.Fatalf("got %+v, want a disabled group to never match", cat)
	}
}

func TestEvaluateErrorRulesFirstMatchWinsAcrossGroups(t *testing.T) {
	stdoutPath := writeLogFile(t, "BUILD FAILED\n")
	config := &errorRulesConfig{RuleGroups: []errorRuleGroup{
		{
			Name:                "First",
			Enabled:             &errRuleGroupEnabledDefault,
			ConditionExpression: &errorConditionExpr{Key: fieldKeyStandardOutput, Operand: operandContains, Value: "BUILD FAILED"},
			Actions:             []errorAction{{Type: actionTypeSetErrorCategory, Value: "FirstMatch"}},
		},
		{
			Name:                "Second",
			Enabled:             &errRuleGroupEnabledDefault,
			ConditionExpression: &errorConditionExpr{Key: fieldKeyStandardOutput, Operand: operandContains, Value: "BUILD FAILED"},
			Actions:             []errorAction{{Type: actionTypeSetErrorCategory, Value: "SecondMatch"}},
		},
	}}

	cat, err := evaluateErrorRules(context.Background(), config, &errorStepContext{StdoutPath: stdoutPath})
	if err != nil {
		t.Fatalf("evaluateErrorRules: %v", err)
	}
	if cat == nil || cat.Category != "FirstMatch" {
		t.Fatalf("got %+v, want the first rule group to win", cat)
	}
}

func TestEvaluateErrorRulesMissingLogFileDegradesToEmptyContent(t *testing.T) {
	config := &errorRulesConfig{RuleGroups: []errorRuleGroup{
		{
			Name:                "Needs stdout",
			Enabled:             &errRuleGroupEnabledDefault,
			ConditionExpression: &errorConditionExpr{Key: fieldKeyStandardOutput, Operand: operandDoesNotMatch, Value: "anything"},
			Actions:             []errorAction{{Type: actionTypeSetErrorCategory, Value: "Matched"}},
		},
	}}

	stepCtx := &errorStepContext{StdoutPath: filepath.Join(t.TempDir(), "does-not-exist.log")}
	cat, err := evaluateErrorRules(context.Background(), config, stepCtx)
	if err != nil {
		t.Fatalf("evaluateErrorRules should not error on a missing log file: %v", err)
	}
	if cat == nil {
		t.Fatal("doesNotMatch against empty content should be true, expected a match")
	}
}

func TestEvaluateErrorRulesMetadataConditionsDoNotTouchFiles(t *testing.T) {
	config := &errorRulesConfig{RuleGroups: []errorRuleGroup{
		{
			Name:                "By step id",
			Enabled:             &errRuleGroupEnabledDefault,
			ConditionExpression: &errorConditionExpr{Key: fieldKeyStepId, Operand: operandIs, Value: "build-step"},
			Actions:             []errorAction{{Type: actionTypeSetErrorCategory, Value: "StepMatch"}},
		},
	}}

	cat, err := evaluateErrorRules(context.Background(), config, &errorStepContext{StepID: "build-step"})
	if err != nil {
		t.Fatalf("evaluateErrorRules: %v", err)
	}
	if cat == nil || cat.Category != "StepMatch" {
		t.Fatalf("got %+v, want a step-id match with no log paths set", cat)
	}
}

func TestEvaluateErrorExpressionFromCacheDepthExceeded(t *testing.T) {
	expr := &errorConditionExpr{Key: fieldKeyStepId, Operand: operandIs, Value: "x"}
	for i := 0; i <= maxConditionDepth+1; i++ {
		expr = &errorConditionExpr{Operator: operatorAND, Conditions: []errorConditionExpr{*expr}}
	}
	cache := newErrorConditionResultCache()
	_, err := evaluateErrorExpressionFromCache(context.Background(), expr, &errorStepContext{}, cache, 0)
	if err == nil {
		t.Fatal("expected a depth-exceeded error")
	}
}

func TestEvaluateErrorExpressionFromCacheRespectsTimeout(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Nanosecond)
	defer cancel()
	time.Sleep(time.Millisecond)

	expr := &errorConditionExpr{Key: fieldKeyStepId, Operand: operandIs, Value: "x"}
	cache := newErrorConditionResultCache()
	_, err := evaluateErrorExpressionFromCache(ctx, expr, &errorStepContext{}, cache, 0)
	if err == nil {
		t.Fatal("expected a timeout error once the context deadline has passed")
	}
}

func TestEvaluateErrorIntConditionTypeCoercion(t *testing.T) {
	tests := []struct {
		name  string
		value interface{}
		want  bool
	}{
		{"int", 137, true},
		{"int64", int64(137), true},
		{"float64", float64(137), true},
		{"string", "137", true},
		{"mismatched string", "138", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := evaluateErrorIntCondition(operandIs, 137, tt.value)
			if err != nil {
				t.Fatalf("evaluateErrorIntCondition: %v", err)
			}
			if got != tt.want {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestEvaluateErrorIntConditionRejectsContains(t *testing.T) {
	if _, err := evaluateErrorIntCondition(operandContains, 1, "1"); err == nil {
		t.Fatal("expected an error: 'contains' is not supported for errorCode")
	}
}
