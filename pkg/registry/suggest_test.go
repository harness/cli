// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package registry

import (
	"strings"
	"testing"

	"github.com/harness/cli/pkg/cmdctx"
	"github.com/harness/cli/pkg/spec"
)

// buildTestRegistry creates a minimal registry with a few nouns and commands
// for use in SuggestRootCommand tests.
func buildTestRegistry(t *testing.T) *Registry {
	t.Helper()
	r := New()
	for _, nd := range []spec.NounDef{
		{Noun: "pr", NounAliases: []string{"prs", "pull_request"}},
		{Noun: "pipeline", NounAliases: []string{"pipelines"}},
		{Noun: "connector"},
		{Noun: "artifact"},
	} {
		if err := r.RegisterNoun(nd); err != nil {
			t.Fatalf("RegisterNoun %q: %v", nd.Noun, err)
		}
	}
	// Use workflow handler to avoid endpoint validation requirements in tests.
	wfID := "test:noop"
	r.RegisterWorkflow(wfID, func(*cmdctx.Ctx) error { return nil })
	for _, cs := range []*spec.CommandSpec{
		{Command: "create pr", Verb: VerbCreate, Noun: "pr", Module: "code", HandlerType: spec.HandlerWorkflow, WorkflowID: wfID},
		{Command: "list pr", Verb: VerbList, Noun: "pr", Module: "code", HandlerType: spec.HandlerWorkflow, WorkflowID: wfID},
		{Command: "list pr:mine", Verb: VerbList, Noun: "pr", NounVariant: "mine", Module: "code", HandlerType: spec.HandlerWorkflow, WorkflowID: wfID},
		{Command: "get pr", Verb: VerbGet, Noun: "pr", Module: "code", HandlerType: spec.HandlerWorkflow, WorkflowID: wfID},
		{Command: "execute pr:merge", Verb: VerbExecute, Noun: "pr", NounVariant: "merge", Module: "code", HandlerType: spec.HandlerWorkflow, WorkflowID: wfID},
		{Command: "create pipeline", Verb: VerbCreate, Noun: "pipeline", Module: "pipeline", HandlerType: spec.HandlerWorkflow, WorkflowID: wfID},
		{Command: "list pipeline", Verb: VerbList, Noun: "pipeline", Module: "pipeline", HandlerType: spec.HandlerWorkflow, WorkflowID: wfID},
		{Command: "get pipeline:summary", Verb: VerbGet, Noun: "pipeline", NounVariant: "summary", Module: "pipeline", HandlerType: spec.HandlerWorkflow, WorkflowID: wfID},
		{Command: "list connector", Verb: VerbList, Noun: "connector", Module: "platform", HandlerType: spec.HandlerWorkflow, WorkflowID: wfID},
		{Command: "list artifact", Verb: VerbList, Noun: "artifact", Module: "har", HandlerType: spec.HandlerWorkflow, WorkflowID: wfID},
		{Command: "push artifact:generic", Verb: VerbPush, Noun: "artifact", NounVariant: "generic", Module: "har", HandlerType: spec.HandlerWorkflow, WorkflowID: wfID},
		{Command: "push artifact:npm", Verb: VerbPush, Noun: "artifact", NounVariant: "npm", Module: "har", HandlerType: spec.HandlerWorkflow, WorkflowID: wfID},
	} {
		if err := r.Register(cs); err != nil {
			t.Fatalf("Register %s %s: %v", cs.Verb, cs.Noun, err)
		}
	}
	return r
}

// TestSuggestRootCommand_Wiring is a thin integration smoke test confirming
// SuggestRootCommand correctly locates the verb/noun tokens via IndexVerbNoun
// and dispatches to the right suggest* function. The exhaustive case matrix
// for each kind of suggestion lives in suggest_logic_test.go (pure
// string-in/string-out, no flag combinations to worry about) and
// flag_test.go (token-index extraction).
func TestSuggestRootCommand_Wiring(t *testing.T) {
	r := buildTestRegistry(t)

	tests := []struct {
		name        string
		args        []string
		wantContain string
	}{
		{
			name:        "transposition, with flags interspersed",
			args:        []string{"--profile", "prod", "pr", "create", "--set", "title=foo"},
			wantContain: "harness create pr",
		},
		{
			name:        "migrate transposition beats generic transposition",
			args:        []string{"github_repo", "migrate", "scm_bundle:repo"},
			wantContain: "harness migrate ...",
		},
		{
			name:        "export in verb slot, no export verb exists",
			args:        []string{"repo", "export", "scm_bundle:repo"},
			wantContain: "harness migrate ...",
		},
		{
			name:        "verb typo",
			args:        []string{"creaet", "pipeline"},
			wantContain: "harness create",
		},
		{
			name:        "plugin-owned noun",
			args:        []string{"registry", "list"},
			wantContain: `"registry" is provided by the "har" plugin, which isn't installed`,
		},
	}

	r.RecordPluginOwnedNouns("har", []spec.NounDef{{Noun: "registry"}})
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := r.SuggestRootCommand(tt.args)
			if got == "" {
				t.Fatal("expected a suggestion, got empty string")
			}
			if !strings.Contains(got, tt.wantContain) {
				t.Errorf("suggestion %q does not contain %q", got, tt.wantContain)
			}
		})
	}
}

// registered_noun_not_intercepted verifies that a noun already registered
// (e.g. "artifact", registered by buildTestRegistry to simulate an installed
// plugin) is never misreported as missing, even when it also appears in the
// pluginOwnedNouns map.
func TestSuggestRootCommand_PluginOwnedNoun_AlreadyInstalled(t *testing.T) {
	r := buildTestRegistry(t)
	r.RecordPluginOwnedNouns("har", []spec.NounDef{{Noun: "artifact"}})

	got := r.SuggestRootCommand([]string{"artifact", "list"})
	if strings.Contains(got, "isn't installed") {
		t.Errorf("expected no plugin-missing suggestion for installed noun, got %q", got)
	}
}

// TestSuggestRootCommand_VerbTypoBeatsPluginOwnedNoun guards the priority fix:
// a plugin-owned noun name that also happens to be a near-typo of a real verb
// (e.g. a hypothetical noun "got", one edit away from "get") must surface the
// verb-typo suggestion, not a misleading "plugin not installed" message.
func TestSuggestRootCommand_VerbTypoBeatsPluginOwnedNoun(t *testing.T) {
	r := buildTestRegistry(t)
	r.RecordPluginOwnedNouns("hypothetical", []spec.NounDef{{Noun: "got"}})

	got := r.SuggestRootCommand([]string{"got", "pipeline"})
	if strings.Contains(got, "isn't installed") {
		t.Errorf("expected verb-typo suggestion, got plugin-owned-noun message: %q", got)
	}
	if !strings.Contains(got, "harness get") {
		t.Errorf("suggestion %q does not contain %q", got, "harness get")
	}
}

func TestSuggestRootCommand_NoSuggestion(t *testing.T) {
	r := buildTestRegistry(t)

	tests := []struct {
		name string
		args []string
	}{
		{
			name: "completely unknown command",
			args: []string{"foobar"},
		},
		{
			name: "noun alone with no second arg",
			args: []string{"pr"},
		},
		{
			name: "noun + unknown second arg (not a verb)",
			args: []string{"pr", "foobar"},
		},
		{
			name: "valid verb + noun (correct order, should not intercept)",
			args: []string{"create", "pr"},
		},
		{
			name: "empty args",
			args: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := r.SuggestRootCommand(tt.args)
			if got != "" {
				t.Errorf("expected no suggestion, got %q", got)
			}
		})
	}
}
