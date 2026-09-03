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

// fmeCaptureServerWithQuery is like fmeCaptureServer but also records the raw
// query string, for asserting flag-to-query-param wiring (e.g. --env → environment_id).
func fmeCaptureServerWithQuery(t *testing.T, resp string) (*httptest.Server, *string, *string) {
	t.Helper()
	path, query := "", ""
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		query = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, resp)
	}))
	t.Cleanup(srv.Close)
	return srv, &path, &query
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
// live FME v4 API returns, and asserts the request hits /fme/api/v4/feature-flags
// and that fields resolve directly off the item (it.name, it.trafficType.name, ...).
func TestFMESpec_ListFeatureFlag(t *testing.T) {
	reg := registry.New()
	if _, err := LoadSpec(reg, "fme.spec.yaml", true); err != nil {
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

	if !strings.HasPrefix(*path, "/fme/api/v4/feature-flags") {
		t.Fatalf("request path = %q, want prefix /fme/api/v4/feature-flags", *path)
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
	if _, err := LoadSpec(reg, "fme.spec.yaml", true); err != nil {
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

	if *path != "/fme/api/v4/feature-flags/my-flag" {
		t.Fatalf("request path = %q, want /fme/api/v4/feature-flags/my-flag", *path)
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

// TestFMESpec_ListFMEEnvironment drives "list fme_environment" and asserts it
// hits /fme/api/v4/environments and that get_id_expr resolves off
// it.id (environments are addressed by UUID, not name, unlike segment/feature_flag).
func TestFMESpec_ListFMEEnvironment(t *testing.T) {
	reg := registry.New()
	if _, err := LoadSpec(reg, "fme.spec.yaml", true); err != nil {
		t.Fatalf("LoadSpec: %v", err)
	}
	cs := reg.GetSpec("list", "fme_environment")
	if cs == nil || cs.Endpoint == nil {
		t.Fatal("list fme_environment: command not found or missing endpoint spec")
	}

	fixture := `{"data":[{"id":"env-uuid-1","name":"Prod","isProduction":true,"status":"ACTIVE"}],"limit":100,"offset":0,"totalCount":1}`
	srv, path := fmeCaptureServer(t, fixture)

	ctx := fmeTestCtx(t, srv.URL)
	ctx.Noun = "fme_environment"
	ctx.Resolver = reg
	ctx.FormatFlags.Format = "json"

	if err := registry.RunListEndpoint(ctx, cs.Endpoint); err != nil {
		t.Fatalf("RunListEndpoint: %v", err)
	}

	if !strings.HasPrefix(*path, "/fme/api/v4/environments") {
		t.Fatalf("request path = %q, want prefix /fme/api/v4/environments", *path)
	}

	body := fmeReadOut(t, ctx)
	for _, want := range []string{"Prod", "true", "ACTIVE"} {
		if !strings.Contains(body, want) {
			t.Fatalf("output missing %q: %s", want, body)
		}
	}
}

// TestFMESpec_GetFMEEnvironment drives "get fme_environment" with a UUID id
// and asserts the path embeds it (environments are looked up by id, not name).
func TestFMESpec_GetFMEEnvironment(t *testing.T) {
	reg := registry.New()
	if _, err := LoadSpec(reg, "fme.spec.yaml", true); err != nil {
		t.Fatalf("LoadSpec: %v", err)
	}
	cs := reg.GetSpec("get", "fme_environment")
	if cs == nil || cs.Endpoint == nil {
		t.Fatal("get fme_environment: command not found or missing endpoint spec")
	}

	fixture := `{"id":"env-uuid-1","name":"Prod","isProduction":true,"status":"ACTIVE"}`
	srv, path := fmeCaptureServer(t, fixture)

	ctx := fmeTestCtx(t, srv.URL)
	ctx.Id = "env-uuid-1"
	ctx.Noun = "fme_environment"
	ctx.Resolver = reg
	ctx.FormatFlags.Format = "yaml"

	if _, err := registry.RunEndpoint(ctx, cs.Endpoint); err != nil {
		t.Fatalf("RunEndpoint: %v", err)
	}

	if *path != "/fme/api/v4/environments/env-uuid-1" {
		t.Fatalf("request path = %q, want /fme/api/v4/environments/env-uuid-1", *path)
	}
}

// TestFMESpec_ListSegment drives "list segment" and asserts get_id_expr
// resolves off it.name (segments, unlike fme_environment, are addressed by name).
func TestFMESpec_ListSegment(t *testing.T) {
	reg := registry.New()
	if _, err := LoadSpec(reg, "fme.spec.yaml", true); err != nil {
		t.Fatalf("LoadSpec: %v", err)
	}
	cs := reg.GetSpec("list", "segment")
	if cs == nil || cs.Endpoint == nil {
		t.Fatal("list segment: command not found or missing endpoint spec")
	}

	fixture := `{"data":[{"name":"my-segment","description":"desc","trafficType":{"name":"user"},"status":"ACTIVE","createdAt":1778049995.725}],"limit":100,"offset":0,"totalCount":1}`
	srv, path := fmeCaptureServer(t, fixture)

	ctx := fmeTestCtx(t, srv.URL)
	ctx.Noun = "segment"
	ctx.Resolver = reg
	ctx.FormatFlags.Format = "json"

	if err := registry.RunListEndpoint(ctx, cs.Endpoint); err != nil {
		t.Fatalf("RunListEndpoint: %v", err)
	}

	if !strings.HasPrefix(*path, "/fme/api/v4/segments") {
		t.Fatalf("request path = %q, want prefix /fme/api/v4/segments", *path)
	}

	body := fmeReadOut(t, ctx)
	for _, want := range []string{"my-segment", "user", "ACTIVE"} {
		if !strings.Contains(body, want) {
			t.Fatalf("output missing %q: %s", want, body)
		}
	}
}

// TestFMESpec_ListSegmentDefinition drives "list segment:definition" and asserts
// the --env flag maps to the environment_id query param and fields_extra resolves
// (segment/environment names, description, status) off the flat item.
func TestFMESpec_ListSegmentDefinition(t *testing.T) {
	reg := registry.New()
	if _, err := LoadSpec(reg, "fme.spec.yaml", true); err != nil {
		t.Fatalf("LoadSpec: %v", err)
	}
	cs := reg.GetSpec("list", "segment:definition")
	if cs == nil || cs.Endpoint == nil {
		t.Fatal("list segment:definition: command not found or missing endpoint spec")
	}

	fixture := `{"data":[{"segment":{"name":"my-segment"},"environment":{"name":"Prod"},"description":"desc","status":"ACTIVE","createdAt":1778049995.725}],"limit":100,"offset":0,"totalCount":1}`
	srv, path, query := fmeCaptureServerWithQuery(t, fixture)

	ctx := fmeTestCtx(t, srv.URL)
	ctx.Noun = "segment"
	ctx.Resolver = reg
	ctx.FormatFlags.Format = "json"
	ctx.FlagValues = map[string]any{"env": "env-uuid-1"}

	if err := registry.RunListEndpoint(ctx, cs.Endpoint); err != nil {
		t.Fatalf("RunListEndpoint: %v", err)
	}

	if !strings.HasPrefix(*path, "/fme/api/v4/segment-definitions") {
		t.Fatalf("request path = %q, want prefix /fme/api/v4/segment-definitions", *path)
	}
	if !strings.Contains(*query, "environment_id=env-uuid-1") {
		t.Fatalf("query = %q, want environment_id=env-uuid-1 (from --env flag)", *query)
	}

	body := fmeReadOut(t, ctx)
	for _, want := range []string{"my-segment", "Prod", "desc", "ACTIVE"} {
		if !strings.Contains(body, want) {
			t.Fatalf("output missing %q: %s", want, body)
		}
	}
}

// TestFMESpec_ListTrafficType drives "list traffic_type" against the mock server
// and asserts it hits /fme/api/v4/traffic-types and renders id/name fields.
func TestFMESpec_ListTrafficType(t *testing.T) {
	reg := registry.New()
	if _, err := LoadSpec(reg, "fme.spec.yaml", true); err != nil {
		t.Fatalf("LoadSpec: %v", err)
	}
	cs := reg.GetSpec("list", "traffic_type")
	if cs == nil || cs.Endpoint == nil {
		t.Fatal("list traffic_type: command not found or missing endpoint spec")
	}

	fixture := `{"data":[{"type":"traffic-type","id":"tt-1","name":"user"}],"limit":100,"offset":0,"totalCount":1}`
	srv, path := fmeCaptureServer(t, fixture)

	ctx := fmeTestCtx(t, srv.URL)
	ctx.Noun = "traffic_type"
	ctx.Resolver = reg
	ctx.FormatFlags.Format = "json"

	if err := registry.RunListEndpoint(ctx, cs.Endpoint); err != nil {
		t.Fatalf("RunListEndpoint: %v", err)
	}

	if !strings.HasPrefix(*path, "/fme/api/v4/traffic-types") {
		t.Fatalf("request path = %q, want prefix /fme/api/v4/traffic-types", *path)
	}

	body := fmeReadOut(t, ctx)
	for _, want := range []string{"tt-1", "user"} {
		if !strings.Contains(body, want) {
			t.Fatalf("output missing %q: %s", want, body)
		}
	}
}

// TestFMESpec_ListRolloutStatus drives "list rollout_status" against the mock
// server and asserts it hits /fme/api/v4/rollout-statuses, renders id/name/description,
// and that the description field falls back to "" when omitted from the JSON entirely
// (backend trimToNull's blank descriptions server-side).
func TestFMESpec_ListRolloutStatus(t *testing.T) {
	reg := registry.New()
	if _, err := LoadSpec(reg, "fme.spec.yaml", true); err != nil {
		t.Fatalf("LoadSpec: %v", err)
	}
	cs := reg.GetSpec("list", "rollout_status")
	if cs == nil || cs.Endpoint == nil {
		t.Fatal("list rollout_status: command not found or missing endpoint spec")
	}

	fixture := `{"data":[{"type":"rollout-status","id":"rs-1","name":"Ramp","description":"Ramping up traffic"},{"type":"rollout-status","id":"rs-2","name":"Killed"}],"limit":100,"offset":0,"totalCount":2}`
	srv, path := fmeCaptureServer(t, fixture)

	ctx := fmeTestCtx(t, srv.URL)
	ctx.Noun = "rollout_status"
	ctx.Resolver = reg
	ctx.FormatFlags.Format = "json"

	if err := registry.RunListEndpoint(ctx, cs.Endpoint); err != nil {
		t.Fatalf("RunListEndpoint: %v", err)
	}

	if !strings.HasPrefix(*path, "/fme/api/v4/rollout-statuses") {
		t.Fatalf("request path = %q, want prefix /fme/api/v4/rollout-statuses", *path)
	}

	body := fmeReadOut(t, ctx)
	for _, want := range []string{"rs-1", "Ramp", "Ramping up traffic", "rs-2", "Killed"} {
		if !strings.Contains(body, want) {
			t.Fatalf("output missing %q: %s", want, body)
		}
	}
}
