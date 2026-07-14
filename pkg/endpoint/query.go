// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package endpoint

import (
	"fmt"

	"github.com/harness/cli/pkg/cmdctx"
	"github.com/harness/cli/pkg/spec"
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
