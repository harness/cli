// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package rt

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/harness/cli/v3/pkg/cmdctx"
	"github.com/harness/cli/v3/pkg/exprenv"
	"github.com/harness/cli/v3/pkg/format"
	"github.com/harness/cli/v3/pkg/registry"
	"github.com/harness/cli/v3/pkg/spec"
)

// reportHeader is the header the service writes, as it arrives after JSON decoding.
var reportHeader = []any{"ServiceID", "Org", "Project", "RunCount", "PeakUsers", "MaxWorkers", "DurationSec", "VUSeconds", "Complexity"}

func TestUsageReportRows(t *testing.T) {
	data := []any{
		reportHeader,
		[]any{"checkout", "eng", "payments", "12", "500", "4", "3600", "1800000", "42.50"},
		[]any{"search", "eng", "discovery", "3", "100", "1", "900", "90000", "7.25"},
		[]any{"TOTAL", "", "", "", "", "", "", "", "50"},
	}

	items, fields, meta, err := usageReportRows(nil, data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if meta.Notice != "" {
		t.Errorf("expected no paging notice, got %q", meta.Notice)
	}

	// The header is columns, not a row; every other row is an item, TOTAL included.
	if len(items) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(items))
	}
	if len(fields) != len(reportHeader) {
		t.Fatalf("expected %d fields, got %d", len(reportHeader), len(fields))
	}

	// --columns matches the id, while the printed header keeps the service's wording.
	wantIDs := []string{"service_id", "org", "project", "run_count", "peak_users", "max_workers", "duration_sec", "vu_seconds", "complexity"}
	for i, want := range wantIDs {
		if fields[i].ID != want {
			t.Errorf("field %d: id = %q, want %q", i, fields[i].ID, want)
		}
		if label, _ := reportHeader[i].(string); fields[i].Label != label {
			t.Errorf("field %d: label = %q, want the service's %q", i, fields[i].Label, label)
		}
		if want := `it["` + wantIDs[i] + `"]`; fields[i].Expr != want {
			t.Errorf("field %d: expr = %q, want %q", i, fields[i].Expr, want)
		}
	}

	first, ok := items[0].(map[string]any)
	if !ok {
		t.Fatalf("row 0 is %T, want map[string]any", items[0])
	}
	if first["service_id"] != "checkout" || first["complexity"] != "42.50" {
		t.Errorf("row 0 = %v, want checkout/42.50", first)
	}

	// The account total is the figure the report exists to report.
	last, _ := items[2].(map[string]any)
	if last["service_id"] != "TOTAL" || last["complexity"] != "50" {
		t.Errorf("last row = %v, want the TOTAL row", last)
	}
}

