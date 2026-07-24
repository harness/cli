// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package registry

import (
	"io"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/harness/cli/pkg/auth"
	"github.com/harness/cli/pkg/cmdctx"
	"github.com/harness/cli/pkg/hbase"
	"github.com/harness/cli/pkg/spec"
)

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func wfSpec(verb, noun, module string) *spec.CommandSpec {
	return &spec.CommandSpec{
		Command:     verb + " " + noun,
		Verb:        verb,
		Noun:        noun,
		Module:      module,
		HandlerType: spec.HandlerWorkflow,
		WorkflowID:  "test:noop",
	}
}

func registryWithNoop(t *testing.T) *Registry {
	t.Helper()
	r := New()
	r.RegisterWorkflow("test:noop", func(*cmdctx.Ctx) error { return nil })
	return r
}

// ---------------------------------------------------------------------------
// New
// ---------------------------------------------------------------------------

// TestNew_StrictYAML: env off → false, env on → true.
func TestNew_StrictYAML(t *testing.T) {
	tests := []struct {
		envVal string
		want   bool
	}{
		{"0", false},
		{"1", true},
	}
	for _, tc := range tests {
		t.Run(tc.envVal, func(t *testing.T) {
			t.Setenv(hbase.EnvCheckSpecs, tc.envVal)
			r := New()
			if r.StrictYAML != tc.want {
				t.Errorf("StrictYAML = %v, want %v", r.StrictYAML, tc.want)
			}
		})
	}
}

func TestNew_MapsInitialised(t *testing.T) {
	r := New()
	if r.specs == nil || r.nouns == nil || r.workflows == nil {
		t.Fatal("New() must initialise internal maps")
	}
}

// ---------------------------------------------------------------------------
// SetModuleMeta / GetModuleMetas / externalBinaryFor
// ---------------------------------------------------------------------------

func TestSetModuleMeta_AppendsAndSorts(t *testing.T) {
	r := New()
	r.SetModuleMeta(spec.ModuleMeta{Name: "pipeline"})
	r.SetModuleMeta(spec.ModuleMeta{Name: "core"})
	metas := r.GetModuleMetas()
	if len(metas) != 2 {
		t.Fatalf("GetModuleMetas() len = %d, want 2", len(metas))
	}
	if metas[0].Name != "core" {
		t.Errorf("first module = %q, want %q after SortModules", metas[0].Name, "core")
	}
}

func TestGetModuleMetas_ReturnsCopy(t *testing.T) {
	r := New()
	r.SetModuleMeta(spec.ModuleMeta{Name: "pipeline"})
	a := r.GetModuleMetas()
	a[0].Name = "mutated"
	if b := r.GetModuleMetas(); b[0].Name == "mutated" {
		t.Fatal("GetModuleMetas() must return a copy, not a reference to the internal slice")
	}
}

