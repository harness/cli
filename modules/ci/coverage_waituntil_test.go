// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package ci

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func coverageBaseWaitFlags() coverageWaitUntilFlags {
	return coverageWaitUntilFlags{
		accountID:   "acct",
		executionID: "exec-1",
		timeout:     "300ms",
		interval:    "20ms",
	}
}

func TestCoverageHTTPError(t *testing.T) {
	t.Parallel()

	err := &coverageHTTPError{StatusCode: 404, Body: "not found"}
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
		e := &coverageHTTPError{StatusCode: tt.statusCode}
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

	if d, err := coverageParseWaitDuration("", coverageDefaultWaitTimeout, "--timeout"); err != nil || d != coverageDefaultWaitTimeout {
		t.Errorf("coverageParseWaitDuration(\"\") = (%v, %v), want (%v, nil)", d, err, coverageDefaultWaitTimeout)
	}
	if _, err := coverageParseWaitDuration("not-a-duration", coverageDefaultWaitTimeout, "--timeout"); err == nil {
		t.Error("coverageParseWaitDuration(\"not-a-duration\") expected an error")
	}
	if _, err := coverageParseWaitDuration("0s", coverageDefaultWaitTimeout, "--interval"); err == nil || !strings.Contains(err.Error(), "greater than zero") {
		t.Errorf("coverageParseWaitDuration(\"0s\") error = %v, want it to mention greater than zero", err)
	}
	if _, err := coverageParseWaitDuration("-5s", coverageDefaultWaitTimeout, "--interval"); err == nil {
		t.Error("coverageParseWaitDuration(\"-5s\") expected an error")
	}
}

func TestCoverageEvaluateThresholds(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		flags   coverageWaitUntilFlags
		summary *coverageSummary
		wantErr bool
	}{
		{
			name:    "nil summary always errors",
			flags:   coverageWaitUntilFlags{gt: 70},
			summary: nil,
			wantErr: true,
		},
		{
			name:    "gt passes when coverage is strictly above",
			flags:   coverageWaitUntilFlags{gt: 70},
			summary: &coverageSummary{Coverage: 70.01},
			wantErr: false,
		},
		{
			name:    "gt fails when coverage equals the threshold",
			flags:   coverageWaitUntilFlags{gt: 70},
			summary: &coverageSummary{Coverage: 70},
			wantErr: true,
		},
		{
			name:    "gte passes when coverage equals the threshold",
			flags:   coverageWaitUntilFlags{gte: 70},
			summary: &coverageSummary{Coverage: 70},
			wantErr: false,
		},
		{
			name:    "gte fails when coverage is below the threshold",
			flags:   coverageWaitUntilFlags{gte: 70},
			summary: &coverageSummary{Coverage: 69.99},
			wantErr: true,
		},
		{
			name:    "patch threshold skipped when no patch data is available",
			flags:   coverageWaitUntilFlags{patchGt: 80},
			summary: &coverageSummary{Coverage: 90, Patch: nil},
			wantErr: false,
		},
		{
			name:    "patch-gt fails when patch coverage equals the threshold",
			flags:   coverageWaitUntilFlags{patchGt: 80},
			summary: &coverageSummary{Coverage: 90, Patch: &coveragePatchSummary{Coverage: 80}},
			wantErr: true,
		},
		{
			name:    "patch-gte passes when patch coverage equals the threshold",
			flags:   coverageWaitUntilFlags{patchGte: 80},
			summary: &coverageSummary{Coverage: 90, Patch: &coveragePatchSummary{Coverage: 80}},
			wantErr: false,
		},
		{
			name:  "overall and patch thresholds both failing report both",
			flags: coverageWaitUntilFlags{gt: 70, patchGt: 80},
			summary: &coverageSummary{
				Coverage: 60,
				Patch:    &coveragePatchSummary{Coverage: 75},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := coverageEvaluateThresholds(tt.flags, tt.summary)
			if (err != nil) != tt.wantErr {
				t.Errorf("coverageEvaluateThresholds() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr && tt.summary != nil && !strings.Contains(err.Error(), "quality gate failed") {
				t.Errorf("coverageEvaluateThresholds() error = %v, want it to mention quality gate failed", err)
			}
		})
	}
}

