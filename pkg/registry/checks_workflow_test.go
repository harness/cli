// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package registry

import (
	"strings"
	"testing"

	"github.com/harness/cli/pkg/cmdctx"
	"github.com/harness/cli/pkg/spec"
)

// ---------------------------------------------------------------------------
// checkFunctionsSpec — all fn-reference branches
// ---------------------------------------------------------------------------

func TestCheckFunctions_TextFormatterMissing(t *testing.T) {
	r := New()
	r.RegisterNoun(spec.NounDef{Noun: "thing"})
	r.specs[VerbGet] = append(r.specs[VerbGet], &spec.CommandSpec{
		Command:     "get thing",
		Verb:        VerbGet,
		Noun:        "thing",
		Module:      "test",
		HandlerType: spec.HandlerEndpoint,
		Endpoint: &spec.EndpointSpec{
			Method:        "GET",
			TextFormatter: "missing_formatter",
			ItemExpr:      "it",
		},
	})
	err := r.CheckFunctions()
	if err == nil {
		t.Fatal("expected error for missing text_formatter")
	}
	if !strings.Contains(err.Error(), "text_formatter") {
		t.Fatalf("error = %q, want text_formatter mention", err)
	}
}

func TestCheckFunctions_BodyFnMissing(t *testing.T) {
	r := New()
	r.RegisterNoun(spec.NounDef{Noun: "thing2"})
	r.specs[VerbCreate] = append(r.specs[VerbCreate], &spec.CommandSpec{
		Command:     "create thing2",
		Verb:        VerbCreate,
		Noun:        "thing2",
		Module:      "test",
		HandlerType: spec.HandlerEndpoint,
		Endpoint: &spec.EndpointSpec{
			Method: "POST",
			BodyFn: "missing_body_fn",
		},
	})
	err := r.CheckFunctions()
	if err == nil {
		t.Fatal("expected error for missing body_fn")
	}
	if !strings.Contains(err.Error(), "body_fn") {
		t.Fatalf("error = %q, want body_fn mention", err)
	}
}

func TestCheckFunctions_QueryParamsFnMissing(t *testing.T) {
	r := New()
	r.RegisterNoun(spec.NounDef{Noun: "thing3"})
	r.specs[VerbList] = append(r.specs[VerbList], &spec.CommandSpec{
		Command:     "list thing3",
		Verb:        VerbList,
		Noun:        "thing3",
		Module:      "test",
		HandlerType: spec.HandlerEndpoint,
		Endpoint: &spec.EndpointSpec{
			Method:        "GET",
			ItemsExpr:     "it",
			QueryParamsFn: "missing_qp_fn",
		},
	})
	err := r.CheckFunctions()
	if err == nil {
		t.Fatal("expected error for missing query_params_fn")
	}
	if !strings.Contains(err.Error(), "query_params_fn") {
		t.Fatalf("error = %q, want query_params_fn mention", err)
	}
}

func TestCheckFunctions_FollowFnMissing(t *testing.T) {
	r := New()
	r.RegisterNoun(spec.NounDef{Noun: "thing4"})
	r.specs[VerbExecute] = append(r.specs[VerbExecute], &spec.CommandSpec{
		Command:     "execute thing4",
		Verb:        VerbExecute,
		Noun:        "thing4",
		Module:      "test",
		HandlerType: spec.HandlerWorkflow,
		WorkflowID:  "test:noop_follow",
		FollowFn:    "missing_follow_fn",
	})
	r.RegisterWorkflow("test:noop_follow", func(*cmdctx.Ctx) error { return nil })
	err := r.CheckFunctions()
	if err == nil {
		t.Fatal("expected error for missing follow_fn")
	}
	if !strings.Contains(err.Error(), "follow_fn") {
		t.Fatalf("error = %q, want follow_fn mention", err)
	}
}

