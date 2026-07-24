// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package gitops

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
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

func (noopResolver) ResolveTextFormatter(id string) cmdctx.TextFormatterFn                        { return nil }
func (noopResolver) ResolveBodyFn(id string) cmdctx.CreateBodyFn                                  { return nil }
func (noopResolver) ResolveQueryParamsFn(id string) cmdctx.QueryParamsFn                          { return nil }
func (noopResolver) ResolveFlagResolveFn(id string) cmdctx.FlagResolveFn                          { return nil }
func (noopResolver) ResolveFetchFn(id string) (cmdctx.FetchFn, error)                             { return nil, nil }
func (noopResolver) ResolveEndpointValidator(id string) cmdctx.EndpointValidatorFn                { return nil }
func (noopResolver) GetSpec(verb, noun string) *spec.CommandSpec                                  { return nil }
func (noopResolver) GetNoun(noun string) *spec.NounDef                                            { return nil }
func (noopResolver) ResolveNounAlias(alias string) string                                         { return "" }
func (noopResolver) RunEndpoint(ctx *cmdctx.Ctx, ep *spec.EndpointSpec) (any, error)              { return nil, nil }
func (noopResolver) FormatList(*cmdctx.Ctx, []any, []spec.FieldDef, []string) error               { return nil }
func (noopResolver) FetchItems(*cmdctx.Ctx, *spec.EndpointSpec, cmdctx.PagingFlags) ([]any, error) { return nil, nil }
func (noopResolver) GetModuleMetas() []spec.ModuleMeta                                            { return nil }
func (noopResolver) GetSpecsForModule(string) []*spec.CommandSpec                                 { return nil }
func (noopResolver) GetAllSpecs() []*spec.CommandSpec                                             { return nil }
func (noopResolver) GetVerbInfos() []spec.VerbInfo                                                { return nil }
func (noopResolver) ResolveCommandFields(*spec.CommandSpec) []spec.FieldDef                       { return nil }

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

func agentGetSpec(path string) *spec.CommandSpec {
	return &spec.CommandSpec{
		Command: "get gitops_agent", Verb: "get", VerbHandler: "get",
		Noun: "gitops_agent", Module: "gitops", HandlerType: spec.HandlerEndpoint,
		Endpoint: &spec.EndpointSpec{Method: "GET", Path: path, ItemExpr: "it"},
	}
}

func resolvedAuth(apiURL string) *auth.ResolvedAuth {
	return &auth.ResolvedAuth{
		AuthType: auth.AuthTypePAT, APIUrl: apiURL,
		AccountID: "acct", OrgID: "org", ProjectID: "proj", PATToken: "test-token",
	}
}

func testCtx(flags map[string]any) *cmdctx.Ctx {
	return &cmdctx.Ctx{
		FlagValues: flags,
		Auth:       &auth.ResolvedAuth{AccountID: "acct", OrgID: "org", ProjectID: "proj"},
		Context:    context.Background(),
	}
}

// newAgentCtx wires a spy resolver and resolved auth pointing at srvURL.
func newAgentCtx(flags map[string]any, srvURL string) *cmdctx.Ctx {
	ctx := testCtx(flags)
	ctx.Id = "my-agent"
	ctx.Auth = resolvedAuth(srvURL)
	ctx.Resolver = spyResolver{getSpec: func(_, _ string) *spec.CommandSpec {
		return agentGetSpec("/gitops/api/v1/agents/my-agent")
	}}
	return ctx
}

func jsonOf(v any) []byte { b, _ := json.Marshal(v); return b }

// agentBody is a reusable agent GET response with a namespace.
var agentBody = jsonOf(map[string]any{"metadata": map[string]any{"namespace": "ns"}})

// ---------------------------------------------------------------------------
// validation — no HTTP needed
// ---------------------------------------------------------------------------