func TestCoverageGetSummary(t *testing.T) {
	t.Run("status complete with summary", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != coverageStatusPath("acct", "exec-1") {
				t.Fatalf("unexpected path %s, want tree never called", r.URL.Path)
			}
			writeCoverageJSON(t, w, coverageStatusResponse{
				State:   "complete",
				Summary: &coverageSummary{Coverage: 85, ReportCount: 2},
			})
		}))
		defer server.Close()

		ctx := coverageTestCtx(t, server.URL, "acct")
		summary, complete, err := coverageGetSummary(ctx, coverageWaitUntilFlags{}, "acct", "exec-1")
		if err != nil {
			t.Fatalf("coverageGetSummary() error = %v", err)
		}
		if !complete || summary == nil || summary.Coverage != 85 {
			t.Fatalf("coverageGetSummary() = (%+v, %v), want complete with coverage 85", summary, complete)
		}
	})

	t.Run("status 404 falls back to tree", func(t *testing.T) {
		var treeCalled bool
		mux := http.NewServeMux()
		mux.HandleFunc(coverageStatusPath("acct", "exec-1"), func(w http.ResponseWriter, _ *http.Request) {
			http.NotFound(w, nil)
		})
		mux.HandleFunc(coverageTreePath("acct", "exec-1"), func(w http.ResponseWriter, _ *http.Request) {
			treeCalled = true
			writeCoverageJSON(t, w, coverageTreeResponse{Summary: &coverageSummary{Coverage: 42}})
		})
		server := httptest.NewServer(mux)
		defer server.Close()

		ctx := coverageTestCtx(t, server.URL, "acct")
		summary, complete, err := coverageGetSummary(ctx, coverageWaitUntilFlags{}, "acct", "exec-1")
		if err != nil {
			t.Fatalf("coverageGetSummary() error = %v", err)
		}
		if !treeCalled {
			t.Fatal("tree endpoint was not called after a 404 from status")
		}
		if complete || summary == nil || summary.Coverage != 42 {
			t.Fatalf("coverageGetSummary() = (%+v, %v), want tree's summary and complete=false", summary, complete)
		}
	})

	t.Run("status without patch data falls back to tree when a patch threshold is set", func(t *testing.T) {
		var treeCalled bool
		mux := http.NewServeMux()
		mux.HandleFunc(coverageStatusPath("acct", "exec-1"), func(w http.ResponseWriter, _ *http.Request) {
			writeCoverageJSON(t, w, coverageStatusResponse{
				State:   "pending",
				Summary: &coverageSummary{Coverage: 50, Patch: nil},
			})
		})
		mux.HandleFunc(coverageTreePath("acct", "exec-1"), func(w http.ResponseWriter, _ *http.Request) {
			treeCalled = true
			writeCoverageJSON(t, w, coverageTreeResponse{
				Summary: &coverageSummary{Coverage: 50, Patch: &coveragePatchSummary{Coverage: 77}},
			})
		})
		server := httptest.NewServer(mux)
		defer server.Close()

		ctx := coverageTestCtx(t, server.URL, "acct")
		summary, complete, err := coverageGetSummary(ctx, coverageWaitUntilFlags{patchGt: 70}, "acct", "exec-1")
		if err != nil {
			t.Fatalf("coverageGetSummary() error = %v", err)
		}
		if !treeCalled {
			t.Fatal("tree endpoint was not called despite a patch threshold with no patch data in the status response")
		}
		if complete || summary == nil || summary.Patch == nil || summary.Patch.Coverage != 77 {
			t.Fatalf("coverageGetSummary() = (%+v, %v), want tree's patch summary", summary, complete)
		}
	})

	t.Run("tree failure after a 404 status propagates a tree endpoint failed error", func(t *testing.T) {
		mux := http.NewServeMux()
		mux.HandleFunc(coverageStatusPath("acct", "exec-1"), func(w http.ResponseWriter, _ *http.Request) {
			http.NotFound(w, nil)
		})
		mux.HandleFunc(coverageTreePath("acct", "exec-1"), func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "internal error", http.StatusInternalServerError)
		})
		server := httptest.NewServer(mux)
		defer server.Close()

		ctx := coverageTestCtx(t, server.URL, "acct")
		_, _, err := coverageGetSummary(ctx, coverageWaitUntilFlags{}, "acct", "exec-1")
		if err == nil || !strings.Contains(err.Error(), "tree endpoint failed") {
			t.Fatalf("coverageGetSummary() error = %v, want it to mention tree endpoint failed", err)
		}
	})

	t.Run("non-404 status failure is a client error without consulting tree", func(t *testing.T) {
		var treeCalled bool
		mux := http.NewServeMux()
		mux.HandleFunc(coverageStatusPath("acct", "exec-1"), func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "forbidden", http.StatusForbidden)
		})
		mux.HandleFunc(coverageTreePath("acct", "exec-1"), func(w http.ResponseWriter, _ *http.Request) {
			treeCalled = true
			writeCoverageJSON(t, w, coverageTreeResponse{})
		})
		server := httptest.NewServer(mux)
		defer server.Close()

		ctx := coverageTestCtx(t, server.URL, "acct")
		_, _, err := coverageGetSummary(ctx, coverageWaitUntilFlags{}, "acct", "exec-1")
		if err == nil {
			t.Fatal("coverageGetSummary() expected an error for a 403 status response")
		}
		var httpErr *coverageHTTPError
		if !errors.As(err, &httpErr) || !httpErr.IsClientError() || httpErr.IsNotFound() {
			t.Fatalf("coverageGetSummary() error = %v, want a non-404 *coverageHTTPError", err)
		}
		if treeCalled {
			t.Error("tree endpoint was called despite a non-404 status failure")
		}
	})
}

