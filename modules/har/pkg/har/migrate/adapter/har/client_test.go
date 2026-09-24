package har

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/harness/cli/modules/har/pkg/har/genapi/ar_pkg"
	"github.com/harness/cli/modules/har/pkg/har/migrate/types"
)

// newTestClient builds a har client whose generated pkg client points at the
// given test server URL.
func newTestClient(t *testing.T, serverURL string) *client {
	t.Helper()
	pc, err := ar_pkg.NewClientWithResponses(serverURL)
	if err != nil {
		t.Fatalf("new pkg client: %v", err)
	}
	return &client{pkgClient: pc, url: serverURL, accountID: "acct1"}
}

// TestUploadTerraformFile covers module (.tar.gz) and provider (.zip) upload paths
// including conflict (409 → ErrArtifactAlreadyExists) and non-2xx error handling.
func TestUploadTerraformFile(t *testing.T) {
	tests := []struct {
		name         string
		fileName     string
		pkg          string
		version      string
		status       int
		wantConflict bool
		wantErr      bool
		wantPathHas  string
	}{
		{
			name:     "module upload success",
			fileName: "vpc-1.0.0.tar.gz", pkg: "hashicorp/vpc/aws", version: "1.0.0",
			status: http.StatusCreated, wantPathHas: "/hashicorp/vpc/aws/1.0.0",
		},
		{
			name:     "module upload tgz extension",
			fileName: "vpc-1.0.0.tgz", pkg: "hashicorp/vpc/aws", version: "1.0.0",
			status: http.StatusCreated, wantPathHas: "/hashicorp/vpc/aws/1.0.0",
		},
		{
			name:     "module upload conflict",
			fileName: "vpc-1.0.0.tar.gz", pkg: "hashicorp/vpc/aws", version: "1.0.0",
			status: http.StatusConflict, wantConflict: true,
		},
		{
			name:     "module upload error",
			fileName: "vpc-1.0.0.tar.gz", pkg: "hashicorp/vpc/aws", version: "1.0.0",
			status: http.StatusBadRequest, wantErr: true,
		},
		{
			name:     "provider upload success",
			fileName: "terraform-provider-aws_2.0.0_linux_amd64.zip", pkg: "hashicorp/aws", version: "2.0.0",
			status: http.StatusCreated, wantPathHas: "/hashicorp/aws/2.0.0",
		},
		{
			name:     "provider upload conflict",
			fileName: "terraform-provider-aws_2.0.0_linux_amd64.zip", pkg: "hashicorp/aws", version: "2.0.0",
			status: http.StatusConflict, wantConflict: true,
		},
		{
			name:     "provider upload error",
			fileName: "terraform-provider-aws_2.0.0_linux_amd64.zip", pkg: "hashicorp/aws", version: "2.0.0",
			status: http.StatusInternalServerError, wantErr: true,
		},
		{
			name:     "unsupported extension",
			fileName: "terraform-something.txt", pkg: "hashicorp/aws", version: "2.0.0",
			status: http.StatusCreated, wantErr: true,
		},
		{
			name:     "module bad pkg format",
			fileName: "vpc-1.0.0.tar.gz", pkg: "hashicorp", version: "1.0.0",
			status: http.StatusCreated, wantErr: true,
		},
		{
			name:     "provider bad pkg format",
			fileName: "terraform-provider-aws_2.0.0_linux_amd64.zip", pkg: "hashicorp", version: "2.0.0",
			status: http.StatusCreated, wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotPath string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotPath = r.URL.Path
				if tt.status >= 400 {
					w.WriteHeader(tt.status)
					_, _ = w.Write([]byte("rejected"))
					return
				}
				w.WriteHeader(tt.status)
			}))
			defer srv.Close()

			c := newTestClient(t, srv.URL)
			f := &types.File{Name: tt.fileName, Uri: "/" + tt.fileName}
			body := io.NopCloser(strings.NewReader("bytes"))

			err := c.uploadTerraformFile("reg1", f, tt.pkg, tt.version, body)

			switch {
			case tt.wantConflict:
				if !errors.Is(err, types.ErrArtifactAlreadyExists) {
					t.Fatalf("expected ErrArtifactAlreadyExists, got %v", err)
				}
			case tt.wantErr:
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
			default:
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if tt.wantPathHas != "" && !strings.Contains(gotPath, tt.wantPathHas) {
					t.Errorf("path = %q, want to contain %q", gotPath, tt.wantPathHas)
				}
			}
		})
	}
}
