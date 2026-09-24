// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package specloader

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/harness/cli/v3/pkg/auth"
	"github.com/harness/cli/v3/pkg/cmdctx"
	"github.com/harness/cli/v3/pkg/registry"
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

// TestFMESpec_GetFeatureFlag_TagsOwnersTextFormat drives "get feature_flag"
// in default text format and asserts the tags/owners fields render their
// joined names instead of blank — the exprs used method-call syntax
// (it.tags.map(t, t.name).join(", ")) which expr-lang silently fails to
// evaluate at runtime (map/join are only valid as bare builtin calls with a
// "#" predicate, e.g. join(map(it.tags, {#.name}), ", ")); JSON/YAML format
// didn't surface it since they serialize raw data without evaluating expr.
func TestFMESpec_GetFeatureFlag_TagsOwnersTextFormat(t *testing.T) {
	reg := registry.New()
	if _, err := LoadSpec(reg, "fme.spec.yaml", true); err != nil {
		t.Fatalf("LoadSpec: %v", err)
	}
	cs := reg.GetSpec("get", "feature_flag")
	if cs == nil || cs.Endpoint == nil {
		t.Fatal("get feature_flag: command not found or missing endpoint spec")
	}

	fixture := `{"name":"my-flag","description":"desc","trafficType":{"name":"user"},"status":"ACTIVE","rolloutStatus":{"name":"Ramp"},"tags":[{"id":"t-1","name":"demo"}],"owners":[{"id":"u-1","name":"alice","type":"USER"}],"createdAt":"2026-01-01T00:00:00Z"}`
	srv, _ := fmeCaptureServer(t, fixture)

	ctx := fmeTestCtx(t, srv.URL)
	ctx.Id = "my-flag"
	ctx.Noun = "feature_flag"
	ctx.Resolver = reg
	ctx.FormatFlags.Format = "text"

	if _, err := registry.RunEndpoint(ctx, cs.Endpoint); err != nil {
		t.Fatalf("RunEndpoint: %v", err)
	}

	body := fmeReadOut(t, ctx)
	if !strings.Contains(body, "demo") {
		t.Fatalf("output missing tag %q (map/join expr silently failed): %s", "demo", body)
	}
	if !strings.Contains(body, "alice") {
		t.Fatalf("output missing owner %q (map/join expr silently failed): %s", "alice", body)
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
	for _, want := range []string{"env-uuid-1", "Prod", "true", "ACTIVE"} {
		if !strings.Contains(body, want) {
			t.Fatalf("output missing %q (id column must be present so get/update/delete are usable off list output): %s", want, body)
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

// TestFMESpec_CreateFMEEnvironment drives "create fme_environment <name> --production"
// through the set-fields create strategy: create_body_init seeds {name, isProduction}
// from ctx.id/--production, and item_expr unwraps the {entity, governance} envelope.
func TestFMESpec_CreateFMEEnvironment(t *testing.T) {
	reg := registry.New()
	if _, err := LoadSpec(reg, "fme.spec.yaml", true); err != nil {
		t.Fatalf("LoadSpec: %v", err)
	}
	cs := reg.GetSpec("create", "fme_environment")
	if cs == nil || cs.Endpoint == nil {
		t.Fatal("create fme_environment: command not found or missing endpoint spec")
	}

	var gotMethod, gotPath, gotQuery, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"entity":{"id":"env-uuid-2","name":"cli-test-env","isProduction":true,"status":"ACTIVE"},"governance":null}`)
	}))
	t.Cleanup(srv.Close)

	ctx := fmeTestCtx(t, srv.URL)
	ctx.Id = "cli-test-env"
	ctx.Noun = "fme_environment"
	ctx.Resolver = reg
	ctx.FormatFlags.Format = "json"
	ctx.FlagValues = map[string]any{"production": true}

	if _, err := registry.RunEndpoint(ctx, cs.Endpoint); err != nil {
		t.Fatalf("RunEndpoint: %v", err)
	}

	if gotMethod != http.MethodPost {
		t.Fatalf("method = %q, want POST", gotMethod)
	}
	if gotPath != "/fme/api/v4/environments" {
		t.Fatalf("request path = %q, want /fme/api/v4/environments", gotPath)
	}
	if !strings.Contains(gotQuery, "organization_identifier=org") || !strings.Contains(gotQuery, "project_identifier=proj") {
		t.Fatalf("query = %q, want organization_identifier=org and project_identifier=proj", gotQuery)
	}
	if !strings.Contains(gotBody, `"name":"cli-test-env"`) {
		t.Fatalf("request body missing name: %s", gotBody)
	}
	if !strings.Contains(gotBody, `"isProduction":true`) {
		t.Fatalf("request body missing isProduction: %s", gotBody)
	}

	out := fmeReadOut(t, ctx)
	if !strings.Contains(out, `"id": "env-uuid-2"`) {
		t.Fatalf("output missing entity-unwrapped id: %s", out)
	}
}

// TestFMESpec_UpdateFMEEnvironment drives "update fme_environment --set name=..." through
// the get-then-patch strategy and asserts the curated update_body_pick sends only
// {name, isProduction} — not the full GET response (id/status/createdAt would break the
// backend's Nulls.FAIL UpdateEnvironmentRequest if it ever tightened to reject unknowns).
func TestFMESpec_UpdateFMEEnvironment(t *testing.T) {
	reg := registry.New()
	if _, err := LoadSpec(reg, "fme.spec.yaml", true); err != nil {
		t.Fatalf("LoadSpec: %v", err)
	}
	cs := reg.GetSpec("update", "fme_environment")
	if cs == nil || cs.Endpoint == nil {
		t.Fatal("update fme_environment: command not found or missing endpoint spec")
	}

	getFixture := `{"id":"env-uuid-1","name":"Prod","isProduction":true,"status":"ACTIVE"}`
	var gotMethod, gotPath, gotBody, gotContentType string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet {
			fmt.Fprint(w, getFixture)
			return
		}
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotContentType = r.Header.Get("Content-Type")
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		fmt.Fprint(w, `{"entity":`+gotBody+`}`)
	}))
	t.Cleanup(srv.Close)

	ctx := fmeTestCtx(t, srv.URL)
	ctx.Id = "env-uuid-1"
	ctx.Noun = "fme_environment"
	ctx.VerbHandler = cs.VerbHandler
	ctx.Resolver = reg
	ctx.FormatFlags.Format = "json"
	ctx.SetArgs = map[string]string{"name": "Renamed"}

	if _, err := registry.RunEndpoint(ctx, cs.Endpoint); err != nil {
		t.Fatalf("RunEndpoint: %v", err)
	}

	if gotMethod != http.MethodPatch {
		t.Fatalf("method = %q, want PATCH", gotMethod)
	}
	if gotPath != "/fme/api/v4/environments/env-uuid-1" {
		t.Fatalf("request path = %q, want /fme/api/v4/environments/env-uuid-1", gotPath)
	}
	if gotContentType != "application/merge-patch+json" {
		t.Fatalf("Content-Type = %q, want application/merge-patch+json", gotContentType)
	}
	if !strings.Contains(gotBody, `"name":"Renamed"`) {
		t.Fatalf("PATCH body missing mutated name: %s", gotBody)
	}
	if !strings.Contains(gotBody, `"isProduction":true`) {
		t.Fatalf("PATCH body missing carried-over isProduction from GET: %s", gotBody)
	}
	if strings.Contains(gotBody, "status") || strings.Contains(gotBody, `"id"`) {
		t.Fatalf("PATCH body should be curated to {name, isProduction} only, got: %s", gotBody)
	}
}

// TestFMESpec_DeleteFMEEnvironment drives "delete fme_environment" and asserts the DELETE
// request hits the {id} path. The real API returns {"governance": {...}} with no entity on
// delete, but this command has no text_header, so no output rendering is attempted either way.
func TestFMESpec_DeleteFMEEnvironment(t *testing.T) {
	reg := registry.New()
	if _, err := LoadSpec(reg, "fme.spec.yaml", true); err != nil {
		t.Fatalf("LoadSpec: %v", err)
	}
	cs := reg.GetSpec("delete", "fme_environment")
	if cs == nil || cs.Endpoint == nil {
		t.Fatal("delete fme_environment: command not found or missing endpoint spec")
	}

	var gotMethod, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"governance":{"result":"ok"}}`)
	}))
	t.Cleanup(srv.Close)

	ctx := fmeTestCtx(t, srv.URL)
	ctx.Id = "env-uuid-1"
	ctx.Noun = "fme_environment"
	ctx.VerbHandler = cs.VerbHandler
	ctx.Resolver = reg
	ctx.FormatFlags.Format = "json"

	if _, err := registry.RunEndpoint(ctx, cs.Endpoint); err != nil {
		t.Fatalf("RunEndpoint: %v", err)
	}

	if gotMethod != http.MethodDelete {
		t.Fatalf("method = %q, want DELETE", gotMethod)
	}
	if gotPath != "/fme/api/v4/environments/env-uuid-1" {
		t.Fatalf("request path = %q, want /fme/api/v4/environments/env-uuid-1", gotPath)
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

	fixture := `{"data":[{"name":"my-segment","description":"desc","trafficType":{"name":"user"},"segmentType":"STANDARD","status":"ACTIVE","createdAt":1778049995.725}],"limit":100,"offset":0,"totalCount":1}`
	srv, path, query := fmeCaptureServerWithQuery(t, fixture)

	ctx := fmeTestCtx(t, srv.URL)
	ctx.Noun = "segment"
	ctx.Resolver = reg
	ctx.FormatFlags.Format = "json"
	ctx.FlagValues = map[string]any{"segment-type": "LARGE", "status": "ARCHIVED"}

	if err := registry.RunListEndpoint(ctx, cs.Endpoint); err != nil {
		t.Fatalf("RunListEndpoint: %v", err)
	}

	if !strings.HasPrefix(*path, "/fme/api/v4/segments") {
		t.Fatalf("request path = %q, want prefix /fme/api/v4/segments", *path)
	}
	// SegmentListQueryParams marks segment_type @NotNull, so a list call without it is a
	// 400 rather than an unfiltered list.
	for _, want := range []string{"segment_type=LARGE", "status=ARCHIVED"} {
		if !strings.Contains(*query, want) {
			t.Fatalf("query = %q, want %s", *query, want)
		}
	}

	body := fmeReadOut(t, ctx)
	for _, want := range []string{"my-segment", "user", "ACTIVE"} {
		if !strings.Contains(body, want) {
			t.Fatalf("output missing %q: %s", want, body)
		}
	}
}