func TestCheckFunctions_FlagCompletionFnMissing(t *testing.T) {
	r := New()
	wfID := "test:noop_completion"
	r.RegisterWorkflow(wfID, func(*cmdctx.Ctx) error { return nil })
	r.specs[VerbExecute] = append(r.specs[VerbExecute], &spec.CommandSpec{
		Command:     "execute thing5",
		Verb:        VerbExecute,
		Noun:        "thing5",
		Module:      "test",
		HandlerType: spec.HandlerWorkflow,
		WorkflowID:  wfID,
		Flags: []spec.Flag{
			{Name: "env", CompletionFn: "missing_completion_fn"},
		},
	})
	err := r.CheckFunctions()
	if err == nil {
		t.Fatal("expected error for missing completion_fn")
	}
	if !strings.Contains(err.Error(), "completion_fn") {
		t.Fatalf("error = %q, want completion_fn mention", err)
	}
}

func TestCheckFunctions_FlagResolveFnMissing(t *testing.T) {
	r := New()
	wfID := "test:noop_resolvefn"
	r.RegisterWorkflow(wfID, func(*cmdctx.Ctx) error { return nil })
	r.specs[VerbExecute] = append(r.specs[VerbExecute], &spec.CommandSpec{
		Command:     "execute thing6",
		Verb:        VerbExecute,
		Noun:        "thing6",
		Module:      "test",
		HandlerType: spec.HandlerWorkflow,
		WorkflowID:  wfID,
		Flags: []spec.Flag{
			{Name: "myarg", FlagResolveFn: "missing_resolve_fn"},
		},
	})
	err := r.CheckFunctions()
	if err == nil {
		t.Fatal("expected error for missing flag_resolve_fn")
	}
	if !strings.Contains(err.Error(), "flag_resolve_fn") {
		t.Fatalf("error = %q, want flag_resolve_fn mention", err)
	}
}

func TestCheckFunctions_InitErrsIncluded(t *testing.T) {
	r := New()
	r.initErrs = append(r.initErrs, "synthetic init error")
	err := r.CheckFunctions()
	if err == nil {
		t.Fatal("expected error from initErrs")
	}
	if !strings.Contains(err.Error(), "synthetic init error") {
		t.Fatalf("error = %q, want init error text", err)
	}
}

// ---------------------------------------------------------------------------
// CheckWarnings
// ---------------------------------------------------------------------------

func TestCheckWarnings_ListMissingPaging(t *testing.T) {
	r := New()
	r.RegisterNoun(spec.NounDef{Noun: "nopaging"})
	r.specs[VerbList] = append(r.specs[VerbList], &spec.CommandSpec{
		Command:     "list nopaging",
		Verb:        VerbList,
		VerbHandler: VerbList,
		Noun:        "nopaging",
		Module:      "test",
		HandlerType: spec.HandlerEndpoint,
		Endpoint:    &spec.EndpointSpec{Method: "GET", ItemsExpr: "it"},
	})
	warns := r.CheckWarnings()
	found := false
	for _, w := range warns {
		if strings.Contains(w, "paging_strategy") {
			found = true
		}
	}
	if !found {
		t.Fatalf("CheckWarnings() = %v, want paging_strategy warning", warns)
	}
}

func TestCheckWarnings_ListMissingGetIdExpr(t *testing.T) {
	r := New()
	r.RegisterNoun(spec.NounDef{Noun: "nogetid"})
	r.specs[VerbList] = append(r.specs[VerbList], &spec.CommandSpec{
		Command:     "list nogetid",
		Verb:        VerbList,
		VerbHandler: VerbList,
		Noun:        "nogetid",
		Module:      "test",
		HandlerType: spec.HandlerEndpoint,
		Endpoint: &spec.EndpointSpec{
			Method:    "GET",
			ItemsExpr: "it",
			Paging:    &spec.PagingSpec{PagingStrategy: spec.PagingStrategyFlatList},
		},
	})
	warns := r.CheckWarnings()
	found := false
	for _, w := range warns {
		if strings.Contains(w, "get_id_expr") {
			found = true
		}
	}
	if !found {
		t.Fatalf("CheckWarnings() = %v, want get_id_expr warning", warns)
	}
}

