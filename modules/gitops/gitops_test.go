// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package gitops

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/harness/cli/v3/pkg/auth"
	"github.com/harness/cli/v3/pkg/cmdctx"
	"github.com/harness/cli/v3/pkg/console"
	"github.com/harness/cli/v3/pkg/extractutil"
	"github.com/harness/cli/v3/pkg/registry"
	"github.com/harness/cli/v3/pkg/spec"
)

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

type noopResolver struct{}

func (noopResolver) ResolveTextFormatter(id string) cmdctx.TextFormatterFn { return nil }
func (noopResolver) ResolveFieldType(string) (cmdctx.FieldTypeHandler, bool) {
	return cmdctx.FieldTypeHandler{}, false
}
func (noopResolver) ResolveBodyFn(id string) cmdctx.CreateBodyFn                     { return nil }
func (noopResolver) ResolveQueryParamsFn(id string) cmdctx.QueryParamsFn             { return nil }
func (noopResolver) ResolveFlagResolveFn(id string) cmdctx.FlagResolveFn             { return nil }
func (noopResolver) ResolveFetchFn(id string) (cmdctx.FetchFn, error)                { return nil, nil }
func (noopResolver) ResolveListTransformFn(id string) cmdctx.ListTransformFn         { return nil }
func (noopResolver) ResolveItemFn(id string) cmdctx.ItemFn                           { return nil }
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
func (noopResolver) GetHiddenModule(string) *spec.ModuleMeta                { return nil }
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
			name: ".yaml.txt extension accepted",
			setup: func(dir string) map[string]any {
				return map[string]any{"output_file": filepath.Join(dir, "out.yaml.txt")}
			},
		},
		{
			name: "write error on bad path",
			setup: func(dir string) map[string]any {
				return map[string]any{"output_file": filepath.Join(dir, "nodir", "out.yaml")}
			},
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

func TestModuleInit_RegistersWorkflowAndBodyFn(t *testing.T) {
	workflows := map[string]bool{}
	bodyFns := map[string]bool{}
	formatters := map[string]bool{}
	spy := &moduleInitSpy{
		registerWorkflow:      func(id string) { workflows[id] = true },
		registerBodyFn:        func(id string) { bodyFns[id] = true },
		registerTextFormatter: func(id string) { formatters[id] = true },
	}
	ModuleInit(spy)
	if !workflows[installWorkflowID] {
		t.Fatalf("ModuleInit did not register workflow %q", installWorkflowID)
	}
	if !workflows[importAgentWorkflowID] {
		t.Fatalf("ModuleInit did not register workflow %q", importAgentWorkflowID)
	}
	if !workflows[listAutocreateLogWorkflowID] {
		t.Fatalf("ModuleInit did not register workflow %q", listAutocreateLogWorkflowID)
	}
	if !bodyFns[createAppProjectMappingBodyFnID] {
		t.Fatalf("ModuleInit did not register body_fn %q", createAppProjectMappingBodyFnID)
	}
	if !bodyFns[deleteAppProjectMappingBodyFnID] {
		t.Fatalf("ModuleInit did not register body_fn %q", deleteAppProjectMappingBodyFnID)
	}
	if !bodyFns[updateAppProjectMappingBodyFnID] {
		t.Fatalf("ModuleInit did not register body_fn %q", updateAppProjectMappingBodyFnID)
	}
	if !bodyFns[importAgentBodyFnID] {
		t.Fatalf("ModuleInit did not register body_fn %q", importAgentBodyFnID)
	}
	if !formatters[importAgentSummaryFormatterID] {
		t.Fatalf("ModuleInit did not register text_formatter %q", importAgentSummaryFormatterID)
	}
}

type moduleInitSpy struct {
	registerWorkflow      func(id string)
	registerBodyFn        func(id string)
	registerTextFormatter func(id string)
}

func (s *moduleInitSpy) Register(*spec.CommandSpec) error { return nil }
func (s *moduleInitSpy) QualifyNoun(*spec.NounDef)        {}
func (s *moduleInitSpy) RegisterWorkflow(id string, _ registry.WorkflowFn) {
	if s.registerWorkflow != nil {
		s.registerWorkflow(id)
	}
}
func (s *moduleInitSpy) RegisterTextFormatter(id string, _ cmdctx.TextFormatterFn) {
	if s.registerTextFormatter != nil {
		s.registerTextFormatter(id)
	}
}
func (s *moduleInitSpy) RegisterBodyFn(id string, _ cmdctx.CreateBodyFn) {
	if s.registerBodyFn != nil {
		s.registerBodyFn(id)
	}
}
func (s *moduleInitSpy) RegisterQueryParamsFn(string, cmdctx.QueryParamsFn)             {}
func (s *moduleInitSpy) RegisterFollowFn(string, cmdctx.FollowFn)                       {}
func (s *moduleInitSpy) RegisterFetchFn(string, cmdctx.FetchFn)                         {}
func (s *moduleInitSpy) RegisterListTransformFn(string, cmdctx.ListTransformFn)         {}
func (s *moduleInitSpy) RegisterItemFn(string, cmdctx.ItemFn)                           {}
func (s *moduleInitSpy) RegisterFlagCompletionFn(string, registry.FlagCompletionFn)     {}
func (s *moduleInitSpy) RegisterFlagResolveFn(string, cmdctx.FlagResolveFn)             {}
func (s *moduleInitSpy) RegisterEndpointValidatorFn(string, cmdctx.EndpointValidatorFn) {}
func (s *moduleInitSpy) RegisterFieldType(string, cmdctx.FieldTypeHandler)              {}

func TestDeleteAppProjectMappingBody(t *testing.T) {
	tests := []struct {
		name    string
		setArgs map[string]string
		wantErr string
	}{
		{
			name: "happy path",
			setArgs: map[string]string{
				setArgOrg:     "default",
				setArgProject: "proj1",
			},
		},
		{
			name:    "missing org",
			setArgs: map[string]string{setArgProject: "proj1"},
			wantErr: "--set org=",
		},
		{
			name:    "missing project",
			setArgs: map[string]string{setArgOrg: "default"},
			wantErr: "--set project=",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := deleteAppProjectMappingBody(&cmdctx.Ctx{SetArgs: tc.setArgs})
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want substring %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if got != nil {
				t.Fatalf("body = %#v, want nil", got)
			}
		})
	}
}