// TestFMESpec_GetSegment asserts --segment-type reaches the segment_type query param
// (required on the v4 GET) and that the segment noun renders the tags/owners/segmentType
// fields the response carries.
func TestFMESpec_GetSegment(t *testing.T) {
	reg := registry.New()
	if _, err := LoadSpec(reg, "fme.spec.yaml", true); err != nil {
		t.Fatalf("LoadSpec: %v", err)
	}
	cs := reg.GetSpec("get", "segment")
	if cs == nil || cs.Endpoint == nil {
		t.Fatal("get segment: command not found or missing endpoint spec")
	}

	fixture := `{"id":"s1","name":"my-segment","description":"desc","trafficType":{"name":"user"},` +
		`"segmentType":"STANDARD","status":"ACTIVE",` +
		`"tags":[{"id":"t1","name":"alpha"}],` +
		`"owners":[{"id":"u1","name":"alice","type":"USER","email":"alice@x.com"}],` +
		`"createdAt":1778049995.725}`
	srv, path, query := fmeCaptureServerWithQuery(t, fixture)

	ctx := fmeTestCtx(t, srv.URL)
	ctx.Id = "my-segment"
	ctx.Noun = "segment"
	ctx.Resolver = reg
	ctx.FormatFlags.Format = "json"
	ctx.FlagValues = map[string]any{"segment-type": "STANDARD"}

	if _, err := registry.RunEndpoint(ctx, cs.Endpoint); err != nil {
		t.Fatalf("RunEndpoint: %v", err)
	}

	if *path != "/fme/api/v4/segments/my-segment" {
		t.Fatalf("request path = %q, want /fme/api/v4/segments/my-segment", *path)
	}
	if !strings.Contains(*query, "segment_type=STANDARD") {
		t.Fatalf("query = %q, want segment_type=STANDARD (from --segment-type)", *query)
	}

	out := fmeReadOut(t, ctx)
	for _, want := range []string{"my-segment", "STANDARD", "alpha", "alice"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q: %s", want, out)
		}
	}
}

// TestFMESpec_CreateSegment asserts segmentType lands in the create body — it is
// @NotNull on CreateSegmentRequest, so omitting it is a 400 "Field 'segmentType' is
// required" on every create.
func TestFMESpec_CreateSegment(t *testing.T) {
	reg := registry.New()
	if _, err := LoadSpec(reg, "fme.spec.yaml", true); err != nil {
		t.Fatalf("LoadSpec: %v", err)
	}
	cs := reg.GetSpec("create", "segment")
	if cs == nil || cs.Endpoint == nil {
		t.Fatal("create segment: command not found or missing endpoint spec")
	}

	srv, caps := fmeSequenceServer(t, []string{`{"entity":{"name":"new-segment"}}`})

	ctx := fmeTestCtx(t, srv.URL)
	ctx.Id = "new-segment"
	ctx.Noun = "segment"
	ctx.Resolver = reg
	ctx.FormatFlags.Format = "json"
	ctx.FlagValues = map[string]any{
		"traffic-type": "user",
		"segment-type": "RULE_BASED",
		"description":  "from the cli",
	}

	if _, err := registry.RunEndpoint(ctx, cs.Endpoint); err != nil {
		t.Fatalf("RunEndpoint: %v", err)
	}

	got := (*caps)[0]
	if got.method != "POST" || got.path != "/fme/api/v4/segments" {
		t.Fatalf("request = %s %s, want POST /fme/api/v4/segments", got.method, got.path)
	}
	var body map[string]any
	if err := json.Unmarshal(got.body, &body); err != nil {
		t.Fatalf("unmarshal request body: %v", err)
	}
	for k, want := range map[string]string{
		"name":        "new-segment",
		"trafficType": "user",
		"segmentType": "RULE_BASED",
		"description": "from the cli",
	} {
		if body[k] != want {
			t.Fatalf("body[%q] = %v, want %q (full body: %v)", k, body[k], want, body)
		}
	}
}

