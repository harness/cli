// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package code

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/harness/cli/pkg/auth"
	"github.com/harness/cli/pkg/cmdctx"
	"github.com/harness/cli/pkg/extractutil"
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
func (noopResolver) RunUIHandler(ctx *cmdctx.Ctx, fnID string) error                 { return nil }
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
	if strings.Contains(out, "AI Code Review") {
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
	if !strings.Contains(out, "AI Code Review") {
		t.Fatalf("output must contain the AI Code Review heading, got:\n%s", out)
	}
	if !strings.Contains(out, "#1") {
		t.Fatalf("output must contain the PR header, got:\n%s", out)
	}

	// The header must render first, Insight last (right before the link), and the
	// PR link must print exactly once, at the very end.
	numberIdx := strings.Index(out, "#1")
	insightIdx := strings.Index(out, "AI Code Review")
	if insightIdx == -1 || numberIdx == -1 || insightIdx < numberIdx {
		t.Fatalf("expected the header to render before Insight, got:\n%s", out)
	}
	lastLinkIdx := strings.LastIndex(out, "/pulls/42")
	if lastLinkIdx == -1 || lastLinkIdx < insightIdx {
		t.Fatalf("expected the PR link to appear after the Insight section, got:\n%s", out)
	}
	if linkCount := strings.Count(out, "/pulls/42"); linkCount != 1 {
		t.Fatalf("expected exactly one PR link (sections must not duplicate it), got %d in:\n%s", linkCount, out)
	}
}

func TestGetPRWorkflow_ActivityFailureOmitsSectionButSucceeds(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/activities") {
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
		t.Fatalf("get pr must succeed even when the activity endpoint fails, got: %v", err)
	}
	if strings.Contains(out, "Not showing") {
		t.Fatalf("output must omit the comments summary when the activity fetch fails, got:\n%s", out)
	}
}

func TestGetPRWorkflow_ActivityWithNoCommentsOmitsSection(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/activities"):
			w.Write([]byte(`[]`))
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
	if strings.Contains(out, "Not showing") {
		t.Fatalf("output must omit the comments summary when there are no comments, got:\n%s", out)
	}
}

