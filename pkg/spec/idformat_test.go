// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package spec

import (
	"reflect"
	"strings"
	"testing"
)

func mustFormats(t *testing.T, raws ...string) []IdFormat {
	t.Helper()
	formats, err := ParseIdFormats(raws)
	if err != nil {
		t.Fatalf("ParseIdFormats(%q): %v", raws, err)
	}
	return formats
}

func TestParseId(t *testing.T) {
	branch := []string{"<repo>/<branch...>"}
	har := []string{"<registry>/<name...>/<version>"}
	lead := []string{"<path...>/<file>"}
	exec := []string{"<pipeline>/<execution>", "<execution>"}
	tests := []struct {
		name    string
		formats []string
		input   string
		want    []string
		ids     map[string]string
		rest    int
		wantErr string
	}{
		{name: "rest at end", formats: branch, input: "r/feature/x", want: []string{"r", "feature/x"}, rest: 2},
		{name: "rest at end single segment", formats: branch, input: "r/main", want: []string{"r", "main"}, rest: 1},
		{name: "rest in middle", formats: har, input: "reg/@scope/pkg/1.0", want: []string{"reg", "@scope/pkg", "1.0"}, rest: 2},
		{name: "rest at start", formats: lead, input: "a/b/c.txt", want: []string{"a/b", "c.txt"}, rest: 2},
		{name: "too few segments", formats: har, input: "reg/pkg", wantErr: `does not match <registry>/<name...>/<version>`},
		{name: "optional leading part present", formats: exec, input: "p/e", want: []string{"p", "e"},
			ids: map[string]string{"pipeline": "p", "execution": "e"}},
		{name: "optional leading part absent", formats: exec, input: "e", want: []string{"e"},
			ids: map[string]string{"pipeline": "", "execution": "e"}},
		{name: "no format matched lists all", formats: exec, input: "a/b/c", wantErr: "<pipeline>/<execution> | <execution>"},
		{name: "literal percent", formats: branch, input: "r/a%2Fb", want: []string{"r", "a%2Fb"}, rest: 1},
		{name: "json form", formats: har, input: `["reg","@scope/pkg","1.0/beta"]`, want: []string{"reg", "@scope/pkg", "1.0/beta"}},
		{name: "json numeric elements", formats: exec, input: `["p", 42]`, want: []string{"p", "42"}},
		{name: "json surrounding whitespace", formats: exec, input: "  [\"e\"]\n", want: []string{"e"}},
		{name: "json wrong length", formats: exec, input: `["a","b","c"]`, wantErr: "has 3 parts"},
		{name: "json malformed never falls back", formats: branch, input: `[r/main]`, wantErr: "invalid JSON id"},
		{name: "json non-scalar element", formats: exec, input: `[["e"]]`, wantErr: "element 0"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, err := ParseId(mustFormats(t, tt.formats...), tt.input)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("ParseId(%q) err = %v, want %q", tt.input, err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseId(%q): %v", tt.input, err)
			}
			if !reflect.DeepEqual(p.Values, tt.want) {
				t.Errorf("Values = %q, want %q", p.Values, tt.want)
			}
			if tt.ids != nil && !reflect.DeepEqual(p.Ids, tt.ids) {
				t.Errorf("Ids = %v, want %v", p.Ids, tt.ids)
			}
			if p.RestSegments != tt.rest {
				t.Errorf("RestSegments = %d, want %d", p.RestSegments, tt.rest)
			}
		})
	}
}

func TestParseIdFormatsRejects(t *testing.T) {
	tests := []struct {
		name    string
		formats []string
		wantErr string
	}{
		{"same part count", []string{"<a>/<b>", "<c>/<d>"}, "ambiguous"},
		{"two rest formats", []string{"<a>/<b...>", "<c...>"}, "ambiguous"},
		{"rest overlaps longer fixed format", []string{"<a>/<b...>", "<c>/<d>/<e>"}, "ambiguous"},
		{"two rest parts", []string{"<a...>/<b...>"}, "at most one"},
		{"duplicate name", []string{"<a>/<a>"}, "duplicate part name"},
		{"unbracketed part", []string{"<a>/b"}, "must be <name> or <name...>"},
		{"invalid name", []string{"<a-b>"}, "invalid part name"},
		{"empty format", []string{""}, "empty format"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := ParseIdFormats(tt.formats); err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("ParseIdFormats(%q) err = %v, want %q", tt.formats, err, tt.wantErr)
			}
		})
	}
	if _, err := ParseIdFormats([]string{"<a>/<b>/<c...>", "<a>/<b>", "<b>"}); err != nil {
		t.Fatalf("non-overlapping set rejected: %v", err)
	}
}

func TestIdJoinRoundTrip(t *testing.T) {
	tests := []struct {
		formats   []string
		values    []string
		wantPlain bool
	}{
		{[]string{"<repo>/<branch...>"}, []string{"r", "main"}, true},
		{[]string{"<repo>/<branch...>"}, []string{"r", "feature/x"}, true},
		{[]string{"<repo>/<branch...>"}, []string{"r/x", "main"}, false},
		{[]string{"<registry>/<name...>/<version>"}, []string{"reg", "@scope/pkg", "1.0"}, true},
		{[]string{"<registry>/<name...>/<version>"}, []string{"reg", "@scope/pkg", "1.0/beta"}, false},
		{[]string{"<pipeline>/<execution>", "<execution>"}, []string{"p", "e"}, true},
		{[]string{"<pipeline>/<execution>", "<execution>"}, []string{"a/b"}, false},
		{[]string{"<pipeline>/<execution>", "<execution>"}, []string{"[x]"}, false},
		{[]string{"<a>/<b>"}, []string{`q"<&>`, "1"}, true},
	}
	for _, tt := range tests {
		formats := mustFormats(t, tt.formats...)
		id := IdJoin(formats, tt.values...)
		if plain := !strings.HasPrefix(id, "["); plain != tt.wantPlain {
			t.Errorf("IdJoin(%q) = %q, want plain=%v", tt.values, id, tt.wantPlain)
		}
		p, err := ParseId(formats, id)
		if err != nil || !reflect.DeepEqual(p.Values, tt.values) {
			t.Errorf("ParseId(IdJoin(%q) = %q) = %v, %v; want round trip", tt.values, id, p, err)
		}
	}
}

func TestIdJoinWithoutFormats(t *testing.T) {
	for values, want := range map[[2]string]string{
		{"a", "b"}:   "a/b",
		{"a", "b/c"}: `["a","b/c"]`,
		{"a", ""}:    `["a",""]`,
	} {
		if got := IdJoin(nil, values[:]...); got != want {
			t.Errorf("IdJoin(nil, %q) = %q, want %q", values, got, want)
		}
	}
}

func TestAmbiguityHint(t *testing.T) {
	formats := mustFormats(t, "<registry>/<name...>/<version>")
	p, _ := ParseId(formats, "reg/somepath/name/1.0")
	want := `hint: id "reg/somepath/name/1.0" was read as registry="reg" name="somepath/name" version="1.0"; to pass the parts exactly, use '["reg","somepath/name","1.0"]'`
	if got := p.AmbiguityHint("reg/somepath/name/1.0"); got != want {
		t.Errorf("AmbiguityHint = %q\nwant %q", got, want)
	}
	p, _ = ParseId(formats, "reg/name/1.0")
	if got := p.AmbiguityHint("reg/name/1.0"); got != "" {
		t.Errorf("single-segment rest hint = %q, want empty", got)
	}
}