// TestFMESpec_UpdateSegment asserts the get-then-patch body is narrowed to the three
// fields UpdateSegmentRequest accepts. Echoing the GET back (the previous
// update_body_pick: it) sent id/name/trafficType/status/segmentType, which v4 rejects
// as unknown properties, so every segment update was a 400.
func TestFMESpec_UpdateSegment(t *testing.T) {
	reg := registry.New()
	if _, err := LoadSpec(reg, "fme.spec.yaml", true); err != nil {
		t.Fatalf("LoadSpec: %v", err)
	}
	cs := reg.GetSpec("update", "segment")
	if cs == nil || cs.Endpoint == nil {
		t.Fatal("update segment: command not found or missing endpoint spec")
	}

	getResp := `{"id":"s1","name":"my-segment","description":"old desc","trafficType":{"name":"user"},` +
		`"segmentType":"STANDARD","status":"ACTIVE","createdAt":1778049995.725}`
	srv, caps := fmeSequenceServer(t, []string{getResp, `{"entity":{"name":"my-segment"}}`})

	ctx := fmeTestCtx(t, srv.URL)
	ctx.Id = "my-segment"
	ctx.Noun = "segment"
	ctx.Resolver = reg
	ctx.FormatFlags.Format = "json"
	ctx.FlagValues = map[string]any{"segment-type": "STANDARD"}
	ctx.SetArgs = map[string]string{"description": "new desc"}

	if _, err := registry.RunEndpoint(ctx, cs.Endpoint); err != nil {
		t.Fatalf("RunEndpoint: %v", err)
	}

	if len(*caps) != 2 {
		t.Fatalf("got %d requests, want 2 (GET, PATCH)", len(*caps))
	}
	patch := (*caps)[1]
	if patch.method != "PATCH" {
		t.Fatalf("2nd request method = %q, want PATCH", patch.method)
	}
	if !strings.Contains(patch.rawQuery, "segment_type=STANDARD") {
		t.Fatalf("PATCH query = %q, want segment_type=STANDARD", patch.rawQuery)
	}
	var body map[string]any
	if err := json.Unmarshal(patch.body, &body); err != nil {
		t.Fatalf("unmarshal PATCH body: %v", err)
	}
	if len(body) != 3 || body["description"] != "new desc" {
		t.Fatalf("PATCH body = %v, want exactly {description, tags, owners}", body)
	}
	for _, k := range []string{"tags", "owners"} {
		arr, ok := body[k].([]any)
		if !ok || len(arr) != 0 {
			t.Fatalf("PATCH body[%q] = %v, want empty array", k, body[k])
		}
	}
}

// TestFMESpec_UpdateSegment_TagsOwners mirrors the feature_flag behaviour for segments:
// existing members are carried into the PATCH so --set/--del amend the collection instead
// of replacing it, and they are reshaped to the write DTOs (tags drop the read-only id,
// owners become {type, id} / {type, identifier}) because v4 rejects the read shape.
func TestFMESpec_UpdateSegment_TagsOwners(t *testing.T) {
	reg := registry.New()
	if _, err := LoadSpec(reg, "fme.spec.yaml", true); err != nil {
		t.Fatalf("LoadSpec: %v", err)
	}
	cs := reg.GetSpec("update", "segment")
	if cs == nil || cs.Endpoint == nil {
		t.Fatal("update segment: command not found or missing endpoint spec")
	}

	getResp := `{"name":"my-segment","description":"desc",` +
		`"tags":[{"id":"t1","name":"alpha"},{"id":"t2","name":"beta"}],` +
		`"owners":[{"id":"u1","name":"alice","type":"USER","email":"alice@x.com"},` +
		`{"id":"g1","name":"platform","type":"GROUP"}]}`
	srv, caps := fmeSequenceServer(t, []string{getResp, `{"entity":{"name":"my-segment"}}`})

	ctx := fmeTestCtx(t, srv.URL)
	ctx.Id = "my-segment"
	ctx.Noun = "segment"
	ctx.Resolver = reg
	ctx.FormatFlags.Format = "json"
	ctx.FlagValues = map[string]any{"segment-type": "STANDARD"}
	ctx.SetArgs = map[string]string{"tags.gamma": "", "owners.user:u2": ""}
	ctx.DelArgs = []string{"tags.alpha"}

	if _, err := registry.RunEndpoint(ctx, cs.Endpoint); err != nil {
		t.Fatalf("RunEndpoint: %v", err)
	}

	var body map[string]any
	if err := json.Unmarshal((*caps)[1].body, &body); err != nil {
		t.Fatalf("unmarshal PATCH body: %v", err)
	}

	gotTags, err := json.Marshal(body["tags"])
	if err != nil {
		t.Fatalf("marshal tags: %v", err)
	}
	if wantTags := `[{"name":"beta"},{"name":"gamma"}]`; string(gotTags) != wantTags {
		t.Errorf("PATCH tags = %s, want %s (beta kept, alpha removed, gamma added, ids dropped)", gotTags, wantTags)
	}

	gotOwners, err := json.Marshal(body["owners"])
	if err != nil {
		t.Fatalf("marshal owners: %v", err)
	}
	wantOwners := `[{"id":"u1","type":"USER"},{"identifier":"g1","type":"GROUP"},{"id":"u2","type":"USER"}]`
	if string(gotOwners) != wantOwners {
		t.Errorf("PATCH owners = %s, want %s (existing kept and reshaped, u2 added)", gotOwners, wantOwners)
	}
}

