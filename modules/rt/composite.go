// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package rt

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/harness/cli/pkg/cmdctx"
)

const compositeBodyFnID = "composite_body"

// A composite becomes a pipeline, and pipeline identifiers are stricter than load test
// ones — notably hyphens are fine in a load test identity and rejected here.
var (
	compositeIdentifierPattern = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_$]*$`)
	compositeIdentifierFix     = strings.NewReplacer("-", "_", ".", "_", " ", "_", "/", "_")
)

const compositeIdentifierMaxLen = 128

// Not in the spec so the identifier can be checked here: the server's own rejection
// arrives as an HTTP 500 quoting a pipeline rule, naming no argument.
func compositeBody(ctx *cmdctx.Ctx) (any, error) {
	if err := checkCompositeIdentifier(ctx.Id); err != nil {
		return nil, err
	}
	name := cmdctx.GetString(ctx.FlagValues, "name")
	if name == "" {
		name = ctx.Id
	}
	return map[string]any{
		"identifier":  ctx.Id,
		"name":        name,
		"description": cmdctx.GetString(ctx.FlagValues, "description"),
		"objective":   cmdctx.GetString(ctx.FlagValues, "objective"),
		"loadTest": map[string]any{
			"loadTestRef": cmdctx.GetString(ctx.FlagValues, "loadtest"),
		},
		"probe": map[string]any{
			"identity":       cmdctx.GetString(ctx.FlagValues, "probe"),
			"infraReference": cmdctx.GetString(ctx.FlagValues, "probe-infra"),
			"duration":       cmdctx.GetString(ctx.FlagValues, "probe-duration"),
		},
	}, nil
}

func checkCompositeIdentifier(identifier string) error {
	if identifier == "" {
		return errors.New("create composite_loadtest requires an <id>")
	}
	if len(identifier) > compositeIdentifierMaxLen {
		return fmt.Errorf("%q is %d characters: a pipeline identifier is capped at %d",
			identifier, len(identifier), compositeIdentifierMaxLen)
	}
	if compositeIdentifierPattern.MatchString(identifier) {
		return nil
	}
	const problem = "%q is not a valid pipeline identifier: it has to start with a letter and hold only letters, digits, underscores or dollar signs. A composite load test is a pipeline, so it will not take the hyphens a load test identity does"
	// Only suggest a rewrite that passes: swapping separators cannot fix a leading digit.
	if fixed := compositeIdentifierFix.Replace(identifier); compositeIdentifierPattern.MatchString(fixed) {
		return fmt.Errorf(problem+"; try %q", identifier, fixed)
	}
	return fmt.Errorf(problem, identifier)
}
