// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package code

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/harness/cli/v3/pkg/cmdctx"
	"github.com/harness/cli/v3/pkg/console"
)

const testDiff = "diff --git a/a.txt b/a.txt\n--- a/a.txt\n+++ b/a.txt\n@@ -1 +1 @@\n-old\n+new\n"

type seenRequest struct {
	path   string
	accept string
	query  url.Values
}

// prDiffCtx points insightTestCtx at a server answering every request with status/body, and
// sends the command's output to a file so it can be read back uncolored.
func prDiffCtx(t *testing.T, status int, body string) (*cmdctx.Ctx, *seenRequest, string) {
	t.Helper()
	seen := &seenRequest{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*seen = seenRequest{path: r.URL.Path, accept: r.Header.Get("Accept"), query: r.URL.Query()}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)

	out := filepath.Join(t.TempDir(), "out.diff")
	ctx := insightTestCtx(srv.URL, "")
	ctx.FormatFlags.OutFile = out
	return ctx, seen, out
}

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

func TestGetPRDiffWorkflow_TextRequestsOnePathAsPlainText(t *testing.T) {
	ctx, seen, out := prDiffCtx(t, http.StatusOK, testDiff)
	ctx.FlagValues = map[string]any{"text": true}
	ctx.Ids = map[string]string{"repo_id": "r", "pr_number": "7", "path": "dir/a.txt"}
	// Text mode must not depend on the stats endpoint spec being resolvable.
	ctx.Resolver = noopResolver{}

	if err := getPRDiffWorkflow(ctx); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if seen.path != "/code/api/v1/repos/r/pullreq/7/diff" {
		t.Errorf("path = %q", seen.path)
	}
	if seen.accept != "text/plain" {
		t.Errorf("Accept = %q, want text/plain", seen.accept)
	}
	for k, want := range map[string]string{"path": "dir/a.txt", "orgIdentifier": "org", "projectIdentifier": "proj", "accountIdentifier": "acct"} {
		if got := seen.query.Get(k); got != want {
			t.Errorf("query %s = %q, want %q", k, got, want)
		}
	}
	if got, _ := os.ReadFile(out); string(got) != testDiff {
		t.Errorf("output = %q, want the diff written through uncolored", got)
	}
}

func TestGetPRDiffTextWorkflow_WholePRSendsNoPathFilter(t *testing.T) {
	ctx, seen, out := prDiffCtx(t, http.StatusOK, testDiff)
	ctx.IdParts = []string{"r", "7"}

	if err := getPRDiffTextWorkflow(ctx); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := seen.query["path"]; ok {
		t.Errorf("whole-PR diff sent a path filter: %v", seen.query["path"])
	}
	if got, _ := os.ReadFile(out); string(got) != testDiff {
		t.Errorf("output = %q", got)
	}
}

func TestGetPRDiffTextWorkflow_RejectsMalformedId(t *testing.T) {
	ctx, _, _ := prDiffCtx(t, http.StatusOK, testDiff)
	ctx.IdParts = []string{"r"}
	if err := getPRDiffTextWorkflow(ctx); err == nil {
		t.Fatal("expected an error for an id without a PR number")
	}
}

func TestWritePRDiffText_APIErrorIsReportedAndWritesNothing(t *testing.T) {
	ctx, _, out := prDiffCtx(t, http.StatusNotFound, `{"message":"pull request not found"}`)

	err := writePRDiffText(ctx, "r", "99999", "")
	if err == nil || !strings.Contains(err.Error(), "404") || !strings.Contains(err.Error(), "pull request not found") {
		t.Fatalf("error = %v, want the 404 and the API message", err)
	}
	if _, statErr := os.Stat(out); statErr == nil {
		t.Error("output file was created for a failed request")
	}
}

func TestGetPRDiffWorkflow_StatsModeNeedsTheGetSpec(t *testing.T) {
	ctx, seen, _ := prDiffCtx(t, http.StatusOK, testDiff)
	ctx.Resolver = noopResolver{}

	err := getPRDiffWorkflow(ctx)
	if err == nil || !strings.Contains(err.Error(), "spec not found") {
		t.Fatalf("error = %v, want the missing-spec error", err)
	}
	if seen.path != "" {
		t.Errorf("stats mode called the raw diff route %q", seen.path)
	}
}
