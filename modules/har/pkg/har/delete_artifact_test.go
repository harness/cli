// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package har

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/harness/cli/v3/pkg/auth"
	"github.com/harness/cli/v3/pkg/cmdctx"
)

func bulkDeleteTestCtx(flags map[string]any, apiURL string) *cmdctx.Ctx {
	return &cmdctx.Ctx{
		Context:    context.Background(),
		Args:       []string{"nginx-*"},
		FlagValues: flags,
		Auth: &auth.ResolvedAuth{
			AuthType:  auth.AuthTypePAT,
			APIUrl:    apiURL,
			AccountID: "acct",
			OrgID:     "org",
			ProjectID: "proj",
			PATToken:  "test-token",
		},
	}
}

// recordedCall captures one request's decoded bulkDeleteRequest body.
type recordedCall struct {
	DryRun bool `json:"dryRun"`
}

// bulkDeleteServerOpts shapes the fake server's behavior after the real
// (dryRun=false) call: whether the packages actually disappear, and how many
// the real call reports as failed.
type bulkDeleteServerOpts struct {
	survivesDelete bool
	failed         int
}

func newBulkDeleteServer(t *testing.T, successPackages []string) (*httptest.Server, *[]recordedCall) {
	return newBulkDeleteServerWith(t, successPackages, bulkDeleteServerOpts{})
}

func newBulkDeleteServerWith(t *testing.T, successPackages []string, opts bulkDeleteServerOpts) (*httptest.Server, *[]recordedCall) {
	t.Helper()
	calls := &[]recordedCall{}
	deleted := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var call recordedCall
		_ = json.Unmarshal(body, &call)
		*calls = append(*calls, call)

		matching := successPackages
		if deleted && !opts.survivesDelete {
			matching = nil
		}
		resp := bulkDeleteResponse{
			DryRun:          call.DryRun,
			Success:         len(matching),
			Total:           len(matching),
			SuccessPackages: matching,
			Registry:        "mikereg",
		}
		if !call.DryRun {
			deleted = true
			resp.Failed = opts.failed
			resp.Total += opts.failed
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	t.Cleanup(srv.Close)
	return srv, calls
}

// withStdin temporarily replaces os.Stdin with a pipe pre-loaded with input and
// reports stdin as a terminal, simulating a user typing at the prompt.
func withStdin(t *testing.T, input string) {
	t.Helper()
	withStdinTerminal(t, true)
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("failed to create pipe: %v", err)
	}
	old := os.Stdin
	os.Stdin = r
	t.Cleanup(func() { os.Stdin = old })

	if _, err := w.WriteString(input); err != nil {
		t.Fatalf("failed to write to pipe: %v", err)
	}
	_ = w.Close()
}

func withStdinTerminal(t *testing.T, isTerminal bool) {
	t.Helper()
	old := stdinIsTerminal
	stdinIsTerminal = func() bool { return isTerminal }
	t.Cleanup(func() { stdinIsTerminal = old })
}

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("failed to create pipe: %v", err)
	}
	old := os.Stdout
	os.Stdout = w
	fn()
	os.Stdout = old
	_ = w.Close()
	out, _ := io.ReadAll(r)
	return string(out)
}

func TestBulkDeleteArtifactHandler_ConfirmationGates(t *testing.T) {
	cases := []struct {
		name      string
		flags     map[string]any
		format    string
		terminal  bool
		wantErr   string
		wantCalls int
	}{
		{"--yes with the default dry run is rejected", map[string]any{"yes": true}, "", true, "cannot be combined", 0},
		{"piped stdin cannot confirm a real delete", map[string]any{"dry-run": "false"}, "", false, "--yes", 1},
		{"dry run with --force never prompts", map[string]any{"force": true}, "", false, "", 1},
		{"json real delete without --yes is refused before any call", map[string]any{"dry-run": "false"}, "json", true, "--yes", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv, calls := newBulkDeleteServer(t, []string{"nginx-test"})
			withStdinTerminal(t, tc.terminal)
			tc.flags["registry"] = "mikereg"
			ctx := bulkDeleteTestCtx(tc.flags, srv.URL)
			ctx.FormatFlags.Format = tc.format

			err := bulkDeleteArtifactHandler(ctx)
			if tc.wantErr == "" && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)) {
				t.Fatalf("error = %v, want it to contain %q", err, tc.wantErr)
			}
			if len(*calls) != tc.wantCalls {
				t.Fatalf("expected %d API calls, got %d", tc.wantCalls, len(*calls))
			}
		})
	}
}

func TestBulkDeleteArtifactHandler_MethodNotAllowed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusMethodNotAllowed)
	}))
	t.Cleanup(srv.Close)

	err := bulkDeleteArtifactHandler(bulkDeleteTestCtx(map[string]any{"registry": "mikereg"}, srv.URL))
	if err == nil || !strings.Contains(err.Error(), "nothing was deleted") {
		t.Fatalf("error = %v, want the HTTP 405 explanation", err)
	}
}

func TestBulkDeleteArtifactHandler_JSONReportsMutation(t *testing.T) {
	cases := []struct {
		name        string
		flags       map[string]any
		wantMutated bool
	}{
		{"preview", map[string]any{}, false},
		{"real delete", map[string]any{"dry-run": "false", "yes": true}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv, _ := newBulkDeleteServer(t, []string{"nginx-test"})
			tc.flags["registry"] = "mikereg"
			ctx := bulkDeleteTestCtx(tc.flags, srv.URL)
			ctx.FormatFlags.Format = "json"

			var err error
			out := captureStdout(t, func() { err = bulkDeleteArtifactHandler(ctx) })
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			var got bulkDeleteResult
			if jsonErr := json.Unmarshal([]byte(out), &got); jsonErr != nil {
				t.Fatalf("output is not a single JSON object: %v\n%s", jsonErr, out)
			}
			if got.Mutated != tc.wantMutated || len(got.Packages) != 1 {
				t.Fatalf("got mutated=%v packages=%v, want mutated=%v and 1 package", got.Mutated, got.Packages, tc.wantMutated)
			}
		})
	}
}