func TestCoverageWaitUntil_RequiresAThreshold(t *testing.T) {
	f := coverageBaseWaitFlags()
	err := coverageWaitUntil(coverageTestCtx(t, "http://unused.invalid", "acct"), f)
	if err == nil || !strings.Contains(err.Error(), "at least one threshold flag must be specified") {
		t.Fatalf("coverageWaitUntil() error = %v, want it to mention a required threshold flag", err)
	}
}

func TestCoverageWaitUntil_RequiresDetectedCIVendorEvenWithExplicitFlags(t *testing.T) {
	coverageClearCIEnv(t)

	f := coverageBaseWaitFlags()
	f.gte = 80
	err := coverageWaitUntil(coverageTestCtx(t, "http://unused.invalid", "acct"), f)
	if err == nil || !strings.Contains(err.Error(), "failed to get CI vendor info") {
		t.Fatalf("coverageWaitUntil() error = %v, want it to require a detected CI vendor even though --account/--execution were set", err)
	}
}

func TestCoverageWaitUntil_PassesOnceComplete(t *testing.T) {
	t.Setenv("CI", "true")
	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != coverageStatusPath("acct", "exec-1") {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		n := atomic.AddInt32(&calls, 1)
		state := "pending"
		if n >= 2 {
			state = "complete"
		}
		writeCoverageJSON(t, w, coverageStatusResponse{State: state, Summary: &coverageSummary{Coverage: 90}})
	}))
	defer server.Close()

	ctx := coverageTestCtx(t, server.URL, "acct")
	f := coverageBaseWaitFlags()
	f.gte = 80

	if err := coverageWaitUntil(ctx, f); err != nil {
		t.Fatalf("coverageWaitUntil() error = %v", err)
	}
	if atomic.LoadInt32(&calls) < 2 {
		t.Fatalf("status endpoint was called %d times, want at least 2 (poll until complete)", calls)
	}
}

func TestCoverageWaitUntil_QualityGateFailureIsReturned(t *testing.T) {
	t.Setenv("CI", "true")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeCoverageJSON(t, w, coverageStatusResponse{State: "complete", Summary: &coverageSummary{Coverage: 50}})
	}))
	defer server.Close()

	ctx := coverageTestCtx(t, server.URL, "acct")
	f := coverageBaseWaitFlags()
	f.gte = 80

	err := coverageWaitUntil(ctx, f)
	if err == nil || !strings.Contains(err.Error(), "quality gate failed") {
		t.Fatalf("coverageWaitUntil() error = %v, want it to mention quality gate failed", err)
	}
}

func TestCoverageWaitUntil_TimesOutWithNoData(t *testing.T) {
	t.Setenv("CI", "true")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "pending", http.StatusServiceUnavailable)
	}))
	defer server.Close()

	ctx := coverageTestCtx(t, server.URL, "acct")
	f := coverageBaseWaitFlags()
	f.gte = 80

	err := coverageWaitUntil(ctx, f)
	if err == nil || !strings.Contains(err.Error(), "timeout") {
		t.Fatalf("coverageWaitUntil() error = %v, want a timeout error", err)
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

	ctx := coverageTestCtx(t, server.URL, "acct")
	f := coverageBaseWaitFlags()
	f.gte = 80
	f.timeout = "5s"
	f.interval = "20ms"

	err := coverageWaitUntil(ctx, f)
	if err == nil || !strings.Contains(err.Error(), "HTTP 403") {
		t.Fatalf("coverageWaitUntil() error = %v, want it to propagate the 403 immediately", err)
	}
	if atomic.LoadInt32(&calls) > 2 {
		t.Fatalf("status endpoint was called %d times, want it to fail fast on the first 403 instead of retrying toward the 5s timeout", calls)
	}
}