func TestUpdateAppProjectMappingBody(t *testing.T) {
	tests := []struct {
		name    string
		idParts []string
		setArgs map[string]string
		wantErr string
		want    map[string]any
	}{
		{
			name:    "toggle true",
			idParts: []string{"agent", "argo-a"},
			setArgs: map[string]string{
				setArgOrg:                  "default",
				setArgProject:              "proj1",
				setArgAutoCreateServiceEnv: setArgTrue,
			},
			want: map[string]any{
				wireKeyAppProjMap: map[string]any{
					"argo-a": map[string]any{
						wireKeyOrgIdentifier:        "default",
						wireKeyProjectIdentifier:    "proj1",
						wireKeyAutoCreateServiceEnv: true,
					},
				},
			},
		},
		{
			name:    "toggle false",
			idParts: []string{"agent", "argo-a"},
			setArgs: map[string]string{
				setArgOrg:                  "default",
				setArgProject:              "proj1",
				setArgAutoCreateServiceEnv: setArgFalse,
			},
			want: map[string]any{
				wireKeyAppProjMap: map[string]any{
					"argo-a": map[string]any{
						wireKeyOrgIdentifier:        "default",
						wireKeyProjectIdentifier:    "proj1",
						wireKeyAutoCreateServiceEnv: false,
					},
				},
			},
		},
		{
			name:    "missing org",
			idParts: []string{"a", "b"},
			setArgs: map[string]string{setArgProject: "p", setArgAutoCreateServiceEnv: setArgTrue},
			wantErr: "--set org=",
		},
		{
			name:    "missing project",
			idParts: []string{"a", "b"},
			setArgs: map[string]string{setArgOrg: "o", setArgAutoCreateServiceEnv: setArgTrue},
			wantErr: "--set project=",
		},
		{
			name:    "missing autocreate",
			idParts: []string{"a", "b"},
			setArgs: map[string]string{setArgOrg: "o", setArgProject: "p"},
			wantErr: "--set autoCreateServiceEnv=",
		},
		{
			name:    "bad autocreate",
			idParts: []string{"a", "b"},
			setArgs: map[string]string{setArgOrg: "o", setArgProject: "p", setArgAutoCreateServiceEnv: "yes"},
			wantErr: "--set autoCreateServiceEnv=",
		},
		{
			name:    "missing id parts",
			idParts: []string{"agent"},
			setArgs: map[string]string{setArgOrg: "o", setArgProject: "p", setArgAutoCreateServiceEnv: setArgTrue},
			wantErr: "expected <agent/argoproject>",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := updateAppProjectMappingBody(&cmdctx.Ctx{IdParts: tc.idParts, SetArgs: tc.setArgs})
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want containing %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			gotJSON, _ := json.Marshal(got)
			wantJSON, _ := json.Marshal(tc.want)
			if string(gotJSON) != string(wantJSON) {
				t.Fatalf("body = %s, want %s", gotJSON, wantJSON)
			}
		})
	}
}

