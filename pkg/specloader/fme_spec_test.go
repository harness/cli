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

	"github.com/harness/cli/v3/modules/fme"
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

// TestFMESpec_UpdateFMEEnvironment checks that a name change sends no unchanged GET fields.
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
	if gotBody != `{"name":"Renamed"}` {
		t.Fatalf("PATCH body = %s, want only changed name", gotBody)
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

// TestFMESpec_UpdateSegment asserts the get-then-patch body is narrowed to the fields
// UpdateSegmentRequest accepts. Echoing the GET back (the previous update_body_pick: it)
// sent id/name/trafficType/status/segmentType, which v4 rejects as unknown properties,
// so every segment update was a 400.
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
	if len(body) != 1 || body["description"] != "new desc" {
		t.Fatalf("PATCH body = %v, want exactly {description: new desc} — no leaked id/name/trafficType/status/segmentType", body)
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

// TestFMESpec_UpdateFeatureFlagDefinition checks that only the changed treatment is sent.
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
	if gotBody != `{"defaultTreatment":"on"}` {
		t.Fatalf("PATCH body = %s, want only changed defaultTreatment", gotBody)
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

// TestFMESpec_ExecuteFeatureFlagKill_AuditFields asserts --comment/--title reach the POST
// body for "execute feature_flag:kill", and that an unset title is omitted rather than sent
// as an empty string.
func TestFMESpec_ExecuteFeatureFlagKill_AuditFields(t *testing.T) {
	reg := registry.New()
	if _, err := LoadSpec(reg, "fme.spec.yaml", true); err != nil {
		t.Fatalf("LoadSpec: %v", err)
	}
	cs := reg.GetSpec("execute", "feature_flag:kill")
	if cs == nil || cs.Endpoint == nil {
		t.Fatal("execute feature_flag:kill: command not found or missing endpoint spec")
	}

	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"entity":`+gotBody+`}`)
	}))
	t.Cleanup(srv.Close)

	ctx := fmeTestCtx(t, srv.URL)
	ctx.Id = "cli-test-flag"
	ctx.Noun = "feature_flag"
	ctx.VerbHandler = cs.VerbHandler
	ctx.Resolver = reg
	ctx.FormatFlags.Format = "json"
	ctx.FlagValues = map[string]any{"env": "env-uuid-1", "comment": "audit note", "title": "my title"}

	if _, err := registry.RunEndpoint(ctx, cs.Endpoint); err != nil {
		t.Fatalf("RunEndpoint: %v", err)
	}

	for _, want := range []string{`"comment":"audit note"`, `"title":"my title"`} {
		if !strings.Contains(gotBody, want) {
			t.Fatalf("POST body missing %s: %s", want, gotBody)
		}
	}

	ctx.FlagValues = map[string]any{"env": "env-uuid-1"}
	if _, err := registry.RunEndpoint(ctx, cs.Endpoint); err != nil {
		t.Fatalf("RunEndpoint: %v", err)
	}
	for _, absent := range []string{`"comment"`, `"title"`} {
		if strings.Contains(gotBody, absent) {
			t.Fatalf("POST body contains %s though neither flag was passed: %s", absent, gotBody)
		}
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
	if len(body) != 1 || body["description"] != "new desc" {
		t.Fatalf("PATCH body = %v, want exactly {description: new desc} — no leaked id/createdAt/nested objects", body)
	}
}

// TestFMESpec_UpdateFeatureFlag_RolloutStatus drives
// "update feature_flag --set rollout_status=<id>" and asserts the PATCH body
// sends {rolloutStatus: {id: ...}} — the v4 API rejects {rolloutStatus: {name: ...}}
// (the shape documented in Confluence) with a 400 "Invalid json structure".
//
// An unchanged description must be omitted, not sent as a deletion.
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
	if _, present := body["description"]; present || len(body) != 1 {
		t.Errorf("PATCH body = %v, want only rolloutStatus", body)
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

// TestFMESpec_UpdateFeatureFlag_AddDelTags drives "update feature_flag --add
// tags.foo --del tags.delta" and asserts the PATCH body carries the surviving
// tags collection (epsilon kept, delta removed, foo appended) and omits
// description/owners, which were never touched (FME-17257: fme:tags field type).
func TestFMESpec_UpdateFeatureFlag_AddDelTags(t *testing.T) {
	reg := registry.New()
	fme.ModuleInit(reg.Module("fme"))
	if _, err := LoadSpec(reg, "fme.spec.yaml", true); err != nil {
		t.Fatalf("LoadSpec: %v", err)
	}
	cs := reg.GetSpec("update", "feature_flag")
	if cs == nil || cs.Endpoint == nil {
		t.Fatal("update feature_flag: command not found or missing endpoint spec")
	}

	getResp := `{"name":"my-flag","description":"old desc","trafficType":{"name":"user"},"status":"ACTIVE",` +
		`"rolloutStatus":{"name":"Ramp"},` +
		`"tags":[{"id":"t1","name":"delta"},{"id":"t2","name":"epsilon"}],` +
		`"owners":[{"id":"u1","type":"USER","name":"Alice"}],"createdAt":"2026-01-01T00:00:00Z"}`
	patchResp := `{"entity":{"name":"my-flag"}}`
	srv, caps := fmeSequenceServer(t, []string{getResp, patchResp})

	ctx := fmeTestCtx(t, srv.URL)
	ctx.Id = "my-flag"
	ctx.Noun = "feature_flag"
	ctx.Resolver = reg
	ctx.FormatFlags.Format = "json"
	ctx.MutationOrderCaptured = true
	ctx.MutationFlags = []cmdctx.FieldMutation{
		{Kind: cmdctx.MutationDelete, Key: "tags.delta", Raw: "tags.delta"},
		{Kind: cmdctx.MutationAdd, Key: "tags.foo", Raw: "tags.foo"},
	}

	if _, err := registry.RunEndpoint(ctx, cs.Endpoint); err != nil {
		t.Fatalf("RunEndpoint: %v", err)
	}

	patch := (*caps)[1]
	var body map[string]any
	if err := json.Unmarshal(patch.body, &body); err != nil {
		t.Fatalf("unmarshal PATCH body: %v", err)
	}
	if _, present := body["description"]; present {
		t.Errorf("PATCH body = %v, description should be omitted (untouched)", body)
	}
	if _, present := body["owners"]; present {
		t.Errorf("PATCH body = %v, owners should be omitted (untouched)", body)
	}
	tags, ok := body["tags"].([]any)
	if !ok || len(tags) != 2 {
		t.Fatalf("PATCH body tags = %v, want 2 entries (epsilon, foo)", body["tags"])
	}
	got := []string{tagName(tags[0]), tagName(tags[1])}
	want := []string{"epsilon", "foo"}
	if got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("PATCH body tags = %v, want %v", got, want)
	}
	for _, tag := range tags {
		m := tag.(map[string]any)
		if len(m) != 1 {
			t.Errorf("tag entry %v carries more than {name}; expected read-only id stripped", m)
		}
	}
}

func tagName(v any) string {
	m, _ := v.(map[string]any)
	name, _ := m["name"].(string)
	return name
}

// TestFMESpec_UpdateFeatureFlag_AddDelOwners drives "update feature_flag --del
// owners.user:u1 --add owners.user:bob@example.com" and asserts the PATCH body
// sends the write shape ({type, id|email}) with the deleted USER owner gone
// and the new one added by email (FME-17257: fme:owners field type, owners by email).
func TestFMESpec_UpdateFeatureFlag_AddDelOwners(t *testing.T) {
	reg := registry.New()
	fme.ModuleInit(reg.Module("fme"))
	if _, err := LoadSpec(reg, "fme.spec.yaml", true); err != nil {
		t.Fatalf("LoadSpec: %v", err)
	}
	cs := reg.GetSpec("update", "feature_flag")
	if cs == nil || cs.Endpoint == nil {
		t.Fatal("update feature_flag: command not found or missing endpoint spec")
	}

	getResp := `{"name":"my-flag","description":"old desc","trafficType":{"name":"user"},"status":"ACTIVE",` +
		`"rolloutStatus":{"name":"Ramp"},"tags":[],` +
		`"owners":[{"id":"u1","type":"USER","name":"Alice"}],"createdAt":"2026-01-01T00:00:00Z"}`
	patchResp := `{"entity":{"name":"my-flag"}}`
	srv, caps := fmeSequenceServer(t, []string{getResp, patchResp})

	ctx := fmeTestCtx(t, srv.URL)
	ctx.Id = "my-flag"
	ctx.Noun = "feature_flag"
	ctx.Resolver = reg
	ctx.FormatFlags.Format = "json"
	ctx.MutationOrderCaptured = true
	ctx.MutationFlags = []cmdctx.FieldMutation{
		{Kind: cmdctx.MutationDelete, Key: "owners.user:u1", Raw: "owners.user:u1"},
		{Kind: cmdctx.MutationAdd, Key: "owners.user:bob@example.com", Raw: "owners.user:bob@example.com"},
	}

	if _, err := registry.RunEndpoint(ctx, cs.Endpoint); err != nil {
		t.Fatalf("RunEndpoint: %v", err)
	}

	patch := (*caps)[1]
	var body map[string]any
	if err := json.Unmarshal(patch.body, &body); err != nil {
		t.Fatalf("unmarshal PATCH body: %v", err)
	}
	owners, ok := body["owners"].([]any)
	if !ok || len(owners) != 1 {
		t.Fatalf("PATCH body owners = %v, want exactly 1 entry (bob, added by email)", body["owners"])
	}
	entry := owners[0].(map[string]any)
	if entry["type"] != "USER" || entry["email"] != "bob@example.com" {
		t.Fatalf("owner entry = %v, want {type: USER, email: bob@example.com}", entry)
	}
	if _, present := entry["id"]; present {
		t.Errorf("owner entry = %v, added-by-email owner should not carry an id", entry)
	}
}

// TestFMESpec_UpdateFeatureFlag_Owners_PreservedGroupRoundTrips asserts that an
// existing GROUP owner picked up from a GET — whose read shape carries the
// group's identifier under "id" (Harness user groups have no separate UUID;
// their identifier is their id) — is correctly re-encoded as {type: GROUP,
// identifier} when preserved across an unrelated owners edit, instead of
// being dropped or erroring (FME-17257; verified live against qa0).
func TestFMESpec_UpdateFeatureFlag_Owners_PreservedGroupRoundTrips(t *testing.T) {
	reg := registry.New()
	fme.ModuleInit(reg.Module("fme"))
	if _, err := LoadSpec(reg, "fme.spec.yaml", true); err != nil {
		t.Fatalf("LoadSpec: %v", err)
	}
	cs := reg.GetSpec("update", "feature_flag")
	if cs == nil || cs.Endpoint == nil {
		t.Fatal("update feature_flag: command not found or missing endpoint spec")
	}

	getResp := `{"name":"my-flag","description":"old desc","trafficType":{"name":"user"},"status":"ACTIVE",` +
		`"rolloutStatus":{"name":"Ramp"},"tags":[],` +
		`"owners":[{"id":"platform-team","type":"GROUP","name":"platform-team"}],"createdAt":"2026-01-01T00:00:00Z"}`
	srv, caps := fmeSequenceServer(t, []string{getResp, `{"entity":{"name":"my-flag"}}`})

	ctx := fmeTestCtx(t, srv.URL)
	ctx.Id = "my-flag"
	ctx.Noun = "feature_flag"
	ctx.Resolver = reg
	ctx.FormatFlags.Format = "json"
	ctx.MutationOrderCaptured = true
	ctx.MutationFlags = []cmdctx.FieldMutation{
		{Kind: cmdctx.MutationAdd, Key: "owners.user:bob@example.com", Raw: "owners.user:bob@example.com"},
	}

	if _, err := registry.RunEndpoint(ctx, cs.Endpoint); err != nil {
		t.Fatalf("RunEndpoint: %v", err)
	}

	var body map[string]any
	if err := json.Unmarshal((*caps)[len(*caps)-1].body, &body); err != nil {
		t.Fatalf("unmarshal request body: %v", err)
	}
	owners, _ := body["owners"].([]any)
	if len(owners) != 2 {
		t.Fatalf("owners = %v, want 2 entries (preserved group + added user)", owners)
	}
	group, ok := owners[0].(map[string]any)
	if !ok || group["type"] != "GROUP" || group["identifier"] != "platform-team" {
		t.Fatalf("owners[0] = %v, want {type: GROUP, identifier: platform-team}", owners[0])
	}
}

// TestFMESpec_CreateFeatureFlag_TagsOwners drives "create feature_flag --add
// tags.foo --add owners.user:bob@example.com" (create_strategy: set-fields,
// no GET) and asserts the POST body carries both collections in their write
// shape from a first --add touch (FME-17257).
func TestFMESpec_CreateFeatureFlag_TagsOwners(t *testing.T) {
	reg := registry.New()
	fme.ModuleInit(reg.Module("fme"))
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
	ctx.MutationOrderCaptured = true
	ctx.MutationFlags = []cmdctx.FieldMutation{
		{Kind: cmdctx.MutationAdd, Key: "tags.foo", Raw: "tags.foo"},
		{Kind: cmdctx.MutationAdd, Key: "owners.user:bob@example.com", Raw: "owners.user:bob@example.com"},
	}

	if _, err := registry.RunEndpoint(ctx, cs.Endpoint); err != nil {
		t.Fatalf("RunEndpoint: %v", err)
	}

	got := (*caps)[0]
	var body map[string]any
	if err := json.Unmarshal(got.body, &body); err != nil {
		t.Fatalf("unmarshal request body: %v", err)
	}
	tags, ok := body["tags"].([]any)
	if !ok || len(tags) != 1 || tagName(tags[0]) != "foo" {
		t.Fatalf("body tags = %v, want [{name: foo}]", body["tags"])
	}
	owners, ok := body["owners"].([]any)
	if !ok || len(owners) != 1 {
		t.Fatalf("body owners = %v, want 1 entry", body["owners"])
	}
	entry := owners[0].(map[string]any)
	if entry["type"] != "USER" || entry["email"] != "bob@example.com" {
		t.Fatalf("owner entry = %v, want {type: USER, email: bob@example.com}", entry)
	}
}

// TestFMESpec_UpdateSegment_AddDelTags mirrors
// TestFMESpec_UpdateFeatureFlag_AddDelTags for the segment noun, which shares
// the fme:tags field type (FME-17257 follow-up).
func TestFMESpec_UpdateSegment_AddDelTags(t *testing.T) {
	reg := registry.New()
	fme.ModuleInit(reg.Module("fme"))
	if _, err := LoadSpec(reg, "fme.spec.yaml", true); err != nil {
		t.Fatalf("LoadSpec: %v", err)
	}
	cs := reg.GetSpec("update", "segment")
	if cs == nil || cs.Endpoint == nil {
		t.Fatal("update segment: command not found or missing endpoint spec")
	}

	getResp := `{"name":"my-segment","description":"old desc","trafficType":{"name":"user"},"status":"ACTIVE",` +
		`"segmentType":"STANDARD",` +
		`"tags":[{"id":"t1","name":"delta"},{"id":"t2","name":"epsilon"}],` +
		`"owners":[{"id":"u1","type":"USER","name":"Alice"}],"createdAt":"2026-01-01T00:00:00Z"}`
	srv, caps := fmeSequenceServer(t, []string{getResp, `{"entity":{"name":"my-segment"}}`})

	ctx := fmeTestCtx(t, srv.URL)
	ctx.Id = "my-segment"
	ctx.Noun = "segment"
	ctx.Resolver = reg
	ctx.FormatFlags.Format = "json"
	ctx.FlagValues = map[string]any{"segment-type": "STANDARD"}
	ctx.MutationOrderCaptured = true
	ctx.MutationFlags = []cmdctx.FieldMutation{
		{Kind: cmdctx.MutationDelete, Key: "tags.delta", Raw: "tags.delta"},
		{Kind: cmdctx.MutationAdd, Key: "tags.foo", Raw: "tags.foo"},
	}

	if _, err := registry.RunEndpoint(ctx, cs.Endpoint); err != nil {
		t.Fatalf("RunEndpoint: %v", err)
	}

	patch := (*caps)[1]
	var body map[string]any
	if err := json.Unmarshal(patch.body, &body); err != nil {
		t.Fatalf("unmarshal PATCH body: %v", err)
	}
	if _, present := body["owners"]; present {
		t.Errorf("PATCH body = %v, owners should be omitted (untouched)", body)
	}
	tags, ok := body["tags"].([]any)
	if !ok || len(tags) != 2 {
		t.Fatalf("PATCH body tags = %v, want 2 entries (epsilon, foo)", body["tags"])
	}
	got := []string{tagName(tags[0]), tagName(tags[1])}
	want := []string{"epsilon", "foo"}
	if got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("PATCH body tags = %v, want %v", got, want)
	}
}

// TestFMESpec_UpdateSegment_AddDelOwners mirrors
// TestFMESpec_UpdateFeatureFlag_AddDelOwners for the segment noun
// (FME-17257 follow-up).
func TestFMESpec_UpdateSegment_AddDelOwners(t *testing.T) {
	reg := registry.New()
	fme.ModuleInit(reg.Module("fme"))
	if _, err := LoadSpec(reg, "fme.spec.yaml", true); err != nil {
		t.Fatalf("LoadSpec: %v", err)
	}
	cs := reg.GetSpec("update", "segment")
	if cs == nil || cs.Endpoint == nil {
		t.Fatal("update segment: command not found or missing endpoint spec")
	}

	getResp := `{"name":"my-segment","description":"old desc","trafficType":{"name":"user"},"status":"ACTIVE",` +
		`"segmentType":"STANDARD","tags":[],` +
		`"owners":[{"id":"u1","type":"USER","name":"Alice"}],"createdAt":"2026-01-01T00:00:00Z"}`
	srv, caps := fmeSequenceServer(t, []string{getResp, `{"entity":{"name":"my-segment"}}`})

	ctx := fmeTestCtx(t, srv.URL)
	ctx.Id = "my-segment"
	ctx.Noun = "segment"
	ctx.Resolver = reg
	ctx.FormatFlags.Format = "json"
	ctx.FlagValues = map[string]any{"segment-type": "STANDARD"}
	ctx.MutationOrderCaptured = true
	ctx.MutationFlags = []cmdctx.FieldMutation{
		{Kind: cmdctx.MutationDelete, Key: "owners.user:u1", Raw: "owners.user:u1"},
		{Kind: cmdctx.MutationAdd, Key: "owners.user:bob@example.com", Raw: "owners.user:bob@example.com"},
	}

	if _, err := registry.RunEndpoint(ctx, cs.Endpoint); err != nil {
		t.Fatalf("RunEndpoint: %v", err)
	}

	patch := (*caps)[1]
	var body map[string]any
	if err := json.Unmarshal(patch.body, &body); err != nil {
		t.Fatalf("unmarshal PATCH body: %v", err)
	}
	owners, ok := body["owners"].([]any)
	if !ok || len(owners) != 1 {
		t.Fatalf("PATCH body owners = %v, want exactly 1 entry (bob, added by email)", body["owners"])
	}
	entry := owners[0].(map[string]any)
	if entry["type"] != "USER" || entry["email"] != "bob@example.com" {
		t.Fatalf("owner entry = %v, want {type: USER, email: bob@example.com}", entry)
	}
}

// TestFMESpec_CreateSegment_TagsOwners mirrors
// TestFMESpec_CreateFeatureFlag_TagsOwners for the segment noun
// (FME-17257 follow-up).
func TestFMESpec_CreateSegment_TagsOwners(t *testing.T) {
	reg := registry.New()
	fme.ModuleInit(reg.Module("fme"))
	if _, err := LoadSpec(reg, "fme.spec.yaml", true); err != nil {
		t.Fatalf("LoadSpec: %v", err)
	}
	cs := reg.GetSpec("create", "segment")
	if cs == nil || cs.Endpoint == nil {
		t.Fatal("create segment: command not found or missing endpoint spec")
	}

	fixture := `{"entity":{"name":"new-segment","trafficType":{"name":"user"},"status":"ACTIVE"}}`
	srv, caps := fmeSequenceServer(t, []string{fixture})

	ctx := fmeTestCtx(t, srv.URL)
	ctx.Id = "new-segment"
	ctx.Noun = "segment"
	ctx.Resolver = reg
	ctx.FormatFlags.Format = "json"
	ctx.FlagValues = map[string]any{"traffic-type": "user", "segment-type": "STANDARD"}
	ctx.MutationOrderCaptured = true
	ctx.MutationFlags = []cmdctx.FieldMutation{
		{Kind: cmdctx.MutationAdd, Key: "tags.foo", Raw: "tags.foo"},
		{Kind: cmdctx.MutationAdd, Key: "owners.user:bob@example.com", Raw: "owners.user:bob@example.com"},
	}

	if _, err := registry.RunEndpoint(ctx, cs.Endpoint); err != nil {
		t.Fatalf("RunEndpoint: %v", err)
	}

	got := (*caps)[0]
	var body map[string]any
	if err := json.Unmarshal(got.body, &body); err != nil {
		t.Fatalf("unmarshal request body: %v", err)
	}
	tags, ok := body["tags"].([]any)
	if !ok || len(tags) != 1 || tagName(tags[0]) != "foo" {
		t.Fatalf("body tags = %v, want [{name: foo}]", body["tags"])
	}
	owners, ok := body["owners"].([]any)
	if !ok || len(owners) != 1 {
		t.Fatalf("body owners = %v, want 1 entry", body["owners"])
	}
	entry := owners[0].(map[string]any)
	if entry["type"] != "USER" || entry["email"] != "bob@example.com" {
		t.Fatalf("owner entry = %v, want {type: USER, email: bob@example.com}", entry)
	}
}

// TestFMESpec_CreateFeatureFlag_FileBody drives "create feature_flag <name> -f ff.json"
// with no --traffic-type flag at all, confirming file_body: optional lets -f alone satisfy
// what --traffic-type used to enforce via required: true (that required flag blocked any
// -f-only invocation with a 400 before the request even went out, the same bug class
// FME-19300 fixed for metric).
func TestFMESpec_CreateFeatureFlag_FileBody(t *testing.T) {
	reg := registry.New()
	fme.ModuleInit(reg.Module("fme"))
	if _, err := LoadSpec(reg, "fme.spec.yaml", true); err != nil {
		t.Fatalf("LoadSpec: %v", err)
	}
	cs := reg.GetSpec("create", "feature_flag")
	if cs == nil || cs.Endpoint == nil {
		t.Fatal("create feature_flag: command not found or missing endpoint spec")
	}

	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"entity":`+gotBody+`}`)
	}))
	t.Cleanup(srv.Close)

	fileBody := `{"trafficType":"user","description":"from file"}`
	filePath := filepath.Join(t.TempDir(), "ff.json")
	if err := os.WriteFile(filePath, []byte(fileBody), 0o600); err != nil {
		t.Fatalf("writing input file: %v", err)
	}

	ctx := fmeTestCtx(t, srv.URL)
	ctx.Id = "new-flag"
	ctx.Noun = "feature_flag"
	ctx.Resolver = reg
	ctx.FormatFlags.Format = "json"
	ctx.FlagValues = map[string]any{"file": filePath}

	if _, err := registry.RunEndpoint(ctx, cs.Endpoint); err != nil {
		t.Fatalf("RunEndpoint: %v", err)
	}

	var body map[string]any
	if err := json.Unmarshal([]byte(gotBody), &body); err != nil {
		t.Fatalf("unmarshal request body: %v", err)
	}
	if body["trafficType"] != "user" || body["description"] != "from file" {
		t.Fatalf("body = %v, want trafficType/description from file", body)
	}
	if body["name"] != "new-flag" {
		t.Fatalf("body[name] = %v, want new-flag (from create_body_init, not overridden by file)", body["name"])
	}
}

