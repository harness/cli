// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package code

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/harness/cli/v3/pkg/auth"
	"github.com/harness/cli/v3/pkg/cmdctx"
)

func TestPRCommentInvalidLocation(t *testing.T) {
	for _, tc := range []struct {
		name  string
		flags map[string]any
		want  string
	}{
		{"path only", map[string]any{"path": "src/main.go"}, "--line"},
		{"line only", map[string]any{"line": "42"}, "--path"},
		{"end only", map[string]any{"end-line": "45"}, "--path"},
		{"side only", map[string]any{"side": "new"}, "--path"},
		{"empty path", map[string]any{"path": ""}, "--path"},
		{"blank path", map[string]any{"path": "  ", "line": "42"}, "--path"},
		{"zero line", map[string]any{"path": "src/main.go", "line": "0"}, "--line"},
		{"negative line", map[string]any{"path": "src/main.go", "line": "-1"}, "--line"},
		{"invalid line", map[string]any{"path": "src/main.go", "line": "42x"}, "--line"},
		{"overflow line", map[string]any{"path": "src/main.go", "line": "999999999999999999999999"}, "--line"},
		{"zero end", map[string]any{"path": "src/main.go", "line": "42", "end-line": "0"}, "--end-line"},
		{"invalid end", map[string]any{"path": "src/main.go", "line": "42", "end-line": "x"}, "--end-line"},
		{"empty end", map[string]any{"path": "src/main.go", "line": "42", "end-line": ""}, "--end-line"},
		{"reversed range", map[string]any{"path": "src/main.go", "line": "42", "end-line": "41"}, "greater than or equal"},
		{"invalid side", map[string]any{"path": "src/main.go", "line": "42", "side": "both"}, "--side"},
		{"empty side", map[string]any{"path": "src/main.go", "line": "42", "side": ""}, "--side"},
		{"reply location", map[string]any{"path": "src/main.go", "line": "42", "reply-to": "123"}, "--reply-to"},
		{"empty reply location", map[string]any{"path": "src/main.go", "line": "42", "reply-to": ""}, "--reply-to"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := &cmdctx.Ctx{FlagValues: tc.flags, ProvidedFlags: map[string]bool{}}
			for key := range tc.flags {
				ctx.ProvidedFlags[key] = true
			}
			// No file or auth: validation must precede input reads and the PR fetch.
			body, err := createPRCommentBodyFn(ctx)
			if err == nil || !strings.Contains(err.Error(), tc.want) || body != nil {
				t.Fatalf("got body %v, error %v; want error containing %q", body, err, tc.want)
			}
		})
	}
}

func TestCreatePRCommentBody(t *testing.T) {
	file := filepath.Join(t.TempDir(), "comment.md")
	if err := os.WriteFile(file, []byte("Review feedback\n\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		flags  map[string]any
		end    int
		old    bool
		inline bool
	}{
		{name: "general"},
		{name: "reply", flags: map[string]any{"reply-to": "123"}},
		{name: "single new", flags: map[string]any{"path": "src/main.go", "line": "42"}, end: 42, inline: true},
		{name: "new range", flags: map[string]any{"path": "src/main.go", "line": "42", "end-line": "45", "side": "new"}, end: 45, inline: true},
		{name: "single old", flags: map[string]any{"path": "src/main.go", "line": "42", "side": "old"}, end: 42, old: true, inline: true},
		{name: "old range", flags: map[string]any{"path": "src/main.go", "line": "42", "end-line": "45", "side": "old"}, end: 45, old: true, inline: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			requests := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				if r.Method != http.MethodGet || r.URL.Path != "/code/api/v1/repos/test-repo/pullreq/42" {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
				}
				for key, value := range map[string]string{"accountIdentifier": "test-account", "orgIdentifier": "test-org", "projectIdentifier": "test-project"} {
					if r.URL.Query().Get(key) != value {
						t.Errorf("incorrect scope parameter %s", key)
					}
				}
				fmt.Fprint(w, `{"source_sha":"source-commit","merge_base_sha":"merge-base-commit"}`)
			}))
			defer srv.Close()
			ctx := &cmdctx.Ctx{
				Context: context.Background(), IdParts: []string{"test-repo", "42"},
				Auth:           &auth.ResolvedAuth{AuthType: auth.AuthTypePAT, APIUrl: srv.URL, AccountID: "test-account", OrgID: "test-org", ProjectID: "test-project"},
				FlagValues:     map[string]any{"file": file, "path": "", "line": "", "end-line": "", "side": "", "reply-to": ""},
				RequestPreview: &bytes.Buffer{},
			}
			for key, value := range tc.flags {
				ctx.SetFlag(key, value)
			}
			body, err := createPRCommentBodyFn(ctx)
			if err != nil {
				t.Fatal(err)
			}
			want := map[string]any{"text": "Review feedback"}
			wantRequests := 0
			if tc.inline {
				wantRequests = 1
				want["path"] = "src/main.go"
				want["line_start"], want["line_end"] = 42, tc.end
				want["line_start_new"], want["line_end_new"] = !tc.old, !tc.old
				want["source_commit_sha"], want["target_commit_sha"] = "source-commit", "merge-base-commit"
			} else if tc.name == "reply" {
				want["parent_id"] = 123
			}
			if !reflect.DeepEqual(body, want) {
				t.Errorf("body = %#v, want %#v", body, want)
			}
			if requests != wantRequests {
				t.Errorf("requests = %d, want %d", requests, wantRequests)
			}
		})
	}
}

func TestCreatePRCommentSHAFailures(t *testing.T) {
	file := filepath.Join(t.TempDir(), "comment.md")
	if err := os.WriteFile(file, []byte("Review feedback"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name     string
		status   int
		response string
		want     string
	}{
		{"fetch failed", http.StatusForbidden, `{"message":"access denied"}`, "fetching PR to get commit SHAs"},
		{"unexpected response", http.StatusOK, `[]`, "unexpected PR response type"},
		{"missing source", http.StatusOK, `{"merge_base_sha":"merge-base-commit"}`, "missing source_sha"},
		{"missing diff base", http.StatusOK, `{"source_sha":"source-commit"}`, "missing merge_base_sha"},
		{"invalid source SHA type", http.StatusOK, `{"source_sha":42,"merge_base_sha":"merge-base-commit"}`, "missing source_sha"},
		{"invalid diff base SHA type", http.StatusOK, `{"source_sha":"source-commit","merge_base_sha":42}`, "missing merge_base_sha"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet {
					t.Errorf("unexpected write: %s", r.Method)
				}
				w.WriteHeader(tc.status)
				fmt.Fprint(w, tc.response)
			}))
			defer srv.Close()
			ctx := &cmdctx.Ctx{
				Context: context.Background(), IdParts: []string{"test-repo", "42"},
				Auth:       &auth.ResolvedAuth{AuthType: auth.AuthTypePAT, APIUrl: srv.URL},
				FlagValues: map[string]any{"file": file, "path": "src/main.go", "line": "42"},
			}
			body, err := createPRCommentBodyFn(ctx)
			if err == nil || !strings.Contains(err.Error(), tc.want) || body != nil {
				t.Fatalf("got body %v, error %v; want error containing %q", body, err, tc.want)
			}
		})
	}
}