func TestBulkDeleteArtifactHandler_DryRunDefault_PreviewOnlyNoPrompt(t *testing.T) {
	srv, calls := newBulkDeleteServer(t, []string{"nginx-test", "nginx-other"})

	ctx := bulkDeleteTestCtx(map[string]any{
		"registry": "mikereg",
	}, srv.URL)

	if err := bulkDeleteArtifactHandler(ctx); err != nil {
		t.Fatalf("expected nil error for dry-run preview, got: %v", err)
	}

	if len(*calls) != 1 {
		t.Fatalf("expected exactly 1 API call for a pure dry-run, got %d", len(*calls))
	}
	if !(*calls)[0].DryRun {
		t.Fatalf("expected the preview call to send dryRun=true")
	}
}

func TestBulkDeleteArtifactHandler_DryRunFalse_ConfirmedYes_Executes(t *testing.T) {
	srv, calls := newBulkDeleteServer(t, []string{"nginx-test", "nginx-other"})
	withStdin(t, "y\n")

	ctx := bulkDeleteTestCtx(map[string]any{
		"registry": "mikereg",
		"dry-run":  "false",
	}, srv.URL)

	if err := bulkDeleteArtifactHandler(ctx); err != nil {
		t.Fatalf("expected nil error after confirming with 'y', got: %v", err)
	}

	if len(*calls) != 3 {
		t.Fatalf("expected 3 API calls (preview + real delete + read-back), got %d", len(*calls))
	}
	if !(*calls)[0].DryRun || (*calls)[1].DryRun || !(*calls)[2].DryRun {
		t.Fatalf("expected call order preview, real delete, read-back preview; got %+v", *calls)
	}
}

func TestBulkDeleteArtifactHandler_DryRunFalse_ConfirmedNo_DoesNotExecute(t *testing.T) {
	srv, calls := newBulkDeleteServer(t, []string{"nginx-test", "nginx-other"})
	withStdin(t, "n\n")

	ctx := bulkDeleteTestCtx(map[string]any{
		"registry": "mikereg",
		"dry-run":  "false",
	}, srv.URL)

	if err := bulkDeleteArtifactHandler(ctx); err == nil {
		t.Fatalf("expected an error when the user declines the confirmation prompt")
	}

	if len(*calls) != 1 {
		t.Fatalf("expected only the preview call when the user declines, got %d calls", len(*calls))
	}
}

func TestBulkDeleteArtifactHandler_DryRunFalseYes_ExecutesWithoutPrompt(t *testing.T) {
	srv, calls := newBulkDeleteServer(t, []string{"nginx-test", "nginx-other"})
	// No stdin is wired up: if the handler tried to read a confirmation, the
	// scanner would fail on the real (unmocked) os.Stdin in the test binary,
	// so a successful run here proves the prompt was skipped.

	ctx := bulkDeleteTestCtx(map[string]any{
		"registry": "mikereg",
		"dry-run":  "false",
		"yes":      true,
	}, srv.URL)

	if err := bulkDeleteArtifactHandler(ctx); err != nil {
		t.Fatalf("expected nil error with --yes, got: %v", err)
	}

	if len(*calls) != 3 {
		t.Fatalf("expected 3 API calls (preview + real delete + read-back), got %d", len(*calls))
	}
	if !(*calls)[0].DryRun || (*calls)[1].DryRun {
		t.Fatalf("expected call order preview(dryRun=true) then real(dryRun=false), got %+v", *calls)
	}
}

func TestBulkDeleteArtifactHandler_RealRunFailures(t *testing.T) {
	cases := []struct {
		name      string
		opts      bulkDeleteServerOpts
		wantErr   string
		wantCalls int
	}{
		{"server reports failed packages", bulkDeleteServerOpts{failed: 1}, "failed for 1 of 3", 2},
		{"packages still match after delete", bulkDeleteServerOpts{survivesDelete: true}, "still match: nginx-test, nginx-other", 3},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv, calls := newBulkDeleteServerWith(t, []string{"nginx-test", "nginx-other"}, tc.opts)
			ctx := bulkDeleteTestCtx(map[string]any{
				"registry": "mikereg",
				"dry-run":  "false",
				"yes":      true,
			}, srv.URL)

			err := bulkDeleteArtifactHandler(ctx)
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error = %v, want it to contain %q", err, tc.wantErr)
			}
			if len(*calls) != tc.wantCalls {
				t.Fatalf("expected %d API calls, got %d", tc.wantCalls, len(*calls))
			}
		})
	}
}

func TestStillPresent(t *testing.T) {
	got := stillPresent([]string{"express", "express-util"}, []string{"express-util", "express-new"})
	if strings.Join(got, ",") != "express-util" {
		t.Fatalf("stillPresent = %v, want only the deleted name that still matches", got)
	}
}

func TestBulkDeleteArtifactHandler_NoMatches_ReturnsEarly(t *testing.T) {
	srv, calls := newBulkDeleteServer(t, nil)

	ctx := bulkDeleteTestCtx(map[string]any{
		"registry": "mikereg",
		"dry-run":  "false",
		"yes":      true,
	}, srv.URL)

	if err := bulkDeleteArtifactHandler(ctx); err != nil {
		t.Fatalf("expected nil error when nothing matches, got: %v", err)
	}
	if len(*calls) != 1 {
		t.Fatalf("expected only the preview call when nothing matches, got %d", len(*calls))
	}
}