// TestFMESpec_UpdateFeatureFlag_FileBody drives "update feature_flag <name> -f patch.json",
// confirming the file is sent as-is (no GET, no update_body_pick) as a merge-patch document.
func TestFMESpec_UpdateFeatureFlag_FileBody(t *testing.T) {
	reg := registry.New()
	fme.ModuleInit(reg.Module("fme"))
	if _, err := LoadSpec(reg, "fme.spec.yaml", true); err != nil {
		t.Fatalf("LoadSpec: %v", err)
	}
	cs := reg.GetSpec("update", "feature_flag")
	if cs == nil || cs.Endpoint == nil {
		t.Fatal("update feature_flag: command not found or missing endpoint spec")
	}

	var gotMethod, gotPath, gotCT, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotCT = r.Header.Get("Content-Type")
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"entity":`+gotBody+`}`)
	}))
	t.Cleanup(srv.Close)

	fileBody := `{"description":"new desc"}`
	filePath := filepath.Join(t.TempDir(), "patch.json")
	if err := os.WriteFile(filePath, []byte(fileBody), 0o600); err != nil {
		t.Fatalf("writing input file: %v", err)
	}

	ctx := fmeTestCtx(t, srv.URL)
	ctx.Id = "my-flag"
	ctx.Noun = "feature_flag"
	ctx.Resolver = reg
	ctx.FormatFlags.Format = "json"
	ctx.FlagValues = map[string]any{"file": filePath}

	if _, err := registry.RunEndpoint(ctx, cs.Endpoint); err != nil {
		t.Fatalf("RunEndpoint: %v", err)
	}

	if gotMethod != "PATCH" || gotPath != "/fme/api/v4/feature-flags/my-flag" {
		t.Fatalf("request = %s %s, want PATCH /fme/api/v4/feature-flags/my-flag", gotMethod, gotPath)
	}
	if gotCT != "application/merge-patch+json" {
		t.Fatalf("Content-Type = %q, want application/merge-patch+json", gotCT)
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(gotBody), &body); err != nil {
		t.Fatalf("unmarshal PATCH body: %v", err)
	}
	if len(body) != 1 || body["description"] != "new desc" {
		t.Fatalf("PATCH body = %v, want exactly {description: new desc} (file sent as-is, no GET/pick)", body)
	}
}

// TestFMESpec_CreateSegment_FileBody drives "create segment <name> -f segment.json" with
// neither --traffic-type nor --segment-type passed, confirming both required: true flags
// were correctly dropped (file_body: optional now lets -f alone satisfy them).
func TestFMESpec_CreateSegment_FileBody(t *testing.T) {
	reg := registry.New()
	fme.ModuleInit(reg.Module("fme"))
	if _, err := LoadSpec(reg, "fme.spec.yaml", true); err != nil {
		t.Fatalf("LoadSpec: %v", err)
	}
	cs := reg.GetSpec("create", "segment")
	if cs == nil || cs.Endpoint == nil {
		t.Fatal("create segment: command not found or missing endpoint spec")
	}

	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"entity":`+gotBody+`}`)
	}))
	t.Cleanup(srv.Close)

	fileBody := `{"trafficType":"user","segmentType":"STANDARD","description":"from file"}`
	filePath := filepath.Join(t.TempDir(), "segment.json")
	if err := os.WriteFile(filePath, []byte(fileBody), 0o600); err != nil {
		t.Fatalf("writing input file: %v", err)
	}

	ctx := fmeTestCtx(t, srv.URL)
	ctx.Id = "new-segment"
	ctx.Noun = "segment"
	ctx.Resolver = reg
	ctx.FormatFlags.Format = "json"
	ctx.FlagValues = map[string]any{"file": filePath}

	if _, err := registry.RunEndpoint(ctx, cs.Endpoint); err != nil {
		t.Fatalf("RunEndpoint: %v", err)
	}

	var body map[string]any
	if err := json.Unmarshal([]byte(gotBody), &body); err != nil {
		t.Fatalf("unmarshal request body: %v", err)
	}
	if body["trafficType"] != "user" || body["segmentType"] != "STANDARD" || body["description"] != "from file" {
		t.Fatalf("body = %v, want trafficType/segmentType/description from file", body)
	}
	if body["name"] != "new-segment" {
		t.Fatalf("body[name] = %v, want new-segment (from create_body_init, not overridden by file)", body["name"])
	}
}

