// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package errors

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
)

var scannerBufPool = sync.Pool{
	New: func() interface{} {
		buf := make([]byte, maxLineSize)
		return &buf
	},
}

type logCondition struct {
	operand    string
	value      string
	cacheKey   string
	regex      *regexp.Regexp
	valueLower string
}

type conditionResultCache struct {
	mu      sync.Mutex
	results map[string]bool
}

func newConditionResultCache() *conditionResultCache {
	return &conditionResultCache{results: make(map[string]bool)}
}

func (c *conditionResultCache) set(key string, value bool) {
	c.mu.Lock()
	c.results[key] = value
	c.mu.Unlock()
}

func (c *conditionResultCache) get(key string) (bool, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	v, ok := c.results[key]
	return v, ok
}

func conditionCacheKey(fieldKey, operand, value string) string {
	return fieldKey + ":" + operand + ":" + value
}

// evaluateRules is the 3-phase orchestrator: collect which log conditions
// are even needed, scan stdout/stderr once each to resolve them, then walk each
// enabled rule group's condition tree and apply the first matching group's
// actions. Groups are evaluated in file order — first match wins.
func evaluateRules(ctx context.Context, config *rulesConfig, stepCtx *stepContext) (*categorization, error) {
	if config == nil {
		return nil, fmt.Errorf("rules config is nil")
	}

	if len(config.RuleGroups) > maxRuleGroups {
		config.RuleGroups = config.RuleGroups[:maxRuleGroups]
	}

	cache := newConditionResultCache()

	stdoutSeen := make(map[string]bool)
	stderrSeen := make(map[string]bool)
	var stdoutConds, stderrConds []logCondition

	for i := range config.RuleGroups {
		group := &config.RuleGroups[i]
		if group.Enabled != nil && !*group.Enabled {
			continue
		}
		collectLogConditions(group.ConditionExpression, &stdoutConds, &stderrConds, stdoutSeen, stderrSeen)
	}

	if len(stdoutConds) > 0 || len(stderrConds) > 0 {
		scanLogFiles(ctx, stepCtx, stdoutConds, stderrConds, cache)
	}

	for i := range config.RuleGroups {
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("evaluation timed out")
		default:
		}

		group := &config.RuleGroups[i]
		if group.Enabled != nil && !*group.Enabled {
			continue
		}

		match, err := evaluateExpressionFromCache(ctx, group.ConditionExpression, stepCtx, cache, 0)
		if err != nil {
			continue
		}

		if match {
			categorization := &categorization{
				MatchedRule: group.Name,
				Source:      sourceCustom,
			}
			for _, action := range group.Actions {
				switch action.Type {
				case actionTypeSetErrorCategory:
					categorization.Category = action.Value
				case actionTypeSetErrorSubcategory:
					categorization.Subcategory = action.Value
				case actionTypeSetErrorMessage:
					categorization.Message = action.Value
				}
			}
			return categorization, nil
		}
	}

	return nil, nil
}

func collectLogConditions(expr *conditionExpr, stdout, stderr *[]logCondition, stdoutSeen, stderrSeen map[string]bool) {
	if expr == nil {
		return
	}

	if expr.Key != "" {
		if expr.Key != fieldKeyStandardOutput && expr.Key != fieldKeyStandardErrorOutput {
			return
		}

		valueStr, ok := expr.Value.(string)
		if !ok {
			return
		}

		cKey := conditionCacheKey(expr.Key, expr.Operand, valueStr)
		cond := logCondition{operand: expr.Operand, value: valueStr, cacheKey: cKey}

		if expr.Operand == operandRegex {
			if len(valueStr) <= maxRegexPatternLength {
				if re, err := regexp.Compile(valueStr); err == nil {
					cond.regex = re
				}
			}
		}
		if expr.Operand == operandContains || expr.Operand == operandDoesNotMatch {
			cond.valueLower = strings.ToLower(valueStr)
		}

		if expr.Key == fieldKeyStandardOutput {
			if !stdoutSeen[cKey] {
				stdoutSeen[cKey] = true
				*stdout = append(*stdout, cond)
			}
		} else {
			if !stderrSeen[cKey] {
				stderrSeen[cKey] = true
				*stderr = append(*stderr, cond)
			}
		}
		return
	}

	for i := range expr.Conditions {
		collectLogConditions(&expr.Conditions[i], stdout, stderr, stdoutSeen, stderrSeen)
	}
}

func scanLogFiles(ctx context.Context, stepCtx *stepContext, stdoutConds, stderrConds []logCondition, cache *conditionResultCache) {
	needsStdout := len(stdoutConds) > 0
	needsStderr := len(stderrConds) > 0

	if needsStdout && needsStderr {
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			scanLogFileForConditions(ctx, stepCtx.StdoutPath, stdoutConds, cache)
		}()
		go func() {
			defer wg.Done()
			scanLogFileForConditions(ctx, stepCtx.StderrPath, stderrConds, cache)
		}()
		wg.Wait()
		return
	}

	if needsStdout {
		scanLogFileForConditions(ctx, stepCtx.StdoutPath, stdoutConds, cache)
		return
	}
	scanLogFileForConditions(ctx, stepCtx.StderrPath, stderrConds, cache)
}