// TestFMESpec_DeleteSegment asserts the archive call carries segment_type, which the v4
// DELETE requires just as GET and PATCH do.
func TestFMESpec_DeleteSegment(t *testing.T) {
	reg := registry.New()
	if _, err := LoadSpec(reg, "fme.spec.yaml", true); err != nil {
		t.Fatalf("LoadSpec: %v", err)
	}
	cs := reg.GetSpec("delete", "segment")
	if cs == nil || cs.Endpoint == nil {
		t.Fatal("delete segment: command not found or missing endpoint spec")
	}

	srv, caps := fmeSequenceServer(t, []string{`{}`})

	ctx := fmeTestCtx(t, srv.URL)
	ctx.Id = "my-segment"
	ctx.Noun = "segment"
	ctx.Resolver = reg
	ctx.FormatFlags.Format = "json"
	ctx.FlagValues = map[string]any{"segment-type": "LARGE"}

	if _, err := registry.RunEndpoint(ctx, cs.Endpoint); err != nil {
		t.Fatalf("RunEndpoint: %v", err)
	}

	got := (*caps)[0]
	if got.method != "DELETE" || got.path != "/fme/api/v4/segments/my-segment" {
		t.Fatalf("request = %s %s, want DELETE /fme/api/v4/segments/my-segment", got.method, got.path)
	}
	if !strings.Contains(got.rawQuery, "segment_type=LARGE") {
		t.Fatalf("query = %q, want segment_type=LARGE", got.rawQuery)
	}
}