// TestFMESpec_UpdateSegment_FileBody drives "update segment <name> --segment-type STANDARD
// -f patch.json", confirming the file is sent as-is as the merge-patch document while
// --segment-type still reaches the query string (it stays required: true because it routes
// to the right partition, not a body field a file could supply instead).
func TestFMESpec_UpdateSegment_FileBody(t *testing.T) {
	reg := registry.New()
	fme.ModuleInit(reg.Module("fme"))
	if _, err := LoadSpec(reg, "fme.spec.yaml", true); err != nil {
		t.Fatalf("LoadSpec: %v", err)
	}
	cs := reg.GetSpec("update", "segment")
	if cs == nil || cs.Endpoint == nil {
		t.Fatal("update segment: command not found or missing endpoint spec")
	}

	var gotMethod, gotPath, gotQuery, gotCT, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		gotCT = r.Header.Get("Content-Type")
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"entity":`+gotBody+`}`)
	}))
	t.Cleanup(srv.Close)

	fileBody := `{"description":"new desc"}`
	filePath := filepath.Join(t.TempDir(), "patch.json")
	if err := os.WriteFile(filePath, []byte(fileBody), 0o600); err != nil {
		t.Fatalf("writing input file: %v", err)
	}

	ctx := fmeTestCtx(t, srv.URL)
	ctx.Id = "my-segment"
	ctx.Noun = "segment"
	ctx.Resolver = reg
	ctx.FormatFlags.Format = "json"
	ctx.FlagValues = map[string]any{"segment-type": "STANDARD", "file": filePath}

	if _, err := registry.RunEndpoint(ctx, cs.Endpoint); err != nil {
		t.Fatalf("RunEndpoint: %v", err)
	}

	if gotMethod != "PATCH" || gotPath != "/fme/api/v4/segments/my-segment" {
		t.Fatalf("request = %s %s, want PATCH /fme/api/v4/segments/my-segment", gotMethod, gotPath)
	}
	if !strings.Contains(gotQuery, "segment_type=STANDARD") {
		t.Fatalf("query = %q, want segment_type=STANDARD", gotQuery)
	}
	if gotCT != "application/merge-patch+json" {
		t.Fatalf("Content-Type = %q, want application/merge-patch+json", gotCT)
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(gotBody), &body); err != nil {
		t.Fatalf("unmarshal PATCH body: %v", err)
	}
	if len(body) != 1 || body["description"] != "new desc" {
		t.Fatalf("PATCH body = %v, want exactly {description: new desc} (file sent as-is, no GET/pick)", body)
	}
}

// TestFMESpec_UpdateFeatureFlagDefinition_AddDelFlagSets drives "update ff:definition <name>
// --env <id> --add flag_sets.<id> / --del flag_sets.<id>", asserting the fme:flag_sets field
// type produces the {id} write shape (FlagSetReference — no name/type accepted) and that
// flagSets being in update_body_pick lets --add/--del touch it without dropping the other
// picked scalars (defaultTreatment/baselineTreatment/trafficAllocation) from the merge patch.
func TestFMESpec_UpdateFeatureFlagDefinition_AddDelFlagSets(t *testing.T) {
	reg := registry.New()
	fme.ModuleInit(reg.Module("fme"))
	if _, err := LoadSpec(reg, "fme.spec.yaml", true); err != nil {
		t.Fatalf("LoadSpec: %v", err)
	}
	cs := reg.GetSpec("update", "feature_flag:definition")
	if cs == nil || cs.Endpoint == nil {
		t.Fatal("update feature_flag:definition: command not found or missing endpoint spec")
	}

	getResp := `{"defaultTreatment":"off","baselineTreatment":"off","trafficAllocation":50,` +
		`"flagSets":[{"id":"fs-1"},{"id":"fs-2"}]}`
	patchResp := `{"entity":{"defaultTreatment":"off"}}`
	srv, caps := fmeSequenceServer(t, []string{getResp, patchResp})

	ctx := fmeTestCtx(t, srv.URL)
	ctx.Id = "cli-test-flag"
	ctx.Noun = "feature_flag"
	ctx.FieldsNoun = cs.FieldsNoun
	ctx.VerbHandler = cs.VerbHandler
	ctx.Resolver = reg
	ctx.FormatFlags.Format = "json"
	ctx.FlagValues = map[string]any{"env": "env-uuid-1"}
	ctx.MutationOrderCaptured = true
	ctx.MutationFlags = []cmdctx.FieldMutation{
		{Kind: cmdctx.MutationDelete, Key: "flag_sets.fs-1", Raw: "flag_sets.fs-1"},
		{Kind: cmdctx.MutationAdd, Key: "flag_sets.fs-3", Raw: "flag_sets.fs-3"},
	}

	if _, err := registry.RunEndpoint(ctx, cs.Endpoint); err != nil {
		t.Fatalf("RunEndpoint: %v", err)
	}

	patch := (*caps)[1]
	var body map[string]any
	if err := json.Unmarshal(patch.body, &body); err != nil {
		t.Fatalf("unmarshal PATCH body: %v", err)
	}
	if _, present := body["defaultTreatment"]; present {
		t.Errorf("PATCH body = %v, defaultTreatment should be omitted (untouched)", body)
	}
	flagSets, ok := body["flagSets"].([]any)
	if !ok || len(flagSets) != 2 {
		t.Fatalf("PATCH body flagSets = %v, want 2 entries (fs-2, fs-3)", body["flagSets"])
	}
	idOf := func(v any) string {
		m, _ := v.(map[string]any)
		id, _ := m["id"].(string)
		return id
	}
	got := []string{idOf(flagSets[0]), idOf(flagSets[1])}
	want := []string{"fs-2", "fs-3"}
	if got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("PATCH body flagSets = %v, want %v", got, want)
	}
	for _, fs := range flagSets {
		if m := fs.(map[string]any); len(m) != 1 {
			t.Errorf("flag set entry %v carries more than {id}", m)
		}
	}
}

func TestFMESpec_ListMetrics(t *testing.T) {
	reg := registry.New()
	if _, err := LoadSpec(reg, "fme.spec.yaml", true); err != nil {
		t.Fatalf("LoadSpec: %v", err)
	}
	cs := reg.GetSpec("list", "metric")
	if cs == nil || cs.Endpoint == nil {
		t.Fatal("list metric: command not found or missing endpoint spec")
	}

	fixture := `{"data":[{"id":"metric-1","name":"my-metric","trafficType":{"name":"user"},"format":"NUMBER","aggregation":"COUNT","status":"ACTIVE"}],"limit":100,"offset":0,"totalCount":1}`
	srv, path, query := fmeCaptureServerWithQuery(t, fixture)

	ctx := fmeTestCtx(t, srv.URL)
	ctx.Noun = "metric"
	ctx.Resolver = reg
	ctx.FormatFlags.Format = "json"
	ctx.FlagValues = map[string]any{
		"name":            "my-metric",
		"traffic-type-id": "tt-1",
		"event-type-id":   []string{"et-1", "et-2"},
		"tag":             []string{"tag-a"},
		"id":              []string{"metric-1"},
		"sort-order":      "DESCENDING",
	}

	if err := registry.RunListEndpoint(ctx, cs.Endpoint); err != nil {
		t.Fatalf("RunListEndpoint: %v", err)
	}

	if !strings.HasPrefix(*path, "/fme/api/v4/metrics") {
		t.Fatalf("request path = %q, want prefix /fme/api/v4/metrics", *path)
	}
	for _, want := range []string{"name=my-metric", "traffic_type_id=tt-1", "event_type_ids=et-1", "tags=tag-a", "ids=metric-1", "sort_order=DESCENDING"} {
		if !strings.Contains(*query, want) {
			t.Fatalf("query = %q, want %q", *query, want)
		}
	}
	if strings.Contains(*query, "et-2") {
		t.Fatalf("query = %q, event_type_ids must send only the first value, not the whole repeated slice", *query)
	}

	body := fmeReadOut(t, ctx)
	for _, want := range []string{"metric-1", "my-metric", "user", "NUMBER", "COUNT", "ACTIVE"} {
		if !strings.Contains(body, want) {
			t.Fatalf("output missing %q: %s", want, body)
		}
	}
}

// TestFMESpec_GetMetric drives "get metric <id>" and asserts the path embeds the
// id (metrics are addressed by id, not name, unlike feature_flag/segment) and that
// tags/owners render their joined names in text format.
func TestFMESpec_GetMetric(t *testing.T) {
	reg := registry.New()
	if _, err := LoadSpec(reg, "fme.spec.yaml", true); err != nil {
		t.Fatalf("LoadSpec: %v", err)
	}
	cs := reg.GetSpec("get", "metric")
	if cs == nil || cs.Endpoint == nil {
		t.Fatal("get metric: command not found or missing endpoint spec")
	}

	fixture := `{"id":"metric-1","name":"my-metric","description":"desc","trafficType":{"name":"user"},"format":"NUMBER","aggregation":"COUNT","isPositive":true,"spread":"PER","baseEventTypes":[{"eventTypeId":"signup"}],"tags":[{"id":"t-1","name":"demo"}],"owners":[{"id":"u-1","name":"alice"}],"status":"ACTIVE","createdAt":"2026-01-01T00:00:00Z"}`
	srv, path := fmeCaptureServer(t, fixture)

	ctx := fmeTestCtx(t, srv.URL)
	ctx.Id = "metric-1"
	ctx.Noun = "metric"
	ctx.Resolver = reg
	ctx.FormatFlags.Format = "text"

	if _, err := registry.RunEndpoint(ctx, cs.Endpoint); err != nil {
		t.Fatalf("RunEndpoint: %v", err)
	}

	if *path != "/fme/api/v4/metrics/metric-1" {
		t.Fatalf("request path = %q, want /fme/api/v4/metrics/metric-1", *path)
	}

	body := fmeReadOut(t, ctx)
	for _, want := range []string{"my-metric", "signup", "demo", "alice"} {
		if !strings.Contains(body, want) {
			t.Fatalf("output missing %q: %s", want, body)
		}
	}
}

// TestFMESpec_CreateMetric drives "create metric <name> --traffic-type ... --event-type ..."
// and asserts the POST body carries name/trafficType/baseEventTypes (the latter built from
// a repeatable --event-type via map(flags["event-type"], {eventTypeId: #})), plus any --set
// scalars, and that the response unwraps via item_expr: it.entity.
func TestFMESpec_CreateMetric(t *testing.T) {
	reg := registry.New()
	if _, err := LoadSpec(reg, "fme.spec.yaml", true); err != nil {
		t.Fatalf("LoadSpec: %v", err)
	}
	cs := reg.GetSpec("create", "metric")
	if cs == nil || cs.Endpoint == nil {
		t.Fatal("create metric: command not found or missing endpoint spec")
	}

	fixture := `{"entity":{"id":"metric-2","name":"new-metric","trafficType":{"name":"user"},"status":"ACTIVE"}}`
	srv, caps := fmeSequenceServer(t, []string{fixture})

	ctx := fmeTestCtx(t, srv.URL)
	ctx.Id = "new-metric"
	ctx.Noun = "metric"
	ctx.Resolver = reg
	ctx.FormatFlags.Format = "json"
	ctx.FlagValues = map[string]any{"traffic-type": "user", "event-type": []string{"signup"}}
	ctx.SetArgs = map[string]string{"format": "NUMBER", "aggregation": "COUNT", "is_positive": "true"}

	if _, err := registry.RunEndpoint(ctx, cs.Endpoint); err != nil {
		t.Fatalf("RunEndpoint: %v", err)
	}

	if len(*caps) != 1 {
		t.Fatalf("got %d requests, want 1", len(*caps))
	}
	got := (*caps)[0]
	if got.method != "POST" || got.path != "/fme/api/v4/metrics" {
		t.Fatalf("request = %s %s, want POST /fme/api/v4/metrics", got.method, got.path)
	}
	var body map[string]any
	if err := json.Unmarshal(got.body, &body); err != nil {
		t.Fatalf("unmarshal request body: %v", err)
	}
	if body["name"] != "new-metric" || body["trafficType"] != "user" {
		t.Fatalf("body = %v, want name=new-metric trafficType=user", body)
	}
	baseEventTypes, ok := body["baseEventTypes"].([]any)
	if !ok || len(baseEventTypes) != 1 {
		t.Fatalf("body[baseEventTypes] = %v, want one entry built from --event-type", body["baseEventTypes"])
	}
	entry, ok := baseEventTypes[0].(map[string]any)
	if !ok || entry["eventTypeId"] != "signup" {
		t.Fatalf("baseEventTypes[0] = %v, want {eventTypeId: signup}", baseEventTypes[0])
	}
	if body["format"] != "NUMBER" || body["aggregation"] != "COUNT" || body["isPositive"] != "true" {
		t.Fatalf("body = %v, want --set scalars carried through", body)
	}

	out := fmeReadOut(t, ctx)
	if !strings.Contains(out, "new-metric") {
		t.Fatalf("output missing new-metric (item_expr it.entity did not unwrap): %s", out)
	}
}

// TestFMESpec_CreateMetric_FileBody drives "create metric <name> -f metric.json" with
// neither --traffic-type nor --event-type set, confirming trafficType/baseEventTypes
// supplied inside the file body are not blocked by a required-flag check.
func TestFMESpec_CreateMetric_FileBody(t *testing.T) {
	reg := registry.New()
	if _, err := LoadSpec(reg, "fme.spec.yaml", true); err != nil {
		t.Fatalf("LoadSpec: %v", err)
	}
	cs := reg.GetSpec("create", "metric")
	if cs == nil || cs.Endpoint == nil {
		t.Fatal("create metric: command not found or missing endpoint spec")
	}

	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"entity":`+gotBody+`,"governance":null}`)
	}))
	t.Cleanup(srv.Close)

	fileBody := `{"trafficType":"user","format":"NUMBER","aggregation":"COUNT","isPositive":true,"baseEventTypes":[{"eventTypeId":"signup"}]}`
	filePath := filepath.Join(t.TempDir(), "metric.json")
	if err := os.WriteFile(filePath, []byte(fileBody), 0o600); err != nil {
		t.Fatalf("writing input file: %v", err)
	}

	ctx := fmeTestCtx(t, srv.URL)
	ctx.Id = "new-metric"
	ctx.Noun = "metric"
	ctx.Resolver = reg
	ctx.FormatFlags.Format = "json"
	ctx.FlagValues = map[string]any{"file": filePath}

	if _, err := registry.RunEndpoint(ctx, cs.Endpoint); err != nil {
		t.Fatalf("RunEndpoint: %v", err)
	}

	var body map[string]any
	if err := json.Unmarshal([]byte(gotBody), &body); err != nil {
		t.Fatalf("unmarshal request body: %v", err)
	}
	if body["trafficType"] != "user" {
		t.Fatalf("body[trafficType] = %v, want user (from file, not flag)", body["trafficType"])
	}
	baseEventTypes, ok := body["baseEventTypes"].([]any)
	if !ok || len(baseEventTypes) != 1 {
		t.Fatalf("body[baseEventTypes] = %v, want one entry from file", body["baseEventTypes"])
	}
	if body["name"] != "new-metric" {
		t.Fatalf("body[name] = %v, want new-metric (from create_body_init, not overridden by file)", body["name"])
	}
}

