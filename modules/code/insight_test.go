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

func (s spyResolver) ResolveTextFormatter(id string) cmdctx.TextFormatterFn {
	switch id {
	case reviewGroupTextFormatterID:
		return reviewGroupTextFormatter
	case insightTextFormatterID:
		return insightTextFormatter
	default:
		return nil
	}
}

// testNounURLPath is a stand-in url_path template shared by test nouns: it resolves
// from ctx.idParts (repo/PR are always in position, unlike each noun's own response body).
const testNounURLPath = "/pulls/{{ctx.idParts[1]}}"

func (s spyResolver) GetNoun(noun string) *spec.NounDef {
	switch noun {
	case "pr":
		return &spec.NounDef{UrlPath: testNounURLPath, Fields: []spec.FieldDef{{ID: "number", Expr: "it.number"}}}
	case "pr_insight":
		return &spec.NounDef{UrlPath: testNounURLPath, Fields: []spec.FieldDef{{ID: "risk", Expr: "it.risk"}}}
	case "pr_review_group":
		return &spec.NounDef{UrlPath: testNounURLPath}
	default:
		return nil
	}
}

func prSpec(path string) *spec.CommandSpec {
	return &spec.CommandSpec{
		Command: "get pr", Verb: "get", VerbHandler: "get",
		Noun: "pr", Module: "code", HandlerType: spec.HandlerWorkflow,
		Endpoint: &spec.EndpointSpec{Method: "GET", Path: path, ItemExpr: "it", TextFooter: "\n{{url(it)}}\n"},
	}
}

func insightSpec(path string) *spec.CommandSpec {
	return &spec.CommandSpec{
		Command: "get pr:insight", Verb: "get", VerbHandler: "get",
		Noun: "pr", NounVariant: "insight", FieldsNoun: "pr_insight", Module: "code",
		HandlerType: spec.HandlerEndpoint,
		Endpoint: &spec.EndpointSpec{
			Method: "GET", Path: path, ItemExpr: "it", TextFooter: "\n{{url(it)}}\n",
			TextFormatter: insightTextFormatterID,
		},
	}
}

func reviewGroupSpec(path string) *spec.CommandSpec {
	return &spec.CommandSpec{
		Command: "get pr:review_group", Verb: "get", VerbHandler: "get",
		Noun: "pr", NounVariant: "review_group", FieldsNoun: "pr_review_group", Module: "code",
		HandlerType: spec.HandlerEndpoint,
		Endpoint:    &spec.EndpointSpec{Method: "GET", Path: path, ItemExpr: "it", TextFormatter: reviewGroupTextFormatterID},
	}
}

