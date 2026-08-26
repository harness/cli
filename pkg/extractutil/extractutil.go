// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package extractutil

import (
	"strconv"
	"time"

	"github.com/expr-lang/expr"

	"github.com/harness/cli/pkg/strutil"
)

type accessor struct {
	env map[string]any
	raw any
}

// MakeDataAccessor returns an accessor backed by expr-lang.
// baseEnv is the full expr environment (ctx, auth, flags, helpers); "it" is injected bound to data.
func MakeDataAccessor(baseEnv map[string]any, data any) *accessor {
	env := make(map[string]any, len(baseEnv)+1)
	for k, v := range baseEnv {
		env[k] = v
	}
	env["it"] = data
	return &accessor{env: env, raw: data}
}

func (a *accessor) GetData() any {
	return a.raw
}

func (a *accessor) eval(path string) any {
	program, err := expr.Compile(path, expr.Env(a.env), expr.AsAny())
	if err != nil {
		return nil
	}
	out, err := expr.Run(program, a.env)
	if err != nil {
		return nil
	}
	return out
}

func (a *accessor) GetString(path string) string {
	v := a.eval(path)
	if v == nil {
		return ""
	}
	s, ok := v.(string)
	if !ok {
		return strutil.Stringify(v)
	}
	return s
}

func (a *accessor) GetInt64(path string) int64 {
	v := a.eval(path)
	switch n := v.(type) {
	case float64:
		return int64(n)
	case int64:
		return n
	case int:
		return int64(n)
	}
	return 0
}

func (a *accessor) GetBool(path string) bool {
	v := a.eval(path)
	b, _ := v.(bool)
	return b
}

func (a *accessor) GetSlice(path string) []any {
	v := a.eval(path)
	if v == nil {
		return nil
	}
	s, _ := v.([]any)
	return s
}

func (a *accessor) GetTs(path string) string {
	v := a.eval(path)
	var ms int64
	switch n := v.(type) {
	case float64:
		ms = int64(n)
	case int64:
		ms = n
	case int:
		ms = int64(n)
	case string:
		parsed, err := strconv.ParseInt(n, 10, 64)
		if err != nil {
			return ""
		}
		ms = parsed
	default:
		return ""
	}
	return time.UnixMilli(ms).UTC().Format("2006-01-02 15:04:05")
}