// TestFMESpec_UpdateMetric drives "update metric <id> --set description=..." and asserts
// the sparse PATCH body contains only the touched field — untouched picked fields (tags,
// owners, cap, filterEventType, baseEventTypes) never leak into a merge-patch body since
// buildMutationBodyWithPick only writes touched fieldIDs into the sparse result.
func TestFMESpec_UpdateMetric(t *testing.T) {
	reg := registry.New()
	if _, err := LoadSpec(reg, "fme.spec.yaml", true); err != nil {
		t.Fatalf("LoadSpec: %v", err)
	}
	cs := reg.GetSpec("update", "metric")
	if cs == nil || cs.Endpoint == nil {
		t.Fatal("update metric: command not found or missing endpoint spec")
	}

	getResp := `{"id":"metric-1","name":"my-metric","description":"old desc","trafficType":{"name":"user"},"format":"NUMBER","aggregation":"COUNT","isPositive":true,"spread":"PER","baseEventTypes":[{"eventTypeId":"signup"}],"tags":[{"id":"t-1","name":"demo"}],"owners":[{"id":"u-1","name":"alice"}],"status":"ACTIVE"}`
	patchResp := `{"entity":{"id":"metric-1","description":"new desc"}}`
	srv, caps := fmeSequenceServer(t, []string{getResp, patchResp})

	ctx := fmeTestCtx(t, srv.URL)
	ctx.Id = "metric-1"
	ctx.Noun = "metric"
	ctx.VerbHandler = cs.VerbHandler
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
	if patch.path != "/fme/api/v4/metrics/metric-1" {
		t.Fatalf("2nd request path = %q, want /fme/api/v4/metrics/metric-1", patch.path)
	}
	var body map[string]any
	if err := json.Unmarshal(patch.body, &body); err != nil {
		t.Fatalf("unmarshal PATCH body: %v", err)
	}
	wantKeys := map[string]any{"description": "new desc"}
	if len(body) != len(wantKeys) {
		t.Fatalf("PATCH body = %v, want exactly %v", body, wantKeys)
	}
	for k, v := range wantKeys {
		if body[k] != v {
			t.Fatalf("PATCH body[%s] = %v, want %v (full body: %v)", k, body[k], v, body)
		}
	}
	for _, leaked := range []string{"format", "aggregation", "isPositive", "spread", "tags", "owners", "baseEventTypes", "name", "trafficType", "id", "status"} {
		if _, present := body[leaked]; present {
			t.Fatalf("PATCH body must not include %q (deferred/immutable field leaked): %v", leaked, body)
		}
	}
}

