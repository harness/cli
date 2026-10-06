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
	"strings"
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
			command: "eval_dataset_item:bulk_upsert",
			noun:    "eval_dataset_item",
			id:      "dataset-id",
			method:  http.MethodPatch,
			path:    "/gateway/ai-evals/api/v1/orgs/org/projects/proj/dataset/dataset-id/items/bulk",
			body:    `{"items":[{"id":"case-1","input":{"prompt":"hello"}}]}`,
		},
		{
			name:    "dataset bulk delete sends item IDs",
			command: "eval_dataset_item:bulk_delete",
			noun:    "eval_dataset_item",
			id:      "dataset-id",
			method:  http.MethodPost,
			path:    "/gateway/ai-evals/api/v1/orgs/org/projects/proj/dataset/dataset-id/items/bulk-delete",
			body:    `{"item_ids":["item-id"]}`,
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

func TestAIEvalsSpec_ObservabilityEvalBodies(t *testing.T) {
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
			name:    "observability eval create preserves material fields",
			verb:    "create",
			command: "observability_eval",
			noun:    "observability_eval",
			method:  http.MethodPost,
			path:    "/gateway/ai-evals/api/v1/orgs/org/projects/proj/online-eval-configs",
			body:    `{"name":"quality","scope":"trace","metric_set_id":"metric-set","selector_filters":[{"field":"service_name","op":"eq","value":"agent"}]}`,
		},
		{
			name:    "observability eval patch preserves explicit null",
			verb:    "update",
			command: "observability_eval",
			noun:    "observability_eval",
			id:      "config-id",
			method:  http.MethodPatch,
			path:    "/gateway/ai-evals/api/v1/orgs/org/projects/proj/online-eval-configs/config-id",
			body:    `{"target_id":null}`,
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

func TestAIEvalsSpec_MetricFileBodies(t *testing.T) {
	reg := registry.New()
	if _, err := LoadSpec(reg, "aievals.spec.yaml", true); err != nil {
		t.Fatalf("LoadSpec: %v", err)
	}

	tests := []struct {
		name   string
		verb   string
		id     string
		method string
		path   string
		body   string
	}{
		{
			name:   "create accepts nested config",
			verb:   "create",
			method: http.MethodPost,
			path:   "/gateway/ai-evals/api/v1/orgs/org/projects/proj/metrics",
			body:   `{"name":"judge","type":"llm","dimension":"correctness","config":{"prompt":"score the answer"}}`,
		},
		{
			name:   "update sends patch body",
			verb:   "update",
			id:     "metric-id",
			method: http.MethodPatch,
			path:   "/gateway/ai-evals/api/v1/orgs/org/projects/proj/metrics/metric-id",
			body:   `{"description":"updated"}`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			command := reg.GetSpec(test.verb, "eval_metric")
			if command == nil || command.Endpoint == nil {
				t.Fatalf("%s eval_metric: command not found", test.verb)
			}
			server, request := aiEvalsCaptureServer(t)
			ctx := aiEvalsTestCtx(t, server.URL, reg)
			ctx.Noun = "eval_metric"
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

func TestAIEvalsSpec_MetricSetEntryMutationRoutes(t *testing.T) {
	reg := registry.New()
	if _, err := LoadSpec(reg, "aievals.spec.yaml", true); err != nil {
		t.Fatalf("LoadSpec: %v", err)
	}

	tests := []struct {
		name   string
		verb   string
		method string
		body   string
	}{
		{"update uses entry ID", "update", http.MethodPatch, `{"weight":0.5}`},
		{"delete uses entry ID", "delete", http.MethodDelete, ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			command := reg.GetSpec(test.verb, "eval_metric_set_entry")
			if command == nil || command.Endpoint == nil {
				t.Fatalf("%s eval_metric_set_entry: command not found", test.verb)
			}
			server, request := aiEvalsCaptureServer(t)
			ctx := aiEvalsTestCtx(t, server.URL, reg)
			ctx.Noun = "eval_metric_set_entry"
			ctx.Id = "set-id/entry-id"
			if test.body != "" {
				ctx.FlagValues = map[string]any{"file": aiEvalsBodyFile(t, test.body)}
			}

			if _, err := registry.RunEndpoint(ctx, command.Endpoint); err != nil {
				t.Fatalf("RunEndpoint: %v", err)
			}
			wantPath := "/gateway/ai-evals/api/v1/orgs/org/projects/proj/metric-sets/set-id/entries/entry-id"
			if request.method != test.method || request.path != wantPath {
				t.Fatalf("request = %s %s, want %s %s", request.method, request.path, test.method, wantPath)
			}
			if request.body != test.body {
				t.Fatalf("body = %s, want %s", request.body, test.body)
			}
		})
	}
}

func TestAIEvalsSpec_HelpExamplesUseValidSetSyntax(t *testing.T) {
	reg := registry.New()
	if _, err := LoadSpec(reg, "aievals.spec.yaml", true); err != nil {
		t.Fatalf("LoadSpec: %v", err)
	}

	tests := []struct {
		verb    string
		command string
		want    string
	}{
		{"create", "eval_dataset", "--set name=... --set identifier=..."},
		{"create", "evaluation", "--set name=... --set dataset_id=... --set target_id=... --set metric_set_id=..."},
		{"create", "eval_metric", "--set name=... --set type=heuristic --set dimension=correctness"},
		{"create", "eval_metric_set_entry", "--set metric_id=... --set threshold=..."},
		{"create", "eval_target", "target.json with -f"},
		{"update", "evaluation", "--set dataset_id=... --set target_id=... --set metric_set_id=..."},
		{"execute", "evaluation:run", "--set sampling_strategy=... --set branch=... --set notes=..."},
		{"execute", "eval_suite:run", "<suite_id>"},
	}

	for _, test := range tests {
		command := reg.GetSpec(test.verb, test.command)
		if command == nil {
			t.Fatalf("%s %s: command not found", test.verb, test.command)
		}
		if !strings.Contains(command.Short, test.want) {
			t.Fatalf("%s %s help = %q, want %q", test.verb, test.command, command.Short, test.want)
		}
	}
}

func TestAIEvalsSpec_RequiredBodiesFailBeforeRequest(t *testing.T) {
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
		file    string
		set     map[string]string
		wantErr string
	}{
		{
			name:    "observability eval create requires scope and metric set",
			verb:    "create",
			command: "observability_eval",
			noun:    "observability_eval",
			file:    `{"name":"quality"}`,
			wantErr: "scope, metric_set_id",
		},
		{
			name:    "managed target requires type and config",
			verb:    "create",
			command: "eval_target",
			noun:    "eval_target",
			set:     map[string]string{"name": "target"},
			wantErr: "type, config",
		},
		{
			name:    "bulk delete requires item IDs",
			verb:    "execute",
			command: "eval_dataset_item:bulk_delete",
			noun:    "eval_dataset_item",
			id:      "dataset-id",
			file:    `{"item_ids":[]}`,
			wantErr: "must not be empty: item_ids",
		},
		{
			name:    "observability eval update requires a change",
			verb:    "update",
			command: "observability_eval",
			noun:    "observability_eval",
			id:      "config-id",
			wantErr: "at least 1 field",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			command := reg.GetSpec(test.verb, test.command)
			if command == nil || command.Endpoint == nil {
				t.Fatalf("%s %s: command not found", test.verb, test.command)
			}
			requests := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				requests++
				fmt.Fprint(w, `{}`)
			}))
			t.Cleanup(server.Close)

			ctx := aiEvalsTestCtx(t, server.URL, reg)
			ctx.Noun = test.noun
			ctx.Id = test.id
			ctx.SetArgs = test.set
			if test.file != "" {
				ctx.FlagValues = map[string]any{"file": aiEvalsBodyFile(t, test.file)}
			}
			_, err := registry.RunEndpoint(ctx, command.Endpoint)
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("RunEndpoint error = %v, want %q", err, test.wantErr)
			}
			if requests != 0 {
				t.Fatalf("request count = %d, want 0", requests)
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

	create := reg.GetSpec("create", "observability_eval")
	if create == nil || create.Endpoint == nil {
		t.Fatal("create observability_eval: command not found")
	}
	createCtx := aiEvalsTestCtx(t, server.URL, reg)
	createCtx.Noun = "observability_eval"
	createCtx.SetArgs = map[string]string{"name": "quality", "scope": "trace", "metric_set_id": "metric-set"}
	if _, err := registry.RunEndpoint(createCtx, create.Endpoint); err != nil {
		t.Fatalf("create RunEndpoint: %v", err)
	}

	update := reg.GetSpec("update", "observability_eval")
	if update == nil || update.Endpoint == nil {
		t.Fatal("update observability_eval: command not found")
	}
	updateCtx := aiEvalsTestCtx(t, server.URL, reg)
	updateCtx.Noun = "observability_eval"
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

func TestAIEvalsSpec_ObservabilityEvalDiscoveryAndOfflineRunRouting(t *testing.T) {
	reg := registry.New()
	if _, err := LoadSpec(reg, "aievals.spec.yaml", true); err != nil {
		t.Fatalf("LoadSpec: %v", err)
	}

	for _, verb := range []string{"list", "get", "create", "update", "delete"} {
		evalCommand := reg.GetSpec(verb, "observability_eval")
		if evalCommand == nil || evalCommand.Endpoint == nil {
			t.Fatalf("%s observability_eval: command not found", verb)
		}
	}
	for _, legacyNoun := range []string{"online_eval_rule", "online_eval_config"} {
		if legacyRuleList := reg.GetSpec("list", legacyNoun); legacyRuleList != nil {
			t.Fatalf("legacy %s command must not be exposed", legacyNoun)
		}
	}
	evalList := reg.GetSpec("list", "observability_eval")
	if evalList.Endpoint.GetIdExpr != "it.config_id" {
		t.Fatalf("observability eval list ID expression = %q, want it.config_id", evalList.Endpoint.GetIdExpr)
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
	if datasetItemNoun := reg.GetNoun("eval_dataset_item"); datasetItemNoun == nil {
		t.Fatal("eval_dataset_item noun must be registered")
	}

	metricEntryUpdate := reg.GetSpec("update", "eval_metric_set_entry")
	if metricEntryUpdate == nil || metricEntryUpdate.Endpoint == nil {
		t.Fatal("update eval_metric_set_entry: command not found")
	}
	wantEntryPath := "/gateway/ai-evals/api/v1/orgs/{{auth.org}}/projects/{{auth.project}}/metric-sets/{{ctx.idParts[0]}}/entries/{{ctx.idParts[1]}}"
	if metricEntryUpdate.Endpoint.Path != wantEntryPath {
		t.Fatalf("metric entry update path = %q, want %q", metricEntryUpdate.Endpoint.Path, wantEntryPath)
	}
	if metricEntryUpdate.IdLabel != "<set-id/entry-id>" {
		t.Fatalf("metric entry update ID label = %q, want <set-id/entry-id>", metricEntryUpdate.IdLabel)
	}
	metricEntryNoun := reg.GetNoun("eval_metric_set_entry")
	if metricEntryNoun == nil || len(metricEntryNoun.Fields) == 0 || metricEntryNoun.Fields[0].ID != "id" {
		t.Fatal("metric-set entries must expose their entry ID")
	}

	for _, action := range []string{"bulk_upsert", "bulk_delete"} {
		command := reg.GetSpec("execute", "eval_dataset_item:"+action)
		if command == nil || command.Endpoint == nil {
			t.Fatalf("execute eval_dataset_item:%s: command not found", action)
		}
		if legacy := reg.GetSpec("execute", "eval_dataset:"+action); legacy != nil {
			t.Fatalf("legacy eval_dataset:%s command must not be exposed", action)
		}
	}

	metricCreate := reg.GetSpec("create", "eval_metric")
	if metricCreate == nil || metricCreate.Endpoint == nil || metricCreate.Endpoint.FileBody != "optional" {
		t.Fatal("create eval_metric must accept an optional file body")
	}
	metricUpdate := reg.GetSpec("update", "eval_metric")
	if metricUpdate == nil || metricUpdate.Endpoint == nil || metricUpdate.Endpoint.FileBody != "required" {
		t.Fatal("update eval_metric must require a file body")
	}
}