// TestExternalBinaryFor: three branches differ only in IsMainBinary + whether the module
// is known — pure table.
func TestExternalBinaryFor(t *testing.T) {
	tests := []struct {
		name         string
		isMainBinary bool
		moduleName   string // non-empty → SetModuleMeta with ExternalBinary "harness-har"
		lookup       string
		want         string
	}{
		{name: "not_main_binary", isMainBinary: false, moduleName: "har", lookup: "har", want: ""},
		{name: "main_binary_known_module", isMainBinary: true, moduleName: "har", lookup: "har", want: "harness-har"},
		{name: "main_binary_unknown_module", isMainBinary: true, moduleName: "", lookup: "unknown", want: ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := New()
			r.IsMainBinary = tc.isMainBinary
			if tc.moduleName != "" {
				r.SetModuleMeta(spec.ModuleMeta{Name: tc.moduleName, ExternalBinary: "harness-har"})
			}
			if got := r.externalBinaryFor(tc.lookup); got != tc.want {
				t.Errorf("externalBinaryFor(%q) = %q, want %q", tc.lookup, got, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// RegisterNoun
// ---------------------------------------------------------------------------

func TestRegisterNoun_HappyPath(t *testing.T) {
	r := New()
	nd := spec.NounDef{Noun: "pipeline", NounAliases: []string{"pipelines"}}
	if err := r.RegisterNoun(nd); err != nil {
		t.Fatalf("RegisterNoun: %v", err)
	}
	if got := r.GetNoun("pipeline"); got == nil || got.Noun != "pipeline" {
		t.Fatal("GetNoun returned nil after successful RegisterNoun")
	}
}

func TestRegisterNoun_AliasesRegistered(t *testing.T) {
	r := New()
	nd := spec.NounDef{Noun: "pipeline", NounAliases: []string{"pipelines", "pl"}}
	if err := r.RegisterNoun(nd); err != nil {
		t.Fatalf("RegisterNoun: %v", err)
	}
	for _, alias := range []string{"pipelines", "pl"} {
		if got := r.ResolveNounAlias(alias); got != "pipeline" {
			t.Errorf("ResolveNounAlias(%q) = %q, want %q", alias, got, "pipeline")
		}
	}
}

// TestRegisterNoun_Errors: four error branches share the same call shape (setup + RegisterNoun
// → err containing wantSubstring).
func TestRegisterNoun_Errors(t *testing.T) {
	tests := []struct {
		name    string
		setup   func(*Registry)
		nd      spec.NounDef
		wantErr string
	}{
		{
			name:    "duplicate_noun",
			setup:   func(r *Registry) { r.RegisterNoun(spec.NounDef{Noun: "pipeline"}) },
			nd:      spec.NounDef{Noun: "pipeline"},
			wantErr: `duplicate noun "pipeline"`,
		},
		{
			name:    "alias_conflicts_with_noun",
			setup:   func(r *Registry) { r.RegisterNoun(spec.NounDef{Noun: "pr"}) },
			nd:      spec.NounDef{Noun: "pullrequest", NounAliases: []string{"pr"}},
			wantErr: "conflicts with existing noun",
		},
		{
			name:    "alias_conflicts_with_alias",
			setup:   func(r *Registry) { r.RegisterNoun(spec.NounDef{Noun: "pr", NounAliases: []string{"prs"}}) },
			nd:      spec.NounDef{Noun: "pullrequest", NounAliases: []string{"prs"}},
			wantErr: "already claimed by noun",
		},
		{
			name:    "invalid_field_missing_expr",
			setup:   func(*Registry) {},
			nd:      spec.NounDef{Noun: "bad", Fields: []spec.FieldDef{{ID: "name", Expr: ""}}},
			wantErr: "expr is required",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := New()
			tc.setup(r)
			err := r.RegisterNoun(tc.nd)
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("RegisterNoun err = %v, want to contain %q", err, tc.wantErr)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// GetNoun / ResolveNounAlias
// ---------------------------------------------------------------------------

func TestGetNoun_NotFound(t *testing.T) {
	r := New()
	if got := r.GetNoun("nonexistent"); got != nil {
		t.Fatalf("GetNoun unknown = %v, want nil", got)
	}
}

func TestResolveNounAlias_NotFound(t *testing.T) {
	r := New()
	if got := r.ResolveNounAlias("nothing"); got != "" {
		t.Errorf("ResolveNounAlias unknown = %q, want empty", got)
	}
}

// ---------------------------------------------------------------------------
// Register — side-effect tests stay separate; error tests are grouped
// ---------------------------------------------------------------------------

func TestRegister_HappyPath(t *testing.T) {
	r := registryWithNoop(t)
	if err := r.Register(wfSpec(VerbCreate, "pipeline", "pipeline")); err != nil {
		t.Fatalf("Register: %v", err)
	}
}

// TestRegister_Errors: four error paths share the same assertion shape (err containing
// wantSubstring). Setups differ, so t.Run with a setup func.
func TestRegister_Errors(t *testing.T) {
	tests := []struct {
		name    string
		setup   func(*Registry)
		cs      func() *spec.CommandSpec
		wantErr string
	}{
		{
			name:  "unknown_verb",
			setup: func(*Registry) {},
			cs: func() *spec.CommandSpec {
				return &spec.CommandSpec{Command: "frobnicate pipeline", Verb: "frobnicate", Noun: "pipeline", Module: "pipeline"}
			},
			wantErr: "not in the allowed verb set",
		},
		{
			name:  "external_flag_rejected",
			setup: func(*Registry) {},
			cs: func() *spec.CommandSpec {
				return &spec.CommandSpec{Command: "create pipeline", Verb: VerbCreate, Noun: "pipeline", Module: "pipeline", External: true}
			},
			wantErr: "External must not be set before registration",
		},
		{
			name: "duplicate_leaf_verb",
			setup: func(r *Registry) {
				r.RegisterWorkflow("test:noop", func(*cmdctx.Ctx) error { return nil })
				r.Register(&spec.CommandSpec{Command: "version", Verb: VerbVersion, Module: "core", HandlerType: spec.HandlerWorkflow, WorkflowID: "test:noop"})
			},
			cs: func() *spec.CommandSpec {
				return &spec.CommandSpec{Command: "version", Verb: VerbVersion, Module: "core", HandlerType: spec.HandlerWorkflow, WorkflowID: "test:noop"}
			},
			wantErr: "duplicate leaf verb",
		},
		{
			name: "duplicate_noun_verb",
			setup: func(r *Registry) {
				r.RegisterWorkflow("test:noop", func(*cmdctx.Ctx) error { return nil })
				r.Register(wfSpec(VerbCreate, "pipeline", "pipeline"))
			},
			cs:      func() *spec.CommandSpec { return wfSpec(VerbCreate, "pipeline", "pipeline") },
			wantErr: "duplicate command",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := New()
			tc.setup(r)
			if err := r.Register(tc.cs()); err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("Register() err = %v, want to contain %q", err, tc.wantErr)
			}
		})
	}
}

func TestRegister_DevOnlySkippedInProd(t *testing.T) {
	if hbase.IsDev() {
		t.Skip("skip in dev build where DevOnly commands are allowed")
	}
	r := registryWithNoop(t)
	cs := wfSpec(VerbCreate, "pipeline", "pipeline")
	cs.DevOnly = true
	if err := r.Register(cs); err != nil {
		t.Fatalf("DevOnly Register in prod should return nil, got %v", err)
	}
	if r.GetSpec(VerbCreate, "pipeline") != nil {
		t.Fatal("DevOnly command must not appear in registry in a prod build")
	}
}

func TestRegister_VerbHandlerDefaultsToVerb(t *testing.T) {
	r := registryWithNoop(t)
	cs := wfSpec(VerbCreate, "pipeline", "pipeline")
	cs.VerbHandler = ""
	r.Register(cs)
	if cs.VerbHandler != VerbCreate {
		t.Errorf("VerbHandler = %q, want %q", cs.VerbHandler, VerbCreate)
	}
}

func TestRegister_IsMainBinarySetsExternal(t *testing.T) {
	r := registryWithNoop(t)
	r.IsMainBinary = true
	r.SetModuleMeta(spec.ModuleMeta{Name: "har", ExternalBinary: "harness-har"})
	cs := wfSpec(VerbList, "artifact", "har")
	if err := r.Register(cs); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if !cs.External {
		t.Fatal("Register must set External=true when IsMainBinary and module has ExternalBinary")
	}
}

// ---------------------------------------------------------------------------
// GetSpec / GetAllSpecs / GetSpecsForModule
// ---------------------------------------------------------------------------

func TestGetSpec_Found(t *testing.T) {
	r := registryWithNoop(t)
	r.Register(wfSpec(VerbCreate, "pipeline", "pipeline"))
	got := r.GetSpec(VerbCreate, "pipeline")
	if got == nil || got.Noun != "pipeline" {
		t.Errorf("GetSpec(VerbCreate, pipeline) = %v, want non-nil spec with noun=pipeline", got)
	}
}

func TestGetSpec_NotFound(t *testing.T) {
	if got := New().GetSpec(VerbCreate, "nonexistent"); got != nil {
		t.Fatalf("GetSpec unknown = %v, want nil", got)
	}
}

func TestGetSpec_NounVariant(t *testing.T) {
	r := registryWithNoop(t)
	cs := wfSpec(VerbList, "pr", "code")
	cs.NounVariant = "mine"
	cs.Command = "list pr:mine"
	r.Register(cs)
	if got := r.GetSpec(VerbList, "pr:mine"); got == nil {
		t.Fatal("GetSpec(VerbList, \"pr:mine\") returned nil")
	}
}

func TestGetAllSpecs(t *testing.T) {
	r := registryWithNoop(t)
	r.Register(wfSpec(VerbCreate, "pipeline", "pipeline"))
	r.Register(wfSpec(VerbList, "pipeline", "pipeline"))
	if got := r.GetAllSpecs(); len(got) != 2 {
		t.Errorf("GetAllSpecs() len = %d, want 2", len(got))
	}
}

func TestGetSpecsForModule(t *testing.T) {
	r := registryWithNoop(t)
	r.Register(wfSpec(VerbCreate, "pipeline", "pipeline"))
	r.Register(wfSpec(VerbCreate, "artifact", "har"))
	got := r.GetSpecsForModule("pipeline")
	if len(got) != 1 || got[0].Noun != "pipeline" {
		t.Errorf("GetSpecsForModule(\"pipeline\") = %v, want single pipeline spec", got)
	}
}

// ---------------------------------------------------------------------------
// GetVerbInfos
// ---------------------------------------------------------------------------

func TestGetVerbInfos_OnlyRegisteredVerbs(t *testing.T) {
	r := registryWithNoop(t)
	r.Register(wfSpec(VerbCreate, "pipeline", "pipeline"))
	for _, vi := range r.GetVerbInfos() {
		if vi.Verb == VerbCreate {
			return
		}
	}
	t.Fatal("GetVerbInfos() did not include VerbCreate")
}

func TestGetVerbInfos_EmptyVerbsExcluded(t *testing.T) {
	r := New()
	for _, vi := range r.GetVerbInfos() {
		if len(r.specs[vi.Verb]) == 0 {
			t.Errorf("GetVerbInfos includes verb %q with no registered specs", vi.Verb)
		}
	}
}

// ---------------------------------------------------------------------------
// Resolve* helpers — five nil-return tests grouped; FetchFn variants stay separate
// ---------------------------------------------------------------------------

// TestResolveMapFns_NotFound: five resolver methods all return a nil func value for
// unregistered keys. Use a bool-returning closure to avoid the interface-nil pitfall.
func TestResolveMapFns_NotFound(t *testing.T) {
	tests := []struct {
		name  string
		isNil func(*Registry) bool
	}{
		{"text_formatter", func(r *Registry) bool { return r.ResolveTextFormatter("missing") == nil }},
		{"body_fn", func(r *Registry) bool { return r.ResolveBodyFn("missing") == nil }},
		{"query_params_fn", func(r *Registry) bool { return r.ResolveQueryParamsFn("missing") == nil }},
		{"flag_resolve_fn", func(r *Registry) bool { return r.ResolveFlagResolveFn("missing") == nil }},
		{"endpoint_validator", func(r *Registry) bool { return r.ResolveEndpointValidator("missing") == nil }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if !tc.isNil(New()) {
				t.Errorf("Resolve %s(missing) should return nil for unregistered key", tc.name)
			}
		})
	}
}

func TestResolveFetchFn_NotFound(t *testing.T) {
	if _, err := New().ResolveFetchFn("missing"); err == nil {
		t.Fatal("expected error for unregistered fetch fn")
	}
}

func TestResolveFetchFn_Found(t *testing.T) {
	r := New()
	r.RegisterFetchFn("test:fetch", func(*cmdctx.Ctx, *spec.EndpointSpec, int, int, any) (*cmdctx.PageResult, error) {
		return nil, nil
	})
	fn, err := r.ResolveFetchFn("test:fetch")
	if err != nil || fn == nil {
		t.Fatalf("ResolveFetchFn: err=%v fn=%v", err, fn)
	}
}

// ---------------------------------------------------------------------------
// Register* panic on duplicate — all 9 grouped by shared structure
// ---------------------------------------------------------------------------

// TestRegisterFns_PanicOnDuplicate: every Register* method panics when the same key is
// registered twice. The `register` func is called once to seed, once to trigger the panic.
func TestRegisterFns_PanicOnDuplicate(t *testing.T) {
	tests := []struct {
		name     string
		register func(*Registry)
	}{
		{"workflow", func(r *Registry) {
			r.RegisterWorkflow("core:wf", func(*cmdctx.Ctx) error { return nil })
		}},
		{"body_fn", func(r *Registry) {
			r.RegisterBodyFn("core:b", func(*cmdctx.Ctx) (any, error) { return nil, nil })
		}},
		{"query_params_fn", func(r *Registry) {
			r.RegisterQueryParamsFn("core:q", func(*cmdctx.Ctx) (map[string]string, error) { return nil, nil })
		}},
		{"follow_fn", func(r *Registry) {
			r.RegisterFollowFn("core:f", func(*cmdctx.Ctx, any) error { return nil })
		}},
		{"fetch_fn", func(r *Registry) {
			r.RegisterFetchFn("core:fetch", func(*cmdctx.Ctx, *spec.EndpointSpec, int, int, any) (*cmdctx.PageResult, error) {
				return nil, nil
			})
		}},
		{"flag_completion_fn", func(r *Registry) {
			r.RegisterFlagCompletionFn("core:comp", func(*cmdctx.Ctx, []string, *pflag.FlagSet) ([]string, error) { return nil, nil })
		}},
		{"flag_resolve_fn", func(r *Registry) {
			r.RegisterFlagResolveFn("core:rv", func(*cmdctx.Ctx, string) (string, error) { return "", nil })
		}},
		{"text_formatter", func(r *Registry) {
			r.RegisterTextFormatter("core:tf", func(io.Writer, cmdctx.DataAccessor) error { return nil })
		}},
		{"endpoint_validator_fn", func(r *Registry) {
			r.RegisterEndpointValidatorFn("core:ev", func(*cmdctx.Ctx, cmdctx.EndpointRequest) error { return nil })
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := New()
			tc.register(r) // first registration succeeds
			defer func() {
				if rec := recover(); rec == nil {
					t.Errorf("expected panic on duplicate %s registration, got none", tc.name)
				}
			}()
			tc.register(r) // duplicate — must panic
		})
	}
}

// ---------------------------------------------------------------------------
// unknownNounError
// ---------------------------------------------------------------------------

func TestUnknownNounError(t *testing.T) {
	tests := []struct {
		name        string
		setup       func(*Registry)
		verb, noun  string
		wantContain string // empty → assert "Did you mean:" is absent
	}{
		{
			name: "noun_registered_wrong_verb",
			setup: func(r *Registry) {
				r.RegisterNoun(spec.NounDef{Noun: "pipeline"})
				r.Register(wfSpec(VerbList, "pipeline", "pipeline"))
			},
			verb: VerbCreate, noun: "pipeline",
			wantContain: `"pipeline" is not supported for "create"`,
		},
		{
			name: "completely_unknown_noun",
			setup: func(r *Registry) {
				r.Register(wfSpec(VerbList, "pipeline", "pipeline"))
			},
			verb: VerbList, noun: "xyz",
			wantContain: `"xyz" is not a valid noun for "list"`,
		},
		{
			name: "suggests_close_match",
			setup: func(r *Registry) {
				r.RegisterNoun(spec.NounDef{Noun: "pipeline"})
				r.Register(wfSpec(VerbList, "pipeline", "pipeline"))
			},
			verb: VerbList, noun: "pipelyne",
			wantContain: "Did you mean:",
		},
		{
			name: "no_suggestion_for_unrelated_input",
			setup: func(r *Registry) {
				r.Register(wfSpec(VerbList, "pipeline", "pipeline"))
			},
			verb: VerbList, noun: "zzzzzzz",
			wantContain: "", // assert "Did you mean:" is absent
		},
		{
			name: "alias_resolved_before_lookup",
			setup: func(r *Registry) {
				r.RegisterNoun(spec.NounDef{Noun: "pipeline", NounAliases: []string{"pipelines"}})
				r.Register(wfSpec(VerbList, "pipeline", "pipeline"))
			},
			verb: VerbCreate, noun: "pipelines",
			wantContain: "not supported for",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := registryWithNoop(t)
			tc.setup(r)
			err := r.unknownNounError(tc.verb, tc.noun)
			if tc.wantContain == "" {
				if strings.Contains(err.Error(), "Did you mean:") {
					t.Errorf("expected no suggestion, got: %v", err)
				}
			} else if !strings.Contains(err.Error(), tc.wantContain) {
				t.Errorf("err = %q, want to contain %q", err.Error(), tc.wantContain)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// buildUseString — pure table: zero setup variation, all inputs → output checks
// ---------------------------------------------------------------------------

func TestBuildUseString(t *testing.T) {
	tests := []struct {
		name        string
		cs          *spec.CommandSpec
		vs          VerbSpec
		wantEqual   string
		wantContain string
		wantAbsent  string
	}{
		{
			name:      "leaf_verb_no_noun",
			cs:        &spec.CommandSpec{Verb: VerbVersion},
			vs:        verbRegistry[VerbVersion],
			wantEqual: VerbVersion,
		},
		{
			name:      "verb_with_noun_no_id_requirement",
			cs:        &spec.CommandSpec{Verb: VerbDescribe, Noun: "pipeline"},
			vs:        verbRegistry[VerbDescribe],
			wantEqual: "pipeline",
		},
		{
			name:        "requires_id_default_label",
			cs:          &spec.CommandSpec{Verb: VerbGet, Noun: "pipeline"},
			vs:          verbRegistry[VerbGet],
			wantContain: "<id>",
		},
		{
			name:        "requires_id_custom_label",
			cs:          &spec.CommandSpec{Verb: VerbGet, Noun: "pipeline", IdLabel: "<pipeline-id>"},
			vs:          verbRegistry[VerbGet],
			wantContain: "<pipeline-id>",
		},
		{
			name:       "requires_id_suppressed_by_no_id",
			cs:         &spec.CommandSpec{Verb: VerbGet, Noun: "pipeline", NoId: true},
			vs:         verbRegistry[VerbGet],
			wantAbsent: "<id>",
		},
		{
			name:        "allows_id_optional_bracket",
			cs:          &spec.CommandSpec{Verb: VerbCreate, Noun: "pipeline"},
			vs:          verbRegistry[VerbCreate],
			wantContain: "[id]",
		},
		{
			name:        "allows_parentid_optional",
			cs:          &spec.CommandSpec{Verb: VerbList, Noun: "stage"},
			vs:          verbRegistry[VerbList],
			wantContain: "[parentid]",
		},
		{
			name:        "allows_parentid_required",
			cs:          &spec.CommandSpec{Verb: VerbList, Noun: "stage", RequiresParentId: true},
			vs:          verbRegistry[VerbList],
			wantContain: "<parentid>",
		},
		{
			name:        "allows_parentid_custom_label",
			cs:          &spec.CommandSpec{Verb: VerbList, Noun: "stage", ParentIdLabel: "pipeline-id"},
			vs:          verbRegistry[VerbList],
			wantContain: "pipeline-id",
		},
		{
			name:        "requires_id_with_args_label",
			cs:          &spec.CommandSpec{Verb: VerbGet, Noun: "pipeline", HasArgs: true, ArgsLabel: "<file>"},
			vs:          verbRegistry[VerbGet],
			wantContain: "<file>",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := buildUseString(tc.cs, tc.vs)
			if tc.wantEqual != "" && got != tc.wantEqual {
				t.Errorf("buildUseString = %q, want %q", got, tc.wantEqual)
			}
			if tc.wantContain != "" && !strings.Contains(got, tc.wantContain) {
				t.Errorf("buildUseString = %q, want to contain %q", got, tc.wantContain)
			}
			if tc.wantAbsent != "" && strings.Contains(got, tc.wantAbsent) {
				t.Errorf("buildUseString = %q, should not contain %q", got, tc.wantAbsent)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// buildPagingFlags — pure table: same helper setup, differ in flags + paging spec
// ---------------------------------------------------------------------------

func makePagingFlagSet(t *testing.T) *pflag.FlagSet {
	t.Helper()
	fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
	fs.Int("offset", 0, "")
	fs.Int("limit", 0, "")
	fs.Bool("all", false, "")
	fs.Bool("count", false, "")
	return fs
}

var countablePaging = &spec.PagingSpec{PagingStrategy: spec.PagingStrategyPageIndex, Countable: true}
var notCountablePaging = &spec.PagingSpec{PagingStrategy: spec.PagingStrategyPageHeader}

func TestBuildPagingFlags(t *testing.T) {
	tests := []struct {
		name       string
		setFlags   func(*pflag.FlagSet)
		paging     *spec.PagingSpec
		wantErr    string
		wantOffset int
		wantLimit  int
	}{
		{
			name:       "happy_path",
			setFlags:   func(fs *pflag.FlagSet) { fs.Set("offset", "5"); fs.Set("limit", "10") },
			paging:     countablePaging,
			wantOffset: 5, wantLimit: 10,
		},
		{
			name:     "negative_offset",
			setFlags: func(fs *pflag.FlagSet) { fs.Set("offset", "-1") },
			paging:   countablePaging,
			wantErr:  "--offset must be non-negative",
		},
		{
			name:     "negative_limit",
			setFlags: func(fs *pflag.FlagSet) { fs.Set("limit", "-1") },
			paging:   countablePaging,
			wantErr:  "--limit must be non-negative",
		},
		{
			name:     "all_on_non_countable",
			setFlags: func(fs *pflag.FlagSet) { fs.Set("all", "true") },
			paging:   notCountablePaging,
			wantErr:  "--all is not supported",
		},
		{
			name:     "count_on_non_countable",
			setFlags: func(fs *pflag.FlagSet) { fs.Set("count", "true") },
			paging:   notCountablePaging,
			wantErr:  "--count is not supported",
		},
		{
			name:     "all_incompatible_with_offset",
			setFlags: func(fs *pflag.FlagSet) { fs.Set("all", "true"); fs.Set("offset", "5") },
			paging:   countablePaging,
			wantErr:  "--all is incompatible",
		},
		{
			name:     "all_incompatible_with_limit",
			setFlags: func(fs *pflag.FlagSet) { fs.Set("all", "true"); fs.Set("limit", "5") },
			paging:   countablePaging,
			wantErr:  "--all is incompatible",
		},
		{
			name:     "count_incompatible_with_offset",
			setFlags: func(fs *pflag.FlagSet) { fs.Set("count", "true"); fs.Set("offset", "5") },
			paging:   countablePaging,
			wantErr:  "--count is incompatible",
		},
		{
			name:     "count_incompatible_with_all",
			setFlags: func(fs *pflag.FlagSet) { fs.Set("count", "true"); fs.Set("all", "true") },
			paging:   countablePaging,
			wantErr:  "--count is incompatible",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fs := makePagingFlagSet(t)
			tc.setFlags(fs)
			pf, err := buildPagingFlags(fs, tc.paging)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Errorf("buildPagingFlags err = %v, want to contain %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("buildPagingFlags unexpected error: %v", err)
			}
			if pf.Offset != tc.wantOffset || pf.Limit != tc.wantLimit {
				t.Errorf("pf = %+v, want Offset=%d Limit=%d", pf, tc.wantOffset, tc.wantLimit)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// buildFlagValues — t.Run: setups differ (different CommandSpec + FlagSet per case)
// ---------------------------------------------------------------------------

func makeFlagSetForSpec(cs *spec.CommandSpec) *pflag.FlagSet {
	fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
	for _, f := range cs.Flags {
		if f.IsBool {
			fs.Bool(f.Name, false, "")
		} else if f.IsMulti {
			fs.StringArray(f.Name, nil, "")
		} else {
			fs.String(f.Name, f.Default, "")
		}
	}
	return fs
}

func TestBuildFlagValues(t *testing.T) {
	t.Run("string_flag", func(t *testing.T) {
		cs := &spec.CommandSpec{Flags: []spec.Flag{{Name: "env", Default: "dev"}}}
		fs := makeFlagSetForSpec(cs)
		fs.Set("env", "prod")
		if got := buildFlagValues(fs, cs)["env"]; got != "prod" {
			t.Errorf("fv[env] = %v, want prod", got)
		}
	})
	t.Run("bool_flag", func(t *testing.T) {
		cs := &spec.CommandSpec{Flags: []spec.Flag{{Name: "verbose", IsBool: true}}}
		fs := makeFlagSetForSpec(cs)
		fs.Set("verbose", "true")
		if got := buildFlagValues(fs, cs)["verbose"]; got != true {
			t.Errorf("fv[verbose] = %v, want true", got)
		}
	})
	t.Run("multi_flag", func(t *testing.T) {
		cs := &spec.CommandSpec{Flags: []spec.Flag{{Name: "tags", IsMulti: true}}}
		fs := makeFlagSetForSpec(cs)
		fs.Set("tags", "a")
		tags, ok := buildFlagValues(fs, cs)["tags"].([]string)
		if !ok || len(tags) == 0 {
			t.Errorf("fv[tags] = %v, want non-empty []string", buildFlagValues(fs, cs)["tags"])
		}
	})
	t.Run("builtin_page_flag_0indexed", func(t *testing.T) {
		cs := &spec.CommandSpec{BuiltinFlags: spec.BuiltinFlags{Page: true}}
		fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
		fs.Int("page", 1, "")
		fs.Set("page", "3")
		if got := buildFlagValues(fs, cs)["page"]; got != 2 {
			t.Errorf("fv[page] = %v, want 2 (0-indexed from page=3)", got)
		}
	})
	t.Run("format_flag", func(t *testing.T) {
		cs := &spec.CommandSpec{}
		fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
		fs.String("format", "", "")
		fs.Set("format", "json")
		if got := buildFlagValues(fs, cs)["format"]; got != "json" {
			t.Errorf("fv[format] = %v, want json", got)
		}
	})
	t.Run("file_flag", func(t *testing.T) {
		cs := &spec.CommandSpec{}
		fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
		fs.String("file", "", "")
		fs.Set("file", "/tmp/spec.yaml")
		if got := buildFlagValues(fs, cs)["file"]; got != "/tmp/spec.yaml" {
			t.Errorf("fv[file] = %v, want /tmp/spec.yaml", got)
		}
	})
	t.Run("no_auth_injects_profile_org_project", func(t *testing.T) {
		cs := &spec.CommandSpec{NoAuth: true}
		fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
		fs.String("profile", "", "")
		fs.String("org", "", "")
		fs.String("project", "", "")
		fs.Set("profile", "prod")
		if got := buildFlagValues(fs, cs)["profile"]; got != "prod" {
			t.Errorf("fv[profile] = %v, want prod", got)
		}
	})
	t.Run("no_auth_does_not_override_explicit_flag", func(t *testing.T) {
		cs := &spec.CommandSpec{NoAuth: true, Flags: []spec.Flag{{Name: "org"}}}
		fs := makeFlagSetForSpec(cs)
		fs.String("project", "", "")
		fs.Set("org", "from-explicit")
		if got := buildFlagValues(fs, cs)["org"]; got != "from-explicit" {
			t.Errorf("fv[org] = %v, want from-explicit", got)
		}
	})
}

// ---------------------------------------------------------------------------
// authTelemetryFields — pure table: same call, differ only in input + expected fields
// ---------------------------------------------------------------------------

func TestAuthTelemetryFields(t *testing.T) {
	tests := []struct {
		name           string
		input          *auth.ResolvedAuth
		wantAccountID  string
		wantTokenKind  string
		wantAuthSource string
	}{
		{
			name:  "nil_input_returns_empty_strings",
			input: nil,
		},
		{
			name: "env_source",
			input: &auth.ResolvedAuth{
				AccountID: "acc123",
				Email:     "user@example.com",
				TokenKind: auth.TokenKindPAT,
				Source:    auth.SourceEnv,
			},
			wantAccountID:  "acc123",
			wantTokenKind:  "pat",
			wantAuthSource: "env",
		},
		{
			name:           "profile_source",
			input:          &auth.ResolvedAuth{Source: "profile:default", TokenKind: auth.TokenKindSAT},
			wantTokenKind:  "sat",
			wantAuthSource: "profile",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			accountID, _, tokenKind, authSource := authTelemetryFields(tc.input)
			if accountID != tc.wantAccountID {
				t.Errorf("accountID = %q, want %q", accountID, tc.wantAccountID)
			}
			if tokenKind != tc.wantTokenKind {
				t.Errorf("tokenKind = %q, want %q", tokenKind, tc.wantTokenKind)
			}
			if authSource != tc.wantAuthSource {
				t.Errorf("authSource = %q, want %q", authSource, tc.wantAuthSource)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// telemetryAuth
// ---------------------------------------------------------------------------

// AuthAlreadySet checks pointer identity — stays separate from the nil-check cases.
func TestTelemetryAuth_AuthAlreadySet(t *testing.T) {
	a := &auth.ResolvedAuth{AccountID: "acc"}
	if got := telemetryAuth(&spec.CommandSpec{}, &cmdctx.Ctx{Auth: a}); got != a {
		t.Fatal("telemetryAuth should return ctx.Auth unchanged when it is already set")
	}
}

func TestTelemetryAuth(t *testing.T) {
	t.Run("no_auth_false_nil_auth_returns_nil", func(t *testing.T) {
		got := telemetryAuth(&spec.CommandSpec{NoAuth: false}, &cmdctx.Ctx{Auth: nil})
		if got != nil {
			t.Fatalf("expected nil, got %v", got)
		}
	})
}

// ---------------------------------------------------------------------------
// misconfiguredCmd
// ---------------------------------------------------------------------------

func TestMisconfiguredCmd(t *testing.T) {
	tests := []struct {
		name    string
		module  string
		wantErr string
	}{
		{name: "known_module_prefix_in_error", module: "pipeline", wantErr: "misconfigured command"},
		{name: "empty_module_falls_back_to_unknown", module: "", wantErr: "unknown"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cmd := &cobra.Command{Use: "pipeline"}
			cs := &spec.CommandSpec{Module: tc.module}
			misconfiguredCmd(cmd, cs, "something broke")
			if cmd.RunE == nil {
				t.Fatal("misconfiguredCmd must set RunE")
			}
			err := cmd.RunE(cmd, nil)
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("RunE err = %v, want to contain %q", err, tc.wantErr)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// moduleRegistrar — Register*, qualify error branches
// ---------------------------------------------------------------------------

func TestModuleRegistrar_RegisterWorkflow(t *testing.T) {
	r := New()
	m := r.Module("mymod")
	m.RegisterWorkflow("mywf", func(*cmdctx.Ctx) error { return nil })
	if _, ok := r.workflows["mymod:mywf"]; !ok {
		t.Fatal("workflow not found under mymod:mywf after registration")
	}
}

func TestModuleRegistrar_RegisterTextFormatter(t *testing.T) {
	r := New()
	m := r.Module("mymod")
	m.RegisterTextFormatter("fmt", func(io.Writer, cmdctx.DataAccessor) error { return nil })
	if r.ResolveTextFormatter("mymod:fmt") == nil {
		t.Fatal("text formatter not found after registration")
	}
}

func TestModuleRegistrar_RegisterBodyFn(t *testing.T) {
	r := New()
	m := r.Module("mymod")
	m.RegisterBodyFn("body", func(*cmdctx.Ctx) (any, error) { return nil, nil })
	if r.ResolveBodyFn("mymod:body") == nil {
		t.Fatal("body_fn not found after registration")
	}
}

func TestModuleRegistrar_RegisterQueryParamsFn(t *testing.T) {
	r := New()
	m := r.Module("mymod")
	m.RegisterQueryParamsFn("qp", func(*cmdctx.Ctx) (map[string]string, error) { return nil, nil })
	if r.ResolveQueryParamsFn("mymod:qp") == nil {
		t.Fatal("query_params_fn not found after registration")
	}
}

func TestModuleRegistrar_RegisterFollowFn(t *testing.T) {
	r := New()
	m := r.Module("mymod")
	m.RegisterFollowFn("follow", func(*cmdctx.Ctx, any) error { return nil })
	if _, ok := r.followFns["mymod:follow"]; !ok {
		t.Fatal("follow_fn not found after registration")
	}
}

func TestModuleRegistrar_RegisterFetchFn(t *testing.T) {
	r := New()
	m := r.Module("mymod")
	m.RegisterFetchFn("fetch", func(*cmdctx.Ctx, *spec.EndpointSpec, int, int, any) (*cmdctx.PageResult, error) {
		return nil, nil
	})
	if fn, err := r.ResolveFetchFn("mymod:fetch"); err != nil || fn == nil {
		t.Fatalf("fetch_fn not found: err=%v fn=%v", err, fn)
	}
}

func TestModuleRegistrar_RegisterFlagCompletionFn(t *testing.T) {
	r := New()
	m := r.Module("mymod")
	m.RegisterFlagCompletionFn("comp", func(*cmdctx.Ctx, []string, *pflag.FlagSet) ([]string, error) { return nil, nil })
	if _, ok := r.flagCompletionFns["mymod:comp"]; !ok {
		t.Fatal("flag_completion_fn not found after registration")
	}
}

func TestModuleRegistrar_RegisterFlagResolveFn(t *testing.T) {
	r := New()
	m := r.Module("mymod")
	m.RegisterFlagResolveFn("rv", func(*cmdctx.Ctx, string) (string, error) { return "", nil })
	if r.ResolveFlagResolveFn("mymod:rv") == nil {
		t.Fatal("flag_resolve_fn not found after registration")
	}
}

func TestModuleRegistrar_RegisterEndpointValidatorFn(t *testing.T) {
	r := New()
	m := r.Module("mymod")
	m.RegisterEndpointValidatorFn("ev", func(*cmdctx.Ctx, cmdctx.EndpointRequest) error { return nil })
	if r.ResolveEndpointValidator("mymod:ev") == nil {
		t.Fatal("endpoint_validator_fn not found after registration")
	}
}

func TestModuleRegistrar_QualifyErrors(t *testing.T) {
	tests := []struct {
		name    string
		shortID string
	}{
		{
			name:    "reserved core prefix rejected on register",
			shortID: "core:wf",
		},
		{
			name:    "wrong module prefix rejected",
			shortID: "other:wf",
		},
		{
			name:    "double colon rejected",
			shortID: "mymod:x:y",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := New()
			m := r.Module("mymod")
			m.RegisterWorkflow(tc.shortID, func(*cmdctx.Ctx) error { return nil })
			if len(r.initErrs) == 0 {
				t.Fatalf("expected initErr for shortID %q, got none", tc.shortID)
			}
		})
	}
}

func TestModuleRegistrar_Register_QualifiesWorkflowID(t *testing.T) {
	r := New()
	m := r.Module("mymod")
	m.RegisterWorkflow("mywf", func(*cmdctx.Ctx) error { return nil })
	cs := &spec.CommandSpec{
		Command:     "execute thing",
		Verb:        VerbExecute,
		Noun:        "thing",
		HandlerType: spec.HandlerWorkflow,
		WorkflowID:  "mywf", // short ID — should be qualified to mymod:mywf
	}
	if err := m.Register(cs); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if cs.WorkflowID != "mymod:mywf" {
		t.Errorf("WorkflowID = %q, want %q", cs.WorkflowID, "mymod:mywf")
	}
	if cs.Module != "mymod" {
		t.Errorf("Module = %q, want %q", cs.Module, "mymod")
	}
}
