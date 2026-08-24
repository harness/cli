// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package registry

import (
	"strings"
	"testing"

	"github.com/harness/cli/pkg/spec"
)

func TestSuggestTransposition(t *testing.T) {
	r := buildTestRegistry(t)

	tests := []struct {
		name        string
		maybeNoun   string
		maybeVerb   string
		wantOK      bool
		wantContain string
	}{
		{
			name:        "noun-verb swap: pr create",
			maybeNoun:   "pr",
			maybeVerb:   "create",
			wantOK:      true,
			wantContain: "harness create pr",
		},
		{
			name:        "noun-verb swap: pr list",
			maybeNoun:   "pr",
			maybeVerb:   "list",
			wantOK:      true,
			wantContain: "harness list pr",
		},
		{
			name:        "alias used: prs list",
			maybeNoun:   "prs",
			maybeVerb:   "list",
			wantOK:      true,
			wantContain: "harness list pr",
		},
		{
			name:        "alias used: pull_request create",
			maybeNoun:   "pull_request",
			maybeVerb:   "create",
			wantOK:      true,
			wantContain: "harness create pr", // suggestion uses canonical noun
		},
		{
			name:        "noun:variant with verb: pipeline:summary get",
			maybeNoun:   "pipeline:summary",
			maybeVerb:   "get",
			wantOK:      true,
			wantContain: "harness get pipeline:summary",
		},
		{
			name:        "verb:variant transposition: pr list:mine",
			maybeNoun:   "pr",
			maybeVerb:   "list:mine",
			wantOK:      true,
			wantContain: "harness list pr:mine",
		},
		{
			name:        "verb:variant transposition: pr execute:merge",
			maybeNoun:   "pr",
			maybeVerb:   "execute:merge",
			wantOK:      true,
			wantContain: "harness execute pr:merge",
		},
		{
			name:        "variant-only verb: artifact push falls back to bare noun",
			maybeNoun:   "artifact",
			maybeVerb:   "push",
			wantOK:      true,
			wantContain: "harness push artifact",
		},
		{
			name:      "second token not a known verb",
			maybeNoun: "pr",
			maybeVerb: "foobar",
			wantOK:    false,
		},
		{
			name:      "first token not a known noun",
			maybeNoun: "foobar",
			maybeVerb: "list",
			wantOK:    false,
		},
		{
			name:      "known noun + known verb but combo not registered",
			maybeNoun: "connector",
			maybeVerb: "create",
			wantOK:    false,
		},
		{
			name:      "already in correct order: create is not a noun",
			maybeNoun: "create",
			maybeVerb: "pr",
			wantOK:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotOK, gotMsg := r.suggestTransposition(tt.maybeNoun, tt.maybeVerb)
			if gotOK != tt.wantOK {
				t.Fatalf("suggestTransposition(%q, %q) ok = %v, want %v (msg: %q)", tt.maybeNoun, tt.maybeVerb, gotOK, tt.wantOK, gotMsg)
			}
			if tt.wantOK && !strings.Contains(gotMsg, tt.wantContain) {
				t.Errorf("suggestTransposition(%q, %q) = %q, want to contain %q", tt.maybeNoun, tt.maybeVerb, gotMsg, tt.wantContain)
			}
		})
	}
}

