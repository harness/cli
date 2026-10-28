// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package waituntil

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/harness/cli/v3/pkg/auth"
	"github.com/harness/cli/v3/pkg/cmdctx"
)

func baseWaitFlags() waitUntilFlags {
	return waitUntilFlags{
		accountID:   "acct",
		executionID: "exec-1",
		timeout:     "300ms",
		interval:    "20ms",
	}
}

func TestCoverageHTTPError(t *testing.T) {
	t.Parallel()

	err := &hTTPError{StatusCode: 404, Body: "not found"}
	if got := err.Error(); got != "HTTP 404: not found" {
		t.Errorf("Error() = %q, want %q", got, "HTTP 404: not found")
	}

	tests := []struct {
		statusCode      int
		wantNotFound    bool
		wantClientError bool
	}{
		{200, false, false},
		{399, false, false},
		{400, false, true},
		{404, true, true},
		{499, false, true},
		{500, false, false},
	}
	for _, tt := range tests {
		e := &hTTPError{StatusCode: tt.statusCode}
		if got := e.IsNotFound(); got != tt.wantNotFound {
			t.Errorf("IsNotFound(%d) = %v, want %v", tt.statusCode, got, tt.wantNotFound)
		}
		if got := e.IsClientError(); got != tt.wantClientError {
			t.Errorf("IsClientError(%d) = %v, want %v", tt.statusCode, got, tt.wantClientError)
		}
	}
}

func TestCoverageParseWaitDuration(t *testing.T) {
	t.Parallel()

	if d, err := parseWaitDuration("", defaultWaitTimeout, "--timeout"); err != nil || d != defaultWaitTimeout {
		t.Errorf("parseWaitDuration(\"\") = (%v, %v), want (%v, nil)", d, err, defaultWaitTimeout)
	}
	if _, err := parseWaitDuration("not-a-duration", defaultWaitTimeout, "--timeout"); err == nil {
		t.Error("parseWaitDuration(\"not-a-duration\") expected an error")
	}
	if _, err := parseWaitDuration("0s", defaultWaitTimeout, "--interval"); err == nil || !strings.Contains(err.Error(), "greater than zero") {
		t.Errorf("parseWaitDuration(\"0s\") error = %v, want it to mention greater than zero", err)
	}
	if _, err := parseWaitDuration("-5s", defaultWaitTimeout, "--interval"); err == nil {
		t.Error("parseWaitDuration(\"-5s\") expected an error")
	}
}

func TestCoverageEvaluateThresholds(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		flags   waitUntilFlags
		summary *reportSummary
		wantErr bool
	}{
		{
			name:    "nil summary always errors",
			flags:   waitUntilFlags{gt: 70},
			summary: nil,
			wantErr: true,
		},
		{
			name:    "gt passes when coverage is strictly above",
			flags:   waitUntilFlags{gt: 70},
			summary: &reportSummary{Coverage: 70.01},
			wantErr: false,
		},
		{
			name:    "gt fails when coverage equals the threshold",
			flags:   waitUntilFlags{gt: 70},
			summary: &reportSummary{Coverage: 70},
			wantErr: true,
		},
		{
			name:    "gte passes when coverage equals the threshold",
			flags:   waitUntilFlags{gte: 70},
			summary: &reportSummary{Coverage: 70},
			wantErr: false,
		},
		{
			name:    "gte fails when coverage is below the threshold",
			flags:   waitUntilFlags{gte: 70},
			summary: &reportSummary{Coverage: 69.99},
			wantErr: true,
		},
		{
			name:    "patch threshold skipped when no patch data is available",
			flags:   waitUntilFlags{patchGt: 80},
			summary: &reportSummary{Coverage: 90, Patch: nil},
			wantErr: false,
		},
		{
			name:    "patch-gt fails when patch coverage equals the threshold",
			flags:   waitUntilFlags{patchGt: 80},
			summary: &reportSummary{Coverage: 90, Patch: &patchSummary{Coverage: 80}},
			wantErr: true,
		},
		{
			name:    "patch-gte passes when patch coverage equals the threshold",
			flags:   waitUntilFlags{patchGte: 80},
			summary: &reportSummary{Coverage: 90, Patch: &patchSummary{Coverage: 80}},
			wantErr: false,
		},
		{
			name:  "overall and patch thresholds both failing report both",
			flags: waitUntilFlags{gt: 70, patchGt: 80},
			summary: &reportSummary{
				Coverage: 60,
				Patch:    &patchSummary{Coverage: 75},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := evaluateThresholds(tt.flags, tt.summary)
			if (err != nil) != tt.wantErr {
				t.Errorf("evaluateThresholds() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr && tt.summary != nil && !strings.Contains(err.Error(), "quality gate failed") {
				t.Errorf("evaluateThresholds() error = %v, want it to mention quality gate failed", err)
			}
		})
	}
}

