// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package registry

import (
	"testing"

	"github.com/harness/cli/pkg/spec"
)

func TestIndexVerbNoun(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		wantVerb int
		wantNoun int
	}{
		{
			name:     "empty args",
			args:     []string{},
			wantVerb: -1,
			wantNoun: -1,
		},
		{
			name:     "leaf verb, no noun",
			args:     []string{"version"},
			wantVerb: 0,
			wantNoun: -1,
		},
		{
			name:     "plain verb noun",
			args:     []string{"list", "pipeline"},
			wantVerb: 0,
			wantNoun: 1,
		},
		{
			name:     "verb noun with id",
			args:     []string{"get", "pr", "123"},
			wantVerb: 0,
			wantNoun: 1,
		},
		{
			name:     "leading bool flag skipped",
			args:     []string{"--debug", "list", "pipeline"},
			wantVerb: 1,
			wantNoun: 2,
		},
		{
			name:     "leading value flag and its argument skipped",
			args:     []string{"--profile", "prod", "list", "pipeline"},
			wantVerb: 2,
			wantNoun: 3,
		},
		{
			name:     "--flag=value form does not consume next token",
			args:     []string{"--profile=prod", "list", "pipeline"},
			wantVerb: 1,
			wantNoun: 2,
		},
		{
			name:     "bool flag between verb and noun skipped",
			args:     []string{"list", "--all", "pipeline"},
			wantVerb: 0,
			wantNoun: 2,
		},
		{
			name:     "value flag between verb and noun skipped",
			args:     []string{"list", "--columns", "name,org", "pipeline"},
			wantVerb: 0,
			wantNoun: 3,
		},
		{
			name:     "mixed flags before verb and between verb and noun",
			args:     []string{"--debug", "--profile", "prod", "create", "--file", "x.yaml", "pipeline"},
			wantVerb: 3,
			wantNoun: 6,
		},
		{
			name:     "only flags, no positional args at all",
			args:     []string{"--debug", "--profile", "prod"},
			wantVerb: -1,
			wantNoun: -1,
		},
		{
			name:     "noun:variant token",
			args:     []string{"get", "pipeline:summary"},
			wantVerb: 0,
			wantNoun: 1,
		},
		{
			name:     "known bool flag with = form does not consume next token",
			args:     []string{"--debug=true", "list", "pipeline"},
			wantVerb: 1,
			wantNoun: 2,
		},
		{
			name:     "= form with embedded value containing no dash",
			args:     []string{"list", "--columns=name,org", "pipeline"},
			wantVerb: 0,
			wantNoun: 2,
		},
		{
			name:     "short known value flag consumes next token",
			args:     []string{"create", "-f", "x.yaml", "pipeline"},
			wantVerb: 0,
			wantNoun: 3,
		},
		{
			name:     "unknown flag followed by a plain value: value consumed",
			args:     []string{"--unknown", "VALUE", "list", "pipeline"},
			wantVerb: 2,
			wantNoun: 3,
		},
		{
			// Known gotcha: an unrecognized flag placed before the verb swallows
			// the verb token as its value (mirroring pflag's real parse behavior),
			// so "pipeline" gets misidentified as the verb and the noun is lost.
			name:     "unknown flag immediately before the verb swallows it as a value",
			args:     []string{"--unknown", "list", "pipeline"},
			wantVerb: 2,
			wantNoun: -1,
		},
		{
			name:     "unknown flag followed by another flag: does not consume it",
			args:     []string{"--unknown", "--debug", "list", "pipeline"},
			wantVerb: 2,
			wantNoun: 3,
		},
		{
			name:     "unknown flag with no following token at all",
			args:     []string{"list", "pipeline", "--unknown"},
			wantVerb: 0,
			wantNoun: 1,
		},
		{
			name:     "unknown flag with = form does not consume next token",
			args:     []string{"--unknown=value", "list", "pipeline"},
			wantVerb: 1,
			wantNoun: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotVerb, gotNoun := IndexVerbNoun(tt.args)
			if gotVerb != tt.wantVerb || gotNoun != tt.wantNoun {
				t.Errorf("IndexVerbNoun(%v) = (%d, %d), want (%d, %d)", tt.args, gotVerb, gotNoun, tt.wantVerb, tt.wantNoun)
			}
		})
	}
}

func canonicalizeTestRegistry(t *testing.T) *Registry {
	t.Helper()
	r := New()
	if err := r.RegisterNoun(spec.NounDef{Noun: "repository", NounAliases: []string{"repo"}}); err != nil {
		t.Fatalf("RegisterNoun repository: %v", err)
	}
	if err := r.RegisterNoun(spec.NounDef{Noun: "github_organization", NounAliases: []string{"github_org"}}); err != nil {
		t.Fatalf("RegisterNoun github_organization: %v", err)
	}
	return r
}

func TestCanonicalizeNounArg(t *testing.T) {
	r := canonicalizeTestRegistry(t)

	tests := []struct {
		name string
		verb string
		noun string
		want string
	}{
		{
			name: "plain noun alias canonicalized",
			verb: VerbList,
			noun: "repo",
			want: "repository",
		},
		{
			name: "already canonical, unchanged",
			verb: VerbList,
			noun: "repository",
			want: "repository",
		},
		{
			name: "unknown noun passes through unchanged",
			verb: VerbList,
			noun: "widget",
			want: "widget",
		},
		{
			name: "non-pair verb: variant suffix left untouched",
			verb: VerbGet,
			noun: "repo:summary",
			want: "repository:summary",
		},
		{
			name: "pair verb: alias on the from side canonicalized",
			verb: VerbMigrate,
			noun: "scm_bundle:repo",
			want: "scm_bundle:repository",
		},
		{
			name: "pair verb: aliases on both sides canonicalized",
			verb: VerbMigrate,
			noun: "github_org:repo",
			want: "github_organization:repository",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := r.CanonicalizeNounArg(tt.verb, tt.noun)
			if got != tt.want {
				t.Errorf("CanonicalizeNounArg(%q, %q) = %q, want %q", tt.verb, tt.noun, got, tt.want)
			}
		})
	}
}

func TestCanonicalizeNounArgs(t *testing.T) {
	r := canonicalizeTestRegistry(t)

	tests := []struct {
		name string
		args []string
		want []string
	}{
		{
			name: "noun token located and rewritten",
			args: []string{"list", "repo"},
			want: []string{"list", "repository"},
		},
		{
			name: "flags before verb and noun preserved",
			args: []string{"--profile", "prod", "migrate", "github_org:repo"},
			want: []string{"--profile", "prod", "migrate", "github_organization:repository"},
		},
		{
			name: "no noun token present, args unchanged",
			args: []string{"version"},
			want: []string{"version"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := r.CanonicalizeNounArgs(tt.args)
			if len(got) != len(tt.want) {
				t.Fatalf("CanonicalizeNounArgs(%v) = %v, want %v", tt.args, got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("CanonicalizeNounArgs(%v) = %v, want %v", tt.args, got, tt.want)
					break
				}
			}
		})
	}
}
