// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package rt

import (
	"testing"

	"github.com/harness/cli/v3/pkg/cmdctx"
)

func filtersFor(t *testing.T, tags any) map[string]string {
	t.Helper()
	flags := map[string]any{}
	if tags != nil {
		flags["tag"] = tags
	}
	qp, err := loadTestFilterParams(&cmdctx.Ctx{FlagValues: flags})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	return qp
}

func TestLoadTestFiltersOmitsAnEmptyTagList(t *testing.T) {
	for name, tags := range map[string]any{
		"flag absent": nil,
		"empty slice": []string{},
		"blank value": []string{""},
		"only spaces": []string{"  "},
		"empty parts": []string{",", " , "},
	} {
		t.Run(name, func(t *testing.T) {
			if qp := filtersFor(t, tags); len(qp) != 0 {
				t.Errorf("got %v, want no tags param at all", qp)
			}
		})
	}
}

func TestLoadTestFiltersJoinsWithCommas(t *testing.T) {
	cases := map[string]struct {
		tags []string
		want string
	}{
		"one":              {[]string{"smoke"}, "smoke"},
		"repeated flag":    {[]string{"smoke", "nightly"}, "smoke,nightly"},
		"comma in one":     {[]string{"smoke,nightly"}, "smoke,nightly"},
		"mixed spellings":  {[]string{"smoke,nightly", "weekly"}, "smoke,nightly,weekly"},
		"padding trimmed":  {[]string{" smoke , nightly "}, "smoke,nightly"},
		"blanks dropped":   {[]string{"smoke", "", "nightly"}, "smoke,nightly"},
		"order is kept":    {[]string{"b", "a"}, "b,a"},
		"duplicates kept":  {[]string{"smoke", "smoke"}, "smoke,smoke"},
		"inner space kept": {[]string{"needs review"}, "needs review"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			qp := filtersFor(t, tc.tags)
			if qp["tags"] != tc.want {
				t.Errorf("tags = %q, want %q", qp["tags"], tc.want)
			}
			if len(qp) != 1 {
				t.Errorf("got %v, want only the tags param", qp)
			}
		})
	}
}

func TestLoadTestFiltersSpellingsAgree(t *testing.T) {
	repeated := filtersFor(t, []string{"smoke", "nightly"})
	comma := filtersFor(t, []string{"smoke,nightly"})
	if repeated["tags"] != comma["tags"] {
		t.Errorf("--tag smoke --tag nightly gave %q but --tag smoke,nightly gave %q",
			repeated["tags"], comma["tags"])
	}
}