func TestSuggestMigrateTransposition(t *testing.T) {
	r := buildTestRegistry(t)

	tests := []struct {
		name        string
		maybeNoun   string
		maybeVerb   string
		wantOK      bool
		wantContain string
	}{
		{
			name:        "migrate in verb slot",
			maybeNoun:   "github_repo",
			maybeVerb:   "migrate",
			wantOK:      true,
			wantContain: "harness migrate ...",
		},
		{
			name:        "export in verb slot (no export verb exists)",
			maybeNoun:   "repo",
			maybeVerb:   "export",
			wantOK:      true,
			wantContain: "harness migrate ...",
		},
		{
			name:        "import in verb slot (no import verb exists)",
			maybeNoun:   "repo",
			maybeVerb:   "import",
			wantOK:      true,
			wantContain: "harness migrate ...",
		},
		{
			name:        "case-insensitive",
			maybeNoun:   "repo",
			maybeVerb:   "Migrate",
			wantOK:      true,
			wantContain: "harness migrate ...",
		},
		{
			name:      "unrelated verb is not intercepted",
			maybeNoun: "pr",
			maybeVerb: "create",
			wantOK:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotOK, gotMsg := r.suggestMigrateTransposition(tt.maybeNoun, tt.maybeVerb)
			if gotOK != tt.wantOK {
				t.Fatalf("suggestMigrateTransposition(%q, %q) ok = %v, want %v (msg: %q)", tt.maybeNoun, tt.maybeVerb, gotOK, tt.wantOK, gotMsg)
			}
			if tt.wantOK && !strings.Contains(gotMsg, tt.wantContain) {
				t.Errorf("suggestMigrateTransposition(%q, %q) = %q, want to contain %q", tt.maybeNoun, tt.maybeVerb, gotMsg, tt.wantContain)
			}
		})
	}
}

func TestSuggestVerbTypo(t *testing.T) {
	r := buildTestRegistry(t)

	tests := []struct {
		name        string
		first       string
		wantOK      bool
		wantContain string
	}{
		{
			name:        "one transposed letter: creaet",
			first:       "creaet",
			wantOK:      true,
			wantContain: "harness create",
		},
		{
			name:        "one missing letter: lsit",
			first:       "lsit",
			wantOK:      true,
			wantContain: "harness list",
		},
		{
			name:        "one extra letter: listt",
			first:       "listt",
			wantOK:      true,
			wantContain: "harness list",
		},
		{
			name:   "exact match is not a typo",
			first:  "create",
			wantOK: false,
		},
		{
			name:   "too far from any known verb",
			first:  "creaxyz",
			wantOK: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotOK, gotMsg := r.suggestVerbTypo(tt.first)
			if gotOK != tt.wantOK {
				t.Fatalf("suggestVerbTypo(%q) ok = %v, want %v (msg: %q)", tt.first, gotOK, tt.wantOK, gotMsg)
			}
			if tt.wantOK && !strings.Contains(gotMsg, tt.wantContain) {
				t.Errorf("suggestVerbTypo(%q) = %q, want to contain %q", tt.first, gotMsg, tt.wantContain)
			}
		})
	}
}

func TestSuggestPluginOwnedNoun(t *testing.T) {
	r := buildTestRegistry(t)
	r.RecordPluginOwnedNouns("har", []spec.NounDef{{Noun: "registry"}, {Noun: "artifact"}})

	tests := []struct {
		name        string
		first       string
		wantOK      bool
		wantContain string
	}{
		{
			name:        "noun-first form",
			first:       "registry",
			wantOK:      true,
			wantContain: `"registry" is provided by the "har" plugin, which isn't installed`,
		},
		{
			name:        "noun:variant-first form",
			first:       "registry:npm",
			wantOK:      true,
			wantContain: `"registry:npm" is provided by the "har" plugin, which isn't installed`,
		},
		{
			name:   "already registered noun is not intercepted",
			first:  "artifact", // registered by buildTestRegistry, simulating an installed plugin
			wantOK: false,
		},
		{
			name:   "unrelated unknown token has no recorded owner",
			first:  "foobar",
			wantOK: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotOK, gotMsg := r.suggestPluginOwnedNoun(tt.first)
			if gotOK != tt.wantOK {
				t.Fatalf("suggestPluginOwnedNoun(%q) ok = %v, want %v (msg: %q)", tt.first, gotOK, tt.wantOK, gotMsg)
			}
			if tt.wantOK && !strings.Contains(gotMsg, tt.wantContain) {
				t.Errorf("suggestPluginOwnedNoun(%q) = %q, want to contain %q", tt.first, gotMsg, tt.wantContain)
			}
		})
	}
}
