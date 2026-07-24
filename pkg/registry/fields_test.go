// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package registry

import (
	"bytes"
	"strings"
	"testing"

	"github.com/harness/cli/pkg/cmdctx"
	"github.com/harness/cli/pkg/spec"
)

// ---------------------------------------------------------------------------
// splitFieldIDs
// ---------------------------------------------------------------------------

func TestSplitFieldIDs(t *testing.T) {
	tests := []struct {
		in   string
		want []string
	}{
		{"name,status", []string{"name", "status"}},
		{" name , status ", []string{"name", "status"}},
		{"name", []string{"name"}},
		{"", []string{}},
		{",,,", []string{}},
	}
	for _, tc := range tests {
		got := splitFieldIDs(tc.in)
		if len(got) != len(tc.want) {
			t.Errorf("splitFieldIDs(%q) = %v, want %v", tc.in, got, tc.want)
			continue
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("splitFieldIDs(%q)[%d] = %q, want %q", tc.in, i, got[i], tc.want[i])
			}
		}
	}
}

// ---------------------------------------------------------------------------
// labelFromID / fieldLabel
// ---------------------------------------------------------------------------

func TestLabelFromID(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"status", "Status"},
		{"pipeline_name", "Pipeline Name"},
		{"created_by_user", "Created By User"},
		{"id", "Id"},
		{"", ""},
	}
	for _, tc := range tests {
		if got := labelFromID(tc.in); got != tc.want {
			t.Errorf("labelFromID(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestFieldLabel_UsesExplicitLabel(t *testing.T) {
	f := spec.FieldDef{ID: "my_id", Label: "Custom Label", Expr: "it.id"}
	if got := fieldLabel(f); got != "Custom Label" {
		t.Errorf("fieldLabel with explicit label = %q, want %q", got, "Custom Label")
	}
}

func TestFieldLabel_DerivedFromID(t *testing.T) {
	f := spec.FieldDef{ID: "pipeline_name", Expr: "it.name"}
	if got := fieldLabel(f); got != "Pipeline Name" {
		t.Errorf("fieldLabel from ID = %q, want %q", got, "Pipeline Name")
	}
}

// ---------------------------------------------------------------------------
// resolveNounDef
// ---------------------------------------------------------------------------

type testResolver struct {
	nouns map[string]*spec.NounDef
}

func (tr *testResolver) GetSpec(verb, noun string) *spec.CommandSpec         { return nil }
func (tr *testResolver) GetNoun(noun string) *spec.NounDef                   { return tr.nouns[noun] }
func (tr *testResolver) ResolveNounAlias(alias string) string                { return "" }
func (tr *testResolver) GetVerbInfos() []spec.VerbInfo                       { return nil }
func (tr *testResolver) GetModuleMetas() []spec.ModuleMeta                   { return nil }
func (tr *testResolver) GetAllSpecs() []*spec.CommandSpec                    { return nil }
func (tr *testResolver) GetSpecsForModule(module string) []*spec.CommandSpec { return nil }
func (tr *testResolver) ResolveTextFormatter(id string) cmdctx.TextFormatterFn {
	return nil
}
func (tr *testResolver) ResolveBodyFn(id string) cmdctx.CreateBodyFn         { return nil }
func (tr *testResolver) ResolveQueryParamsFn(id string) cmdctx.QueryParamsFn { return nil }
func (tr *testResolver) ResolveFetchFn(id string) (cmdctx.FetchFn, error)    { return nil, nil }
func (tr *testResolver) ResolveFlagResolveFn(id string) cmdctx.FlagResolveFn { return nil }
func (tr *testResolver) ResolveEndpointValidator(id string) cmdctx.EndpointValidatorFn {
	return nil
}
func (tr *testResolver) RunEndpoint(ctx *cmdctx.Ctx, ep *spec.EndpointSpec) (any, error) {
	return nil, nil
}
func (tr *testResolver) FormatList(ctx *cmdctx.Ctx, rows []any, fields []spec.FieldDef, columnIDs []string) error {
	return nil
}
func (tr *testResolver) FetchItems(ctx *cmdctx.Ctx, ep *spec.EndpointSpec, pf cmdctx.PagingFlags) ([]any, error) {
	return nil, nil
}
func (tr *testResolver) ResolveCommandFields(cs *spec.CommandSpec) []spec.FieldDef { return nil }

func newTestResolver(nouns ...*spec.NounDef) *testResolver {
	m := make(map[string]*spec.NounDef)
	for _, n := range nouns {
		m[n.Noun] = n
	}
	return &testResolver{nouns: m}
}

func TestResolveNounDef_NilResolver(t *testing.T) {
	ctx := &cmdctx.Ctx{Noun: "pipeline"}
	if got := resolveNounDef(ctx); got != nil {
		t.Fatalf("expected nil from nil resolver, got %v", got)
	}
}

func TestResolveNounDef_UsesFieldsNoun(t *testing.T) {
	nd := &spec.NounDef{Noun: "stage", Fields: []spec.FieldDef{{ID: "id", Expr: "it.id"}}}
	ctx := &cmdctx.Ctx{
		Noun:       "pipeline",
		FieldsNoun: "stage",
		Resolver:   newTestResolver(nd),
	}
	got := resolveNounDef(ctx)
	if got == nil || got.Noun != "stage" {
		t.Fatalf("resolveNounDef with FieldsNoun = %v, want stage", got)
	}
}

func TestResolveNounDef_UsesNounWhenNoFieldsNoun(t *testing.T) {
	nd := &spec.NounDef{Noun: "pipeline", Fields: []spec.FieldDef{{ID: "id", Expr: "it.id"}}}
	ctx := &cmdctx.Ctx{Noun: "pipeline", Resolver: newTestResolver(nd)}
	got := resolveNounDef(ctx)
	if got == nil || got.Noun != "pipeline" {
		t.Fatalf("resolveNounDef = %v, want pipeline", got)
	}
}

// ---------------------------------------------------------------------------
// resolveFieldsForCommand
// ---------------------------------------------------------------------------

func TestResolveFieldsForCommand_NilEndpoint(t *testing.T) {
	ctx := &cmdctx.Ctx{Noun: "pipeline", Resolver: newTestResolver()}
	if got := resolveFieldsForCommand(ctx, nil); got != nil {
		t.Fatalf("expected nil for nil endpoint, got %v", got)
	}
}

func TestResolveFieldsForCommand_NoFields(t *testing.T) {
	ctx := &cmdctx.Ctx{Noun: "pipeline", Resolver: newTestResolver()}
	ep := &spec.EndpointSpec{NoFields: true}
	if got := resolveFieldsForCommand(ctx, ep); got != nil {
		t.Fatalf("expected nil for NoFields=true, got %v", got)
	}
}

func TestResolveFieldsForCommand_NilResolver(t *testing.T) {
	ctx := &cmdctx.Ctx{Noun: "pipeline"}
	ep := &spec.EndpointSpec{}
	if got := resolveFieldsForCommand(ctx, ep); got != nil {
		t.Fatalf("expected nil for nil resolver, got %v", got)
	}
}

func TestResolveFieldsForCommand_BaseFields(t *testing.T) {
	fields := []spec.FieldDef{
		{ID: "name", Expr: "it.name"},
		{ID: "status", Expr: "it.status"},
	}
	nd := &spec.NounDef{Noun: "pipeline", Fields: fields}
	ctx := &cmdctx.Ctx{Noun: "pipeline", Resolver: newTestResolver(nd)}
	ep := &spec.EndpointSpec{}
	got := resolveFieldsForCommand(ctx, ep)
	if len(got) != 2 {
		t.Fatalf("expected 2 fields, got %d", len(got))
	}
}

func TestResolveFieldsForCommand_FieldsSubset(t *testing.T) {
	fields := []spec.FieldDef{
		{ID: "name", Expr: "it.name"},
		{ID: "status", Expr: "it.status"},
		{ID: "id", Expr: "it.id"},
	}
	nd := &spec.NounDef{Noun: "pipeline", Fields: fields}
	ctx := &cmdctx.Ctx{Noun: "pipeline", Resolver: newTestResolver(nd)}
	ep := &spec.EndpointSpec{FieldsSubset: []string{"name", "id"}}
	got := resolveFieldsForCommand(ctx, ep)
	if len(got) != 2 {
		t.Fatalf("expected 2 fields after subset, got %d", len(got))
	}
}

func TestResolveFieldsForCommand_FieldsExtra(t *testing.T) {
	fields := []spec.FieldDef{{ID: "name", Expr: "it.name"}}
	nd := &spec.NounDef{Noun: "pipeline", Fields: fields}
	ctx := &cmdctx.Ctx{Noun: "pipeline", Resolver: newTestResolver(nd)}
	extra := spec.FieldDef{ID: "extra_col", Expr: "it.extra"}
	ep := &spec.EndpointSpec{FieldsExtra: []spec.FieldDef{extra}}
	got := resolveFieldsForCommand(ctx, ep)
	if len(got) != 2 {
		t.Fatalf("expected 2 fields (base+extra), got %d", len(got))
	}
	if got[1].ID != "extra_col" {
		t.Fatalf("expected extra_col as second field, got %q", got[1].ID)
	}
}

// ---------------------------------------------------------------------------
// PrintFieldTable
// ---------------------------------------------------------------------------

func TestPrintFieldTable_Empty(t *testing.T) {
	var buf bytes.Buffer
	if err := PrintFieldTable(&buf, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "No fields") {
		t.Fatalf("expected 'No fields' message, got: %q", buf.String())
	}
}

func TestPrintFieldTable_WithFields(t *testing.T) {
	fields := []spec.FieldDef{
		{ID: "name", Expr: "it.name"},
		{ID: "status", Label: "Status Label", Expr: "it.status"},
	}
	var buf bytes.Buffer
	if err := PrintFieldTable(&buf, fields); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "name") {
		t.Errorf("expected 'name' in output, got: %q", out)
	}
	if !strings.Contains(out, "Status Label") {
		t.Errorf("expected 'Status Label' in output, got: %q", out)
	}
}

// ---------------------------------------------------------------------------
// ResolveCommandFields (Registry method)
// ---------------------------------------------------------------------------

func TestResolveCommandFields_NilEndpoint(t *testing.T) {
	r := New()
	cs := &spec.CommandSpec{Command: "list pipeline", Verb: VerbList, Noun: "pipeline"}
	if got := r.ResolveCommandFields(cs); got != nil {
		t.Fatalf("expected nil for nil endpoint, got %v", got)
	}
}

func TestResolveCommandFields_NounNotRegistered(t *testing.T) {
	r := New()
	cs := &spec.CommandSpec{Noun: "pipeline", Endpoint: &spec.EndpointSpec{}}
	if got := r.ResolveCommandFields(cs); got != nil {
		t.Fatalf("expected nil for unregistered noun, got %v", got)
	}
}

func TestResolveCommandFields_HappyPath(t *testing.T) {
	r := New()
	r.RegisterNoun(spec.NounDef{
		Noun:   "pipeline",
		Fields: []spec.FieldDef{{ID: "name", Expr: "it.name"}, {ID: "id", Expr: "it.id"}},
	})
	cs := &spec.CommandSpec{Noun: "pipeline", Endpoint: &spec.EndpointSpec{}}
	got := r.ResolveCommandFields(cs)
	if len(got) != 2 {
		t.Fatalf("expected 2 fields, got %d: %v", len(got), got)
	}
}

func TestResolveCommandFields_WithSubsetAndExtra(t *testing.T) {
	r := New()
	r.RegisterNoun(spec.NounDef{
		Noun: "pipeline",
		Fields: []spec.FieldDef{
			{ID: "name", Expr: "it.name"},
			{ID: "status", Expr: "it.status"},
		},
	})
	cs := &spec.CommandSpec{
		Noun: "pipeline",
		Endpoint: &spec.EndpointSpec{
			FieldsSubset: []string{"name"},
			FieldsExtra:  []spec.FieldDef{{ID: "extra", Expr: "it.extra"}},
		},
	}
	got := r.ResolveCommandFields(cs)
	if len(got) != 2 {
		t.Fatalf("expected 2 (1 subset + 1 extra), got %d", len(got))
	}
}

func TestResolveCommandFields_FieldsNounOverride(t *testing.T) {
	r := New()
	r.RegisterNoun(spec.NounDef{Noun: "pipeline"})
	r.RegisterNoun(spec.NounDef{
		Noun:   "stage",
		Fields: []spec.FieldDef{{ID: "stage_id", Expr: "it.id"}},
	})
	cs := &spec.CommandSpec{Noun: "pipeline", FieldsNoun: "stage", Endpoint: &spec.EndpointSpec{}}
	got := r.ResolveCommandFields(cs)
	if len(got) != 1 || got[0].ID != "stage_id" {
		t.Fatalf("expected stage field via FieldsNoun, got %v", got)
	}
}

// ---------------------------------------------------------------------------
// MutableFields
// ---------------------------------------------------------------------------

func TestMutableFields_NilNoun(t *testing.T) {
	if got := MutableFields(nil); got != nil {
		t.Fatalf("expected nil for nil noun, got %v", got)
	}
}

func TestMutableFields_FiltersNonMutable(t *testing.T) {
	nd := &spec.NounDef{Fields: []spec.FieldDef{
		{ID: "name", Expr: "it.name", MutablePath: "name"},
		{ID: "status", Expr: "it.status"}, // not mutable
	}}
	got := MutableFields(nd)
	if len(got) != 1 || got[0].ID != "name" {
		t.Fatalf("MutableFields = %v, want only 'name'", got)
	}
}

// ---------------------------------------------------------------------------
// PrintMutableFieldTable
// ---------------------------------------------------------------------------

func TestPrintMutableFieldTable_Empty(t *testing.T) {
	var buf bytes.Buffer
	if err := PrintMutableFieldTable(&buf, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "No mutable") {
		t.Fatalf("expected 'No mutable' message, got: %q", buf.String())
	}
}

func TestPrintMutableFieldTable_WithFields(t *testing.T) {
	fields := []spec.FieldDef{
		{ID: "name", Expr: "it.name", MutablePath: "name"},
		{ID: "tags", Expr: "it.tags", MutablePath: "tags", FieldType: "list"},
	}
	var buf bytes.Buffer
	if err := PrintMutableFieldTable(&buf, fields); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "name") {
		t.Errorf("expected 'name' in output, got: %q", out)
	}
	if !strings.Contains(out, "list") {
		t.Errorf("expected 'list' field type in output, got: %q", out)
	}
}