func TestCreateAppProjectMappingBody(t *testing.T) {
	tests := []struct {
		name     string
		setArgs  map[string]string
		fileYAML string
		wantErr  string
		want     map[string]any
	}{
		{
			name: "happy path defaults autoCreate false",
			setArgs: map[string]string{
				setArgArgoProject: "argo-a",
				setArgOrg:         "default",
				setArgProject:     "proj1",
			},
			want: map[string]any{
				wireKeyAppProjMap: map[string]any{
					"argo-a": map[string]any{
						wireKeyOrgIdentifier:        "default",
						wireKeyProjectIdentifier:    "proj1",
						wireKeyAutoCreateServiceEnv: false,
					},
				},
			},
		},
		{
			name: "autoCreate true",
			setArgs: map[string]string{
				setArgArgoProject:          "argo-a",
				setArgOrg:                  "default",
				setArgProject:              "proj1",
				setArgAutoCreateServiceEnv: setArgTrue,
			},
			want: map[string]any{
				wireKeyAppProjMap: map[string]any{
					"argo-a": map[string]any{
						wireKeyOrgIdentifier:        "default",
						wireKeyProjectIdentifier:    "proj1",
						wireKeyAutoCreateServiceEnv: true,
					},
				},
			},
		},
		{
			name:    "missing argoproject",
			setArgs: map[string]string{setArgOrg: "default", setArgProject: "proj1"},
			wantErr: setArgArgoProject,
		},
		{
			name:    "missing org",
			setArgs: map[string]string{setArgArgoProject: "argo-a", setArgProject: "proj1"},
			wantErr: setArgOrg,
		},
		{
			name:    "missing project",
			setArgs: map[string]string{setArgArgoProject: "argo-a", setArgOrg: "default"},
			wantErr: setArgProject,
		},
		{
			name: "file two rows",
			fileYAML: `
mappings:
  - argoproject: a1
    org: default
    project: p1
  - argoproject: a2
    org: default
    project: p2
    autoCreateServiceEnv: true
`,
			want: map[string]any{
				wireKeyAppProjMap: map[string]any{
					"a1": map[string]any{
						wireKeyOrgIdentifier:        "default",
						wireKeyProjectIdentifier:    "p1",
						wireKeyAutoCreateServiceEnv: false,
					},
					"a2": map[string]any{
						wireKeyOrgIdentifier:        "default",
						wireKeyProjectIdentifier:    "p2",
						wireKeyAutoCreateServiceEnv: true,
					},
				},
			},
		},
		{
			name:     "file empty mappings",
			fileYAML: "mappings: []\n",
			wantErr:  yamlKeyMappings,
		},
		{
			name: "file duplicate argoproject",
			fileYAML: `
mappings:
  - argoproject: a1
    org: default
    project: p1
  - argoproject: a1
    org: default
    project: p2
`,
			wantErr: "duplicate argo project",
		},
		{
			name: "file missing org",
			fileYAML: `
mappings:
  - argoproject: a1
    project: p1
`,
			wantErr: setArgOrg,
		},
		{
			name:    "file and set mutually exclusive",
			setArgs: map[string]string{setArgArgoProject: "x"},
			fileYAML: `
mappings:
  - argoproject: a1
    org: default
    project: p1
`,
			wantErr: "not both",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := &cmdctx.Ctx{SetArgs: tc.setArgs, FlagValues: map[string]any{}}
			if tc.fileYAML != "" {
				path := filepath.Join(t.TempDir(), "mappings.yaml")
				if err := os.WriteFile(path, []byte(tc.fileYAML), 0o600); err != nil {
					t.Fatal(err)
				}
				ctx.FlagValues[flagFile] = path
			}
			got, err := createAppProjectMappingBody(ctx)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want containing %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			gotJSON, _ := json.Marshal(got)
			wantJSON, _ := json.Marshal(tc.want)
			if string(gotJSON) != string(wantJSON) {
				t.Fatalf("body = %s, want %s", gotJSON, wantJSON)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// import body + summary (moved from gitops_import_test.go)
// ---------------------------------------------------------------------------

func TestGitopsAgentImportBody(t *testing.T) {
	tests := []struct {
		name     string
		flags    map[string]any
		fileYAML string
		wantErr  string
		want     map[string]any
	}{
		{
			name:  "from flag",
			flags: map[string]any{flagArgoProjectNames: []string{"test", " default "}},
			want:  map[string]any{wireKeyProjectNames: []string{"test", "default"}},
		},
		{
			name: "from file",
			fileYAML: `
argoProjectNames:
  - test
  - other
`,
			want: map[string]any{wireKeyProjectNames: []string{"test", "other"}},
		},
		{
			name:    "neither",
			wantErr: "provide --" + flagArgoProjectNames,
		},
		{
			name:     "both",
			flags:    map[string]any{flagArgoProjectNames: []string{"test"}},
			fileYAML: "argoProjectNames: [x]\n",
			wantErr:  "not both",
		},
		{
			name:    "flag whitespace only",
			flags:   map[string]any{flagArgoProjectNames: []string{"  ", ""}},
			wantErr: "at least one Argo AppProject name",
		},
		{
			name:     "file empty list",
			fileYAML: "argoProjectNames: []\n",
			wantErr:  "at least one Argo AppProject name",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			flags := map[string]any{}
			for k, v := range tc.flags {
				flags[k] = v
			}
			if tc.fileYAML != "" {
				path := filepath.Join(t.TempDir(), "import.yaml")
				if err := os.WriteFile(path, []byte(tc.fileYAML), 0o600); err != nil {
					t.Fatal(err)
				}
				flags[flagFile] = path
			}
			got, err := gitopsAgentImportBody(&cmdctx.Ctx{FlagValues: flags})
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want containing %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			gotJSON, _ := json.Marshal(got)
			wantJSON, _ := json.Marshal(tc.want)
			if string(gotJSON) != string(wantJSON) {
				t.Fatalf("body = %s, want %s", gotJSON, wantJSON)
			}
		})
	}
}

func TestFormatGitopsAgentImportSummary(t *testing.T) {
	// Numbers as float64 — same as client.DoRequest json.Unmarshal into any.
	payload := map[string]any{
		"applicationCount":              float64(31),
		"applicationSetCount":           float64(1),
		"clusterCount":                  float64(3),
		"repositoryCount":               float64(4),
		"repositoryCertificateCount":    float64(14),
		"gnuPGPublicKeyCount":           float64(0),
		"repoCredsCount":                float64(0),
		"applicationPerProjectCount":    map[string]any{"default": float64(31)},
		"applicationSetPerProjectCount": map[string]any{"default": float64(1)},
		"clusterPerProjectCount": map[string]any{
			"":        float64(1),
			"default": float64(2),
		},
		"repositoryPerProjectCount": map[string]any{
			"":        float64(1),
			"default": float64(3),
		},
		"importRequestId": "6ab6769870f2569847bdcf99",
		"reconcileAppResponse": map[string]any{
			"autoCreateCounts": map[string]any{
				"serviceCount":     float64(5),
				"environmentCount": float64(4),
				"clusterLinkCount": float64(5),
			},
		},
	}
	env := map[string]any{
		"ctx": map[string]any{"id": "donotdeletehimanqa"},
	}
	var buf bytes.Buffer
	if err := formatGitopsAgentImportSummary(&buf, extractutil.MakeDataAccessor(env, payload)); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{
		"Import complete for agent donotdeletehimanqa",
		"importRequestId: 6ab6769870f2569847bdcf99",
		"applications:",
		"31  (default=31)",
		"application sets:",
		"1  (default=1)",
		"clusters:",
		"(none)=1",
		"default=2",
		"repositories:",
		"Auto-create planned (async, not final)",
		"services: 5  environments: 4  cluster links: 5",
		"Import History (Agent Details)",
		"harness list gitops_autocreate_log donotdeletehimanqa --import-request-id 6ab6769870f2569847bdcf99 --watch",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q\n---\n%s", want, out)
		}
	}
	if strings.Contains(out, "autoCreateResults") || strings.Contains(out, "failed:") || strings.Contains(out, "autocreate-logs?") {
		t.Fatalf("must not print audit-only results or raw GET hint:\n%s", out)
	}
}

func TestFormatGitopsAgentImportSummary_AppSetKeyFallback(t *testing.T) {
	payload := map[string]any{
		"applicationSetCount": float64(2),
		"appSetPerProjectCount": map[string]any{
			"legacy": float64(2),
		},
	}
	env := map[string]any{"ctx": map[string]any{"id": "a"}}
	var buf bytes.Buffer
	if err := formatGitopsAgentImportSummary(&buf, extractutil.MakeDataAccessor(env, payload)); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "legacy=2") {
		t.Fatalf("expected gogoproto jsontag fallback, got:\n%s", buf.String())
	}
}

func TestAsIntMapAndPerProjectParen(t *testing.T) {
	root := map[string]any{
		"m": map[string]any{"": float64(1), "b": float64(2), "a": float64(3)},
	}
	got := asIntMap(root, "m")
	if got[""] != 1 || got["a"] != 3 || got["b"] != 2 {
		t.Fatalf("asIntMap = %#v", got)
	}
	paren := formatPerProjectParen(got)
	if paren != "(none)=1, a=3, b=2" {
		t.Fatalf("paren = %q", paren)
	}
	if asIntMap(nil, "m") != nil {
		t.Fatal("nil root")
	}
	if asIntMap(root, "missing") != nil {
		t.Fatal("missing key")
	}
}

// ---------------------------------------------------------------------------
// import → autocreate watch plan / prompt
// ---------------------------------------------------------------------------

func TestImportAutocreatePlan(t *testing.T) {
	tests := []struct {
		name         string
		result       any
		wantID       string
		wantExpected int64
	}{
		{name: "nil", result: nil},
		{name: "wrong type", result: "x"},
		{
			name:   "id only no counts",
			result: map[string]any{"importRequestId": "abc"},
			wantID: "abc",
		},
		{
			name: "zero counts",
			result: map[string]any{
				"importRequestId": "abc",
				"reconcileAppResponse": map[string]any{
					"autoCreateCounts": map[string]any{
						"serviceCount": float64(0), "environmentCount": float64(0), "clusterLinkCount": float64(0),
					},
				},
			},
			wantID: "abc",
		},
		{
			name: "float64 services planned",
			result: map[string]any{
				"importRequestId": "req1",
				"reconcileAppResponse": map[string]any{
					"autoCreateCounts": map[string]any{"serviceCount": float64(2)},
				},
			},
			wantID: "req1", wantExpected: 2,
		},
		{
			name: "env planned",
			result: map[string]any{
				"importRequestId": "req2",
				"reconcileAppResponse": map[string]any{
					"autoCreateCounts": map[string]any{"environmentCount": float64(1)},
				},
			},
			wantID: "req2", wantExpected: 1,
		},
		{
			name: "links planned json.Number",
			result: map[string]any{
				"importRequestId": "req3",
				"reconcileAppResponse": map[string]any{
					"autoCreateCounts": map[string]any{"clusterLinkCount": json.Number("3")},
				},
			},
			wantID: "req3", wantExpected: 3,
		},
		{
			name: "sum of three counts",
			result: map[string]any{
				"importRequestId": "req4",
				"reconcileAppResponse": map[string]any{
					"autoCreateCounts": map[string]any{
						"serviceCount": float64(2), "environmentCount": float64(3), "clusterLinkCount": float64(4),
					},
				},
			},
			wantID: "req4", wantExpected: 9,
		},
		{
			name: "planned without id still returns expected",
			result: map[string]any{
				"reconcileAppResponse": map[string]any{
					"autoCreateCounts": map[string]any{"serviceCount": float64(1)},
				},
			},
			wantExpected: 1,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			id, expected := importAutocreatePlan(tc.result)
			if id != tc.wantID || expected != tc.wantExpected {
				t.Fatalf("got (%q, %d), want (%q, %d)", id, expected, tc.wantID, tc.wantExpected)
			}
		})
	}
}

