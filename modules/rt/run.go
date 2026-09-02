// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package rt

import (
	"errors"
	"fmt"
	"math/rand/v2"
	"net/url"
	"regexp"
	"sort"

	"github.com/harness/cli/pkg/client"
	"github.com/harness/cli/pkg/cmdctx"
	"github.com/harness/cli/pkg/registry"
	"github.com/harness/cli/pkg/spec"
)

const (
	startRunBodyFnID = "start_run"
	rerunWorkflowID  = "rerun"

	// Matches the console's suffix, so runs started from either place sort together.
	runSuffixLen      = 3
	runSuffixAlphabet = "abcdefghijklmnopqrstuvwxyz0123456789"

	runIdentityAttempts = 20
	runIdentityScan     = 100
)

// The run API sometimes reports a load test as an internal UUID instead of its name.
var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// Not in the spec: the server wants a client-supplied identity, and overrides as an array.
func startRunBody(ctx *cmdctx.Ctx) (any, error) {
	if ctx.Id == "" {
		return nil, errors.New("execute loadtest requires a <loadtest-id>")
	}
	identity, err := newRunIdentity(ctx, ctx.Id)
	if err != nil {
		return nil, err
	}
	body := map[string]any{"identity": identity}
	if name := cmdctx.GetString(ctx.FlagValues, "name"); name != "" {
		body["name"] = name
	}
	if values := setArgsToValues(ctx.SetArgs); len(values) > 0 {
		body["values"] = values
	}
	return body, nil
}

// Sorted so the body is stable across runs.
func setArgsToValues(set map[string]string) []map[string]string {
	if len(set) == 0 {
		return nil
	}
	names := make([]string, 0, len(set))
	for name := range set {
		names = append(names, name)
	}
	sort.Strings(names)
	values := make([]map[string]string, 0, len(names))
	for _, name := range names {
		values = append(values, map[string]string{"name": name, "value": set[name]})
	}
	return values
}

// Picks a free suffix up front rather than retrying after the server rejects a duplicate.
func newRunIdentity(ctx *cmdctx.Ctx, loadTestID string) (string, error) {
	taken := existingRunIdentities(ctx, loadTestID)
	for range runIdentityAttempts {
		candidate := loadTestID + "-" + randomSuffix()
		if !taken[candidate] {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("could not find an unused run identity for load test %q", loadTestID)
}

func randomSuffix() string {
	b := make([]byte, runSuffixLen)
	for i := range b {
		b[i] = runSuffixAlphabet[rand.IntN(len(runSuffixAlphabet))]
	}
	return string(b)
}

// Best effort: on failure the caller falls back to an unchecked random suffix.
func existingRunIdentities(ctx *cmdctx.Ctx, loadTestID string) map[string]bool {
	taken := map[string]bool{}
	qp := scopeParams(ctx)
	qp["limit"] = fmt.Sprintf("%d", runIdentityScan)
	resp, _, err := client.New(ctx).Get(basePath+"/load-tests/"+url.PathEscape(loadTestID)+"/runs", qp)
	if err != nil {
		return taken
	}
	for _, item := range itemsOf(resp) {
		if id := stringField(asMap(item), "identity"); id != "" {
			taken[id] = true
		}
	}
	return taken
}

// There is no rerun route, so read the previous run for its load test and start a new one.
func rerunWorkflow(ctx *cmdctx.Ctx) error {
	if ctx.Id == "" {
		return errors.New("execute loadtest_run:rerun requires a <run-id>")
	}
	prevID := ctx.Id

	prev, _, err := client.New(ctx).Get(basePath+"/runs/"+url.PathEscape(prevID), scopeParams(ctx))
	if err != nil {
		return fmt.Errorf("reading run %q: %w", prevID, err)
	}
	parent := stringField(asMap(prev), "loadTestIdentity")
	switch {
	case parent == "":
		return fmt.Errorf("run %q does not record which load test it belongs to, so it cannot be rerun", prevID)
	case uuidPattern.MatchString(parent):
		return fmt.Errorf("run %q reports its load test as an internal id (%s) rather than a name; start a new run with: harness execute loadtest <loadtest-id>", prevID, parent)
	}

	// startRunBody reads the load test from ctx.Id, so point it at the parent and put it back.
	ctx.Id = parent
	defer func() { ctx.Id = prevID }()
	if ctx.FlagValues == nil {
		ctx.FlagValues = map[string]any{}
	}
	if cmdctx.GetString(ctx.FlagValues, "name") == "" {
		ctx.FlagValues["name"] = "Rerun of " + prevID
	}

	ep := &spec.EndpointSpec{
		Method:      "POST",
		Path:        basePath + "/load-tests/{{ctx.id}}/runs",
		BodyFn:      "rt:" + startRunBodyFnID,
		ItemExpr:    "it",
		QueryParams: scopeQueryExprs(ctx),
	}
	result, err := registry.RunEndpoint(ctx, ep)
	if err != nil {
		return fmt.Errorf("starting a rerun of %q: %w", prevID, err)
	}
	if cmdctx.GetBool(ctx.FlagValues, "follow") {
		return watchFollowFn(ctx, result)
	}
	return nil
}