func TestCheckWarnings_ModuleMissingHelpText(t *testing.T) {
	r := New()
	r.SetModuleMeta(spec.ModuleMeta{Name: "mymod"}) // no HelpText
	warns := r.CheckWarnings()
	found := false
	for _, w := range warns {
		if strings.Contains(w, "missing help_text") {
			found = true
		}
	}
	if !found {
		t.Fatalf("CheckWarnings() = %v, want missing help_text warning", warns)
	}
}

func TestCheckWarnings_SkipsDevOnly(t *testing.T) {
	r := New()
	r.specs[VerbList] = append(r.specs[VerbList], &spec.CommandSpec{
		Command:     "list devonly",
		Verb:        VerbList,
		VerbHandler: VerbList,
		Noun:        "devonly",
		Module:      "test",
		HandlerType: spec.HandlerEndpoint,
		DevOnly:     true,
		Endpoint:    &spec.EndpointSpec{Method: "GET", ItemsExpr: "it"},
	})
	warns := r.CheckWarnings()
	for _, w := range warns {
		if strings.Contains(w, "devonly") {
			t.Fatalf("CheckWarnings() should skip DevOnly, got: %v", w)
		}
	}
}

// ---------------------------------------------------------------------------
// validateSpec / validateVerbNounShape / validateConfirmMode / validateEndpointConstraints / validatePaging
// ---------------------------------------------------------------------------

func TestValidateSpec_CommandMismatch(t *testing.T) {
	cs := &spec.CommandSpec{
		Command: "create wrong",
		Verb:    VerbCreate,
		Noun:    "pipeline",
		Module:  "test",
	}
	vs := verbRegistry[VerbCreate]
	if err := validateVerbNounShape(cs, vs); err == nil || !strings.Contains(err.Error(), "must equal") {
		t.Fatalf("expected command-mismatch error, got: %v", err)
	}
}

func TestValidateSpec_LeafVerbWithNoun(t *testing.T) {
	cs := &spec.CommandSpec{
		Command: "version extra", // command matches verb+noun so the shape check sees the noun
		Verb:    VerbVersion,
		Noun:    "extra",
		Module:  "core",
	}
	vs := verbRegistry[VerbVersion]
	if err := validateVerbNounShape(cs, vs); err == nil || !strings.Contains(err.Error(), "cannot have a noun") {
		t.Fatalf("expected leaf-verb-with-noun error, got: %v", err)
	}
}

func TestValidateSpec_CoreVerbNoNoun(t *testing.T) {
	cs := &spec.CommandSpec{
		Command: "create",
		Verb:    VerbCreate,
		Noun:    "",
		Module:  "test",
	}
	vs := verbRegistry[VerbCreate]
	if err := validateVerbNounShape(cs, vs); err == nil || !strings.Contains(err.Error(), "requires a noun") {
		t.Fatalf("expected core-verb-no-noun error, got: %v", err)
	}
}

func TestValidateSpec_IdPartsOutOfRange(t *testing.T) {
	cs := &spec.CommandSpec{
		Command: "create pipeline",
		Verb:    VerbCreate,
		Noun:    "pipeline",
		Module:  "test",
		IdParts: 5,
	}
	vs := verbRegistry[VerbCreate]
	if err := validateVerbNounShape(cs, vs); err == nil || !strings.Contains(err.Error(), "id_parts") {
		t.Fatalf("expected id_parts error, got: %v", err)
	}
}

func TestValidateConfirmMode_InvalidValue(t *testing.T) {
	cs := &spec.CommandSpec{Command: "delete pipeline", Verb: VerbDelete, Noun: "pipeline", Module: "test", ConfirmMode: "bad_mode"}
	if err := validateConfirmMode(cs); err == nil || !strings.Contains(err.Error(), "invalid confirm_mode") {
		t.Fatalf("expected invalid confirm_mode error, got: %v", err)
	}
}

