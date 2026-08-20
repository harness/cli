// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package rt

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"testing"

	"github.com/harness/cli/pkg/auth"
	"github.com/harness/cli/pkg/cmdctx"
	"github.com/harness/cli/pkg/registry"
)

// The remaining handlers reach the API through client.New(ctx), so covering them
// means standing a server up in front of a Ctx and recording what was asked of it.

// One inbound request, so a test can assert on the route taken, not just the result.
type call struct {
	method string
	path   string
	query  url.Values
	body   map[string]any
}

// Sent through as-is rather than JSON-encoded, for the one route that answers YAML.
type rawResponse string

// An unmapped path answers 404: what the real API does, and what the best-effort
// readers here have to survive.
func apiCtx(t *testing.T, routes map[string]any) (*cmdctx.Ctx, *[]call) {
	t.Helper()

	calls := &[]call{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c := call{method: r.Method, path: r.URL.Path, query: r.URL.Query()}
		_ = json.NewDecoder(r.Body).Decode(&c.body)
		*calls = append(*calls, c)

		resp, ok := routes[r.URL.Path]
		if !ok {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprintf(w, `{"message":"no such route %s"}`, r.URL.Path)
			return
		}
		if raw, isRaw := resp.(rawResponse); isRaw {
			w.Header().Set("Content-Type", "application/yaml")
			fmt.Fprint(w, string(raw))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	t.Cleanup(srv.Close)

	// A fresh registry per test doubles as a check that ModuleInit wires every id.
	reg := registry.New()
	ModuleInit(reg.Module("rt"))

	ctx := &cmdctx.Ctx{
		Context: context.Background(),
		Auth: &auth.ResolvedAuth{
			APIUrl:    srv.URL,
			AccountID: "acct",
			OrgID:     "eng",
			ProjectID: "payments",
			PATToken:  "pat.test",
			AuthType:  auth.AuthTypePAT,
		},
		FlagValues: map[string]any{},
		Resolver:   reg,
	}
	// Render to a file so endpoint tests do not print over the test output.
	ctx.FormatFlags.OutFile = filepath.Join(t.TempDir(), "out")
	return ctx, calls
}

func api(format string, args ...any) string {
	return basePath + fmt.Sprintf(format, args...)
}

func itemsPage(items ...map[string]any) map[string]any {
	page := make([]any, 0, len(items))
	for _, item := range items {
		page = append(page, item)
	}
	return map[string]any{"items": page}
}

// The runs-of-a-load-test response; the identity picker reads only the identities.
func runList(identities ...string) map[string]any {
	items := make([]map[string]any, 0, len(identities))
	for _, id := range identities {
		items = append(items, map[string]any{"identity": id})
	}
	return itemsPage(items...)
}

func findCall(calls *[]call, method, path string) (call, bool) {
	for _, c := range *calls {
		if c.method == method && c.path == path {
			return c, true
		}
	}
	return call{}, false
}
