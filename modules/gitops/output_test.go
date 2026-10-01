// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package gitops

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/harness/cli/v3/pkg/cmdctx"
	"github.com/harness/cli/v3/pkg/extractutil"
	"github.com/harness/cli/v3/pkg/format"
	"github.com/harness/cli/v3/pkg/hbase"
	"github.com/harness/cli/v3/pkg/registry"
	"github.com/harness/cli/v3/pkg/spec"
)

func TestAutocreateLogOutputPreservesBatches(t *testing.T) {
	t.Setenv(hbase.EnvColumns, "")
	for _, name := range []string{"json", "jsonl", "table"} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "output")
			t.Cleanup(func() {
				if err := hbase.Cleanup(); err != nil {
					t.Error(err)
				}
			})
			ctx := testCtx(nil)
			ctx.Noun = "gitops_autocreate_log"
			ctx.Resolver = registry.New()
			ctx.FormatFlags = cmdctx.FormatFlags{Format: name, OutFile: path}
			fields := []spec.FieldDef{{ID: "resource_ref", Label: "Resource", Expr: "it.resourceRef"}}
			cs := &spec.CommandSpec{Endpoint: &spec.EndpointSpec{Columns: []string{"resource_ref"}}}
			if name == "table" {
				w, err := format.OpenWriter(path)
				if err != nil {
					t.Fatal(err)
				}
				env := map[string]any{"ctx": map[string]any{"id": "example-agent"}}
				if err := formatGitopsAgentImportSummary(w, extractutil.MakeDataAccessor(env, map[string]any{})); err != nil {
					t.Fatal(err)
				}
			}
			for _, ref := range []string{"first-batch", "second-batch"} {
				if err := printAutocreateLogDelta(ctx, cs, fields, []any{map[string]any{"resourceRef": ref}}); err != nil {
					t.Fatal(err)
				}
			}
			out, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if name == "table" {
				for _, want := range []string{"Import complete for agent example-agent", "first-batch", "second-batch"} {
					if !strings.Contains(string(out), want) {
						t.Errorf("output %q missing %q", out, want)
					}
				}
				return
			}
			dec := json.NewDecoder(bytes.NewReader(out))
			for _, want := range []string{"first-batch", "second-batch"} {
				var row map[string]any
				if err := dec.Decode(&row); err != nil || row["resourceRef"] != want {
					t.Fatalf("row = %v, error = %v, want %s", row, err, want)
				}
			}
			if err := dec.Decode(new(any)); err != io.EOF {
				t.Fatalf("unexpected trailing output: %v", err)
			}
		})
	}
}