func TestValidateConfirmMode_OnListVerb(t *testing.T) {
	cs := &spec.CommandSpec{Command: "list pipeline", Verb: VerbList, VerbHandler: VerbList, Noun: "pipeline", Module: "test", ConfirmMode: spec.ConfirmPrompt}
	if err := validateConfirmMode(cs); err == nil || !strings.Contains(err.Error(), "not supported on list") {
		t.Fatalf("expected confirm_mode-on-list error, got: %v", err)
	}
}

func TestValidateEndpointConstraints_PagingOnNonList(t *testing.T) {
	cs := &spec.CommandSpec{
		Command: "get pipeline", Verb: VerbGet, VerbHandler: VerbGet, Noun: "pipeline", Module: "test",
		HandlerType: spec.HandlerEndpoint,
		Endpoint: &spec.EndpointSpec{
			Method:   "GET",
			ItemExpr: "it",
			Paging:   &spec.PagingSpec{PagingStrategy: spec.PagingStrategyFlatList},
		},
	}
	if err := validateEndpointConstraints(cs); err == nil || !strings.Contains(err.Error(), "only allowed on list") {
		t.Fatalf("expected paging-on-non-list error, got: %v", err)
	}
}

func TestValidateEndpointConstraints_ItemsExprOnNonList(t *testing.T) {
	cs := &spec.CommandSpec{
		Command: "get pipeline", Verb: VerbGet, VerbHandler: VerbGet, Noun: "pipeline", Module: "test",
		HandlerType: spec.HandlerEndpoint,
		Endpoint:    &spec.EndpointSpec{Method: "GET", ItemExpr: "it", ItemsExpr: "it.items"},
	}
	if err := validateEndpointConstraints(cs); err == nil || !strings.Contains(err.Error(), "only allowed on list") {
		t.Fatalf("expected items_expr-on-non-list error, got: %v", err)
	}
}

func TestValidateEndpointConstraints_ListMissingItemsExpr(t *testing.T) {
	cs := &spec.CommandSpec{
		Command: "list pipeline", Verb: VerbList, VerbHandler: VerbList, Noun: "pipeline", Module: "test",
		HandlerType: spec.HandlerEndpoint,
		Endpoint:    &spec.EndpointSpec{Method: "GET"},
	}
	if err := validateEndpointConstraints(cs); err == nil || !strings.Contains(err.Error(), "items_expr") {
		t.Fatalf("expected missing items_expr error, got: %v", err)
	}
}

func TestValidateEndpointConstraints_GetMissingItemExpr(t *testing.T) {
	cs := &spec.CommandSpec{
		Command: "get pipeline", Verb: VerbGet, VerbHandler: VerbGet, Noun: "pipeline", Module: "test",
		HandlerType: spec.HandlerEndpoint,
		Endpoint:    &spec.EndpointSpec{Method: "GET"},
	}
	if err := validateEndpointConstraints(cs); err == nil || !strings.Contains(err.Error(), "item_expr") {
		t.Fatalf("expected missing item_expr error, got: %v", err)
	}
}

func TestValidateEndpointConstraints_BodyOnGETNotAllowed(t *testing.T) {
	cs := &spec.CommandSpec{
		Command: "get pipeline", Verb: VerbGet, VerbHandler: VerbGet, Noun: "pipeline", Module: "test",
		HandlerType: spec.HandlerEndpoint,
		Endpoint:    &spec.EndpointSpec{Method: "GET", ItemExpr: "it", BodyParams: map[string]string{"x": "it"}},
	}
	if err := validateEndpointConstraints(cs); err == nil || !strings.Contains(err.Error(), "not allowed on GET") {
		t.Fatalf("expected body-on-GET error, got: %v", err)
	}
}

func TestValidateEndpointConstraints_InvalidFileBody(t *testing.T) {
	cs := &spec.CommandSpec{
		Command: "create pipeline", Verb: VerbCreate, VerbHandler: VerbCreate, Noun: "pipeline", Module: "test",
		HandlerType: spec.HandlerEndpoint,
		Endpoint:    &spec.EndpointSpec{Method: "POST", FileBody: "bad"},
	}
	if err := validateEndpointConstraints(cs); err == nil || !strings.Contains(err.Error(), "invalid file_body") {
		t.Fatalf("expected invalid file_body error, got: %v", err)
	}
}