// ---------------------------------------------------------------------------
// buildTspec
// ---------------------------------------------------------------------------

func TestBuildTspec_EmptyFields(t *testing.T) {
	if got := buildTspec(nil, nil); got != nil {
		t.Fatalf("buildTspec(nil, nil) = %v, want nil", got)
	}
}

func TestBuildTspec_AllFieldsWhenNoColumnIDs(t *testing.T) {
	fields := []spec.FieldDef{
		{ID: "name", Expr: "it.name"},
		{ID: "status", Expr: "it.status"},
	}
	got := buildTspec(nil, fields)
	if got == nil || len(got.Columns) != 2 {
		t.Fatalf("buildTspec(nil, fields) = %v, want 2 columns", got)
	}
}

func TestBuildTspec_SubsetByColumnIDs(t *testing.T) {
	fields := []spec.FieldDef{
		{ID: "name", Expr: "it.name"},
		{ID: "status", Expr: "it.status"},
		{ID: "id", Expr: "it.id"},
	}
	got := buildTspec([]string{"name", "id"}, fields)
	if got == nil || len(got.Columns) != 2 {
		t.Fatalf("buildTspec with columnIDs = %v, want 2 columns", got)
	}
	if got.Columns[0].Header != "Name" {
		t.Errorf("first column header = %q, want %q", got.Columns[0].Header, "Name")
	}
}