func TestCoverageGetSummary(t *testing.T) {
	t.Run("status complete with summary", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != statusPath("acct", "exec-1") {
				t.Fatalf("unexpected path %s, want tree never called", r.URL.Path)
			}
			writeCoverageJSON(t, w, statusResponse{
				State:   "complete",
				Summary: &reportSummary{Coverage: 85, ReportCount: 2},
			})
		}))
		defer server.Close()

		ctx := testCtx(t, server.URL, "acct")
		summary, complete, err := getSummary(ctx, waitUntilFlags{}, "acct", "exec-1")
		if err != nil {
			t.Fatalf("getSummary() error = %v", err)
		}
		if !complete || summary == nil || summary.Coverage != 85 {
			t.Fatalf("getSummary() = (%+v, %v), want complete with coverage 85", summary, complete)
		}
	})

	t.Run("status 404 falls back to tree", func(t *testing.T) {
		var treeCalled bool
		mux := http.NewServeMux()
		mux.HandleFunc(statusPath("acct", "exec-1"), func(w http.ResponseWriter, _ *http.Request) {
			http.NotFound(w, nil)
		})
		mux.HandleFunc(treePath("acct", "exec-1"), func(w http.ResponseWriter, _ *http.Request) {
			treeCalled = true
			writeCoverageJSON(t, w, treeResponse{Summary: &reportSummary{Coverage: 42}})
		})
		server := httptest.NewServer(mux)
		defer server.Close()

		ctx := testCtx(t, server.URL, "acct")
		summary, complete, err := getSummary(ctx, waitUntilFlags{}, "acct", "exec-1")
		if err != nil {
			t.Fatalf("getSummary() error = %v", err)
		}
		if !treeCalled {
			t.Fatal("tree endpoint was not called after a 404 from status")
		}
		if complete || summary == nil || summary.Coverage != 42 {
			t.Fatalf("getSummary() = (%+v, %v), want tree's summary and complete=false", summary, complete)
		}
	})

	t.Run("status without patch data falls back to tree when a patch threshold is set", func(t *testing.T) {
		var treeCalled bool
		mux := http.NewServeMux()
		mux.HandleFunc(statusPath("acct", "exec-1"), func(w http.ResponseWriter, _ *http.Request) {
			writeCoverageJSON(t, w, statusResponse{
				State:   "pending",
				Summary: &reportSummary{Coverage: 50, Patch: nil},
			})
		})
		mux.HandleFunc(treePath("acct", "exec-1"), func(w http.ResponseWriter, _ *http.Request) {
			treeCalled = true
			writeCoverageJSON(t, w, treeResponse{
				Summary: &reportSummary{Coverage: 50, Patch: &patchSummary{Coverage: 77}},
			})
		})
		server := httptest.NewServer(mux)
		defer server.Close()

		ctx := testCtx(t, server.URL, "acct")
		summary, complete, err := getSummary(ctx, waitUntilFlags{patchGt: 70}, "acct", "exec-1")
		if err != nil {
			t.Fatalf("getSummary() error = %v", err)
		}
		if !treeCalled {
			t.Fatal("tree endpoint was not called despite a patch threshold with no patch data in the status response")
		}
		if complete || summary == nil || summary.Patch == nil || summary.Patch.Coverage != 77 {
			t.Fatalf("getSummary() = (%+v, %v), want tree's patch summary", summary, complete)
		}
	})

	t.Run("tree failure after a 404 status propagates a tree endpoint failed error", func(t *testing.T) {
		mux := http.NewServeMux()
		mux.HandleFunc(statusPath("acct", "exec-1"), func(w http.ResponseWriter, _ *http.Request) {
			http.NotFound(w, nil)
		})
		mux.HandleFunc(treePath("acct", "exec-1"), func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "internal error", http.StatusInternalServerError)
		})
		server := httptest.NewServer(mux)
		defer server.Close()

		ctx := testCtx(t, server.URL, "acct")
		_, _, err := getSummary(ctx, waitUntilFlags{}, "acct", "exec-1")
		if err == nil || !strings.Contains(err.Error(), "tree endpoint failed") {
			t.Fatalf("getSummary() error = %v, want it to mention tree endpoint failed", err)
		}
	})

	t.Run("non-404 status failure is a client error without consulting tree", func(t *testing.T) {
		var treeCalled bool
		mux := http.NewServeMux()
		mux.HandleFunc(statusPath("acct", "exec-1"), func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "forbidden", http.StatusForbidden)
		})
		mux.HandleFunc(treePath("acct", "exec-1"), func(w http.ResponseWriter, _ *http.Request) {
			treeCalled = true
			writeCoverageJSON(t, w, treeResponse{})
		})
		server := httptest.NewServer(mux)
		defer server.Close()

		ctx := testCtx(t, server.URL, "acct")
		_, _, err := getSummary(ctx, waitUntilFlags{}, "acct", "exec-1")
		if err == nil {
			t.Fatal("getSummary() expected an error for a 403 status response")
		}
		var httpErr *hTTPError
		if !errors.As(err, &httpErr) || !httpErr.IsClientError() || httpErr.IsNotFound() {
			t.Fatalf("getSummary() error = %v, want a non-404 *hTTPError", err)
		}
		if treeCalled {
			t.Error("tree endpoint was called despite a non-404 status failure")
		}
	})
}