func TestExecuteAgentInstall_validation(t *testing.T) {
	tests := []struct {
		name       string
		id         string
		flags      map[string]any
		resolver   cmdctx.Resolver
		wantErrSub string
		wantNoSub  string
	}{
		{name: "missing agent id", wantErrSub: "requires a positional"},
		{name: "invalid method docker", id: "my-agent", flags: map[string]any{"method": "docker"}, wantErrSub: `must be "helm" or "yaml"`},
		{name: "invalid method kubectl", id: "my-agent", flags: map[string]any{"method": "kubectl"}, wantErrSub: `must be "helm" or "yaml"`},
		{name: "default method ok", id: "my-agent", wantNoSub: "invalid --method"},
		{name: "helm method ok", id: "my-agent", flags: map[string]any{"method": "helm"}, wantNoSub: "invalid --method"},
		{name: "yaml method ok", id: "my-agent", flags: map[string]any{"method": "yaml"}, wantNoSub: "invalid --method"},
		{name: "spec not found", id: "my-agent", wantErrSub: "spec not found"},
		{
			name:       "spec endpoint nil",
			id:         "my-agent",
			resolver:   spyResolver{getSpec: func(_, _ string) *spec.CommandSpec { return &spec.CommandSpec{} }},
			wantErrSub: "spec not found",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := testCtx(tc.flags)
			ctx.Id = tc.id
			if tc.resolver != nil {
				ctx.Resolver = tc.resolver
			} else {
				ctx.Resolver = noopResolver{}
			}
			err := executeAgentInstall(ctx)
			if tc.wantErrSub != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErrSub) {
					t.Fatalf("error = %v, want %q", err, tc.wantErrSub)
				}
			}
			if tc.wantNoSub != "" && err != nil && strings.Contains(err.Error(), tc.wantNoSub) {
				t.Fatalf("error %q must not contain %q", err, tc.wantNoSub)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// HTTP GET path — tests that fail before or without reaching the POST call
// ---------------------------------------------------------------------------

func TestExecuteAgentInstall_GETPath(t *testing.T) {
	tests := []struct {
		name       string
		getStatus  int    // defaults to 200
		getBody    []byte // defaults to agentBody
		setup      func(dir string) map[string]any
		wantErrSub string
	}{
		{
			name:       "CallEndpoint error on 500",
			getStatus:  500,
			wantErrSub: "fetching agent",
		},
		{
			name:       "namespace from agent metadata",
			getBody:    jsonOf(map[string]any{"metadata": map[string]any{"namespace": "harness"}}),
			wantErrSub: "output file is required",
		},
		{
			name:    "namespace from install file",
			getBody: jsonOf(map[string]any{"metadata": map[string]any{}}),
			setup: func(dir string) map[string]any {
				f := filepath.Join(dir, "install.yaml")
				os.WriteFile(f, []byte("namespace: my-ns\n"), 0o600)
				return map[string]any{"file": f}
			},
			wantErrSub: "output file is required",
		},
		{
			name:    "bad YAML in install file",
			getBody: agentBody,
			setup: func(dir string) map[string]any {
				f := filepath.Join(dir, "bad.yaml")
				os.WriteFile(f, []byte(":\n  - [\n"), 0o600)
				return map[string]any{"file": f}
			},
			wantErrSub: "parsing -f install file",
		},
		{
			name:       "no namespace anywhere",
			getBody:    jsonOf(map[string]any{}),
			wantErrSub: "namespace is required",
		},
		{
			name:       "bad output file extension",
			setup:      func(dir string) map[string]any { return map[string]any{"output_file": "out.json"} },
			wantErrSub: "output file must end with .yaml",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			status := tc.getStatus
			if status == 0 {
				status = 200
			}
			body := tc.getBody
			if body == nil {
				body = agentBody
			}
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(status)
				w.Write(body)
			}))
			defer srv.Close()

			dir := t.TempDir()
			var flags map[string]any
			if tc.setup != nil {
				flags = tc.setup(dir)
			}
			err := executeAgentInstall(newAgentCtx(flags, srv.URL))
			if err == nil || !strings.Contains(err.Error(), tc.wantErrSub) {
				t.Fatalf("error = %v, want %q", err, tc.wantErrSub)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// full end-to-end: GET agent → POST helm-overrides → write file
// ---------------------------------------------------------------------------

func TestExecuteAgentInstall_fullPath(t *testing.T) {
	stdPost := jsonOf(map[string]any{"value": "data\n"})

	tests := []struct {
		name       string
		postStatus int    // defaults to 200
		postBody   []byte // defaults to stdPost
		level      string
		setup      func(dir string) map[string]any
		wantErrSub string
		wantFile   string
	}{
		{
			name:     "helm happy path writes JSON-wrapped value",
			postBody: jsonOf(map[string]any{"value": "helm: override\n"}),
			setup:    func(dir string) map[string]any { return map[string]any{"output_file": filepath.Join(dir, "out.yaml")} },
			wantFile: "helm: override\n",
		},
		{
			name: "yaml method writes raw body",
			setup: func(dir string) map[string]any {
				return map[string]any{"output_file": filepath.Join(dir, "out.yaml"), "method": "yaml"}
			},
			wantFile: "data\n",
		},
		{
			name:       "POST 403 returns API error",
			postStatus: 403,
			postBody:   []byte(`{"message":"forbidden"}`),
			setup:      func(dir string) map[string]any { return map[string]any{"output_file": filepath.Join(dir, "out.yaml")} },
			wantErrSub: "API error",
		},
		{
			name: "optional install fields accepted",
			setup: func(dir string) map[string]any {
				f := filepath.Join(dir, "install.yaml")
				os.WriteFile(f, []byte("namespace: custom-ns\nskipCrds: true\ncaData: ca\nprivateKey: pk\nproxy:\n  http: p\nargocdSettings:\n  k: v\n"), 0o600)
				return map[string]any{"file": f, "output_file": filepath.Join(dir, "out.yaml")}
			},
		},
		{
			name:  "org level strips project",
			level: "org",
			setup: func(dir string) map[string]any { return map[string]any{"output_file": filepath.Join(dir, "out.yaml")} },
		},
		{
			name:  "account level strips org and project",
			level: "account",
			setup: func(dir string) map[string]any { return map[string]any{"output_file": filepath.Join(dir, "out.yaml")} },
		},
		{
			name:  ".yaml.txt extension accepted",
			setup: func(dir string) map[string]any { return map[string]any{"output_file": filepath.Join(dir, "out.yaml.txt")} },
		},
		{
			name:       "write error on bad path",
			setup:      func(dir string) map[string]any { return map[string]any{"output_file": filepath.Join(dir, "nodir", "out.yaml")} },
			wantErrSub: "writing",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			postStatus := tc.postStatus
			if postStatus == 0 {
				postStatus = 200
			}
			postBody := tc.postBody
			if postBody == nil {
				postBody = stdPost
			}

			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.Method == "GET" {
					w.Write(agentBody)
				} else {
					w.WriteHeader(postStatus)
					w.Write(postBody)
				}
			}))
			defer srv.Close()

			dir := t.TempDir()
			var flags map[string]any
			if tc.setup != nil {
				flags = tc.setup(dir)
			}
			ctx := newAgentCtx(flags, srv.URL)
			ctx.Level = tc.level

			err := executeAgentInstall(ctx)
			if tc.wantErrSub != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErrSub) {
					t.Fatalf("error = %v, want %q", err, tc.wantErrSub)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tc.wantFile != "" {
				outFile := flags["output_file"].(string)
				written, _ := os.ReadFile(outFile)
				if string(written) != tc.wantFile {
					t.Fatalf("file content = %q, want %q", written, tc.wantFile)
				}
			}
		})
	}
}