func scanLogFileForConditions(ctx context.Context, filePath string, conditions []logCondition, cache *conditionResultCache) {
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		for _, cond := range conditions {
			result, _ := evaluateEmptyContent(cond.operand, cond.value)
			cache.set(cond.cacheKey, result)
		}
		return
	}

	file, err := os.Open(filePath)
	if err != nil {
		for _, cond := range conditions {
			result, _ := evaluateEmptyContent(cond.operand, cond.value)
			cache.set(cond.cacheKey, result)
		}
		return
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return
	}

	tailSeeked := false
	if info.Size() > maxLogFileSize {
		seekOffset := info.Size() - maxLogFileSize
		if _, err := file.Seek(seekOffset, io.SeekStart); err != nil {
			return
		}
		tailSeeked = true
	}

	bufPtr := scannerBufPool.Get().(*[]byte)
	defer scannerBufPool.Put(bufPtr)

	scanner := bufio.NewScanner(file)
	scanner.Buffer(*bufPtr, maxScannerBufSize)

	if tailSeeked {
		scanner.Scan() // discard partial first line
	}

	type condState struct {
		cond     logCondition
		resolved bool
		result   bool
	}

	states := make([]condState, len(conditions))
	unresolved := 0

	for i, cond := range conditions {
		states[i] = condState{cond: cond}

		if cond.operand == operandRegex && cond.regex == nil {
			cache.set(cond.cacheKey, false)
			states[i].resolved = true
			continue
		}

		switch cond.operand {
		case operandIsNot, operandDoesNotMatch:
			states[i].result = true
		default:
			states[i].result = false
		}
		unresolved++
	}

	if unresolved == 0 {
		return
	}

	linesScanned := 0

	for scanner.Scan() {
		if linesScanned > 0 && linesScanned%timeoutCheckInterval == 0 {
			select {
			case <-ctx.Done():
				for i := range states {
					if !states[i].resolved {
						cache.set(states[i].cond.cacheKey, states[i].result)
					}
				}
				return
			default:
			}
		}

		line := scanner.Text()
		linesScanned++

		if len(line) > maxLineSize {
			line = line[:maxLineSize]
		}

		lineLowerComputed := false
		var lineLower string

		for i := range states {
			if states[i].resolved {
				continue
			}

			switch states[i].cond.operand {
			case operandContains:
				if !lineLowerComputed {
					lineLower = strings.ToLower(line)
					lineLowerComputed = true
				}
				if strings.Contains(lineLower, states[i].cond.valueLower) {
					states[i].result = true
					states[i].resolved = true
					unresolved--
				}

			case operandIs:
				if line == states[i].cond.value {
					states[i].result = true
					states[i].resolved = true
					unresolved--
				}

			case operandRegex:
				if states[i].cond.regex.MatchString(line) {
					states[i].result = true
					states[i].resolved = true
					unresolved--
				}

			case operandIsNot:
				if line == states[i].cond.value {
					states[i].result = false
					states[i].resolved = true
					unresolved--
				}

			case operandDoesNotMatch:
				if !lineLowerComputed {
					lineLower = strings.ToLower(line)
					lineLowerComputed = true
				}
				if strings.Contains(lineLower, states[i].cond.valueLower) {
					states[i].result = false
					states[i].resolved = true
					unresolved--
				}
			}
		}

		if unresolved == 0 {
			break
		}
	}

	for i := range states {
		cache.set(states[i].cond.cacheKey, states[i].result)
	}
}

func evaluateExpressionFromCache(ctx context.Context, expr *conditionExpr, stepCtx *stepContext, cache *conditionResultCache, depth int) (bool, error) {
	select {
	case <-ctx.Done():
		return false, fmt.Errorf("evaluation timed out at depth %d", depth)
	default:
	}

	if depth > maxConditionDepth {
		return false, fmt.Errorf("condition depth %d exceeds maximum %d", depth, maxConditionDepth)
	}
	if expr == nil {
		return false, fmt.Errorf("condition expression is nil")
	}

	if expr.Key != "" {
		return evaluateLeafFromCache(expr.Key, expr.Operand, expr.Value, stepCtx, cache)
	}

	if expr.Operator != "" && len(expr.Conditions) > 0 {
		switch expr.Operator {
		case operatorAND:
			for i, nested := range expr.Conditions {
				match, err := evaluateExpressionFromCache(ctx, &nested, stepCtx, cache, depth+1)
				if err != nil {
					return false, fmt.Errorf("AND condition index %d: %w", i, err)
				}
				if !match {
					return false, nil
				}
			}
			return true, nil

		case operatorOR:
			for _, nested := range expr.Conditions {
				match, err := evaluateExpressionFromCache(ctx, &nested, stepCtx, cache, depth+1)
				if err != nil {
					continue
				}
				if match {
					return true, nil
				}
			}
			return false, nil

		default:
			return false, fmt.Errorf("unknown operator: %s", expr.Operator)
		}
	}

	return false, fmt.Errorf("invalid expression")
}

