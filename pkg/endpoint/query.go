// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package endpoint

import (
	"fmt"

	"github.com/harness/cli/v3/pkg/cmdctx"
	"github.com/harness/cli/v3/pkg/exprenv"
	"github.com/harness/cli/v3/pkg/spec"
)

// ApplyQueryParamsFn calls ep.QueryParamsFn (if set) and merges the returned
// params into qp, overwriting any existing keys with non-empty values.
func ApplyQueryParamsFn(ctx *cmdctx.Ctx, ep *spec.EndpointSpec, qp map[string]string) error {
	if ep.QueryParamsFn == "" {
		return nil
	}
	if ctx.Resolver == nil {
		return fmt.Errorf("query_params_fn %q declared but no resolver available", ep.QueryParamsFn)
	}
	fn := ctx.Resolver.ResolveQueryParamsFn(ep.QueryParamsFn)
	if fn == nil {
		return fmt.Errorf("query_params_fn %q not registered", ep.QueryParamsFn)
	}
	extra, err := fn(ctx)
	if err != nil {
		return fmt.Errorf("query_params_fn %q: %w", ep.QueryParamsFn, err)
	}
	for k, v := range extra {
		if v != "" {
			qp[k] = v
		}
	}
	return nil
}

// EvalQueryParams evaluates query_params expressions. A list result becomes a repeated key
// (multi); anything else keeps the single-value string form. Nil, empty and all-blank
// results omit the key.
func EvalQueryParams(env map[string]any, exprs map[string]string) (single map[string]string, multi map[string][]string) {
	single = map[string]string{}
	for name, exprStr := range exprs {
		out, ok := exprenv.EvalExprAny(env, exprStr)
		if !ok {
			continue
		}
		if list, isList := asList(out); isList {
			var vals []string
			for _, item := range list {
				if s := exprenv.ResultString(item); s != "" {
					vals = append(vals, s)
				}
			}
			if len(vals) > 0 {
				if multi == nil {
					multi = map[string][]string{}
				}
				multi[name] = vals
			}
			continue
		}
		if s := exprenv.ResultString(out); s != "" {
			single[name] = s
		}
	}
	return single, multi
}

func asList(v any) ([]any, bool) {
	switch t := v.(type) {
	case []any:
		return t, true
	case []string:
		out := make([]any, len(t))
		for i, s := range t {
			out[i] = s
		}
		return out, true
	}
	return nil, false
}
