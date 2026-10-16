// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// setProbeProxy overrides the proxy used by checkAPIUrl (http.ProxyFromEnvironment
// caches the environment once per process, so t.Setenv is not reliable).
func setProbeProxy(t *testing.T, proxyURL string) {
	t.Helper()
	orig := apiProbeProxy
	t.Cleanup(func() { apiProbeProxy = orig })
	if proxyURL == "" {
		apiProbeProxy = func(*http.Request) (*url.URL, error) { return nil, nil }
		return
	}
	u, _ := url.Parse(proxyURL)
	apiProbeProxy = http.ProxyURL(u)
}

func TestCheckAPIUrl_AnyHTTPResponseIsReachable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	setProbeProxy(t, "")

	// http scheme and a non-443 port must be honored.
	if r := checkAPIUrl(srv.URL, true); !r.OK {
		t.Fatalf("expected reachable, got %+v", r)
	}
}

func TestCheckAPIUrl_UsesProxyFromEnv(t *testing.T) {
	var gotHost string
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHost = r.Host
		w.WriteHeader(http.StatusOK)
	}))
	defer proxy.Close()
	setProbeProxy(t, proxy.URL)

	// example.com is unresolvable/unreachable directly in tests; success proves the proxy was used.
	if r := checkAPIUrl("http://harness.example.com:8080", true); !r.OK {
		t.Fatalf("expected reachable via proxy, got %+v", r)
	}
	if gotHost != "harness.example.com:8080" {
		t.Fatalf("proxy saw host %q", gotHost)
	}
}

func TestCheckAPIUrl_ProxyFailuresNotBlamedOnHost(t *testing.T) {
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusProxyAuthRequired)
	}))
	defer proxy.Close()
	setProbeProxy(t, proxy.URL)

	r := checkAPIUrl("http://harness.example.com", true)
	if r.OK || !strings.Contains(r.Error, "proxy authentication required") {
		t.Fatalf("expected proxy 407 message, got %+v", r)
	}

	// Dead proxy: closed listener.
	dead := httptest.NewServer(http.NotFoundHandler())
	deadURL := dead.URL
	dead.Close()
	setProbeProxy(t, deadURL)
	r = checkAPIUrl("http://harness.example.com", true)
	if r.OK || !strings.Contains(r.Error, "cannot reach proxy") {
		t.Fatalf("expected proxy unreachable message, got %+v", r)
	}
}
