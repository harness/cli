// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package pipeline

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/harness/cli/v3/pkg/auth"
	"github.com/harness/cli/v3/pkg/cmdctx"
)

const triggerDoc = `# keep me
trigger:
  name: My Trigger
  identifier: my_trigger
  enabled: true # toggled by the CLI
  pipelineIdentifier: my_pipeline
  source:
    type: Webhook
    spec:
      type: Custom
      spec:
        payloadConditions: []
`

func TestSetTriggerEnabled(t *testing.T) {
	t.Run("flips_value_and_preserves_order_and_comments", func(t *testing.T) {
		got, changed, err := setTriggerEnabled(triggerDoc, false)
		if err != nil || !changed {
			t.Fatalf("changed=%v err=%v, want changed and no error", changed, err)
		}
		want := strings.Replace(triggerDoc, "enabled: true", "enabled: false", 1)
		if got != want {
			t.Fatalf("output differs beyond the enabled value:\n%s", got)
		}
	})

	t.Run("already_in_state_is_unchanged", func(t *testing.T) {
		got, changed, err := setTriggerEnabled(triggerDoc, true)
		if err != nil || changed || got != triggerDoc {
			t.Fatalf("changed=%v err=%v, want untouched document", changed, err)
		}
	})

	t.Run("quoted_string_is_rewritten_as_bool", func(t *testing.T) {
		doc := strings.Replace(triggerDoc, "enabled: true", `enabled: "true"`, 1)
		got, changed, err := setTriggerEnabled(doc, true)
		if err != nil || !changed || !strings.Contains(got, "enabled: true # toggled") {
			t.Fatalf("changed=%v err=%v got:\n%s", changed, err, got)
		}
	})

	for _, tc := range []struct{ name, doc, wantErr string }{
		{"not_a_mapping", "- a\n- b\n", "not a mapping"},
		{"no_trigger_key", "name: x\n", `no "trigger" mapping`},
		{"no_enabled_key", "trigger:\n  name: x\n", `"trigger.enabled"`},
		{"invalid_yaml", "trigger: [", "parsing trigger YAML"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := setTriggerEnabled(tc.doc, false)
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error = %v, want containing %q", err, tc.wantErr)
			}
		})
	}
}

type toggleCall struct {
	method      string
	path        string
	query       url.Values
	contentType string
	body        string
}

// toggleServer answers the GET with getStatus/getBody and records every request.
func toggleServer(t *testing.T, getStatus int, getBody string) (*httptest.Server, *[]toggleCall) {
	t.Helper()
	calls := &[]toggleCall{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		*calls = append(*calls, toggleCall{r.Method, r.URL.Path, r.URL.Query(), r.Header.Get("Content-Type"), string(b)})
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet {
			w.WriteHeader(getStatus)
			io.WriteString(w, getBody)
			return
		}
		io.WriteString(w, "{}")
	}))
	t.Cleanup(srv.Close)
	return srv, calls
}

func toggleCtx(t *testing.T, apiURL string) (*cmdctx.Ctx, string) {
	t.Helper()
	out := filepath.Join(t.TempDir(), "out.txt")
	ctx := &cmdctx.Ctx{
		Context: context.Background(),
		Auth: &auth.ResolvedAuth{
			APIUrl: apiURL, AccountID: "acct", OrgID: "org", ProjectID: "proj",
			PATToken: "pat.test", AuthType: auth.AuthTypePAT,
		},
		Id:      "pipe/trig",
		IdParts: []string{"pipe", "trig"},
	}
	ctx.FormatFlags.OutFile = out
	return ctx, out
}

func triggerResponse(t *testing.T, doc string) string {
	t.Helper()
	b, err := json.Marshal(map[string]any{"data": map[string]any{"yaml": doc}})
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestToggleTrigger(t *testing.T) {
	t.Run("puts_edited_yaml", func(t *testing.T) {
		srv, calls := toggleServer(t, 200, triggerResponse(t, triggerDoc))
		ctx, out := toggleCtx(t, srv.URL)
		if err := toggleTrigger(ctx, false); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(*calls) != 2 || (*calls)[0].method != "GET" || (*calls)[1].method != "PUT" {
			t.Fatalf("calls = %+v, want GET then PUT", *calls)
		}
		put := (*calls)[1]
		if put.path != "/pipeline/api/triggers/trig" || put.contentType != "application/yaml" {
			t.Fatalf("PUT path=%q content-type=%q", put.path, put.contentType)
		}
		if want := strings.Replace(triggerDoc, "enabled: true", "enabled: false", 1); put.body != want {
			t.Fatalf("PUT body = %q, want %q", put.body, want)
		}
		for k, want := range map[string]string{"targetIdentifier": "pipe", "orgIdentifier": "org", "projectIdentifier": "proj"} {
			if got := put.query.Get(k); got != want {
				t.Errorf("PUT query %s = %q, want %q", k, got, want)
			}
		}
		if b, _ := os.ReadFile(out); !strings.Contains(string(b), "Trigger trig disabled") {
			t.Errorf("output = %q", b)
		}
	})

	t.Run("already_in_state_skips_put", func(t *testing.T) {
		srv, calls := toggleServer(t, 200, triggerResponse(t, triggerDoc))
		ctx, out := toggleCtx(t, srv.URL)
		if err := toggleTrigger(ctx, true); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(*calls) != 1 {
			t.Fatalf("calls = %+v, want GET only", *calls)
		}
		if b, _ := os.ReadFile(out); !strings.Contains(string(b), "already enabled") {
			t.Errorf("output = %q", b)
		}
	})

	t.Run("missing_yaml_errors_without_put", func(t *testing.T) {
		srv, calls := toggleServer(t, 200, `{"data":{}}`)
		ctx, _ := toggleCtx(t, srv.URL)
		if err := toggleTrigger(ctx, true); err == nil || !strings.Contains(err.Error(), "no YAML") {
			t.Fatalf("error = %v, want no-YAML error", err)
		}
		if len(*calls) != 1 {
			t.Fatalf("calls = %+v, want GET only", *calls)
		}
	})

	t.Run("get_failure_errors_without_put", func(t *testing.T) {
		srv, calls := toggleServer(t, 404, `{"message":"Trigger trig does not exist"}`)
		ctx, _ := toggleCtx(t, srv.URL)
		if err := toggleTrigger(ctx, true); err == nil || !strings.Contains(err.Error(), "does not exist") {
			t.Fatalf("error = %v, want not-found error", err)
		}
		if len(*calls) != 1 {
			t.Fatalf("calls = %+v, want GET only", *calls)
		}
	})

	t.Run("bare_id_is_rejected", func(t *testing.T) {
		ctx, _ := toggleCtx(t, "http://unused")
		ctx.Id, ctx.IdParts = "trig", []string{"trig"}
		if err := toggleTrigger(ctx, true); err == nil || !strings.Contains(err.Error(), "<pipeline>/<trigger>") {
			t.Fatalf("error = %v, want id format error", err)
		}
	})
}