func TestBuildTspec_UnknownColumnIDSkipped(t *testing.T) {
	fields := []spec.FieldDef{{ID: "name", Expr: "it.name"}}
	got := buildTspec([]string{"name", "nonexistent"}, fields)
	if got == nil || len(got.Columns) != 1 {
		t.Fatalf("buildTspec: non-existent column ID should be skipped, got %v", got)
	}
}

func TestBuildTspec_AllColumnIDsUnknown(t *testing.T) {
	fields := []spec.FieldDef{{ID: "name", Expr: "it.name"}}
	got := buildTspec([]string{"nonexistent"}, fields)
	if got != nil {
		t.Fatalf("buildTspec: all unknown column IDs should return nil, got %v", got)
	}
}

// ---------------------------------------------------------------------------
// FieldsToTableColumns
// ---------------------------------------------------------------------------

func TestFieldsToTableColumns(t *testing.T) {
	fields := []spec.FieldDef{
		{ID: "name", Label: "Name", Expr: "it.name", Align: "left", FieldType: "scalar", WidthMax: 30},
		{ID: "status", Expr: "it.status"},
	}
	cols := FieldsToTableColumns(fields)
	if len(cols) != 2 {
		t.Fatalf("expected 2 columns, got %d", len(cols))
	}
	if cols[0].Header != "Name" || cols[0].Expr != "it.name" || cols[0].Align != "left" || cols[0].WidthMax != 30 {
		t.Errorf("first column = %v, incorrect fields", cols[0])
	}
	if cols[1].Header != "Status" {
		t.Errorf("second column header = %q, want %q", cols[1].Header, "Status")
	}
}
