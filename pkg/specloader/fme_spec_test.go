// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package specloader

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/harness/cli/pkg/auth"
	"github.com/harness/cli/pkg/cmdctx"
	"github.com/harness/cli/pkg/registry"
)

// fmeCaptureServer returns a mock server that records the inbound request path
// and always replies with resp, plus the *cmdctx.Ctx wired to call it.
func fmeCaptureServer(t *testing.T, resp string) (*httptest.Server, *string) {
	t.Helper()
	path := ""
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, resp)
	}))
	t.Cleanup(srv.Close)
	return srv, &path
}

func fmeTestCtx(t *testing.T, apiURL string) *cmdctx.Ctx {
	t.Helper()
	return &cmdctx.Ctx{
		Context: context.Background(),
		Auth: &auth.ResolvedAuth{
			APIUrl:    apiURL,
			AccountID: "acct",
			OrgID:     "org",
			ProjectID: "proj",
			PATToken:  "pat.test",
			AuthType:  auth.AuthTypePAT,
		},
		FormatFlags: cmdctx.FormatFlags{OutFile: filepath.Join(t.TempDir(), "out")},
	}
}

func fmeReadOut(t *testing.T, ctx *cmdctx.Ctx) string {
	t.Helper()
	b, err := os.ReadFile(ctx.FormatFlags.OutFile)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	return string(b)
}

// TestFMESpec_ListFeatureFlag drives the real embedded fme.spec.yaml "list feature_flag"
// command against a mock server returning the flat (no "entity" wrapper) shape that the
// live FME v4 API returns, and asserts the request hits /fme/internal/api/v4/feature-flags
// and that fields resolve directly off the item (it.name, it.trafficType.name, ...).
func TestFMESpec_ListFeatureFlag(t *testing.T) {
	reg := registry.New()
	if err := LoadSpec(reg, "fme.spec.yaml", true); err != nil {
		t.Fatalf("LoadSpec: %v", err)
	}
	cs := reg.GetSpec("list", "feature_flag")
	if cs == nil || cs.Endpoint == nil {
		t.Fatal("list feature_flag: command not found or missing endpoint spec")
	}

	fixture := `{"data":[{"name":"my-flag","description":"desc","trafficType":{"name":"user"},"status":"ACTIVE","rolloutStatus":{"name":"Ramp"},"createdAt":"2026-01-01T00:00:00Z"}],"limit":20,"offset":0,"totalCount":1}`
	srv, path := fmeCaptureServer(t, fixture)

	ctx := fmeTestCtx(t, srv.URL)
	ctx.Noun = "feature_flag"
	ctx.Resolver = reg
	ctx.FormatFlags.Format = "json"

	if err := registry.RunListEndpoint(ctx, cs.Endpoint); err != nil {
		t.Fatalf("RunListEndpoint: %v", err)
	}

	if !strings.HasPrefix(*path, "/fme/internal/api/v4/feature-flags") {
		t.Fatalf("request path = %q, want prefix /fme/internal/api/v4/feature-flags", *path)
	}

	body := fmeReadOut(t, ctx)
	for _, want := range []string{"my-flag", "user", "ACTIVE", "Ramp"} {
		if !strings.Contains(body, want) {
			t.Fatalf("output missing %q (flat field did not resolve): %s", want, body)
		}
	}
}

// TestFMESpec_GetFeatureFlag drives the real embedded fme.spec.yaml "get feature_flag"
// command against a mock server returning a flat object, and asserts the request path
// and that yaml_pick_expr/item_expr resolve the item directly (it, not it.entity).
func TestFMESpec_GetFeatureFlag(t *testing.T) {
	reg := registry.New()
	if err := LoadSpec(reg, "fme.spec.yaml", true); err != nil {
		t.Fatalf("LoadSpec: %v", err)
	}
	cs := reg.GetSpec("get", "feature_flag")
	if cs == nil || cs.Endpoint == nil {
		t.Fatal("get feature_flag: command not found or missing endpoint spec")
	}

	fixture := `{"name":"my-flag","description":"desc","trafficType":{"name":"user"},"status":"ACTIVE","rolloutStatus":{"name":"Ramp"},"createdAt":"2026-01-01T00:00:00Z"}`
	srv, path := fmeCaptureServer(t, fixture)

	ctx := fmeTestCtx(t, srv.URL)
	ctx.Id = "my-flag"
	ctx.Noun = "feature_flag"
	ctx.Resolver = reg
	ctx.FormatFlags.Format = "yaml"

	if _, err := registry.RunEndpoint(ctx, cs.Endpoint); err != nil {
		t.Fatalf("RunEndpoint: %v", err)
	}

	if *path != "/fme/internal/api/v4/feature-flags/my-flag" {
		t.Fatalf("request path = %q, want /fme/internal/api/v4/feature-flags/my-flag", *path)
	}

	body := fmeReadOut(t, ctx)
	for _, want := range []string{"name: my-flag", "status: ACTIVE"} {
		if !strings.Contains(body, want) {
			t.Fatalf("output missing %q (yaml_pick_expr did not resolve flat item): %s", want, body)
		}
	}
	if strings.Contains(body, "entity") {
		t.Fatalf("output still references entity wrapper: %s", body)
	}
}
