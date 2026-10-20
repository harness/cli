// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/harness/cli/v3/pkg/config"
	"github.com/harness/cli/v3/pkg/hbase"
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

	// https target: a rejected CONNECT is a plain error, not a response.
	connectProxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer connectProxy.Close()
	setProbeProxy(t, connectProxy.URL)
	r = checkAPIUrl("https://harness.example.com", true)
	if r.OK || !strings.Contains(r.Error, "via proxy") {
		t.Fatalf("expected via-proxy message for rejected CONNECT, got %+v", r)
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

func TestPopulateKnownProfileFieldsScopeOverrides(t *testing.T) {
	t.Setenv(hbase.EnvCLIHome, t.TempDir())
	cfg := &config.Config{Profiles: map[string]*config.Profile{
		"p": {AccountID: "acct", OrgID: "saved-org", ProjectID: "saved-project"},
	}}
	if err := config.SaveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, org, project, wantOrg, wantProject string }{
		{"no overrides keep saved scope", "", "", "saved-org", "saved-project"},
		{"overrides replace saved scope", "flag-org", "flag-project", "flag-org", "flag-project"},
		{"project only", "", "flag-project", "saved-org", "flag-project"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var r statusResult
			populateKnownProfileFields(&r, "p", tc.org, tc.project)
			if r.OrgID != tc.wantOrg || r.ProjectID != tc.wantProject {
				t.Errorf("scope = %q/%q, want %q/%q", r.OrgID, r.ProjectID, tc.wantOrg, tc.wantProject)
			}
		})
	}
}