func TestCoverageWaitUntil_RequiresAThreshold(t *testing.T) {
	f := baseWaitFlags()
	err := waitUntil(testCtx(t, "http://unused.invalid", "acct"), f)
	if err == nil || !strings.Contains(err.Error(), "at least one threshold flag must be specified") {
		t.Fatalf("waitUntil() error = %v, want it to mention a required threshold flag", err)
	}
}

func TestCoverageWaitUntil_RequiresDetectedCIVendorEvenWithExplicitFlags(t *testing.T) {
	clearCIEnv(t)

	f := baseWaitFlags()
	f.gte = 80
	err := waitUntil(testCtx(t, "http://unused.invalid", "acct"), f)
	if err == nil || !strings.Contains(err.Error(), "failed to get CI vendor info") {
		t.Fatalf("waitUntil() error = %v, want it to require a detected CI vendor even though --account/--execution were set", err)
	}
}

func TestCoverageWaitUntil_PassesOnceComplete(t *testing.T) {
	t.Setenv("CI", "true")
	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != statusPath("acct", "exec-1") {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		n := atomic.AddInt32(&calls, 1)
		state := "pending"
		if n >= 2 {
			state = "complete"
		}
		writeCoverageJSON(t, w, statusResponse{State: state, Summary: &reportSummary{Coverage: 90}})
	}))
	defer server.Close()

	ctx := testCtx(t, server.URL, "acct")
	f := baseWaitFlags()
	f.gte = 80

	if err := waitUntil(ctx, f); err != nil {
		t.Fatalf("waitUntil() error = %v", err)
	}
	if atomic.LoadInt32(&calls) < 2 {
		t.Fatalf("status endpoint was called %d times, want at least 2 (poll until complete)", calls)
	}
}