func TestAutocreateLogReachedPlan(t *testing.T) {
	if autocreateLogReachedPlan(0, 0) {
		t.Fatal("expected=0 must not complete (list --watch)")
	}
	if autocreateLogReachedPlan(5, 0) {
		t.Fatal("expected=0 must not complete even with total>0")
	}
	if autocreateLogReachedPlan(4, 5) {
		t.Fatal("total < expected must not complete")
	}
	if !autocreateLogReachedPlan(5, 5) {
		t.Fatal("total == expected must complete")
	}
	if !autocreateLogReachedPlan(6, 5) {
		t.Fatal("total > expected must complete")
	}
}

func TestAutocreateLogWatchDoneErr(t *testing.T) {
	if autocreateLogWatchDoneErr(nil) != nil {
		t.Fatal("nil")
	}
	if autocreateLogWatchDoneErr(context.DeadlineExceeded) != nil {
		t.Fatal("deadline must be soft stop")
	}
	if autocreateLogWatchDoneErr(context.Canceled) == nil {
		t.Fatal("cancel must still fail the command")
	}
	if autocreateLogWatchDoneErr(errors.New("boom")) == nil {
		t.Fatal("other errors must propagate")
	}
}

func TestShouldPromptAutocreateWatch(t *testing.T) {
	for _, format := range []string{"json", "yaml", "csv"} {
		ctx := &cmdctx.Ctx{FormatFlags: cmdctx.FormatFlags{Format: format}}
		if shouldPromptAutocreateWatch(ctx) {
			t.Fatalf("format %q must not prompt", format)
		}
	}
	for _, format := range []string{"", "text", "TEXT"} {
		ctx := &cmdctx.Ctx{FormatFlags: cmdctx.FormatFlags{Format: format}}
		if got, want := shouldPromptAutocreateWatch(ctx), console.IsBothTTY(); got != want {
			t.Fatalf("format %q: got %v, want IsBothTTY()=%v", format, got, want)
		}
	}
}

