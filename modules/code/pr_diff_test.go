// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package code

import (
	"testing"

	"github.com/harness/cli/v3/pkg/console"
)

func TestColorUnifiedDiff(t *testing.T) {
	paint := func(c console.Color, s string) string {
		name := map[console.Color]string{console.ColorGreen: "G", console.ColorRed: "R", console.ColorCyan: "C"}[c]
		return "<" + name + ">" + s + "</" + name + ">"
	}
	in := "diff --git a/q.sql b/q.sql\n" +
		"--- a/q.sql\n" +
		"+++ b/q.sql\n" +
		"@@ -1,3 +1,3 @@\n" +
		" keep\n" +
		"--- a SQL comment\n" +
		"+++ a new line starting with plus\n" +
		"-old\n" +
		"+new\n" +
		"diff --git a/r.txt b/r.txt\n" +
		"--- a/r.txt\n" +
		"+++ b/r.txt\n"
	want := "diff --git a/q.sql b/q.sql\n" +
		"--- a/q.sql\n" +
		"+++ b/q.sql\n" +
		"<C>@@ -1,3 +1,3 @@</C>\n" +
		" keep\n" +
		"<R>--- a SQL comment</R>\n" +
		"<G>+++ a new line starting with plus</G>\n" +
		"<R>-old</R>\n" +
		"<G>+new</G>\n" +
		"diff --git a/r.txt b/r.txt\n" +
		"--- a/r.txt\n" +
		"+++ b/r.txt\n"
	if got := colorUnifiedDiff(in, paint); got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}
