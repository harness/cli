// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package har

import (
	"archive/tar"
	"compress/gzip"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/harness/cli/v3/pkg/auth"
	"github.com/harness/cli/v3/pkg/cmdctx"
)

func writeNpmTgz(t *testing.T, packageJSON string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "pkg.tgz")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	gw := gzip.NewWriter(f)
	tw := tar.NewWriter(gw)
	if err := tw.WriteHeader(&tar.Header{Name: "package/package.json", Mode: 0o644, Size: int64(len(packageJSON))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte(packageJSON)); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestPushNpmArtifact_ExistingVersionCheck(t *testing.T) {
	tests := []struct {
		name       string
		id         string
		pkgJSON    string
		getStatus  int
		getBody    string
		wantPath   string
		wantPut    bool
		wantErrSub string
	}{
		{
			name:      "new package (404) is uploaded",
			id:        "reg/left-pad",
			pkgJSON:   `{"name":"left-pad","version":"1.0.0"}`,
			getStatus: http.StatusNotFound,
			wantPath:  "/pkg/acct/reg/npm/left-pad",
			wantPut:   true,
		},
		{
			name:       "already published version is rejected without upload",
			id:         "reg/left-pad",
			pkgJSON:    `{"name":"left-pad","version":"1.0.0"}`,
			getStatus:  http.StatusOK,
			getBody:    `{"name":"left-pad","versions":{"0.9.0":{},"1.0.0":{}}}`,
			wantPath:   "/pkg/acct/reg/npm/left-pad",
			wantErrSub: "left-pad@1.0.0 already exists",
		},
		{
			name:      "new version of an existing package is uploaded",
			id:        "reg/left-pad",
			pkgJSON:   `{"name":"left-pad","version":"1.1.0"}`,
			getStatus: http.StatusOK,
			getBody:   `{"name":"left-pad","versions":{"1.0.0":{}}}`,
			wantPath:  "/pkg/acct/reg/npm/left-pad",
			wantPut:   true,
		},
		{
			name:       "metadata lookup failure aborts without upload",
			id:         "reg/left-pad",
			pkgJSON:    `{"name":"left-pad","version":"1.0.0"}`,
			getStatus:  http.StatusForbidden,
			getBody:    "denied",
			wantPath:   "/pkg/acct/reg/npm/left-pad",
			wantErrSub: "HTTP 403",
		},
		{
			name:      "bare registry with scoped package uses the scoped path",
			id:        "reg",
			pkgJSON:   `{"name":"@acme/demo-pkg","version":"1.0.0"}`,
			getStatus: http.StatusNotFound,
			wantPath:  "/pkg/acct/reg/npm/@acme/demo-pkg",
			wantPut:   true,
		},
		{
			name:      "legacy registry/name id defers to the package.json name",
			id:        "reg/other-name",
			pkgJSON:   `{"name":"left-pad","version":"1.0.0"}`,
			getStatus: http.StatusNotFound,
			wantPath:  "/pkg/acct/reg/npm/left-pad",
			wantPut:   true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotPut bool
			var getPath string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.Method {
				case http.MethodGet:
					getPath = r.URL.Path
					w.WriteHeader(tt.getStatus)
					_, _ = w.Write([]byte(tt.getBody))
				case http.MethodPut:
					gotPut = true
					w.WriteHeader(http.StatusCreated)
				}
			}))
			defer srv.Close()

			ctx := &cmdctx.Ctx{
				Auth:       &auth.ResolvedAuth{RegistryURL: srv.URL, AccountID: "acct", PATToken: "tok"},
				Id:         tt.id,
				Args:       []string{writeNpmTgz(t, tt.pkgJSON)},
				FlagValues: map[string]any{},
			}
			err := pushNpmArtifact(ctx)

			if tt.wantErrSub != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErrSub) {
					t.Fatalf("err = %v, want containing %q", err, tt.wantErrSub)
				}
			} else if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if gotPut != tt.wantPut {
				t.Errorf("PUT sent = %v, want %v", gotPut, tt.wantPut)
			}
			if getPath != tt.wantPath {
				t.Errorf("metadata GET path = %q, want %q", getPath, tt.wantPath)
			}
		})
	}
}