// ---------------------------------------------------------------------------
// autocreate log list / watch helpers
// ---------------------------------------------------------------------------

func TestAutocreateLogPagingFlags(t *testing.T) {
	tests := []struct {
		name    string
		fv      map[string]any
		watch   bool
		want    cmdctx.PagingFlags
		wantErr string
	}{
		{
			name: "defaults",
			fv:   map[string]any{},
			want: cmdctx.PagingFlags{},
		},
		{
			name: "offset limit",
			fv:   map[string]any{flagOffset: "10", flagLimit: "5"},
			want: cmdctx.PagingFlags{Offset: 10, Limit: 5},
		},
		{
			name: "all",
			fv:   map[string]any{flagAll: true},
			want: cmdctx.PagingFlags{All: true},
		},
		{
			name: "count",
			fv:   map[string]any{flagCount: true},
			want: cmdctx.PagingFlags{Count: true},
		},
		{
			name:    "all with offset",
			fv:      map[string]any{flagAll: true, flagOffset: "1"},
			wantErr: "--all is incompatible",
		},
		{
			name:    "count with all",
			fv:      map[string]any{flagCount: true, flagAll: true},
			wantErr: "--count is incompatible",
		},
		{
			name:  "watch forces all",
			fv:    map[string]any{flagAll: true},
			watch: true,
			want:  cmdctx.PagingFlags{All: true},
		},
		{
			name:    "watch with limit",
			fv:      map[string]any{flagLimit: "10"},
			watch:   true,
			wantErr: "--watch is incompatible",
		},
		{
			name:    "watch with count",
			fv:      map[string]any{flagCount: true},
			watch:   true,
			wantErr: "--watch is incompatible",
		},
		{
			name:    "bad offset",
			fv:      map[string]any{flagOffset: "nope"},
			wantErr: "--offset must be an integer",
		},
		{
			name:    "negative limit",
			fv:      map[string]any{flagLimit: "-1"},
			wantErr: "--limit must be non-negative",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := autocreateLogPagingFlags(tc.fv, tc.watch)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want containing %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if got.Offset != tc.want.Offset || got.Limit != tc.want.Limit || got.All != tc.want.All || got.Count != tc.want.Count {
				t.Fatalf("got offset=%d limit=%d all=%v count=%v, want offset=%d limit=%d all=%v count=%v",
					got.Offset, got.Limit, got.All, got.Count,
					tc.want.Offset, tc.want.Limit, tc.want.All, tc.want.Count)
			}
		})
	}
}

