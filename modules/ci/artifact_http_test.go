// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package ci

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/harness/cli/v3/pkg/auth"
)

func TestNormalizeRegistryURL(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"empty", "", ""},
		{"spaces only", "   ", ""},
		{"with https scheme", "https://example.com", "https://example.com"},
		{"with http scheme", "http://example.com", "http://example.com"},
		{"without scheme", "example.com/api", "https://example.com/api"},
		{"trailing slash stripped", "https://example.com/", "https://example.com"},
		{"leading and trailing spaces", "  https://example.com  ", "https://example.com"},
		{"subdomain without scheme", "pkg.harness.io", "https://pkg.harness.io"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := normalizeRegistryURL(tt.input)
			if got != tt.want {
				t.Errorf("normalizeRegistryURL(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestBuildArtifactFileURL(t *testing.T) {
	tests := []struct {
		name         string
		baseURL      string
		accountID    string
		registry     string
		artifactPath string
		want         string
		wantErr      bool
	}{
		{
			name:         "simple artifact path",
			baseURL:      "https://pkg.harness.io",
			accountID:    "acc123",
			registry:     "my-registry",
			artifactPath: "my-artifact/v1/file.zip",
			want:         "https://pkg.harness.io/pkg/acc123/my-registry/files/my-artifact/v1/file.zip",
		},
		{
			name:         "leading slash in artifact path ignored",
			baseURL:      "https://pkg.harness.io",
			accountID:    "acc123",
			registry:     "my-registry",
			artifactPath: "/my-artifact/v1/file.zip",
			want:         "https://pkg.harness.io/pkg/acc123/my-registry/files/my-artifact/v1/file.zip",
		},
		{
			name:         "consecutive slashes in artifact path collapse",
			baseURL:      "https://pkg.harness.io",
			accountID:    "acc123",
			registry:     "my-registry",
			artifactPath: "art//v1/file.zip",
			want:         "https://pkg.harness.io/pkg/acc123/my-registry/files/art/v1/file.zip",
		},
		{
			name:         "base URL trailing slash stripped",
			baseURL:      "https://pkg.harness.io/",
			accountID:    "acc123",
			registry:     "my-registry",
			artifactPath: "art/v1/f.zip",
			want:         "https://pkg.harness.io/pkg/acc123/my-registry/files/art/v1/f.zip",
		},
		{
			name:         "deep nested path",
			baseURL:      "https://pkg.harness.io",
			accountID:    "acc456",
			registry:     "cache-reg",
			artifactPath: "pipe/v42/cache/subdir/node_modules.tar.gz",
			want:         "https://pkg.harness.io/pkg/acc456/cache-reg/files/pipe/v42/cache/subdir/node_modules.tar.gz",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := buildArtifactFileURL(tt.baseURL, tt.accountID, tt.registry, tt.artifactPath)
			if (err != nil) != tt.wantErr {
				t.Fatalf("buildArtifactFileURL() error = %v, wantErr = %v", err, tt.wantErr)
			}
			if !tt.wantErr && got != tt.want {
				t.Errorf("buildArtifactFileURL() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestPushGenericFile_Success(t *testing.T) {
	var gotMethod, gotContentType, gotAuth, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotContentType = r.Header.Get("Content-Type")
		gotAuth = r.Header.Get("Authorization")
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	a := &auth.ResolvedAuth{
		RegistryURL: srv.URL,
		AccountID:   "acc123",
		Headers:     map[string]string{"Authorization": "CIManager eyJsometoken"},
	}
	resp, err := pushGenericFile(context.Background(), a, "my-reg", "art/v1/file.zip", strings.NewReader("hello"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	if gotMethod != http.MethodPut {
		t.Errorf("method = %q, want PUT", gotMethod)
	}
	if gotContentType != "" {
		t.Errorf("Content-Type = %q, want it unset so HAR infers the type from the filename", gotContentType)
	}
	if gotAuth != "CIManager eyJsometoken" {
		t.Errorf("Authorization = %q, want %q", gotAuth, "CIManager eyJsometoken")
	}
	if gotBody != "hello" {
		t.Errorf("body = %q, want %q", gotBody, "hello")
	}
}

func TestPushGenericFile_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	a := &auth.ResolvedAuth{RegistryURL: srv.URL, AccountID: "acc"}
	resp, err := pushGenericFile(context.Background(), a, "reg", "art/v1/f.zip", strings.NewReader("data"))
	if err != nil {
		t.Fatalf("pushGenericFile should not error on HTTP 5xx (caller handles status): %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d", resp.StatusCode)
	}
}