func TestGetPRWorkflow_ActivitySuccessRendersCommentsSummary(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/activities"):
			w.Write([]byte(`[
				{"kind":"comment","type":"comment","author":{"display_name":"Alice"},"text":"first comment","order":1,"created":1000},
				{"kind":"comment","type":"comment","author":{"display_name":"Bob"},"text":"newest comment","order":2,"created":2000}
			]`))
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
	if !strings.Contains(out, "Bob") || !strings.Contains(out, "newest comment") {
		t.Fatalf("expected the newest comment (Bob's) to be shown, got:\n%s", out)
	}
	if strings.Contains(out, "first comment") {
		t.Fatalf("expected only the newest comment to render, not the older one, got:\n%s", out)
	}
	if !strings.Contains(out, "harness list pr_comment repo1/42") {
		t.Fatalf("expected hint pointing at \"list pr_comment\" with the PR id, got:\n%s", out)
	}
	// The comments summary must render after the description and before the footer link.
	summaryIdx := strings.Index(out, "Comments")
	lastLinkIdx := strings.LastIndex(out, "/pulls/42")
	if summaryIdx == -1 || lastLinkIdx == -1 || lastLinkIdx < summaryIdx {
		t.Fatalf("expected the PR link to appear after the comments summary, got:\n%s", out)
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

type fakeDataAccessor struct {
	values map[string]string
	slices map[string][]any
}

func (f fakeDataAccessor) GetString(path string) string { return f.values[path] }
func (f fakeDataAccessor) GetInt64(string) int64        { return 0 }
func (f fakeDataAccessor) GetBool(string) bool          { return false }
func (f fakeDataAccessor) GetTs(string) string          { return "" }
func (f fakeDataAccessor) GetData() any                 { return nil }
func (f fakeDataAccessor) GetSlice(path string) []any   { return f.slices[path] }

// ---------------------------------------------------------------------------
// reviewGroupTextFormatter
// ---------------------------------------------------------------------------

func TestReviewGroupTextFormatter_ColorizesBulletAndRiskTag(t *testing.T) {
	groups := []any{
		map[string]any{
			"title":       "Auth middleware changes",
			"description": "Touches token validation.",
			"tags":        map[string]any{"risk": "high"},
			"files": []any{
				map[string]any{"path": "auth/middleware.go"},
			},
		},
	}
	out := captureStdout(t, func() {
		err := reviewGroupTextFormatter(os.Stdout, fakeDataAccessor{
			slices: map[string][]any{"it.groups": groups},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
	if strings.Contains(out, "Group 1") || strings.Contains(out, "Group ") {
		t.Fatalf("output must not contain a numbered \"Group\" label, got:\n%s", out)
	}
	if !strings.Contains(out, "●") {
		t.Fatalf("output must contain the risk bullet, got:\n%s", out)
	}
	if !strings.Contains(out, "Auth middleware changes") {
		t.Fatalf("output must contain the group title, got:\n%s", out)
	}
	if !strings.Contains(out, "[high]") {
		t.Fatalf("output must contain the risk tag, got:\n%s", out)
	}
	if !strings.Contains(out, "auth/middleware.go") {
		t.Fatalf("output must still list the file path, got:\n%s", out)
	}
}

// ---------------------------------------------------------------------------
// renderPRHeader / relativeTime / checkStatusText / plural / prStateColor
// ---------------------------------------------------------------------------

func TestRelativeTime(t *testing.T) {
	now := time.Now()
	tests := []struct {
		name    string
		epochMs int64
		want    string
	}{
		{"zero timestamp", 0, ""},
		{"negative timestamp", -5, ""},
		{"just now", now.Add(-10 * time.Second).UnixMilli(), "just now"},
		{"minutes ago", now.Add(-5 * time.Minute).UnixMilli(), "• 5 mins ago"},
		{"one minute ago (singular)", now.Add(-1 * time.Minute).UnixMilli(), "• 1 min ago"},
		{"hours ago", now.Add(-3 * time.Hour).UnixMilli(), "• 3 hrs ago"},
		{"days ago", now.Add(-13 * 24 * time.Hour).UnixMilli(), "• 13 days ago"},
		{"one day ago (singular)", now.Add(-1 * 24 * time.Hour).UnixMilli(), "• 1 day ago"},
		{"months ago", now.Add(-60 * 24 * time.Hour).UnixMilli(), "• 2 mons ago"},
		{"years ago", now.Add(-400 * 24 * time.Hour).UnixMilli(), "• 1 yr ago"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := relativeTime(tt.epochMs); got != tt.want {
				t.Fatalf("relativeTime(%d) = %q, want %q", tt.epochMs, got, tt.want)
			}
		})
	}
}

func TestCheckStatusText(t *testing.T) {
	tests := []struct {
		status string
		want   string
	}{
		{"mergeable", "✓ Checks passing"},
		{"Mergeable", "✓ Checks passing"},
		{"unchecked", "… Checks running"},
		{"checking", "… Checks running"},
		{"blocked", "✗ Checks failing (blocked)"},
	}
	for _, tt := range tests {
		t.Run(tt.status, func(t *testing.T) {
			got := checkStatusText(tt.status)
			if !strings.Contains(got, tt.want) {
				t.Fatalf("checkStatusText(%q) = %q, want to contain %q", tt.status, got, tt.want)
			}
		})
	}
}

func TestPlural(t *testing.T) {
	if got := plural(1); got != "" {
		t.Fatalf("plural(1) = %q, want empty", got)
	}
	for _, n := range []int64{0, 2, -1} {
		if got := plural(n); got != "s" {
			t.Fatalf("plural(%d) = %q, want %q", n, got, "s")
		}
	}
}

func TestPRStateColor(t *testing.T) {
	tests := map[string]bool{"open": true, "draft": true, "merged": true, "closed": true, "unknown": false}
	for state, wantColor := range tests {
		got := prStateColor(state) != 0
		if got != wantColor {
			t.Fatalf("prStateColor(%q) colored = %v, want %v", state, got, wantColor)
		}
	}
}

func TestRenderPRHeader_OmitsEmptyChecksAndReviews(t *testing.T) {
	pr := map[string]any{
		"number": 1, "title": "Add feature", "state": "open", "is_draft": false,
		"source_branch": "feature", "target_branch": "main",
		"author": map[string]any{"display_name": "Jane Doe"},
		"stats":  map[string]any{"files_changed": 2, "commits": 1, "additions": 10, "deletions": 3},
	}
	d := extractutil.MakeDataAccessor(map[string]any{}, pr)
	var buf bytes.Buffer
	renderPRHeader(&buf, d)
	out := buf.String()

	if strings.Contains(out, "Checks") {
		t.Fatalf("expected no checks line when merge_check_status is empty, got:\n%s", out)
	}
	if strings.Contains(out, "Reviews:") {
		t.Fatalf("expected no reviews line when required_count is 0, got:\n%s", out)
	}
	if !strings.Contains(out, "#1") || !strings.Contains(out, "Add feature") {
		t.Fatalf("expected header with number and title, got:\n%s", out)
	}
	if !strings.Contains(out, "OPEN") {
		t.Fatalf("expected OPEN badge, got:\n%s", out)
	}
}

func TestRenderPRHeader_IncludesChecksAndReviewsWhenPresent(t *testing.T) {
	pr := map[string]any{
		"number": 111, "title": "Point spec at v4 endpoints", "state": "open", "is_draft": true,
		"source_branch": "worktree-fme-spec-fix", "target_branch": "main",
		"author": map[string]any{"display_name": "Deepak Puthraya"},
		"stats": map[string]any{
			"files_changed": 12, "commits": 8, "additions": 938, "deletions": 58,
			"reviews": map[string]any{"required_count": 1, "latest_approvals": 0},
		},
		"merge_check_status": "mergeable",
	}
	d := extractutil.MakeDataAccessor(map[string]any{}, pr)
	var buf bytes.Buffer
	renderPRHeader(&buf, d)
	out := buf.String()

	if !strings.Contains(out, "DRAFT") {
		t.Fatalf("expected DRAFT badge for is_draft PR in open state, got:\n%s", out)
	}
	if !strings.Contains(out, "Checks passing") {
		t.Fatalf("expected checks line, got:\n%s", out)
	}
	if !strings.Contains(out, "Reviews: 0/1 approved") {
		t.Fatalf("expected reviews line, got:\n%s", out)
	}
	if !strings.Contains(out, "938") || !strings.Contains(out, "58") {
		t.Fatalf("expected additions/deletions stats, got:\n%s", out)
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
func (s *moduleInitSpy) QualifyNoun(*spec.NounDef)        {}
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