func TestRootTimeoutSecs(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want float64
	}{
		{name: "absent", args: []string{"harness", "list"}, want: 0},
		{name: "space form", args: []string{"harness", "--timeout", "30"}, want: 30},
		{name: "equals form", args: []string{"harness", "--timeout=1.5"}, want: 1.5},
		{name: "invalid", args: []string{"harness", "--timeout", "x"}, want: 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := rootTimeoutSecs(tc.args); got != tc.want {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestAutocreateLogDelta(t *testing.T) {
	row := func(rt, rr, status string, created float64) map[string]any {
		return map[string]any{
			"resourceType": rt,
			"resourceRef":  rr,
			"status":       status,
			"createdAt":    created,
		}
	}
	seen := map[string]string{}

	d1, failed := autocreateLogDelta([]any{
		row("SERVICE", "svc-a", "SUCCESS", 1),
		row("SERVICE", "svc-b", "FAILED", 2),
	}, seen)
	if len(d1) != 2 || failed != 1 {
		t.Fatalf("tick1 delta=%d failed=%d", len(d1), failed)
	}

	d2, failed := autocreateLogDelta([]any{
		row("SERVICE", "svc-a", "SUCCESS", 1),
		row("SERVICE", "svc-b", "FAILED", 2),
		row("ENVIRONMENT", "env-a", "SUCCESS", 3),
	}, seen)
	if len(d2) != 1 || failed != 1 {
		t.Fatalf("tick2 delta=%d failed=%d", len(d2), failed)
	}
	if autocreateLogStringField(d2[0].(map[string]any), "resourceRef") != "env-a" {
		t.Fatalf("tick2 expected env-a, got %#v", d2[0])
	}

	d3, failed := autocreateLogDelta([]any{
		row("SERVICE", "svc-b", "SUCCESS", 2),
	}, seen)
	if len(d3) != 1 || failed != 0 {
		t.Fatalf("status flip delta=%d failed=%d", len(d3), failed)
	}

	d4, _ := autocreateLogDelta([]any{
		row("SERVICE", "svc-b", "SUCCESS", 2),
	}, seen)
	if len(d4) != 0 {
		t.Fatalf("unchanged should be empty, got %d", len(d4))
	}
}

func TestParseNonNegIntFlag(t *testing.T) {
	n, err := parseNonNegIntFlag(map[string]any{flagOffset: ""}, flagOffset)
	if err != nil || n != 0 {
		t.Fatalf("empty: n=%d err=%v", n, err)
	}
	n, err = parseNonNegIntFlag(map[string]any{flagOffset: "42"}, flagOffset)
	if err != nil || n != 42 {
		t.Fatalf("42: n=%d err=%v", n, err)
	}
}
