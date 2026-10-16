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
	u, _ := url.Parse(proxyURL)
	apiProbeProxy = http.ProxyURL(u)
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