// TestFMESpec_DeleteSegmentDefinition asserts --env reaches environment_id. The delete
// command declared the flag but never mapped it, and environment_id is @NotBlank on the
// v4 resource, so the call was a 400 no matter what the user passed.
func TestFMESpec_DeleteSegmentDefinition(t *testing.T) {
	reg := registry.New()
	if _, err := LoadSpec(reg, "fme.spec.yaml", true); err != nil {
		t.Fatalf("LoadSpec: %v", err)
	}
	cs := reg.GetSpec("delete", "segment:definition")
	if cs == nil || cs.Endpoint == nil {
		t.Fatal("delete segment:definition: command not found or missing endpoint spec")
	}

	srv, caps := fmeSequenceServer(t, []string{`{}`})

	ctx := fmeTestCtx(t, srv.URL)
	ctx.Id = "my-segment"
	ctx.Noun = "segment"
	ctx.Resolver = reg
	ctx.FormatFlags.Format = "json"
	ctx.FlagValues = map[string]any{"env": "env-uuid-1"}

	if _, err := registry.RunEndpoint(ctx, cs.Endpoint); err != nil {
		t.Fatalf("RunEndpoint: %v", err)
	}

	got := (*caps)[0]
	if got.method != "DELETE" || got.path != "/fme/api/v4/segment-definitions/my-segment" {
		t.Fatalf("request = %s %s, want DELETE /fme/api/v4/segment-definitions/my-segment", got.method, got.path)
	}
	if !strings.Contains(got.rawQuery, "environment_id=env-uuid-1") {
		t.Fatalf("query = %q, want environment_id=env-uuid-1 (from --env)", got.rawQuery)
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

// TestFMESpec_ListFeatureFlagDefinition drives "list feature_flag:definition" and asserts
// the flag-name positional arg maps to the feature_flag_name query param and get_id_expr
// resolves off it.featureFlag.name (definitions are addressed by flag name, not environment id).
func TestFMESpec_ListFeatureFlagDefinition(t *testing.T) {
	reg := registry.New()
	if _, err := LoadSpec(reg, "fme.spec.yaml", true); err != nil {
		t.Fatalf("LoadSpec: %v", err)
	}
	cs := reg.GetSpec("list", "feature_flag:definition")
	if cs == nil || cs.Endpoint == nil {
		t.Fatal("list feature_flag:definition: command not found or missing endpoint spec")
	}

	fixture := `{"data":[{"featureFlag":{"name":"cli-test-flag"},"environment":{"name":"Prod"},"defaultTreatment":"on","trafficAllocation":100,"isKilled":false,"createdAt":1778049995.725}],"limit":20,"offset":0,"totalCount":1}`
	srv, path, query := fmeCaptureServerWithQuery(t, fixture)

	ctx := fmeTestCtx(t, srv.URL)
	ctx.Noun = "feature_flag"
	ctx.ParentId = "cli-test-flag"
	ctx.Resolver = reg
	ctx.FormatFlags.Format = "json"

	if err := registry.RunListEndpoint(ctx, cs.Endpoint); err != nil {
		t.Fatalf("RunListEndpoint: %v", err)
	}

	if !strings.HasPrefix(*path, "/fme/api/v4/feature-flag-definitions") {
		t.Fatalf("request path = %q, want prefix /fme/api/v4/feature-flag-definitions", *path)
	}
	if !strings.Contains(*query, "feature_flag_name=cli-test-flag") {
		t.Fatalf("query = %q, want feature_flag_name=cli-test-flag (from parent-id arg)", *query)
	}

	body := fmeReadOut(t, ctx)
	for _, want := range []string{"Prod", "on", "100"} {
		if !strings.Contains(body, want) {
			t.Fatalf("output missing %q: %s", want, body)
		}
	}
}

// TestFMESpec_GetFeatureFlagDefinition drives "get feature_flag:definition" and asserts
// --env maps to the environment_id query param and item_expr resolves the flat item (it,
// no {entity} wrapper — the API returns the definition directly on GET).
func TestFMESpec_GetFeatureFlagDefinition(t *testing.T) {
	reg := registry.New()
	if _, err := LoadSpec(reg, "fme.spec.yaml", true); err != nil {
		t.Fatalf("LoadSpec: %v", err)
	}
	cs := reg.GetSpec("get", "feature_flag:definition")
	if cs == nil || cs.Endpoint == nil {
		t.Fatal("get feature_flag:definition: command not found or missing endpoint spec")
	}

	fixture := `{"defaultTreatment":"off","baselineTreatment":"off","trafficAllocation":50,"isKilled":false,"createdAt":1778049995.725}`
	srv, path, query := fmeCaptureServerWithQuery(t, fixture)

	ctx := fmeTestCtx(t, srv.URL)
	ctx.Id = "cli-test-flag"
	ctx.Noun = "feature_flag"
	ctx.Resolver = reg
	ctx.FormatFlags.Format = "json"
	ctx.FlagValues = map[string]any{"env": "env-uuid-1"}

	if _, err := registry.RunEndpoint(ctx, cs.Endpoint); err != nil {
		t.Fatalf("RunEndpoint: %v", err)
	}

	if *path != "/fme/api/v4/feature-flag-definitions/cli-test-flag" {
		t.Fatalf("request path = %q, want /fme/api/v4/feature-flag-definitions/cli-test-flag", *path)
	}
	if !strings.Contains(*query, "environment_id=env-uuid-1") {
		t.Fatalf("query = %q, want environment_id=env-uuid-1 (from --env flag)", *query)
	}

	body := fmeReadOut(t, ctx)
	if !strings.Contains(body, `"trafficAllocation": 50`) {
		t.Fatalf("output missing flat trafficAllocation field: %s", body)
	}
}

// TestFMESpec_CreateFeatureFlagDefinition drives "create feature_flag:definition -f <file>"
// and asserts the file body is POSTed as-is to the {flag-name} path with environment_id from
// --env, and that item_expr unwraps the {entity, governance} envelope on the response.
func TestFMESpec_CreateFeatureFlagDefinition(t *testing.T) {
	reg := registry.New()
	if _, err := LoadSpec(reg, "fme.spec.yaml", true); err != nil {
		t.Fatalf("LoadSpec: %v", err)
	}
	cs := reg.GetSpec("create", "feature_flag:definition")
	if cs == nil || cs.Endpoint == nil {
		t.Fatal("create feature_flag:definition: command not found or missing endpoint spec")
	}

	var gotMethod, gotPath, gotQuery, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"entity":`+gotBody+`,"governance":null}`)
	}))
	t.Cleanup(srv.Close)

	fileBody := `{"treatments":[{"name":"on"},{"name":"off"}],"defaultTreatment":"off","baselineTreatment":"off","trafficAllocation":100,"defaultRule":[{"treatment":"off","size":100}],"rules":[]}`
	filePath := filepath.Join(t.TempDir(), "def.json")
	if err := os.WriteFile(filePath, []byte(fileBody), 0o600); err != nil {
		t.Fatalf("writing input file: %v", err)
	}

	ctx := fmeTestCtx(t, srv.URL)
	ctx.Id = "cli-test-flag"
	ctx.Noun = "feature_flag"
	ctx.Resolver = reg
	ctx.FormatFlags.Format = "json"
	ctx.FlagValues = map[string]any{"env": "env-uuid-1", "file": filePath}

	if _, err := registry.RunEndpoint(ctx, cs.Endpoint); err != nil {
		t.Fatalf("RunEndpoint: %v", err)
	}

	if gotMethod != http.MethodPost {
		t.Fatalf("method = %q, want POST", gotMethod)
	}
	if gotPath != "/fme/api/v4/feature-flag-definitions/cli-test-flag" {
		t.Fatalf("request path = %q, want /fme/api/v4/feature-flag-definitions/cli-test-flag", gotPath)
	}
	if !strings.Contains(gotQuery, "environment_id=env-uuid-1") {
		t.Fatalf("query = %q, want environment_id=env-uuid-1 (from --env flag)", gotQuery)
	}
	if gotBody != fileBody {
		t.Fatalf("request body = %q, want file body sent as-is: %q", gotBody, fileBody)
	}

	out := fmeReadOut(t, ctx)
	if !strings.Contains(out, `"trafficAllocation": 100`) {
		t.Fatalf("output missing entity-unwrapped trafficAllocation: %s", out)
	}
}

// TestFMESpec_UpdateFeatureFlagDefinition drives "update feature_flag:definition --set
// default_treatment=on" through the get-then-patch strategy: GET fetches the current
// definition, --set mutates the picked subtree via the field's mutable_path, and PATCH sends
// the merged body as application/merge-patch+json (the FME v4 API's required content type,
// applied automatically by resolveContentType's PATCH default since the spec sets no override).
func TestFMESpec_UpdateFeatureFlagDefinition(t *testing.T) {
	reg := registry.New()
	if _, err := LoadSpec(reg, "fme.spec.yaml", true); err != nil {
		t.Fatalf("LoadSpec: %v", err)
	}
	cs := reg.GetSpec("update", "feature_flag:definition")
	if cs == nil || cs.Endpoint == nil {
		t.Fatal("update feature_flag:definition: command not found or missing endpoint spec")
	}

	getFixture := `{"defaultTreatment":"off","baselineTreatment":"off","trafficAllocation":50}`
	var gotMethod, gotPath, gotQuery, gotBody, gotContentType string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet {
			fmt.Fprint(w, getFixture)
			return
		}
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		gotContentType = r.Header.Get("Content-Type")
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		fmt.Fprint(w, `{"entity":`+gotBody+`}`)
	}))
	t.Cleanup(srv.Close)

	ctx := fmeTestCtx(t, srv.URL)
	ctx.Id = "cli-test-flag"
	ctx.Noun = "feature_flag"
	ctx.FieldsNoun = cs.FieldsNoun
	ctx.VerbHandler = cs.VerbHandler
	ctx.Resolver = reg
	ctx.FormatFlags.Format = "json"
	ctx.FlagValues = map[string]any{"env": "env-uuid-1"}
	ctx.SetArgs = map[string]string{"default_treatment": "on"}

	if _, err := registry.RunEndpoint(ctx, cs.Endpoint); err != nil {
		t.Fatalf("RunEndpoint: %v", err)
	}

	if gotMethod != http.MethodPatch {
		t.Fatalf("method = %q, want PATCH", gotMethod)
	}
	if gotPath != "/fme/api/v4/feature-flag-definitions/cli-test-flag" {
		t.Fatalf("request path = %q, want /fme/api/v4/feature-flag-definitions/cli-test-flag", gotPath)
	}
	if !strings.Contains(gotQuery, "environment_id=env-uuid-1") {
		t.Fatalf("query = %q, want environment_id=env-uuid-1 (from --env flag)", gotQuery)
	}
	if gotContentType != "application/merge-patch+json" {
		t.Fatalf("Content-Type = %q, want application/merge-patch+json", gotContentType)
	}
	if !strings.Contains(gotBody, `"defaultTreatment":"on"`) {
		t.Fatalf("PATCH body missing mutated defaultTreatment: %s", gotBody)
	}
	if !strings.Contains(gotBody, `"baselineTreatment":"off"`) {
		t.Fatalf("PATCH body missing carried-over baselineTreatment from GET: %s", gotBody)
	}
	// Neither --comment nor --title was passed, so their keys must be absent rather than
	// null: under a merge patch, null is an instruction to delete the field.
	for _, absent := range []string{`"comment"`, `"title"`} {
		if strings.Contains(gotBody, absent) {
			t.Fatalf("PATCH body contains %s though the flag was not passed; an unset body_param must be omitted, not sent as null: %s", absent, gotBody)
		}
	}
}

// TestFMESpec_UpdateFeatureFlagDefinition_AuditFields asserts --comment/--title reach the
// PATCH body. They are write-only — v4 accepts them but never returns them — so they cannot
// come from update_body_pick and are declared as body_params instead.
func TestFMESpec_UpdateFeatureFlagDefinition_AuditFields(t *testing.T) {
	reg := registry.New()
	if _, err := LoadSpec(reg, "fme.spec.yaml", true); err != nil {
		t.Fatalf("LoadSpec: %v", err)
	}
	cs := reg.GetSpec("update", "feature_flag:definition")
	if cs == nil || cs.Endpoint == nil {
		t.Fatal("update feature_flag:definition: command not found or missing endpoint spec")
	}

	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet {
			fmt.Fprint(w, `{"defaultTreatment":"off","baselineTreatment":"off","trafficAllocation":50}`)
			return
		}
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		fmt.Fprint(w, `{"entity":`+gotBody+`}`)
	}))
	t.Cleanup(srv.Close)

	ctx := fmeTestCtx(t, srv.URL)
	ctx.Id = "cli-test-flag"
	ctx.Noun = "feature_flag"
	ctx.FieldsNoun = cs.FieldsNoun
	ctx.VerbHandler = cs.VerbHandler
	ctx.Resolver = reg
	ctx.FormatFlags.Format = "json"
	ctx.FlagValues = map[string]any{"env": "env-uuid-1", "comment": "audit note", "title": "my title"}
	ctx.SetArgs = map[string]string{"default_treatment": "on"}

	if _, err := registry.RunEndpoint(ctx, cs.Endpoint); err != nil {
		t.Fatalf("RunEndpoint: %v", err)
	}

	for _, want := range []string{`"comment":"audit note"`, `"title":"my title"`, `"defaultTreatment":"on"`} {
		if !strings.Contains(gotBody, want) {
			t.Fatalf("PATCH body missing %s: %s", want, gotBody)
		}
	}
}

// TestFMESpec_DeleteFeatureFlagDefinition drives "delete feature_flag:definition" and asserts
// the DELETE request hits the {flag-name} path with environment_id from --env, and that
// VerbHandler=delete suppresses body rendering (the API returns 200 with an entity body, but
// delete commands print nothing unless a text_header/text_footer is declared).
func TestFMESpec_DeleteFeatureFlagDefinition(t *testing.T) {
	reg := registry.New()
	if _, err := LoadSpec(reg, "fme.spec.yaml", true); err != nil {
		t.Fatalf("LoadSpec: %v", err)
	}
	cs := reg.GetSpec("delete", "feature_flag:definition")
	if cs == nil || cs.Endpoint == nil {
		t.Fatal("delete feature_flag:definition: command not found or missing endpoint spec")
	}

	var gotMethod, gotPath, gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"defaultTreatment":"off","baselineTreatment":"off","trafficAllocation":100,"isKilled":false}`)
	}))
	t.Cleanup(srv.Close)

	ctx := fmeTestCtx(t, srv.URL)
	ctx.Id = "cli-test-flag"
	ctx.Noun = "feature_flag"
	ctx.VerbHandler = cs.VerbHandler
	ctx.Resolver = reg
	ctx.FormatFlags.Format = "json"
	ctx.FlagValues = map[string]any{"env": "env-uuid-1"}

	if _, err := registry.RunEndpoint(ctx, cs.Endpoint); err != nil {
		t.Fatalf("RunEndpoint: %v", err)
	}

	if gotMethod != http.MethodDelete {
		t.Fatalf("method = %q, want DELETE", gotMethod)
	}
	if gotPath != "/fme/api/v4/feature-flag-definitions/cli-test-flag" {
		t.Fatalf("request path = %q, want /fme/api/v4/feature-flag-definitions/cli-test-flag", gotPath)
	}
	if !strings.Contains(gotQuery, "environment_id=env-uuid-1") {
		t.Fatalf("query = %q, want environment_id=env-uuid-1 (from --env flag)", gotQuery)
	}
}

