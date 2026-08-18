// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package code

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/harness/cli/pkg/auth"
	"github.com/harness/cli/pkg/cmdctx"
	"github.com/harness/cli/pkg/registry"
	"github.com/harness/cli/pkg/spec"
)

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

type noopResolver struct{}

func (noopResolver) ResolveTextFormatter(id string) cmdctx.TextFormatterFn           { return nil }
func (noopResolver) ResolveBodyFn(id string) cmdctx.CreateBodyFn                     { return nil }
func (noopResolver) ResolveQueryParamsFn(id string) cmdctx.QueryParamsFn             { return nil }
func (noopResolver) ResolveFlagResolveFn(id string) cmdctx.FlagResolveFn             { return nil }
func (noopResolver) ResolveFetchFn(id string) (cmdctx.FetchFn, error)                { return nil, nil }
func (noopResolver) ResolveListTransformFn(id string) cmdctx.ListTransformFn         { return nil }
func (noopResolver) ResolveEndpointValidator(id string) cmdctx.EndpointValidatorFn   { return nil }
func (noopResolver) GetSpec(verb, noun string) *spec.CommandSpec                     { return nil }
func (noopResolver) GetNoun(noun string) *spec.NounDef                               { return nil }
func (noopResolver) ResolveNounAlias(alias string) string                            { return "" }
func (noopResolver) RunEndpoint(ctx *cmdctx.Ctx, ep *spec.EndpointSpec) (any, error) { return nil, nil }
func (noopResolver) FormatList(*cmdctx.Ctx, []any, []spec.FieldDef, []string) error  { return nil }
func (noopResolver) FetchItems(*cmdctx.Ctx, *spec.EndpointSpec, cmdctx.PagingFlags) ([]any, error) {
	return nil, nil
}
func (noopResolver) GetModuleMetas() []spec.ModuleMeta                      { return nil }
func (noopResolver) GetSpecsForModule(string) []*spec.CommandSpec           { return nil }
func (noopResolver) GetAllSpecs() []*spec.CommandSpec                       { return nil }
func (noopResolver) GetVerbInfos() []spec.VerbInfo                          { return nil }
func (noopResolver) ResolveCommandFields(*spec.CommandSpec) []spec.FieldDef { return nil }

type spyResolver struct {
	noopResolver
	getSpec func(verb, noun string) *spec.CommandSpec
}

func (s spyResolver) GetSpec(verb, noun string) *spec.CommandSpec {
	if s.getSpec != nil {
		return s.getSpec(verb, noun)
	}
	return nil
}

func prSpec(path string) *spec.CommandSpec {
	return &spec.CommandSpec{
		Command: "get pr", Verb: "get", VerbHandler: "get",
		Noun: "pr", Module: "code", HandlerType: spec.HandlerWorkflow,
		Endpoint: &spec.EndpointSpec{Method: "GET", Path: path, ItemExpr: "it"},
	}
}

func aiOverviewSpec(path string) *spec.CommandSpec {
	return &spec.CommandSpec{
		Command: "get pr:insight", Verb: "get", VerbHandler: "get",
		Noun: "pr", NounVariant: "insight", FieldsNoun: "pr_insight", Module: "code",
		HandlerType: spec.HandlerEndpoint,
		Endpoint:    &spec.EndpointSpec{Method: "GET", Path: path, ItemExpr: "it"},
	}
}

func insightTestCtx(srvURL, format string) *cmdctx.Ctx {
	return &cmdctx.Ctx{
		Context:     context.Background(),
		Noun:        "pr",
		VerbHandler: "get",
		Auth:        &auth.ResolvedAuth{AuthType: auth.AuthTypePAT, APIUrl: srvURL, AccountID: "acct", OrgID: "org", ProjectID: "proj", PATToken: "test-token"},
		FormatFlags: cmdctx.FormatFlags{Format: format},
		FlagValues:  map[string]any{},
		Resolver: spyResolver{getSpec: func(verb, noun string) *spec.CommandSpec {
			if noun == "pr:insight" {
				return aiOverviewSpec("/overview")
			}
			return prSpec("/pr")
		}},
	}
}

