// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package rt

import (
	"strings"

	"github.com/harness/cli/v3/pkg/cmdctx"
)

const loadTestFilterParamsID = "loadtest_filters"

// Not a query_params expression: a list flag formats as the literal "[]" when unset, which
// the route reads as a tag nothing carries and answers with an empty page.
func loadTestFilterParams(ctx *cmdctx.Ctx) (map[string]string, error) {
	tags := make([]string, 0, 4)
	for _, tag := range cmdctx.GetStringSlice(ctx.FlagValues, "tag") {
		// Splitting here keeps --tag a,b and --tag a --tag b the same request.
		for _, part := range strings.Split(tag, ",") {
			if part = strings.TrimSpace(part); part != "" {
				tags = append(tags, part)
			}
		}
	}
	if len(tags) == 0 {
		// Absent rather than empty: an empty filter is not a filter.
		return nil, nil
	}
	return map[string]string{"tags": strings.Join(tags, ",")}, nil
}
