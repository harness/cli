// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package har

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/harness/cli/modules/har/pkg/har/migrate/types"
	"github.com/rs/zerolog"
)

// TestUploadFileTerraformRouting verifies that adapter.UploadFile dispatches
// TERRAFORM to uploadTerraformFile (module and provider paths).
func TestUploadFileTerraformRouting(t *testing.T) {
	tests := []struct {
		name        string
		fileName    string
		pkg         string
		version     string
		wantPathHas string
	}{
		{
			name:        "module routed correctly",
			fileName:    "vpc-1.0.0.tar.gz",
			pkg:         "hashicorp/vpc/aws",
			version:     "1.0.0",
			wantPathHas: "/terraform/v1/modules/hashicorp/vpc/aws/1.0.0",
		},
		{
			name:        "provider routed correctly",
			fileName:    "terraform-provider-aws_2.0.0_linux_amd64.zip",
			pkg:         "hashicorp/aws",
			version:     "2.0.0",
			wantPathHas: "/terraform/v1/providers/hashicorp/aws/2.0.0",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotPath string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotPath = r.URL.Path
				w.WriteHeader(http.StatusCreated)
			}))
			defer srv.Close()

			c := newClient(&types.RegistryConfig{
				Endpoint:    srv.URL,
				AccountID:   "acct1",
				Credentials: types.CredentialsConfig{Username: "user", Password: "token"},
			})
			a := &harAdapter{client: c, logger: zerolog.Nop()}

			f := &types.File{Name: tt.fileName, Uri: "/" + tt.fileName}
			body := io.NopCloser(strings.NewReader("bytes"))

			err := a.UploadFile("reg1", body, f, http.Header{}, tt.pkg, tt.version, types.TERRAFORM, nil)
			if err != nil {
				t.Fatalf("UploadFile error: %v", err)
			}
			if !strings.Contains(gotPath, tt.wantPathHas) {
				t.Errorf("path = %q, want to contain %q", gotPath, tt.wantPathHas)
			}
		})
	}
}