func insightTestCtx(srvURL, format string) *cmdctx.Ctx {
	return &cmdctx.Ctx{
		Context:     context.Background(),
		Noun:        "pr",
		Id:          "repo1/42",
		VerbHandler: "get",
		Auth:        &auth.ResolvedAuth{AuthType: auth.AuthTypePAT, APIUrl: srvURL, AccountID: "acct", OrgID: "org", ProjectID: "proj", PATToken: "test-token"},
		FormatFlags: cmdctx.FormatFlags{Format: format},
		FlagValues:  map[string]any{},
		Resolver: spyResolver{getSpec: func(verb, noun string) *spec.CommandSpec {
			switch noun {
			case "pr:insight":
				return insightSpec("/insight")
			case "pr:review_group":
				return reviewGroupSpec("/review_groups")
			default:
				return prSpec("/pr")
			}
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
	var insightHits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/insight" {
			insightHits.Add(1)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"number":1}`))
	}))
	defer srv.Close()

	if err := GetPRWorkflow(insightTestCtx(srv.URL, "json")); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := insightHits.Load(); got != 0 {
		t.Fatalf("insight endpoint called %d times, want 0 for --format json", got)
	}
}

func TestGetPRWorkflow_InsightFailureOmitsSectionButSucceeds(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/insight" {
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
	if strings.Contains(out, "AI Code Overview") {
		t.Fatalf("output must omit failed sections, got:\n%s", out)
	}
}

func TestGetPRWorkflow_InsightSuccessRendersSection(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/insight":
			w.Write([]byte(`{"risk":"low","content":"looks fine"}`))
		default:
			w.Write([]byte(`{"number":1}`))
		}
	}))
	defer srv.Close()

	var err error
	out := captureStdout(t, func() {
		err = GetPRWorkflow(insightTestCtx(srv.URL, ""))
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "AI Code Overview [low]") {
		t.Fatalf("output must contain the colorized AI Code Overview heading, got:\n%s", out)
	}
	if !strings.Contains(out, "┌") || !strings.Contains(out, "└") {
		t.Fatalf("output must box the AI Code Overview section, got:\n%s", out)
	}
	if !strings.Contains(out, "PR Details") {
		t.Fatalf("output must contain the PR Details section, got:\n%s", out)
	}

	// Insight must render first, PR Details last (right before the link), and the
	// PR link must print exactly once, at the very end.
	insightIdx := strings.Index(out, "AI Code Overview")
	prDetailsIdx := strings.Index(out, "PR Details")
	if insightIdx == -1 || prDetailsIdx == -1 || prDetailsIdx < insightIdx {
		t.Fatalf("expected Insight to render before PR Details, got:\n%s", out)
	}
	lastLinkIdx := strings.LastIndex(out, "/pulls/42")
	if lastLinkIdx == -1 || lastLinkIdx < prDetailsIdx {
		t.Fatalf("expected the PR link to appear after the PR Details section, got:\n%s", out)
	}
	if linkCount := strings.Count(out, "/pulls/42"); linkCount != 1 {
		t.Fatalf("expected exactly one PR link (sections must not duplicate it), got %d in:\n%s", linkCount, out)
	}
}

// TestReviewGroupCommand_StandaloneRendersLink verifies "get pr:review_group" run on
// its own (it's no longer embedded in GetPRWorkflow) still prints its own trailing
// PR link.
func TestReviewGroupCommand_StandaloneRendersLink(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"groups":[]}`))
	}))
	defer srv.Close()

	ctx := insightTestCtx(srv.URL, "")
	ctx.Noun, ctx.FieldsNoun = "pr", "pr_review_group"

	out := captureStdout(t, func() {
		if _, err := registry.RunEndpoint(ctx, reviewGroupSpec("/review_groups").Endpoint); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
	if !strings.Contains(out, "/pulls/42") {
		t.Fatalf("standalone run must print its own PR link, got:\n%s", out)
	}
}

// ---------------------------------------------------------------------------
// insightTextFormatter
// ---------------------------------------------------------------------------

type fakeDataAccessor struct{ values map[string]string }

func (f fakeDataAccessor) GetString(path string) string { return f.values[path] }
func (f fakeDataAccessor) GetInt64(string) int64        { return 0 }
func (f fakeDataAccessor) GetBool(string) bool          { return false }
func (f fakeDataAccessor) GetTs(string) string          { return "" }
func (f fakeDataAccessor) GetData() any                 { return nil }
func (f fakeDataAccessor) GetSlice(string) []any        { return nil }

func TestInsightTextFormatter_HeadingByRisk(t *testing.T) {
	cases := []struct {
		risk    string
		heading string
	}{
		{"low", "AI Code Overview [low]"},
		{"medium", "AI Code Overview [medium]"},
		{"high", "AI Code Overview [high]"},
		{"", "AI Code Overview"},
		{"unknown", "AI Code Overview [unknown]"},
	}
	for _, c := range cases {
		out := captureStdout(t, func() {
			err := insightTextFormatter(os.Stdout, fakeDataAccessor{values: map[string]string{
				"it.risk": c.risk, "it.content": "looks fine",
			}})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
		if !strings.Contains(out, c.heading) {
			t.Fatalf("risk %q: expected heading %q in output, got:\n%s", c.risk, c.heading, out)
		}
		if !strings.HasPrefix(out, "┌") {
			t.Fatalf("risk %q: expected output to start with a box top border, got:\n%s", c.risk, out)
		}
		if !strings.Contains(out, "└") {
			t.Fatalf("risk %q: expected output to contain a box bottom border, got:\n%s", c.risk, out)
		}
		if strings.Contains(out, "http") {
			t.Fatalf("risk %q: formatter must not print a trailing link, got:\n%s", c.risk, out)
		}
	}
}

func TestInsightTextFormatter_WrapsContentWithinBox(t *testing.T) {
	longWord := strings.Repeat("word ", 30)
	out := captureStdout(t, func() {
		err := insightTextFormatter(os.Stdout, fakeDataAccessor{values: map[string]string{
			"it.risk": "medium", "it.content": longWord,
		}})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) < 3 {
		t.Fatalf("expected at least a top border, content, and bottom border, got:\n%s", out)
	}
	boxWidth := len([]rune(lines[0]))
	contentLines := 0
	for _, line := range lines[1 : len(lines)-1] {
		if len([]rune(line)) != boxWidth {
			t.Fatalf("content line %q has length %d, want %d (box width)", line, len([]rune(line)), boxWidth)
		}
		if !strings.HasPrefix(line, "│ ") || !strings.HasSuffix(line, " │") {
			t.Fatalf("content line %q must be bordered with │, got:\n%s", line, out)
		}
		contentLines++
	}
	if contentLines < 2 {
		t.Fatalf("expected long content to wrap across multiple lines, got %d line(s):\n%s", contentLines, out)
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