// fmeCaptured records one request's method/path/query/body — used by
// fmeSequenceServer for multi-request flows (e.g. get-then-patch update).
type fmeCaptured struct {
	method   string
	path     string
	rawQuery string
	body     []byte
}

// fmeSequenceServer returns a different response per call, in order,
// recording every request. Extra calls beyond len(resps) get "{}".
func fmeSequenceServer(t *testing.T, resps []string) (*httptest.Server, *[]fmeCaptured) {
	t.Helper()
	caps := &[]fmeCaptured{}
	i := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c := fmeCaptured{method: r.Method, path: r.URL.Path, rawQuery: r.URL.RawQuery}
		c.body, _ = io.ReadAll(r.Body)
		*caps = append(*caps, c)
		resp := "{}"
		if i < len(resps) {
			resp = resps[i]
			i++
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, resp)
	}))
	t.Cleanup(srv.Close)
	return srv, caps
}

// TestFMESpec_ListFeatureFlag_StatusFilter asserts --status maps to the
// "status" query param (matching @QueryParam("status") on
// FeatureFlagResource.list in service-web-admin), not "rollout_statuses"
// (FME-17257 fix — these are different v4 concepts; the old mapping made
// --status a silent no-op, since the API defaults to ACTIVE-only when the
// "status" param is absent/unrecognized).
func TestFMESpec_ListFeatureFlag_StatusFilter(t *testing.T) {
	reg := registry.New()
	if _, err := LoadSpec(reg, "fme.spec.yaml", true); err != nil {
		t.Fatalf("LoadSpec: %v", err)
	}
	cs := reg.GetSpec("list", "feature_flag")
	if cs == nil || cs.Endpoint == nil {
		t.Fatal("list feature_flag: command not found or missing endpoint spec")
	}

	fixture := `{"data":[],"limit":20,"offset":0,"totalCount":0}`
	srv, _, query := fmeCaptureServerWithQuery(t, fixture)

	ctx := fmeTestCtx(t, srv.URL)
	ctx.Noun = "feature_flag"
	ctx.Resolver = reg
	ctx.FormatFlags.Format = "json"
	ctx.FlagValues = map[string]any{"status": "ACTIVE"}

	if err := registry.RunListEndpoint(ctx, cs.Endpoint); err != nil {
		t.Fatalf("RunListEndpoint: %v", err)
	}

	if !strings.Contains(*query, "status=ACTIVE") {
		t.Fatalf("query = %q, want status=ACTIVE", *query)
	}
	if strings.Contains(*query, "rollout_statuses=") {
		t.Fatalf("query = %q, --status must not map to rollout_statuses", *query)
	}
}

// TestFMESpec_CreateFeatureFlag drives "create feature_flag" and asserts the
// POST body carries name/trafficType from ctx.id/--traffic-type, and that the
// response unwraps via item_expr: it.entity.
func TestFMESpec_CreateFeatureFlag(t *testing.T) {
	reg := registry.New()
	if _, err := LoadSpec(reg, "fme.spec.yaml", true); err != nil {
		t.Fatalf("LoadSpec: %v", err)
	}
	cs := reg.GetSpec("create", "feature_flag")
	if cs == nil || cs.Endpoint == nil {
		t.Fatal("create feature_flag: command not found or missing endpoint spec")
	}

	fixture := `{"entity":{"name":"new-flag","trafficType":{"name":"user"},"status":"ACTIVE"}}`
	srv, caps := fmeSequenceServer(t, []string{fixture})

	ctx := fmeTestCtx(t, srv.URL)
	ctx.Id = "new-flag"
	ctx.Noun = "feature_flag"
	ctx.Resolver = reg
	ctx.FormatFlags.Format = "json"
	ctx.FlagValues = map[string]any{"traffic-type": "user"}

	if _, err := registry.RunEndpoint(ctx, cs.Endpoint); err != nil {
		t.Fatalf("RunEndpoint: %v", err)
	}

	if len(*caps) != 1 {
		t.Fatalf("got %d requests, want 1", len(*caps))
	}
	got := (*caps)[0]
	if got.method != "POST" || got.path != "/fme/api/v4/feature-flags" {
		t.Fatalf("request = %s %s, want POST /fme/api/v4/feature-flags", got.method, got.path)
	}
	var body map[string]any
	if err := json.Unmarshal(got.body, &body); err != nil {
		t.Fatalf("unmarshal request body: %v", err)
	}
	if body["name"] != "new-flag" || body["trafficType"] != "user" {
		t.Fatalf("body = %v, want name=new-flag trafficType=user", body)
	}

	out := fmeReadOut(t, ctx)
	if !strings.Contains(out, "new-flag") {
		t.Fatalf("output missing new-flag (item_expr it.entity did not unwrap): %s", out)
	}
}