func TestCoverageWaitUntil_QualityGateFailureIsReturned(t *testing.T) {
	t.Setenv("CI", "true")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeCoverageJSON(t, w, statusResponse{State: "complete", Summary: &reportSummary{Coverage: 50}})
	}))
	defer server.Close()

	ctx := testCtx(t, server.URL, "acct")
	f := baseWaitFlags()
	f.gte = 80

	err := waitUntil(ctx, f)
	if err == nil || !strings.Contains(err.Error(), "quality gate failed") {
		t.Fatalf("waitUntil() error = %v, want it to mention quality gate failed", err)
	}
}

func TestCoverageWaitUntil_TimesOutWithNoData(t *testing.T) {
	t.Setenv("CI", "true")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "pending", http.StatusServiceUnavailable)
	}))
	defer server.Close()

	ctx := testCtx(t, server.URL, "acct")
	f := baseWaitFlags()
	f.gte = 80

	err := waitUntil(ctx, f)
	if err == nil || !strings.Contains(err.Error(), "timeout") {
		t.Fatalf("waitUntil() error = %v, want a timeout error", err)
	}
}

func TestCoverageWaitUntil_ClientErrorFailsFastWithoutExhaustingTimeout(t *testing.T) {
	t.Setenv("CI", "true")
	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&calls, 1)
		http.Error(w, "forbidden", http.StatusForbidden)
	}))
	defer server.Close()

	ctx := testCtx(t, server.URL, "acct")
	f := baseWaitFlags()
	f.gte = 80
	f.timeout = "5s"
	f.interval = "20ms"

	err := waitUntil(ctx, f)
	if err == nil || !strings.Contains(err.Error(), "HTTP 403") {
		t.Fatalf("waitUntil() error = %v, want it to propagate the 403 immediately", err)
	}
	if atomic.LoadInt32(&calls) > 2 {
		t.Fatalf("status endpoint was called %d times, want it to fail fast on the first 403 instead of retrying toward the 5s timeout", calls)
	}
}

func clearCIEnv(t *testing.T) {
	t.Helper()
	vars := []string{
		"CI", "CONTINUOUS_INTEGRATION", "BUILD_NUMBER", "RUN_ID",
		"APPVEYOR", "SYSTEM_TEAMFOUNDATIONCOLLECTIONURI", "AC_APPCIRCLE",
		"bamboo_planKey", "BITBUCKET_COMMIT", "BITRISE_IO", "BUDDY_WORKSPACE_ID",
		"BUILDKITE", "CIRCLECI", "CIRRUS_CI", "CODEBUILD_BUILD_ARN",
		"CF_BUILD_ID", "CI_NAME", "DRONE", "DSARI", "EAS_BUILD",
		"GITHUB_ACTIONS", "GITLAB_CI", "GO_PIPELINE_LABEL", "LAYERCI",
		"HUDSON_URL", "JENKINS_URL", "BUILD_ID", "MAGNUM", "NETLIFY",
		"NEVERCODE", "RENDER", "SAILCI", "SEMAPHORE", "SCREWDRIVER",
		"SHIPPABLE", "TDDIUM", "STRIDER", "TASK_ID", "TEAMCITY_VERSION",
		"TRAVIS", "NOW_BUILDER", "APPCENTER_BUILD_ID",
	}
	for _, v := range vars {
		orig, ok := os.LookupEnv(v)
		os.Unsetenv(v)
		if ok {
			t.Cleanup(func() { os.Setenv(v, orig) })
		}
	}
}

func testCtx(t *testing.T, srvURL, accountID string) *cmdctx.Ctx {
	t.Helper()
	return &cmdctx.Ctx{
		Context: context.Background(),
		Auth: &auth.ResolvedAuth{
			APIUrl:    srvURL,
			AccountID: accountID,
			PATToken:  "token",
			AuthType:  auth.AuthTypePAT,
		},
	}
}

func writeCoverageJSON(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Fatalf("encode response: %v", err)
	}
}