func TestValidateEndpointConstraints_InvalidContentType(t *testing.T) {
	cs := &spec.CommandSpec{
		Command: "create pipeline", Verb: VerbCreate, VerbHandler: VerbCreate, Noun: "pipeline", Module: "test",
		HandlerType: spec.HandlerEndpoint,
		Endpoint:    &spec.EndpointSpec{Method: "POST", FileBody: spec.FileBodyOptional, ContentType: "text/plain"},
	}
	if err := validateEndpointConstraints(cs); err == nil || !strings.Contains(err.Error(), "invalid content_type") {
		t.Fatalf("expected invalid content_type error, got: %v", err)
	}
}

func TestValidateEndpointConstraints_ContentTypeRequiresFileBody(t *testing.T) {
	cs := &spec.CommandSpec{
		Command: "create pipeline", Verb: VerbCreate, VerbHandler: VerbCreate, Noun: "pipeline", Module: "test",
		HandlerType: spec.HandlerEndpoint,
		Endpoint:    &spec.EndpointSpec{Method: "POST", ContentType: "application/yaml"},
	}
	if err := validateEndpointConstraints(cs); err == nil || !strings.Contains(err.Error(), "content_type requires file_body") {
		t.Fatalf("expected content_type-requires-file_body error, got: %v", err)
	}
}

func TestValidateEndpointConstraints_FileBodyContentTypeRequiresFileBody(t *testing.T) {
	cs := &spec.CommandSpec{
		Command: "create pipeline", Verb: VerbCreate, VerbHandler: VerbCreate, Noun: "pipeline", Module: "test",
		HandlerType: spec.HandlerEndpoint,
		Endpoint:    &spec.EndpointSpec{Method: "POST", FileBodyContentType: "application/yaml"},
	}
	if err := validateEndpointConstraints(cs); err == nil || !strings.Contains(err.Error(), "file_body_content_type requires file_body") {
		t.Fatalf("expected file_body_content_type-requires-file_body error, got: %v", err)
	}
}