// TestFMESpec_UpdateMetric_FileBody drives "update metric <id> -f patch.json", confirming
// the file is sent as-is (no GET, no update_body_pick) as a merge-patch document.
func TestFMESpec_UpdateMetric_FileBody(t *testing.T) {
	reg := registry.New()
	if _, err := LoadSpec(reg, "fme.spec.yaml", true); err != nil {
		t.Fatalf("LoadSpec: %v", err)
	}
	cs := reg.GetSpec("update", "metric")
	if cs == nil || cs.Endpoint == nil {
		t.Fatal("update metric: command not found or missing endpoint spec")
	}

	var gotMethod, gotPath, gotCT, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotCT = r.Header.Get("Content-Type")
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"entity":`+gotBody+`}`)
	}))
	t.Cleanup(srv.Close)

	fileBody := `{"description":"new desc"}`
	filePath := filepath.Join(t.TempDir(), "patch.json")
	if err := os.WriteFile(filePath, []byte(fileBody), 0o600); err != nil {
		t.Fatalf("writing input file: %v", err)
	}

	ctx := fmeTestCtx(t, srv.URL)
	ctx.Id = "metric-1"
	ctx.Noun = "metric"
	ctx.VerbHandler = cs.VerbHandler
	ctx.Resolver = reg
	ctx.FormatFlags.Format = "json"
	ctx.FlagValues = map[string]any{"file": filePath}

	if _, err := registry.RunEndpoint(ctx, cs.Endpoint); err != nil {
		t.Fatalf("RunEndpoint: %v", err)
	}

	if gotMethod != "PATCH" || gotPath != "/fme/api/v4/metrics/metric-1" {
		t.Fatalf("request = %s %s, want PATCH /fme/api/v4/metrics/metric-1", gotMethod, gotPath)
	}
	if gotCT != "application/merge-patch+json" {
		t.Fatalf("Content-Type = %q, want application/merge-patch+json", gotCT)
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(gotBody), &body); err != nil {
		t.Fatalf("unmarshal PATCH body: %v", err)
	}
	if len(body) != 1 || body["description"] != "new desc" {
		t.Fatalf("PATCH body = %v, want exactly {description: new desc} (file sent as-is, no GET/pick)", body)
	}
}

// TestFMESpec_CreateMetric_CapAndFilterEventType drives "create metric ... --set
// cap_metric_value=100 --set cap_granularity=DAYS --set filter_event_type=... --set
// filter_aggregation=..." and asserts all four land at their v4 wire paths.
func TestFMESpec_CreateMetric_CapAndFilterEventType(t *testing.T) {
	reg := registry.New()
	if _, err := LoadSpec(reg, "fme.spec.yaml", true); err != nil {
		t.Fatalf("LoadSpec: %v", err)
	}
	cs := reg.GetSpec("create", "metric")
	if cs == nil || cs.Endpoint == nil {
		t.Fatal("create metric: command not found or missing endpoint spec")
	}

	fixture := `{"entity":{"id":"metric-3","name":"capped-metric"}}`
	srv, caps := fmeSequenceServer(t, []string{fixture})

	ctx := fmeTestCtx(t, srv.URL)
	ctx.Id = "capped-metric"
	ctx.Noun = "metric"
	ctx.Resolver = reg
	ctx.FormatFlags.Format = "json"
	ctx.FlagValues = map[string]any{"traffic-type": "user", "event-type": []string{"signup"}}
	ctx.SetArgs = map[string]string{
		"format": "NUMBER", "aggregation": "COUNT", "is_positive": "true",
		"cap_metric_value": "100", "cap_granularity": "DAYS",
		"filter_event_type": "checkout", "filter_aggregation": "COUNT",
	}

	if _, err := registry.RunEndpoint(ctx, cs.Endpoint); err != nil {
		t.Fatalf("RunEndpoint: %v", err)
	}

	var body map[string]any
	if err := json.Unmarshal((*caps)[0].body, &body); err != nil {
		t.Fatalf("unmarshal request body: %v", err)
	}
	cap, ok := body["cap"].(map[string]any)
	if !ok || cap["metricValueCap"] != "100" || cap["granularity"] != "DAYS" {
		t.Fatalf("body[cap] = %v, want {metricValueCap: 100, granularity: DAYS}", body["cap"])
	}
	filterEventType, ok := body["filterEventType"].(map[string]any)
	if !ok || filterEventType["eventTypeId"] != "checkout" || filterEventType["filterAggregation"] != "COUNT" {
		t.Fatalf("body[filterEventType] = %v, want {eventTypeId: checkout, filterAggregation: COUNT}", body["filterEventType"])
	}
}

// TestFMESpec_UpdateMetric_CapAndFilterEventType drives "update metric <id> --set
// cap_metric_value=... --set filter_event_type=..." and asserts the sparse PATCH body
// carries only the touched nested paths, each as its own {cap: {...}}/{filterEventType: {...}}
// object — relying on the server's RFC 7396 recursive merge to preserve untouched siblings.
func TestFMESpec_UpdateMetric_CapAndFilterEventType(t *testing.T) {
	reg := registry.New()
	if _, err := LoadSpec(reg, "fme.spec.yaml", true); err != nil {
		t.Fatalf("LoadSpec: %v", err)
	}
	cs := reg.GetSpec("update", "metric")
	if cs == nil || cs.Endpoint == nil {
		t.Fatal("update metric: command not found or missing endpoint spec")
	}

	getResp := `{"id":"metric-1","name":"my-metric","trafficType":{"name":"user"},"format":"NUMBER","aggregation":"COUNT","isPositive":true,"cap":{"metricValueCap":50,"granularity":"DAYS"},"status":"ACTIVE"}`
	patchResp := `{"entity":{"id":"metric-1"}}`
	srv, caps := fmeSequenceServer(t, []string{getResp, patchResp})

	ctx := fmeTestCtx(t, srv.URL)
	ctx.Id = "metric-1"
	ctx.Noun = "metric"
	ctx.VerbHandler = cs.VerbHandler
	ctx.Resolver = reg
	ctx.FormatFlags.Format = "json"
	ctx.SetArgs = map[string]string{"cap_metric_value": "200", "filter_event_type": "checkout"}

	if _, err := registry.RunEndpoint(ctx, cs.Endpoint); err != nil {
		t.Fatalf("RunEndpoint: %v", err)
	}

	patch := (*caps)[1]
	var body map[string]any
	if err := json.Unmarshal(patch.body, &body); err != nil {
		t.Fatalf("unmarshal PATCH body: %v", err)
	}
	cap, ok := body["cap"].(map[string]any)
	if !ok || cap["metricValueCap"] != "200" {
		t.Fatalf("body[cap] = %v, want {metricValueCap: 200}", body["cap"])
	}
	filterEventType, ok := body["filterEventType"].(map[string]any)
	if !ok || filterEventType["eventTypeId"] != "checkout" {
		t.Fatalf("body[filterEventType] = %v, want {eventTypeId: checkout}", body["filterEventType"])
	}
	for _, leaked := range []string{"format", "aggregation", "isPositive", "name", "trafficType", "id", "status"} {
		if _, present := body[leaked]; present {
			t.Fatalf("PATCH body must not include %q (untouched field leaked): %v", leaked, body)
		}
	}
}

// TestFMESpec_CreateAndUpdateMetric_TriggerEventType drives --set trigger_event_type=...
// on both create and update, asserting it lands at triggerEventType.eventTypeId (the wire
// shape for the "before event"/HAS_DONE_BEFORE UI concept).
func TestFMESpec_CreateAndUpdateMetric_TriggerEventType(t *testing.T) {
	reg := registry.New()
	if _, err := LoadSpec(reg, "fme.spec.yaml", true); err != nil {
		t.Fatalf("LoadSpec: %v", err)
	}

	createCS := reg.GetSpec("create", "metric")
	if createCS == nil || createCS.Endpoint == nil {
		t.Fatal("create metric: command not found or missing endpoint spec")
	}
	createFixture := `{"entity":{"id":"metric-4","name":"trigger-metric"}}`
	createSrv, createCaps := fmeSequenceServer(t, []string{createFixture})

	createCtx := fmeTestCtx(t, createSrv.URL)
	createCtx.Id = "trigger-metric"
	createCtx.Noun = "metric"
	createCtx.Resolver = reg
	createCtx.FormatFlags.Format = "json"
	createCtx.FlagValues = map[string]any{"traffic-type": "user", "event-type": []string{"signup"}}
	createCtx.SetArgs = map[string]string{
		"format": "NUMBER", "aggregation": "COUNT", "is_positive": "true",
		"trigger_event_type": "signed-up",
	}
	if _, err := registry.RunEndpoint(createCtx, createCS.Endpoint); err != nil {
		t.Fatalf("create RunEndpoint: %v", err)
	}
	var createBody map[string]any
	if err := json.Unmarshal((*createCaps)[0].body, &createBody); err != nil {
		t.Fatalf("unmarshal create request body: %v", err)
	}
	triggerEventType, ok := createBody["triggerEventType"].(map[string]any)
	if !ok || triggerEventType["eventTypeId"] != "signed-up" {
		t.Fatalf("create body[triggerEventType] = %v, want {eventTypeId: signed-up}", createBody["triggerEventType"])
	}

	updateCS := reg.GetSpec("update", "metric")
	if updateCS == nil || updateCS.Endpoint == nil {
		t.Fatal("update metric: command not found or missing endpoint spec")
	}
	getResp := `{"id":"metric-4","name":"trigger-metric","trafficType":{"name":"user"},"format":"NUMBER","aggregation":"COUNT","isPositive":true,"status":"ACTIVE"}`
	patchResp := `{"entity":{"id":"metric-4"}}`
	updateSrv, updateCaps := fmeSequenceServer(t, []string{getResp, patchResp})

	updateCtx := fmeTestCtx(t, updateSrv.URL)
	updateCtx.Id = "metric-4"
	updateCtx.Noun = "metric"
	updateCtx.VerbHandler = updateCS.VerbHandler
	updateCtx.Resolver = reg
	updateCtx.FormatFlags.Format = "json"
	updateCtx.SetArgs = map[string]string{"trigger_event_type": "signed-up-again"}
	if _, err := registry.RunEndpoint(updateCtx, updateCS.Endpoint); err != nil {
		t.Fatalf("update RunEndpoint: %v", err)
	}
	var patchBody map[string]any
	if err := json.Unmarshal((*updateCaps)[1].body, &patchBody); err != nil {
		t.Fatalf("unmarshal PATCH body: %v", err)
	}
	patchedTriggerEventType, ok := patchBody["triggerEventType"].(map[string]any)
	if !ok || patchedTriggerEventType["eventTypeId"] != "signed-up-again" {
		t.Fatalf("PATCH body[triggerEventType] = %v, want {eventTypeId: signed-up-again}", patchBody["triggerEventType"])
	}
	if _, present := patchBody["format"]; present {
		t.Fatalf("PATCH body must not include %q (untouched field leaked): %v", "format", patchBody)
	}
}

// TestFMESpec_UpdateMetric_ImmutableFields asserts --set name=/--set traffic_type=
// are rejected: name/trafficType have no mutable_path on the metric noun, matching
// the v4 API's immutability of both fields on PATCH.
func TestFMESpec_UpdateMetric_ImmutableFields(t *testing.T) {
	reg := registry.New()
	if _, err := LoadSpec(reg, "fme.spec.yaml", true); err != nil {
		t.Fatalf("LoadSpec: %v", err)
	}
	cs := reg.GetSpec("update", "metric")
	if cs == nil || cs.Endpoint == nil {
		t.Fatal("update metric: command not found or missing endpoint spec")
	}

	getResp := `{"id":"metric-1","name":"my-metric","trafficType":{"name":"user"},"format":"NUMBER"}`

	for _, field := range []string{"name", "traffic_type"} {
		t.Run(field, func(t *testing.T) {
			srv, _ := fmeSequenceServer(t, []string{getResp, `{"entity":{}}`})

			ctx := fmeTestCtx(t, srv.URL)
			ctx.Id = "metric-1"
			ctx.Noun = "metric"
			ctx.VerbHandler = cs.VerbHandler
			ctx.Resolver = reg
			ctx.FormatFlags.Format = "json"
			ctx.SetArgs = map[string]string{field: "new-value"}

			_, err := registry.RunEndpoint(ctx, cs.Endpoint)
			if err == nil {
				t.Fatalf("--set %s=...: want error (immutable field), got nil", field)
			}
			if !strings.Contains(err.Error(), field) {
				t.Fatalf("error = %q, want it to mention %q", err.Error(), field)
			}
		})
	}
}

// TestFMESpec_DeleteMetric drives "delete metric <id>" (hard delete, no
// archive/restore) and asserts the DELETE request hits the {id} path.
func TestFMESpec_DeleteMetric(t *testing.T) {
	reg := registry.New()
	if _, err := LoadSpec(reg, "fme.spec.yaml", true); err != nil {
		t.Fatalf("LoadSpec: %v", err)
	}
	cs := reg.GetSpec("delete", "metric")
	if cs == nil || cs.Endpoint == nil {
		t.Fatal("delete metric: command not found or missing endpoint spec")
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
	ctx.Id = "metric-1"
	ctx.Noun = "metric"
	ctx.VerbHandler = cs.VerbHandler
	ctx.Resolver = reg
	ctx.FormatFlags.Format = "json"

	if _, err := registry.RunEndpoint(ctx, cs.Endpoint); err != nil {
		t.Fatalf("RunEndpoint: %v", err)
	}

	if gotMethod != http.MethodDelete {
		t.Fatalf("method = %q, want DELETE", gotMethod)
	}
	if gotPath != "/fme/api/v4/metrics/metric-1" {
		t.Fatalf("request path = %q, want /fme/api/v4/metrics/metric-1", gotPath)
	}
}

// TestFMESpec_ListEventTypes drives "list event_type" and asserts the filters map
// to their query params and traffic_types renders as joined names.
func TestFMESpec_ListEventTypes(t *testing.T) {
	reg := registry.New()
	if _, err := LoadSpec(reg, "fme.spec.yaml", true); err != nil {
		t.Fatalf("LoadSpec: %v", err)
	}
	cs := reg.GetSpec("list", "event_type")
	if cs == nil || cs.Endpoint == nil {
		t.Fatal("list event_type: command not found or missing endpoint spec")
	}

	fixture := `{"data":[{"id":"signup","trafficTypes":[{"name":"user"},{"name":"account"}]}],"limit":100,"offset":0,"totalCount":1}`
	srv, path, query := fmeCaptureServerWithQuery(t, fixture)

	ctx := fmeTestCtx(t, srv.URL)
	ctx.Noun = "event_type"
	ctx.Resolver = reg
	ctx.FormatFlags.Format = "json"
	ctx.FlagValues = map[string]any{"name": "sign", "traffic-type": "user"}

	if err := registry.RunListEndpoint(ctx, cs.Endpoint); err != nil {
		t.Fatalf("RunListEndpoint: %v", err)
	}

	if !strings.HasPrefix(*path, "/fme/api/v4/event-types") {
		t.Fatalf("request path = %q, want prefix /fme/api/v4/event-types", *path)
	}
	if !strings.Contains(*query, "name=sign") || !strings.Contains(*query, "traffic_type=user") {
		t.Fatalf("query = %q, want name=sign and traffic_type=user", *query)
	}

	body := fmeReadOut(t, ctx)
	for _, want := range []string{"signup", "user", "account"} {
		if !strings.Contains(body, want) {
			t.Fatalf("output missing %q: %s", want, body)
		}
	}
}

// TestFMESpec_GetEventType drives "get event_type <id>" and asserts the path
// embeds the id (event types are addressed by id, which doubles as the name).
func TestFMESpec_GetEventType(t *testing.T) {
	reg := registry.New()
	if _, err := LoadSpec(reg, "fme.spec.yaml", true); err != nil {
		t.Fatalf("LoadSpec: %v", err)
	}
	cs := reg.GetSpec("get", "event_type")
	if cs == nil || cs.Endpoint == nil {
		t.Fatal("get event_type: command not found or missing endpoint spec")
	}

	fixture := `{"id":"signup","trafficTypes":[{"name":"user"}]}`
	srv, path := fmeCaptureServer(t, fixture)

	ctx := fmeTestCtx(t, srv.URL)
	ctx.Id = "signup"
	ctx.Noun = "event_type"
	ctx.Resolver = reg
	ctx.FormatFlags.Format = "yaml"

	if _, err := registry.RunEndpoint(ctx, cs.Endpoint); err != nil {
		t.Fatalf("RunEndpoint: %v", err)
	}

	if *path != "/fme/api/v4/event-types/signup" {
		t.Fatalf("request path = %q, want /fme/api/v4/event-types/signup", *path)
	}
}

// TestFMESpec_UpdateMetric_AddDelTags mirrors
// TestFMESpec_UpdateSegment_AddDelTags for the metric noun
// (FME-19300 follow-up: metric owners/tags become mutable).
func TestFMESpec_UpdateMetric_AddDelTags(t *testing.T) {
	reg := registry.New()
	fme.ModuleInit(reg.Module("fme"))
	if _, err := LoadSpec(reg, "fme.spec.yaml", true); err != nil {
		t.Fatalf("LoadSpec: %v", err)
	}
	cs := reg.GetSpec("update", "metric")
	if cs == nil || cs.Endpoint == nil {
		t.Fatal("update metric: command not found or missing endpoint spec")
	}

	getResp := `{"id":"metric-1","name":"my-metric","description":"old desc","trafficType":{"name":"user"},` +
		`"format":"NUMBER","aggregation":"COUNT","isPositive":true,"spread":"PER",` +
		`"tags":[{"id":"t1","name":"delta"},{"id":"t2","name":"epsilon"}],` +
		`"owners":[{"id":"u1","type":"USER","name":"Alice"}],"status":"ACTIVE"}`
	srv, caps := fmeSequenceServer(t, []string{getResp, `{"entity":{"id":"metric-1"}}`})

	ctx := fmeTestCtx(t, srv.URL)
	ctx.Id = "metric-1"
	ctx.Noun = "metric"
	ctx.Resolver = reg
	ctx.FormatFlags.Format = "json"
	ctx.MutationOrderCaptured = true
	ctx.MutationFlags = []cmdctx.FieldMutation{
		{Kind: cmdctx.MutationDelete, Key: "tags.delta", Raw: "tags.delta"},
		{Kind: cmdctx.MutationAdd, Key: "tags.foo", Raw: "tags.foo"},
	}

	if _, err := registry.RunEndpoint(ctx, cs.Endpoint); err != nil {
		t.Fatalf("RunEndpoint: %v", err)
	}

	patch := (*caps)[1]
	var body map[string]any
	if err := json.Unmarshal(patch.body, &body); err != nil {
		t.Fatalf("unmarshal PATCH body: %v", err)
	}
	if _, present := body["owners"]; present {
		t.Errorf("PATCH body = %v, owners should be omitted (untouched)", body)
	}
	tags, ok := body["tags"].([]any)
	if !ok || len(tags) != 2 {
		t.Fatalf("PATCH body tags = %v, want 2 entries (epsilon, foo)", body["tags"])
	}
	got := []string{tagName(tags[0]), tagName(tags[1])}
	want := []string{"epsilon", "foo"}
	if got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("PATCH body tags = %v, want %v", got, want)
	}
}

// TestFMESpec_UpdateMetric_AddDelOwners mirrors
// TestFMESpec_UpdateSegment_AddDelOwners for the metric noun
// (FME-19300 follow-up).
func TestFMESpec_UpdateMetric_AddDelOwners(t *testing.T) {
	reg := registry.New()
	fme.ModuleInit(reg.Module("fme"))
	if _, err := LoadSpec(reg, "fme.spec.yaml", true); err != nil {
		t.Fatalf("LoadSpec: %v", err)
	}
	cs := reg.GetSpec("update", "metric")
	if cs == nil || cs.Endpoint == nil {
		t.Fatal("update metric: command not found or missing endpoint spec")
	}

	getResp := `{"id":"metric-1","name":"my-metric","description":"old desc","trafficType":{"name":"user"},` +
		`"format":"NUMBER","aggregation":"COUNT","isPositive":true,"spread":"PER","tags":[],` +
		`"owners":[{"id":"u1","type":"USER","name":"Alice"}],"status":"ACTIVE"}`
	srv, caps := fmeSequenceServer(t, []string{getResp, `{"entity":{"id":"metric-1"}}`})

	ctx := fmeTestCtx(t, srv.URL)
	ctx.Id = "metric-1"
	ctx.Noun = "metric"
	ctx.Resolver = reg
	ctx.FormatFlags.Format = "json"
	ctx.MutationOrderCaptured = true
	ctx.MutationFlags = []cmdctx.FieldMutation{
		{Kind: cmdctx.MutationDelete, Key: "owners.user:u1", Raw: "owners.user:u1"},
		{Kind: cmdctx.MutationAdd, Key: "owners.user:bob@example.com", Raw: "owners.user:bob@example.com"},
	}

	if _, err := registry.RunEndpoint(ctx, cs.Endpoint); err != nil {
		t.Fatalf("RunEndpoint: %v", err)
	}

	patch := (*caps)[1]
	var body map[string]any
	if err := json.Unmarshal(patch.body, &body); err != nil {
		t.Fatalf("unmarshal PATCH body: %v", err)
	}
	owners, ok := body["owners"].([]any)
	if !ok || len(owners) != 1 {
		t.Fatalf("PATCH body owners = %v, want exactly 1 entry (bob, added by email)", body["owners"])
	}
	entry := owners[0].(map[string]any)
	if entry["type"] != "USER" || entry["email"] != "bob@example.com" {
		t.Fatalf("owner entry = %v, want {type: USER, email: bob@example.com}", entry)
	}
}

// TestFMESpec_CreateMetric_TagsOwners mirrors
// TestFMESpec_CreateSegment_TagsOwners for the metric noun
// (FME-19300 follow-up).
func TestFMESpec_CreateMetric_TagsOwners(t *testing.T) {
	reg := registry.New()
	fme.ModuleInit(reg.Module("fme"))
	if _, err := LoadSpec(reg, "fme.spec.yaml", true); err != nil {
		t.Fatalf("LoadSpec: %v", err)
	}
	cs := reg.GetSpec("create", "metric")
	if cs == nil || cs.Endpoint == nil {
		t.Fatal("create metric: command not found or missing endpoint spec")
	}

	fixture := `{"entity":{"id":"metric-2","name":"new-metric","trafficType":{"name":"user"},"status":"ACTIVE"}}`
	srv, caps := fmeSequenceServer(t, []string{fixture})

	ctx := fmeTestCtx(t, srv.URL)
	ctx.Id = "new-metric"
	ctx.Noun = "metric"
	ctx.Resolver = reg
	ctx.FormatFlags.Format = "json"
	ctx.FlagValues = map[string]any{"traffic-type": "user", "event-type": []string{"signup"}}
	ctx.MutationOrderCaptured = true
	ctx.MutationFlags = []cmdctx.FieldMutation{
		{Kind: cmdctx.MutationAdd, Key: "tags.foo", Raw: "tags.foo"},
		{Kind: cmdctx.MutationAdd, Key: "owners.user:bob@example.com", Raw: "owners.user:bob@example.com"},
	}

	if _, err := registry.RunEndpoint(ctx, cs.Endpoint); err != nil {
		t.Fatalf("RunEndpoint: %v", err)
	}

	got := (*caps)[0]
	var body map[string]any
	if err := json.Unmarshal(got.body, &body); err != nil {
		t.Fatalf("unmarshal request body: %v", err)
	}
	tags, ok := body["tags"].([]any)
	if !ok || len(tags) != 1 || tagName(tags[0]) != "foo" {
		t.Fatalf("body tags = %v, want [{name: foo}]", body["tags"])
	}
	owners, ok := body["owners"].([]any)
	if !ok || len(owners) != 1 {
		t.Fatalf("body owners = %v, want 1 entry", body["owners"])
	}
	entry := owners[0].(map[string]any)
	if entry["type"] != "USER" || entry["email"] != "bob@example.com" {
		t.Fatalf("owner entry = %v, want {type: USER, email: bob@example.com}", entry)
	}
}

// TestFMESpec_ListChangeRequests drives "list change_request" against the v4
// /change-requests contract: path, every filter flag mapped to its query param,
// --mine → filter=APPROVALS, multi-value flags sending only their first value,
// and ISO timestamps / nested submitter and approvers rendering.
func TestFMESpec_ListChangeRequests(t *testing.T) {
	reg := registry.New()
	if _, err := LoadSpec(reg, "fme.spec.yaml", true); err != nil {
		t.Fatalf("LoadSpec: %v", err)
	}
	cs := reg.GetSpec("list", "change_request")
	if cs == nil || cs.Endpoint == nil {
		t.Fatal("list change_request: command not found or missing endpoint spec")
	}

	fixture := `{"data":[{"id":"cr-1","status":"REQUESTED","resourceId":"ff-id","resourceType":"FEATURE_FLAG","resourceName":"my-flag","environment":{"id":"env-1","name":"Production"},"title":"Roll out","createdBy":{"id":"u1","type":"USER","name":"Alice","email":"alice@example.com"},"approvalConfig":{"approvers":[{"id":"u2","name":"Bob"},{"id":"u3","email":"carol@example.com"}]},"createdAt":"2026-08-30T12:00:00Z","modifiedAt":"2026-08-31T09:30:00Z"}],"limit":100,"offset":0,"totalCount":1}`
	srv, path, query := fmeCaptureServerWithQuery(t, fixture)

	ctx := fmeTestCtx(t, srv.URL)
	ctx.Noun = "change_request"
	ctx.Resolver = reg
	ctx.FormatFlags.Format = "json"
	ctx.FlagValues = map[string]any{
		"env":           []string{"env-1", "env-2"},
		"resource-type": "SEGMENT",
		"segment-type":  "STANDARD",
		"status":        []string{"PUBLISHED", "REJECTED"},
		"mine":          true,
	}

	if err := registry.RunListEndpoint(ctx, cs.Endpoint); err != nil {
		t.Fatalf("RunListEndpoint: %v", err)
	}

	if *path != "/fme/api/v4/change-requests" {
		t.Fatalf("request path = %q, want /fme/api/v4/change-requests", *path)
	}
	for _, want := range []string{"environment_id=env-1", "resource_type=SEGMENT", "segment_type=STANDARD", "status=PUBLISHED", "filter=APPROVALS", "account_id=acct"} {
		if !strings.Contains(*query, want) {
			t.Fatalf("query = %q, want %q", *query, want)
		}
	}
	for _, unwanted := range []string{"env-2", "REJECTED"} {
		if strings.Contains(*query, unwanted) {
			t.Fatalf("query = %q, multi-value flags must send only the first value (found %q)", *query, unwanted)
		}
	}

	body := fmeReadOut(t, ctx)
	for _, want := range []string{"cr-1", "REQUESTED", "my-flag", "Production", "Alice", "2026-08-30T12:00:00Z"} {
		if !strings.Contains(body, want) {
			t.Fatalf("output missing %q: %s", want, body)
		}
	}
}

// TestFMESpec_ListChangeRequests_OmitsUnsetParams asserts optional filters are not
// sent at all when their flags are unset, so the server applies its own defaults
// (filter=ALL, status=REQUESTED) instead of receiving empty values.
func TestFMESpec_ListChangeRequests_OmitsUnsetParams(t *testing.T) {
	reg := registry.New()
	if _, err := LoadSpec(reg, "fme.spec.yaml", true); err != nil {
		t.Fatalf("LoadSpec: %v", err)
	}
	cs := reg.GetSpec("list", "change_request")
	if cs == nil || cs.Endpoint == nil {
		t.Fatal("list change_request: command not found or missing endpoint spec")
	}

	srv, _, query := fmeCaptureServerWithQuery(t, `{"data":[],"limit":100,"offset":0,"totalCount":0}`)
	ctx := fmeTestCtx(t, srv.URL)
	ctx.Noun = "change_request"
	ctx.Resolver = reg
	ctx.FormatFlags.Format = "json"
	ctx.FlagValues = map[string]any{}

	if err := registry.RunListEndpoint(ctx, cs.Endpoint); err != nil {
		t.Fatalf("RunListEndpoint: %v", err)
	}
	for _, unwanted := range []string{"environment_id", "resource_type", "segment_type", "status", "filter"} {
		if strings.Contains(*query, unwanted) {
			t.Fatalf("query = %q, %q must be omitted when its flag is unset", *query, unwanted)
		}
	}
}

// TestFMESpec_ListChangeRequests_SubmittedByMe asserts --submitted-by-me maps to
// filter=SUBMISSIONS and that --mine wins when both are given.
func TestFMESpec_ListChangeRequests_SubmittedByMe(t *testing.T) {
	reg := registry.New()
	if _, err := LoadSpec(reg, "fme.spec.yaml", true); err != nil {
		t.Fatalf("LoadSpec: %v", err)
	}
	cs := reg.GetSpec("list", "change_request")
	if cs == nil || cs.Endpoint == nil {
		t.Fatal("list change_request: command not found or missing endpoint spec")
	}

	for name, tc := range map[string]struct {
		flags map[string]any
		want  string
	}{
		"submitted-by-me": {map[string]any{"submitted-by-me": true}, "filter=SUBMISSIONS"},
		"both-mine-wins":  {map[string]any{"mine": true, "submitted-by-me": true}, "filter=APPROVALS"},
	} {
		t.Run(name, func(t *testing.T) {
			srv, _, query := fmeCaptureServerWithQuery(t, `{"data":[],"limit":100,"offset":0,"totalCount":0}`)
			ctx := fmeTestCtx(t, srv.URL)
			ctx.Noun = "change_request"
			ctx.Resolver = reg
			ctx.FormatFlags.Format = "json"
			ctx.FlagValues = tc.flags
			if err := registry.RunListEndpoint(ctx, cs.Endpoint); err != nil {
				t.Fatalf("RunListEndpoint: %v", err)
			}
			if !strings.Contains(*query, tc.want) {
				t.Fatalf("query = %q, want %q", *query, tc.want)
			}
		})
	}
}

// TestFMESpec_ListChangeRequests_Pagination asserts offset/limit are sent as the
// v4 page params and that a sparse item (no environment, submitter or approvers)
// still renders instead of erroring.
func TestFMESpec_ListChangeRequests_Pagination(t *testing.T) {
	reg := registry.New()
	if _, err := LoadSpec(reg, "fme.spec.yaml", true); err != nil {
		t.Fatalf("LoadSpec: %v", err)
	}
	cs := reg.GetSpec("list", "change_request")
	if cs == nil || cs.Endpoint == nil {
		t.Fatal("list change_request: command not found or missing endpoint spec")
	}
	if cs.Endpoint.Paging == nil || cs.Endpoint.Paging.PageSizeMax != 100 {
		t.Fatalf("paging = %+v, want page_size_max 100", cs.Endpoint.Paging)
	}

	srv, _, query := fmeCaptureServerWithQuery(t, `{"data":[{"id":"cr-2","status":"PUBLISHED","resourceId":"seg-id","resourceType":"SEGMENT"}],"limit":10,"offset":20,"totalCount":21}`)
	ctx := fmeTestCtx(t, srv.URL)
	ctx.Noun = "change_request"
	ctx.Resolver = reg
	ctx.FormatFlags.Format = "json"
	ctx.FlagValues = map[string]any{}
	ctx.PagingFlags = cmdctx.PagingFlags{Offset: 20, Limit: 10}

	if err := registry.RunListEndpoint(ctx, cs.Endpoint); err != nil {
		t.Fatalf("RunListEndpoint: %v", err)
	}
	for _, want := range []string{"limit=10", "offset=20"} {
		if !strings.Contains(*query, want) {
			t.Fatalf("query = %q, want %q", *query, want)
		}
	}
	if body := fmeReadOut(t, ctx); !strings.Contains(body, "cr-2") {
		t.Fatalf("sparse item did not render: %s", body)
	}
}

// TestFMESpec_ChangeRequestNounAliases asserts both documented aliases resolve to
// the canonical change_request noun.
func TestFMESpec_ChangeRequestNounAliases(t *testing.T) {
	reg := registry.New()
	if _, err := LoadSpec(reg, "fme.spec.yaml", true); err != nil {
		t.Fatalf("LoadSpec: %v", err)
	}
	for _, alias := range []string{"change_requests", "cr"} {
		if got := reg.ResolveNounAlias(alias); got != "change_request" {
			t.Fatalf("ResolveNounAlias(%q) = %q, want change_request", alias, got)
		}
	}
}