// TestFMESpec_UpdateFeatureFlag drives "update feature_flag --set description=..."
// and asserts the get-then-patch PATCH body is scoped to the writable fields only
// (FME-17257 fix — previously sent the whole GET'd object back, including
// immutable fields and nested objects, causing a 400 on every update).
func TestFMESpec_UpdateFeatureFlag(t *testing.T) {
	reg := registry.New()
	if _, err := LoadSpec(reg, "fme.spec.yaml", true); err != nil {
		t.Fatalf("LoadSpec: %v", err)
	}
	cs := reg.GetSpec("update", "feature_flag")
	if cs == nil || cs.Endpoint == nil {
		t.Fatal("update feature_flag: command not found or missing endpoint spec")
	}

	getResp := `{"name":"my-flag","description":"old desc","trafficType":{"name":"user"},"status":"ACTIVE","rolloutStatus":{"name":"Ramp"},"createdAt":"2026-01-01T00:00:00Z"}`
	patchResp := `{"entity":{"name":"my-flag","description":"new desc"}}`
	srv, caps := fmeSequenceServer(t, []string{getResp, patchResp})

	ctx := fmeTestCtx(t, srv.URL)
	ctx.Id = "my-flag"
	ctx.Noun = "feature_flag"
	ctx.Resolver = reg
	ctx.FormatFlags.Format = "json"
	ctx.SetArgs = map[string]string{"description": "new desc"}

	if _, err := registry.RunEndpoint(ctx, cs.Endpoint); err != nil {
		t.Fatalf("RunEndpoint: %v", err)
	}

	if len(*caps) != 2 {
		t.Fatalf("got %d requests, want 2 (GET, PATCH)", len(*caps))
	}
	patch := (*caps)[1]
	if patch.method != "PATCH" {
		t.Fatalf("2nd request method = %q, want PATCH", patch.method)
	}
	var body map[string]any
	if err := json.Unmarshal(patch.body, &body); err != nil {
		t.Fatalf("unmarshal PATCH body: %v", err)
	}
	if len(body) != 3 || body["description"] != "new desc" {
		t.Fatalf("PATCH body = %v, want {description: new desc, tags, owners} — no leaked id/createdAt/nested objects", body)
	}
	// The GET carried no tags/owners, so the carried-over collections are empty.
	for _, k := range []string{"tags", "owners"} {
		arr, ok := body[k].([]any)
		if !ok || len(arr) != 0 {
			t.Fatalf("PATCH body[%q] = %v, want empty array", k, body[k])
		}
	}
}

// TestFMESpec_UpdateFeatureFlag_TagsOwnersCarryOver drives
// "update feature_flag --set tags.gamma --del tags.alpha --set owners.user:<id>"
// and asserts two things the earlier {description: ...}-only pick got wrong:
//   - the existing tags/owners are carried into the PATCH, so a merge-patch array
//     replacement adds to / removes from the current members instead of wiping them;
//   - they are reshaped to the write DTOs (tags lose the read-only id, owners become
//     {type, id} / {type, identifier}), since v4 rejects unknown properties with a
//     400 "Invalid json structure".
func TestFMESpec_UpdateFeatureFlag_TagsOwnersCarryOver(t *testing.T) {
	reg := registry.New()
	if _, err := LoadSpec(reg, "fme.spec.yaml", true); err != nil {
		t.Fatalf("LoadSpec: %v", err)
	}
	cs := reg.GetSpec("update", "feature_flag")
	if cs == nil || cs.Endpoint == nil {
		t.Fatal("update feature_flag: command not found or missing endpoint spec")
	}

	getResp := `{"name":"my-flag","description":"old desc",` +
		`"tags":[{"id":"t1","name":"alpha"},{"id":"t2","name":"beta"}],` +
		`"owners":[{"id":"u1","name":"alice","type":"USER"},{"id":"g1","name":"platform","type":"GROUP"}]}`
	patchResp := `{"entity":{"name":"my-flag"}}`
	srv, caps := fmeSequenceServer(t, []string{getResp, patchResp})

	ctx := fmeTestCtx(t, srv.URL)
	ctx.Id = "my-flag"
	ctx.Noun = "feature_flag"
	ctx.Resolver = reg
	ctx.FormatFlags.Format = "json"
	ctx.SetArgs = map[string]string{"tags.gamma": "", "owners.user:u2": ""}
	ctx.DelArgs = []string{"tags.alpha"}

	if _, err := registry.RunEndpoint(ctx, cs.Endpoint); err != nil {
		t.Fatalf("RunEndpoint: %v", err)
	}

	var body map[string]any
	if err := json.Unmarshal((*caps)[1].body, &body); err != nil {
		t.Fatalf("unmarshal PATCH body: %v", err)
	}

	gotTags, err := json.Marshal(body["tags"])
	if err != nil {
		t.Fatalf("marshal tags: %v", err)
	}
	if wantTags := `[{"name":"beta"},{"name":"gamma"}]`; string(gotTags) != wantTags {
		t.Errorf("PATCH tags = %s, want %s (beta kept, alpha removed, gamma added, ids dropped)", gotTags, wantTags)
	}

	gotOwners, err := json.Marshal(body["owners"])
	if err != nil {
		t.Fatalf("marshal owners: %v", err)
	}
	wantOwners := `[{"id":"u1","type":"USER"},{"identifier":"g1","type":"GROUP"},{"id":"u2","type":"USER"}]`
	if string(gotOwners) != wantOwners {
		t.Errorf("PATCH owners = %s, want %s (existing kept and reshaped, u2 added)", gotOwners, wantOwners)
	}
}