func evaluateLeafFromCache(key, operand string, value interface{}, stepCtx *stepContext, cache *conditionResultCache) (bool, error) {
	if key == fieldKeyStandardOutput || key == fieldKeyStandardErrorOutput {
		valueStr, ok := value.(string)
		if !ok {
			return false, fmt.Errorf("rule value must be string for field '%s', got %T", key, value)
		}
		cKey := conditionCacheKey(key, operand, valueStr)
		result, exists := cache.get(cKey)
		if !exists {
			return false, nil
		}
		return result, nil
	}

	switch key {
	case fieldKeyErrorCode:
		return evaluateIntCondition(operand, stepCtx.ExitCode, value)
	case fieldKeyStepId:
		return evaluateStringCondition(operand, stepCtx.StepID, value)
	case fieldKeyStageId:
		return evaluateStringCondition(operand, stepCtx.StageID, value)
	case fieldKeyPipelineId:
		return evaluateStringCondition(operand, stepCtx.PipelineID, value)
	default:
		return false, fmt.Errorf("unknown field key: %s", key)
	}
}

func evaluateStringCondition(operand, fieldValue string, ruleValue interface{}) (bool, error) {
	ruleValueStr, ok := ruleValue.(string)
	if !ok {
		return false, fmt.Errorf("rule value must be string, got %T", ruleValue)
	}

	switch operand {
	case operandContains:
		return strings.Contains(strings.ToLower(fieldValue), strings.ToLower(ruleValueStr)), nil
	case operandIs:
		return fieldValue == ruleValueStr, nil
	case operandRegex:
		if len(ruleValueStr) > maxRegexPatternLength {
			return false, fmt.Errorf("regex pattern length exceeds maximum")
		}
		re, err := regexp.Compile(ruleValueStr)
		if err != nil {
			return false, fmt.Errorf("invalid regex pattern: %w", err)
		}
		return re.MatchString(fieldValue), nil
	case operandIsNot:
		return fieldValue != ruleValueStr, nil
	case operandDoesNotMatch:
		return !strings.Contains(strings.ToLower(fieldValue), strings.ToLower(ruleValueStr)), nil
	default:
		return false, fmt.Errorf("unknown operand: %s", operand)
	}
}

func evaluateIntCondition(operand string, fieldValue int, ruleValue interface{}) (bool, error) {
	if operand == operandRegex {
		ruleValueStr, ok := ruleValue.(string)
		if !ok {
			return false, fmt.Errorf("regex operand requires string pattern")
		}
		if len(ruleValueStr) > maxRegexPatternLength {
			return false, fmt.Errorf("regex pattern length exceeds maximum")
		}
		re, err := regexp.Compile(ruleValueStr)
		if err != nil {
			return false, fmt.Errorf("invalid regex pattern: %w", err)
		}
		return re.MatchString(strconv.Itoa(fieldValue)), nil
	}

	var ruleValueInt int
	switch v := ruleValue.(type) {
	case int:
		ruleValueInt = v
	case int64:
		ruleValueInt = int(v)
	case float64:
		ruleValueInt = int(v)
	case string:
		parsed, err := strconv.Atoi(v)
		if err != nil {
			return false, fmt.Errorf("rule value must be numeric for errorCode, got string: %s", v)
		}
		ruleValueInt = parsed
	default:
		return false, fmt.Errorf("rule value must be numeric for errorCode, got %T", ruleValue)
	}

	switch operand {
	case operandIs:
		return fieldValue == ruleValueInt, nil
	case operandIsNot:
		return fieldValue != ruleValueInt, nil
	case operandContains, operandDoesNotMatch:
		return false, fmt.Errorf("operand '%s' is not supported for errorCode", operand)
	default:
		return false, fmt.Errorf("unknown operand: %s", operand)
	}
}

func evaluateEmptyContent(operand, value string) (bool, error) {
	switch operand {
	case operandContains:
		return strings.Contains("", strings.ToLower(value)), nil
	case operandIs:
		return value == "", nil
	case operandRegex:
		re, err := regexp.Compile(value)
		if err != nil {
			return false, err
		}
		return re.MatchString(""), nil
	case operandIsNot:
		return value != "", nil
	case operandDoesNotMatch:
		return !strings.Contains("", strings.ToLower(value)), nil
	default:
		return false, fmt.Errorf("unknown operand: %s", operand)
	}
}
