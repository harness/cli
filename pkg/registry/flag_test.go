// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package registry

import "testing"

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