// TestFMESpec_UpdateFeatureFlag_DelOwnerByID asserts --del owners.user:<id>
// matches the owner the GET returned. Owners are matched on id because the read
// shape has no email, so an email-keyed member could never match and --del
// silently did nothing.
func TestFMESpec_UpdateFeatureFlag_DelOwnerByID(t *testing.T) {
	reg := registry.New()
	if _, err := LoadSpec(reg, "fme.spec.yaml", true); err != nil {
		t.Fatalf("LoadSpec: %v", err)
	}
	cs := reg.GetSpec("update", "feature_flag")
	if cs == nil || cs.Endpoint == nil {
		t.Fatal("update feature_flag: command not found or missing endpoint spec")
	}

	getResp := `{"name":"my-flag","owners":[{"id":"u1","name":"alice","type":"USER"},{"id":"u2","name":"bob","type":"USER"}]}`
	srv, caps := fmeSequenceServer(t, []string{getResp, `{"entity":{"name":"my-flag"}}`})

	ctx := fmeTestCtx(t, srv.URL)
	ctx.Id = "my-flag"
	ctx.Noun = "feature_flag"
	ctx.Resolver = reg
	ctx.FormatFlags.Format = "json"
	ctx.DelArgs = []string{"owners.user:u1"}

	if _, err := registry.RunEndpoint(ctx, cs.Endpoint); err != nil {
		t.Fatalf("RunEndpoint: %v", err)
	}

	var body map[string]any
	if err := json.Unmarshal((*caps)[1].body, &body); err != nil {
		t.Fatalf("unmarshal PATCH body: %v", err)
	}
	got, err := json.Marshal(body["owners"])
	if err != nil {
		t.Fatalf("marshal owners: %v", err)
	}
	if want := `[{"id":"u2","type":"USER"}]`; string(got) != want {
		t.Errorf("PATCH owners = %s, want %s (u1 removed, u2 kept)", got, want)
	}
}

// TestFMESpec_UpdateFeatureFlag_RolloutStatus drives
// "update feature_flag --set rollout_status=<id>" and asserts the PATCH body
// sends {rolloutStatus: {id: ...}} — the v4 API rejects {rolloutStatus: {name: ...}}
// (the shape documented in Confluence) with a 400 "Invalid json structure".
func TestFMESpec_UpdateFeatureFlag_RolloutStatus(t *testing.T) {
	reg := registry.New()
	if _, err := LoadSpec(reg, "fme.spec.yaml", true); err != nil {
		t.Fatalf("LoadSpec: %v", err)
	}
	cs := reg.GetSpec("update", "feature_flag")
	if cs == nil || cs.Endpoint == nil {
		t.Fatal("update feature_flag: command not found or missing endpoint spec")
	}

	getResp := `{"name":"my-flag","description":"old desc","trafficType":{"name":"user"},"status":"ACTIVE","rolloutStatus":{"id":"rs-1","name":"Ramp"},"createdAt":"2026-01-01T00:00:00Z"}`
	patchResp := `{"entity":{"name":"my-flag","rolloutStatus":{"id":"rs-2","name":"Ramping"}}}`
	srv, caps := fmeSequenceServer(t, []string{getResp, patchResp})

	ctx := fmeTestCtx(t, srv.URL)
	ctx.Id = "my-flag"
	ctx.Noun = "feature_flag"
	ctx.Resolver = reg
	ctx.FormatFlags.Format = "json"
	ctx.SetArgs = map[string]string{"rollout_status": "rs-2"}

	if _, err := registry.RunEndpoint(ctx, cs.Endpoint); err != nil {
		t.Fatalf("RunEndpoint: %v", err)
	}

	patch := (*caps)[1]
	var body map[string]any
	if err := json.Unmarshal(patch.body, &body); err != nil {
		t.Fatalf("unmarshal PATCH body: %v", err)
	}
	rs, ok := body["rolloutStatus"].(map[string]any)
	if !ok || rs["id"] != "rs-2" {
		t.Fatalf("PATCH body = %v, want rolloutStatus.id=rs-2 (not rolloutStatus.name)", body)
	}
}

// TestFMESpec_DeleteFeatureFlag drives "delete feature_flag" and asserts the
// DELETE hits the expected path with no body.
func TestFMESpec_DeleteFeatureFlag(t *testing.T) {
	reg := registry.New()
	if _, err := LoadSpec(reg, "fme.spec.yaml", true); err != nil {
		t.Fatalf("LoadSpec: %v", err)
	}
	cs := reg.GetSpec("delete", "feature_flag")
	if cs == nil || cs.Endpoint == nil {
		t.Fatal("delete feature_flag: command not found or missing endpoint spec")
	}

	srv, caps := fmeSequenceServer(t, []string{`{}`})

	ctx := fmeTestCtx(t, srv.URL)
	ctx.Id = "my-flag"
	ctx.Noun = "feature_flag"
	ctx.Resolver = reg
	ctx.FormatFlags.Format = "json"

	if _, err := registry.RunEndpoint(ctx, cs.Endpoint); err != nil {
		t.Fatalf("RunEndpoint: %v", err)
	}

	if len(*caps) != 1 {
		t.Fatalf("got %d requests, want 1", len(*caps))
	}
	got := (*caps)[0]
	if got.method != "DELETE" || got.path != "/fme/api/v4/feature-flags/my-flag" {
		t.Fatalf("request = %s %s, want DELETE /fme/api/v4/feature-flags/my-flag", got.method, got.path)
	}
}

// TestFMESpec_ArchiveUnarchiveFeatureFlag asserts that both --comment and --title
// reach the archive/unarchive body. The v4 ArchiveUnarchiveRequest carries both, but
// the spec originally wired only comment, so --title was silently unsendable.
func TestFMESpec_ArchiveUnarchiveFeatureFlag(t *testing.T) {
	for _, variant := range []string{"archive", "unarchive"} {
		t.Run(variant, func(t *testing.T) {
			reg := registry.New()
			if _, err := LoadSpec(reg, "fme.spec.yaml", true); err != nil {
				t.Fatalf("LoadSpec: %v", err)
			}
			cs := reg.GetSpec("execute", "feature_flag:"+variant)
			if cs == nil || cs.Endpoint == nil {
				t.Fatalf("execute feature_flag:%s: command not found or missing endpoint spec", variant)
			}

			srv, caps := fmeSequenceServer(t, []string{`{"entity":{"name":"my-flag"}}`})

			ctx := fmeTestCtx(t, srv.URL)
			ctx.Id = "my-flag"
			ctx.Noun = "feature_flag"
			ctx.Resolver = reg
			ctx.FormatFlags.Format = "json"
			ctx.FlagValues = map[string]any{"comment": "audit why", "title": "audit what"}

			if _, err := registry.RunEndpoint(ctx, cs.Endpoint); err != nil {
				t.Fatalf("RunEndpoint: %v", err)
			}

			if len(*caps) != 1 {
				t.Fatalf("got %d requests, want 1", len(*caps))
			}
			got := (*caps)[0]
			wantPath := "/fme/api/v4/feature-flags/my-flag/" + variant
			if got.method != "POST" || got.path != wantPath {
				t.Fatalf("request = %s %s, want POST %s", got.method, got.path, wantPath)
			}
			var body map[string]any
			if err := json.Unmarshal(got.body, &body); err != nil {
				t.Fatalf("unmarshal request body: %v", err)
			}
			if body["comment"] != "audit why" || body["title"] != "audit what" {
				t.Fatalf("body = %v, want comment=%q title=%q", body, "audit why", "audit what")
			}
		})
	}
}
