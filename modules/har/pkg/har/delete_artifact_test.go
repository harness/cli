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

func newBulkDeleteServer(t *testing.T, successPackages []string) (*httptest.Server, *[]recordedCall) {
	t.Helper()
	calls := &[]recordedCall{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var call recordedCall
		_ = json.Unmarshal(body, &call)
		*calls = append(*calls, call)

		resp := bulkDeleteResponse{
			DryRun:          call.DryRun,
			Success:         len(successPackages),
			Total:           len(successPackages),
			SuccessPackages: successPackages,
			Registry:        "mikereg",
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	t.Cleanup(srv.Close)
	return srv, calls
}

// withStdin temporarily replaces os.Stdin with a pipe pre-loaded with input.
func withStdin(t *testing.T, input string) {
	t.Helper()
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

	if len(*calls) != 2 {
		t.Fatalf("expected 2 API calls (preview + real delete), got %d", len(*calls))
	}
	if !(*calls)[0].DryRun {
		t.Fatalf("expected first call to be the preview (dryRun=true)")
	}
	if (*calls)[1].DryRun {
		t.Fatalf("expected second call to be the real delete (dryRun=false)")
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

	if len(*calls) != 2 {
		t.Fatalf("expected 2 API calls (preview + real delete), got %d", len(*calls))
	}
	if !(*calls)[0].DryRun || (*calls)[1].DryRun {
		t.Fatalf("expected call order preview(dryRun=true) then real(dryRun=false), got %+v", *calls)
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