func TestUsageReportRendersThroughTheListPipeline(t *testing.T) {
	data := []any{
		reportHeader,
		[]any{"checkout", "eng", "payments", "12", "500", "4", "3600", "1800000", "42.50"},
		[]any{"TOTAL", "", "", "", "", "", "", "", "50"},
	}
	items, fields, meta, err := usageReportRows(nil, data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, formatName := range []string{"csv", "table", "tsv"} {
		t.Run(formatName, func(t *testing.T) {
			out := filepath.Join(t.TempDir(), "report")
			err := format.FormatArrayOutput(
				cmdctx.FormatFlags{Format: formatName, OutFile: out},
				false, items, "it",
				&spec.TableSpec{Columns: registry.FieldsToTableColumns(fields)},
				fields, exprenv.Make(&cmdctx.Ctx{}), &meta,
			)
			if err != nil {
				t.Fatalf("rendering as %s: %v", formatName, err)
			}
			rendered, err := os.ReadFile(out)
			if err != nil {
				t.Fatal(err)
			}
			got := string(rendered)

			// The header keeps the service's own wording rather than the ids.
			if !strings.Contains(got, "ServiceID") || !strings.Contains(got, "VUSeconds") {
				t.Errorf("as %s, got:\n%s\nwant the service's headings", formatName, got)
			}
			// Every cell has to survive the round trip through the expressions.
			for _, want := range []string{"checkout", "payments", "1800000", "42.50", "TOTAL"} {
				if !strings.Contains(got, want) {
					t.Errorf("as %s, got:\n%s\nwant it to contain %q", formatName, got, want)
				}
			}
		})
	}
}

func TestUsageReportRowsEmpty(t *testing.T) {
	items, fields, _, err := usageReportRows(nil, []any{})
	if err != nil {
		t.Fatalf("an empty report is not an error, got: %v", err)
	}
	if len(items) != 0 || len(fields) != 0 {
		t.Fatalf("expected nothing to render, got %d items and %d fields", len(items), len(fields))
	}
}

func TestUsageReportRowsHeaderOnly(t *testing.T) {
	items, fields, _, err := usageReportRows(nil, []any{reportHeader})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(items) != 0 {
		t.Errorf("expected no rows, got %d", len(items))
	}
	if len(fields) != len(reportHeader) {
		t.Errorf("expected the columns to survive, got %d", len(fields))
	}
}

func TestUsageReportRowsNotAMatrix(t *testing.T) {
	for _, data := range []any{map[string]any{"rows": 1}, "text", nil} {
		if _, _, _, err := usageReportRows(nil, data); err == nil {
			t.Errorf("data %v (%T): expected an error", data, data)
		}
	}
}

func TestUsageReportRowsShortRow(t *testing.T) {
	data := []any{
		[]any{"ServiceID", "Org", "Complexity"},
		[]any{"checkout", "eng"},
	}
	items, _, _, err := usageReportRows(nil, data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	row, _ := items[0].(map[string]any)
	if _, present := row["complexity"]; present {
		t.Errorf("missing cell should be absent, got %v", row["complexity"])
	}
	if row["org"] != "eng" {
		t.Errorf("org = %v, want eng", row["org"])
	}
}

func TestUsageReportRowsDuplicateHeaders(t *testing.T) {
	data := []any{
		[]any{"Org", "Org", "Org"},
		[]any{"a", "b", "c"},
	}
	items, fields, _, err := usageReportRows(nil, data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{"org", "org_2", "org_3"}
	for i, id := range want {
		if fields[i].ID != id {
			t.Errorf("field %d: id = %q, want %q", i, fields[i].ID, id)
		}
	}
	row, _ := items[0].(map[string]any)
	if row["org"] != "a" || row["org_2"] != "b" || row["org_3"] != "c" {
		t.Errorf("row = %v, want all three columns kept", row)
	}
}

func TestUsageReportRowsUnnamedColumn(t *testing.T) {
	data := []any{[]any{"Org", "", nil}}
	_, fields, _, err := usageReportRows(nil, data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for i, want := range []string{"org", "col_1", "col_2"} {
		if fields[i].ID != want {
			t.Errorf("field %d: id = %q, want %q", i, fields[i].ID, want)
		}
	}
}

func TestSnakeCase(t *testing.T) {
	cases := map[string]string{
		"ServiceID":   "service_id",
		"VUSeconds":   "vu_seconds",
		"RunCount":    "run_count",
		"MaxWorkers":  "max_workers",
		"DurationSec": "duration_sec",
		"Org":         "org",
		"Complexity":  "complexity",
		"P95Latency":  "p95_latency",
		"already_ok":  "already_ok",
		"peak-users":  "peak_users",
		"ID":          "id",
		"":            "",

		// A capital run closed by a plural s is one word, not two.
		"Total VUs": "total_vus",
		"VUsCount":  "vus_count",
		"APIs":      "apis",
	}
	for in, want := range cases {
		if got := snakeCase(in); got != want {
			t.Errorf("snakeCase(%q) = %q, want %q", in, got, want)
		}
	}
}