func TestValidateEndpointConstraints_FileBodyWrapRequiresFileBody(t *testing.T) {
	cs := &spec.CommandSpec{
		Command: "create pipeline", Verb: VerbCreate, VerbHandler: VerbCreate, Noun: "pipeline", Module: "test",
		HandlerType: spec.HandlerEndpoint,
		Endpoint:    &spec.EndpointSpec{Method: "POST", FileBodyWrapAsString: "yaml_key"},
	}
	if err := validateEndpointConstraints(cs); err == nil || !strings.Contains(err.Error(), "file_body_wrap_as_string requires file_body") {
		t.Fatalf("expected file_body_wrap error, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// validatePaging
// ---------------------------------------------------------------------------

func TestValidatePaging(t *testing.T) {
	tests := []struct {
		name       string
		pg         *spec.PagingSpec
		wantErrSub string
	}{
		{
			name:       "unknown strategy",
			pg:         &spec.PagingSpec{PagingStrategy: "banana"},
			wantErrSub: "unknown paging model",
		},
		{
			name: "flat_list passes with no extra fields",
			pg:   &spec.PagingSpec{PagingStrategy: spec.PagingStrategyFlatList},
		},
		{
			name:       "page_index missing page_size_max",
			pg:         &spec.PagingSpec{PagingStrategy: spec.PagingStrategyPageIndex},
			wantErrSub: "page_size_max > 0",
		},
		{
			name:       "page_index missing page_size_default",
			pg:         &spec.PagingSpec{PagingStrategy: spec.PagingStrategyPageIndex, PageSizeMax: 100},
			wantErrSub: "page_size_default > 0",
		},
		{
			name:       "page_index max < default",
			pg:         &spec.PagingSpec{PagingStrategy: spec.PagingStrategyPageIndex, PageSizeMax: 10, PageSizeDefault: 50, PageIndexParam: "p", PageSizeParam: "s"},
			wantErrSub: "page_size_max",
		},
		{
			name:       "page_index missing page_index_param",
			pg:         &spec.PagingSpec{PagingStrategy: spec.PagingStrategyPageIndex, PageSizeMax: 100, PageSizeDefault: 20},
			wantErrSub: "page_index_param",
		},
		{
			name:       "page_index missing page_size_param",
			pg:         &spec.PagingSpec{PagingStrategy: spec.PagingStrategyPageIndex, PageSizeMax: 100, PageSizeDefault: 20, PageIndexParam: "p"},
			wantErrSub: "page_size_param",
		},
		{
			name:       "page_index missing total_expr",
			pg:         &spec.PagingSpec{PagingStrategy: spec.PagingStrategyPageIndex, PageSizeMax: 100, PageSizeDefault: 20, PageIndexParam: "p", PageSizeParam: "s"},
			wantErrSub: "total_expr",
		},
		{
			name: "page_index fully valid passes",
			pg: &spec.PagingSpec{
				PagingStrategy:  spec.PagingStrategyPageIndex,
				PageSizeMax:     100,
				PageSizeDefault: 20,
				PageIndexParam:  "pageIndex",
				PageSizeParam:   "pageSize",
				TotalExpr:       "it.totalItems",
			},
		},
		{
			name: "offset_limit only needs page_size_max",
			pg: &spec.PagingSpec{
				PagingStrategy: spec.PagingStrategyOffsetLimit,
				PageSizeMax:    100,
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := validatePaging("test command", tc.pg)
			if tc.wantErrSub == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.wantErrSub)
			}
			if !strings.Contains(err.Error(), tc.wantErrSub) {
				t.Fatalf("error = %q, want %q", err, tc.wantErrSub)
			}
		})
	}
}

func TestCheckFunctions_WorkflowRegistered(t *testing.T) {
	r := New()
	wfID := "test:noop"
	r.RegisterWorkflow(wfID, func(*cmdctx.Ctx) error { return nil })
	cs := &spec.CommandSpec{
		Command:     "execute thing",
		Verb:        VerbExecute,
		Noun:        "thing",
		Module:      "test",
		HandlerType: spec.HandlerWorkflow,
		WorkflowID:  wfID,
	}
	if err := r.Register(cs); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if err := r.CheckFunctions(); err != nil {
		t.Fatalf("CheckFunctions() = %v, want nil", err)
	}
}

func TestCheckFunctions_WorkflowMissing(t *testing.T) {
	r := New()
	cs := &spec.CommandSpec{
		Command:     "execute thing",
		Verb:        VerbExecute,
		Noun:        "thing",
		Module:      "test",
		HandlerType: spec.HandlerWorkflow,
		WorkflowID:  "missing_handler",
	}
	if err := r.Register(cs); err != nil {
		t.Fatalf("Register: %v", err)
	}
	err := r.CheckFunctions()
	if err == nil {
		t.Fatal("CheckFunctions() = nil, want error")
	}
	if !strings.Contains(err.Error(), `workflow_id "missing_handler" not registered`) {
		t.Fatalf("CheckFunctions() error %q does not contain expected substring", err)
	}
}

func TestCheckFunctions_SkipsDevOnlyAndExternal(t *testing.T) {
	r := New()
	r.specs[VerbExecute] = append(r.specs[VerbExecute],
		&spec.CommandSpec{
			Command:     "execute devonly",
			Verb:        VerbExecute,
			Noun:        "devonly",
			Module:      "test",
			HandlerType: spec.HandlerWorkflow,
			WorkflowID:  "missing_handler",
			DevOnly:     true,
		},
		&spec.CommandSpec{
			Command:     "execute external",
			Verb:        VerbExecute,
			Noun:        "external",
			Module:      "test",
			HandlerType: spec.HandlerWorkflow,
			WorkflowID:  "missing_handler",
			External:    true,
		},
	)
	if err := r.CheckFunctions(); err != nil {
		t.Fatalf("CheckFunctions() = %v, want nil (DevOnly/External skipped)", err)
	}
}
