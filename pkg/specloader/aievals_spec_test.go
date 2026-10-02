// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package specloader

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/harness/cli/v3/pkg/auth"
	"github.com/harness/cli/v3/pkg/cmdctx"
	"github.com/harness/cli/v3/pkg/registry"
)

type aiEvalsRequest struct {
	method string
	path   string
	body   string
	header http.Header
}

func aiEvalsCaptureServer(t *testing.T) (*httptest.Server, *aiEvalsRequest) {
	t.Helper()
	request := &aiEvalsRequest{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		request.method = r.Method
		request.path = r.URL.Path
		request.header = r.Header.Clone()
		body, _ := io.ReadAll(r.Body)
		request.body = string(body)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{}`)
	}))
	t.Cleanup(server.Close)
	return server, request
}

func aiEvalsTestCtx(t *testing.T, apiURL string, reg *registry.Registry) *cmdctx.Ctx {
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
		Resolver:    reg,
	}
}

func aiEvalsBodyFile(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "body.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write body: %v", err)
	}
	return path
}

func TestAIEvalsSpec_BulkAndReplacementBodies(t *testing.T) {
	reg := registry.New()
	if _, err := LoadSpec(reg, "aievals.spec.yaml", true); err != nil {
		t.Fatalf("LoadSpec: %v", err)
	}

	tests := []struct {
		name    string
		command string
		noun    string
		id      string
		method  string
		path    string
		body    string
	}{
		{
			name:    "dataset bulk upsert preserves items envelope",
			command: "eval_dataset:bulk_upsert",
			noun:    "eval_dataset",
			id:      "dataset-id",
			method:  http.MethodPatch,
			path:    "/gateway/ai-evals/api/v1/orgs/org/projects/proj/dataset/dataset-id/items/bulk",
			body:    `{"items":[{"id":"case-1","input":{"prompt":"hello"}}]}`,
		},
		{
			name:    "metric replacement preserves raw entry array",
			command: "eval_metric_set:replace_metrics",
			noun:    "eval_metric_set",
			id:      "set-id",
			method:  http.MethodPut,
			path:    "/gateway/ai-evals/api/v1/orgs/org/projects/proj/metric-sets/set-id/metrics",
			body:    `[{"metric_id":"metric-id","threshold":0.8}]`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			command := reg.GetSpec("execute", test.command)
			if command == nil || command.Endpoint == nil {
				t.Fatalf("execute %s: command not found", test.command)
			}
			server, request := aiEvalsCaptureServer(t)
			ctx := aiEvalsTestCtx(t, server.URL, reg)
			ctx.Noun = test.noun
			ctx.Id = test.id
			ctx.FlagValues = map[string]any{"file": aiEvalsBodyFile(t, test.body)}

			if _, err := registry.RunEndpoint(ctx, command.Endpoint); err != nil {
				t.Fatalf("RunEndpoint: %v", err)
			}
			if request.method != test.method || request.path != test.path {
				t.Fatalf("request = %s %s, want %s %s", request.method, request.path, test.method, test.path)
			}
			if request.body != test.body {
				t.Fatalf("body = %s, want %s", request.body, test.body)
			}
			if got := request.header.Get("Harness-Account"); got != "acct" {
				t.Fatalf("Harness-Account = %q, want acct", got)
			}
		})
	}
}

func TestAIEvalsSpec_OnlineRuleAndSuiteRunBodies(t *testing.T) {
	reg := registry.New()
	if _, err := LoadSpec(reg, "aievals.spec.yaml", true); err != nil {
		t.Fatalf("LoadSpec: %v", err)
	}

	tests := []struct {
		name    string
		verb    string
		command string
		noun    string
		id      string
		method  string
		path    string
		body    string
	}{
		{
			name:    "online rule create preserves material fields",
			verb:    "create",
			command: "online_eval_rule",
			noun:    "online_eval_rule",
			method:  http.MethodPost,
			path:    "/gateway/ai-evals/api/v1/orgs/org/projects/proj/online-eval-configs",
			body:    `{"name":"quality","scope":"trace","metric_set_id":"metric-set","selector_filters":[{"field":"service_name","op":"eq","value":"agent"}]}`,
		},
		{
			name:    "online rule patch preserves explicit null",
			verb:    "update",
			command: "online_eval_rule",
			noun:    "online_eval_rule",
			id:      "config-id",
			method:  http.MethodPatch,
			path:    "/gateway/ai-evals/api/v1/orgs/org/projects/proj/online-eval-configs/config-id",
			body:    `{"cost_limit_usd":null}`,
		},
		{
			name:    "suite run accepts structured overrides",
			verb:    "execute",
			command: "eval_suite:run",
			noun:    "eval_suite",
			id:      "suite-id",
			method:  http.MethodPost,
			path:    "/gateway/ai-evals/api/v1/orgs/org/projects/proj/suites/suite-id/run",
			body:    `{"branch":"feature/evals"}`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			command := reg.GetSpec(test.verb, test.command)
			if command == nil || command.Endpoint == nil {
				t.Fatalf("%s %s: command not found", test.verb, test.command)
			}
			server, request := aiEvalsCaptureServer(t)
			ctx := aiEvalsTestCtx(t, server.URL, reg)
			ctx.Noun = test.noun
			ctx.Id = test.id
			ctx.FlagValues = map[string]any{"file": aiEvalsBodyFile(t, test.body)}

			if _, err := registry.RunEndpoint(ctx, command.Endpoint); err != nil {
				t.Fatalf("RunEndpoint: %v", err)
			}
			if request.method != test.method || request.path != test.path {
				t.Fatalf("request = %s %s, want %s %s", request.method, request.path, test.method, test.path)
			}
			if request.body != test.body {
				t.Fatalf("body = %s, want %s", request.body, test.body)
			}
		})
	}
}

func TestAIEvalsSpec_StrategyRequestsPreserveScopedHeaders(t *testing.T) {
	reg := registry.New()
	if _, err := LoadSpec(reg, "aievals.spec.yaml", true); err != nil {
		t.Fatalf("LoadSpec: %v", err)
	}

	var requests []aiEvalsRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		requests = append(requests, aiEvalsRequest{
			method: r.Method,
			path:   r.URL.Path,
			body:   string(body),
			header: r.Header.Clone(),
		})
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet {
			fmt.Fprint(w, `{"name":"quality","enabled":true,"active_version":1,"scope":"trace","metric_set_id":"metric-set","selector_filters":[],"sampling_percentage":100}`)
			return
		}
		fmt.Fprint(w, `{}`)
	}))
	t.Cleanup(server.Close)

	create := reg.GetSpec("create", "online_eval_rule")
	if create == nil || create.Endpoint == nil {
		t.Fatal("create online_eval_rule: command not found")
	}
	createCtx := aiEvalsTestCtx(t, server.URL, reg)
	createCtx.Noun = "online_eval_rule"
	createCtx.SetArgs = map[string]string{"name": "quality", "scope": "trace", "metric_set_id": "metric-set"}
	if _, err := registry.RunEndpoint(createCtx, create.Endpoint); err != nil {
		t.Fatalf("create RunEndpoint: %v", err)
	}

	update := reg.GetSpec("update", "online_eval_rule")
	if update == nil || update.Endpoint == nil {
		t.Fatal("update online_eval_rule: command not found")
	}
	updateCtx := aiEvalsTestCtx(t, server.URL, reg)
	updateCtx.Noun = "online_eval_rule"
	updateCtx.Id = "config-id"
	updateCtx.SetArgs = map[string]string{"name": "quality-v2"}
	if _, err := registry.RunEndpoint(updateCtx, update.Endpoint); err != nil {
		t.Fatalf("update RunEndpoint: %v", err)
	}

	if len(requests) != 3 {
		t.Fatalf("request count = %d, want create plus GET/PATCH update", len(requests))
	}
	for _, request := range requests {
		if got := request.header.Get("Harness-Account"); got != "acct" {
			t.Fatalf("%s %s Harness-Account = %q, want acct", request.method, request.path, got)
		}
	}
}

func TestAIEvalsSpec_OnlineRuleDiscoveryAndOfflineRunRouting(t *testing.T) {
	reg := registry.New()
	if _, err := LoadSpec(reg, "aievals.spec.yaml", true); err != nil {
		t.Fatalf("LoadSpec: %v", err)
	}

	ruleList := reg.GetSpec("list", "online_eval_rule")
	if ruleList == nil || ruleList.Endpoint == nil {
		t.Fatal("list online_eval_rule: command not found")
	}
	if ruleList.Endpoint.GetIdExpr != "it.config_id" {
		t.Fatalf("online rule list ID expression = %q, want it.config_id", ruleList.Endpoint.GetIdExpr)
	}

	offlineRuns := reg.GetSpec("list", "eval_run")
	if offlineRuns == nil || offlineRuns.Endpoint == nil {
		t.Fatal("list eval_run: command not found")
	}
	wantPath := "/gateway/ai-evals/api/v1/orgs/{{auth.org}}/projects/{{auth.project}}/evals/{{ctx.parentId}}/runs"
	if offlineRuns.Endpoint.Path != wantPath {
		t.Fatalf("offline run path = %q, want %q", offlineRuns.Endpoint.Path, wantPath)
	}

	datasetItems := reg.GetSpec("list", "eval_dataset_item")
	if datasetItems == nil || datasetItems.Endpoint == nil {
		t.Fatal("list eval_dataset_item: command not found")
	}
	if datasetItems.Endpoint.GetIdExpr != "ctx.parentId + \"/\" + it.uuid" {
		t.Fatalf("dataset item list ID expression = %q, want UUID-based compound ID", datasetItems.Endpoint.GetIdExpr)
	}

	metricEntryUpdate := reg.GetSpec("update", "eval_metric_set_entry")
	if metricEntryUpdate == nil || metricEntryUpdate.Endpoint == nil {
		t.Fatal("update eval_metric_set_entry: command not found")
	}
	wantEntryPath := "/gateway/ai-evals/api/v1/orgs/{{auth.org}}/projects/{{auth.project}}/metric-sets/{{ctx.idParts[0]}}/entries/{{ctx.idParts[1]}}"
	if metricEntryUpdate.Endpoint.Path != wantEntryPath {
		t.Fatalf("metric entry update path = %q, want %q", metricEntryUpdate.Endpoint.Path, wantEntryPath)
	}
}
