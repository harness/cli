// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package errors

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/harness/cli/v3/pkg/cmdctx"
)

const (
	ValidateHandlerID = "execute_error_validate"
	EvaluateHandlerID = "execute_error_evaluate"
)

// ValidateHandler is the `execute error:validate` workflow handler.
// It is a customer-facing, human-readable check of an errors.yaml file's syntax
// and structure — meant to be run manually or in CI before trusting the file in
// a real pipeline, unlike `error:evaluate` which lite-engine calls on every
// failed step.
func ValidateHandler(ctx *cmdctx.Ctx) error {
	if len(ctx.Args) > 0 {
		return fmt.Errorf("unexpected arguments: %s", strings.Join(ctx.Args, " "))
	}

	yamlPath := cmdctx.GetString(ctx.FlagValues, "yaml-path")

	if _, err := os.Stat(yamlPath); os.IsNotExist(err) {
		fmt.Println("✗ File not found:", yamlPath)
		return fmt.Errorf("file not found: %s", yamlPath)
	}

	yamlContent, err := loadYAMLFromPath(yamlPath)
	if err != nil {
		fmt.Println("✗ Failed to read file:", err)
		return err
	}

	if err := validateYAML(yamlContent); err != nil {
		fmt.Println("✗ YAML syntax invalid:")
		fmt.Println(" ", err)
		return err
	}
	fmt.Println("✓ YAML syntax valid")

	config, err := parseYAML(yamlPath)
	if err != nil {
		fmt.Println("✗ Validation failed:")
		fmt.Println(" ", err)
		return err
	}

	fmt.Println("✓ Version:", config.Version)

	enabledCount, disabledCount := 0, 0
	for _, group := range config.RuleGroups {
		if group.Enabled != nil && !*group.Enabled {
			disabledCount++
		} else {
			enabledCount++
		}
	}
	fmt.Printf("✓ Rule groups: %d (%d enabled, %d disabled)\n", len(config.RuleGroups), enabledCount, disabledCount)

	conditionCount := 0
	actionCount := 0
	for _, group := range config.RuleGroups {
		conditionCount += countConditionsInExpression(group.ConditionExpression)
		actionCount += len(group.Actions)
	}
	fmt.Printf("✓ Total conditions: %d\n", conditionCount)
	fmt.Printf("✓ Total actions: %d\n", actionCount)

	fmt.Println("\nRule groups:")
	for i, group := range config.RuleGroups {
		status := "enabled"
		if group.Enabled != nil && !*group.Enabled {
			status = "disabled"
		}
		fmt.Printf("  %d. %s (%s)\n", i+1, group.Name, status)
	}

	fmt.Println("\n✓ Validation successful!")
	return nil
}

func countConditionsInExpression(expr *conditionExpr) int {
	if expr == nil {
		return 0
	}
	if expr.Key != "" {
		return 1
	}
	count := 0
	for i := range expr.Conditions {
		count += countConditionsInExpression(&expr.Conditions[i])
	}
	return count
}

// EvaluateHandler is the `execute error:evaluate` workflow handler.
// It is intended for internal use by lite-engine on every failed step, so it
// never hard-fails the CLI call: any internal error (parse failure, timeout)
// still prints a JSON result and returns nil.
func EvaluateHandler(ctx *cmdctx.Ctx) error {
	if len(ctx.Args) > 0 {
		return fmt.Errorf("unexpected arguments: %s", strings.Join(ctx.Args, " "))
	}

	result := evaluationResult{Matched: false}

	yamlPath := cmdctx.GetString(ctx.FlagValues, "yaml-path")
	cacheDir := cmdctx.GetString(ctx.FlagValues, "cache-dir")

	config, err := getOrParseRules(yamlPath, cacheDir)
	if err != nil {
		result.Error = err.Error()
		printEvaluationResult(result)
		return nil
	}
	if config == nil {
		printEvaluationResult(result)
		return nil
	}

	result.RuleCount = len(config.RuleGroups)

	stepCtx := &stepContext{
		StdoutPath: cmdctx.GetString(ctx.FlagValues, "stdout-path"),
		StderrPath: cmdctx.GetString(ctx.FlagValues, "stderr-path"),
		ExitCode:   cmdctx.GetInt(ctx.FlagValues, "exit-code"),
		StepID:     cmdctx.GetString(ctx.FlagValues, "step-id"),
		StageID:    cmdctx.GetString(ctx.FlagValues, "stage-id"),
		PipelineID: cmdctx.GetString(ctx.FlagValues, "pipeline-id"),
	}

	evalCtx, cancel := context.WithTimeout(context.Background(), evaluationTimeout)
	defer cancel()

	categorization, err := evaluateRules(evalCtx, config, stepCtx)
	if err != nil {
		if errors.Is(evalCtx.Err(), context.DeadlineExceeded) {
			result.TimedOut = true
		}
		printEvaluationResult(result)
		return nil
	}

	if categorization != nil {
		result.Matched = true
		result.Category = strings.ToUpper(categorization.Category)
		result.Subcategory = strings.ToUpper(categorization.Subcategory)
		result.Message = categorization.Message
		result.MatchedRule = categorization.MatchedRule
		result.Source = categorization.Source
	}

	printEvaluationResult(result)
	return nil
}

func printEvaluationResult(result evaluationResult) {
	data, err := json.Marshal(result)
	if err != nil {
		fmt.Printf(`{"matched":false,"error":"failed to marshal result: %s"}`, err.Error())
		fmt.Println()
		return
	}
	fmt.Println(string(data))
}