// captureStdout redirects os.Stdout for fn's duration and returns what it wrote.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stdout = w
	defer func() { os.Stdout = orig }()

	fn()

	w.Close()
	var buf strings.Builder
	buf.Grow(4096)
	chunk := make([]byte, 4096)
	for {
		n, err := r.Read(chunk)
		if n > 0 {
			buf.Write(chunk[:n])
		}
		if err != nil {
			break
		}
	}
	return buf.String()
}

// ---------------------------------------------------------------------------
// GetPRWorkflow
// ---------------------------------------------------------------------------

func TestGetPRWorkflow_BasePRFails(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
	}))
	defer srv.Close()

	err := GetPRWorkflow(insightTestCtx(srv.URL, ""))
	if err == nil {
		t.Fatal("expected error when base PR fetch fails, got nil")
	}
}

func TestGetPRWorkflow_MachineFormatSkipsInsight(t *testing.T) {
	var overviewHits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/overview" {
			overviewHits.Add(1)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"number":1}`))
	}))
	defer srv.Close()

	if err := GetPRWorkflow(insightTestCtx(srv.URL, "json")); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := overviewHits.Load(); got != 0 {
		t.Fatalf("AI overview endpoint called %d times, want 0 for --format json", got)
	}
}

func TestGetPRWorkflow_InsightFailureOmitsSectionButSucceeds(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/overview" {
			w.WriteHeader(500)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"number":1}`))
	}))
	defer srv.Close()

	var err error
	out := captureStdout(t, func() {
		err = GetPRWorkflow(insightTestCtx(srv.URL, ""))
	})
	if err != nil {
		t.Fatalf("get pr must succeed even when an insight endpoint fails, got: %v", err)
	}
	if strings.Contains(out, "Insight") {
		t.Fatalf("output must omit the Insight section on failure, got:\n%s", out)
	}
}

func TestGetPRWorkflow_InsightSuccessRendersSection(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/overview" {
			w.Write([]byte(`{"risk":"low","content":"looks fine"}`))
			return
		}
		w.Write([]byte(`{"number":1}`))
	}))
	defer srv.Close()

	var err error
	out := captureStdout(t, func() {
		err = GetPRWorkflow(insightTestCtx(srv.URL, ""))
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "Insight") {
		t.Fatalf("output must contain the Insight section, got:\n%s", out)
	}
}

// ---------------------------------------------------------------------------
// ModuleInit
// ---------------------------------------------------------------------------

func TestModuleInit_RegistersGetPRWorkflow(t *testing.T) {
	registered := map[string]bool{}
	spy := &moduleInitSpy{register: func(id string) { registered[id] = true }}
	ModuleInit(spy)
	if !registered[getPRWorkflowID] {
		t.Fatalf("ModuleInit did not register workflow %q", getPRWorkflowID)
	}
}

type moduleInitSpy struct{ register func(id string) }

func (s *moduleInitSpy) Register(*spec.CommandSpec) error { return nil }
func (s *moduleInitSpy) RegisterWorkflow(id string, _ registry.WorkflowFn) {
	if s.register != nil {
		s.register(id)
	}
}
func (s *moduleInitSpy) RegisterTextFormatter(string, cmdctx.TextFormatterFn)           {}
func (s *moduleInitSpy) RegisterBodyFn(string, cmdctx.CreateBodyFn)                     {}
func (s *moduleInitSpy) RegisterQueryParamsFn(string, cmdctx.QueryParamsFn)             {}
func (s *moduleInitSpy) RegisterFollowFn(string, cmdctx.FollowFn)                       {}
func (s *moduleInitSpy) RegisterFetchFn(string, cmdctx.FetchFn)                         {}
func (s *moduleInitSpy) RegisterListTransformFn(string, cmdctx.ListTransformFn)         {}
func (s *moduleInitSpy) RegisterFlagCompletionFn(string, registry.FlagCompletionFn)     {}
func (s *moduleInitSpy) RegisterFlagResolveFn(string, cmdctx.FlagResolveFn)             {}
func (s *moduleInitSpy) RegisterEndpointValidatorFn(string, cmdctx.EndpointValidatorFn) {}