// ---------------------------------------------------------------------------
// ModuleInit
// ---------------------------------------------------------------------------

func TestModuleInit_RegistersWorkflow(t *testing.T) {
	registered := map[string]bool{}
	spy := &moduleInitSpy{register: func(id string) { registered[id] = true }}
	ModuleInit(spy)
	if !registered[installWorkflowID] {
		t.Fatalf("ModuleInit did not register workflow %q", installWorkflowID)
	}
}

type moduleInitSpy struct{ register func(id string) }

func (s *moduleInitSpy) Register(*spec.CommandSpec) error                                        { return nil }
func (s *moduleInitSpy) RegisterWorkflow(id string, _ registry.WorkflowFn)                      { if s.register != nil { s.register(id) } }
func (s *moduleInitSpy) RegisterTextFormatter(string, cmdctx.TextFormatterFn)                   {}
func (s *moduleInitSpy) RegisterBodyFn(string, cmdctx.CreateBodyFn)                             {}
func (s *moduleInitSpy) RegisterQueryParamsFn(string, cmdctx.QueryParamsFn)                     {}
func (s *moduleInitSpy) RegisterFollowFn(string, cmdctx.FollowFn)                               {}
func (s *moduleInitSpy) RegisterFetchFn(string, cmdctx.FetchFn)                                 {}
func (s *moduleInitSpy) RegisterFlagCompletionFn(string, registry.FlagCompletionFn)             {}
func (s *moduleInitSpy) RegisterFlagResolveFn(string, cmdctx.FlagResolveFn)                     {}
func (s *moduleInitSpy) RegisterEndpointValidatorFn(string, cmdctx.EndpointValidatorFn)         {}
