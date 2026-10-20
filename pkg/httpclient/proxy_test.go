// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package httpclient

import (
	"net/url"
	"strings"
	"testing"
)

func TestCheckProxyEnv(t *testing.T) {
	const secret = "p%LKsecret"
	tests := []struct {
		name    string
		env     map[string]string
		target  string
		wantErr string
	}{
		{"valid URL", map[string]string{"HTTPS_PROXY": "http://u:pw@proxy.example:8080"}, "https://api.example", ""},
		{"bare host:port", map[string]string{"HTTPS_PROXY": "proxy.example:8080"}, "https://api.example", ""},
		{"invalid escape", map[string]string{"HTTPS_PROXY": "http://u:" + secret + "@proxy.example:8080"}, "https://api.example", "HTTPS_PROXY"},
		{"bad port", map[string]string{"HTTPS_PROXY": "http://u:" + secret + "@proxy.example:port"}, "https://api.example", "HTTPS_PROXY"},
		{"unset", nil, "https://api.example", ""},
		{"lowercase fallback", map[string]string{"https_proxy": "http://u:" + secret + "@proxy.example:8080"}, "https://api.example", "https_proxy"},
		{"http ignores https var", map[string]string{"HTTPS_PROXY": "http://u:" + secret + "@proxy.example:8080"}, "http://api.example", ""},
		{"localhost exempt", map[string]string{"HTTPS_PROXY": "http://u:" + secret + "@proxy.example:8080"}, "https://localhost:8080", ""},
		{"loopback IP exempt", map[string]string{"HTTPS_PROXY": "http://u:" + secret + "@proxy.example:8080"}, "https://127.0.0.1", ""},
		{"http uses http var", map[string]string{"HTTP_PROXY": "http://u:" + secret + "@proxy.example:8080"}, "http://api.example", "HTTP_PROXY"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, k := range []string{"HTTP_PROXY", "http_proxy", "HTTPS_PROXY", "https_proxy"} {
				t.Setenv(k, "")
			}
			for k, v := range tt.env {
				t.Setenv(k, v)
			}
			u, _ := url.Parse(tt.target)
			err := checkProxyEnv(u)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("got %v, want error containing %q", err, tt.wantErr)
			}
			if strings.Contains(err.Error(), secret) {
				t.Fatalf("error leaks password: %v", err)
			}
		})
	}
}
