package har

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/harness/cli/modules/har/pkg/har/genapi/ar"
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

// The index must hold HAR file paths, not names: HasFile maps a stored path
// back to the source form, so a name-only entry never matches on re-runs.
func TestBuildExistingIndexStoresFilePaths(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch p := r.URL.Path; {
		case strings.HasSuffix(p, "/artifacts"):
			_, _ = io.WriteString(w, `{"status":"SUCCESS","data":{"artifacts":[{"name":"@acme/demo"}]}}`)
		case strings.HasSuffix(p, "/versions"):
			_, _ = io.WriteString(w, `{"status":"SUCCESS","data":{"artifactVersions":[{"name":"1.0.1","fileCount":1}]}}`)
		case strings.HasSuffix(p, "/files"):
			_, _ = io.WriteString(w, `{"status":"SUCCESS","data":{"files":[`+
				`{"name":"@acme/demo-1.0.1.tgz","path":"/@acme/demo/1.0.1/@acme/demo-1.0.1.tgz"}]}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	api, err := ar.NewClientWithResponses(srv.URL)
	if err != nil {
		t.Fatalf("new api client: %v", err)
	}
	idx, err := (&client{apiClient: api}).buildExistingIndex(context.Background(), "acct/npmtest", 1)
	if err != nil {
		t.Fatalf("buildExistingIndex: %v", err)
	}
	if !idx.HasFile("@acme/demo", "1.0.1", "/@acme/demo/-/@acme/demo-1.0.1.tgz", types.NPM) {
		t.Error("expected the migrated npm tarball to be found in the index")
	}
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

func TestDownloadExistenceChecks(t *testing.T) {
	const tfBase = "/pkg/acct1/tfreg/terraform/v1"
	const cranBase = "/pkg/acct1/cranreg/cran/src/contrib/"
	tests := []struct {
		name      string
		check     func(c *client) (bool, error)
		responses map[string]int
		wantPaths []string
		want      bool
		wantErr   bool
	}{
		{
			name: "terraform module present",
			check: func(c *client) (bool, error) {
				return c.terraformFileExists(context.Background(), "acct1/org/proj/tfreg", "acme/demo/aws", "1.0.0",
					&types.File{Uri: "/acme/demo/aws/1.0.0/demo-1.0.0.tar.gz"})
			},
			responses: map[string]int{tfBase + "/modules/acme/demo/aws/1.0.0/download": http.StatusNoContent},
			wantPaths: []string{tfBase + "/modules/acme/demo/aws/1.0.0/download"},
			want:      true,
		},
		{
			name: "terraform provider absent",
			check: func(c *client) (bool, error) {
				return c.terraformFileExists(context.Background(), "acct1/org/proj/tfreg", "acme/aws", "5.0.0",
					&types.File{Uri: "/acme/aws/5.0.0/terraform-provider-aws_5.0.0_linux_amd64.zip"})
			},
			wantPaths: []string{tfBase + "/providers/acme/aws/5.0.0/terraform-provider-aws_5.0.0_linux_amd64.zip"},
			want:      false,
		},
		{
			name: "cran redirect to blob means present",
			check: func(c *client) (bool, error) {
				return c.cranFileExists(context.Background(), "acct1/org/proj/cranreg", "src/contrib/demo_1.0.0.tar.gz")
			},
			responses: map[string]int{cranBase + "demo_1.0.0.tar.gz": http.StatusTemporaryRedirect},
			wantPaths: []string{cranBase + "demo_1.0.0.tar.gz"},
			want:      true,
		},
		{
			name: "cran superseded source found in archive",
			check: func(c *client) (bool, error) {
				return c.cranFileExists(context.Background(), "acct1/org/proj/cranreg", "src/contrib/demo_1.0.0.tar.gz")
			},
			responses: map[string]int{cranBase + "Archive/demo/demo_1.0.0.tar.gz": http.StatusTemporaryRedirect},
			wantPaths: []string{cranBase + "demo_1.0.0.tar.gz", cranBase + "Archive/demo/demo_1.0.0.tar.gz"},
			want:      true,
		},
		{
			name: "cran binary has no archive fallback",
			check: func(c *client) (bool, error) {
				return c.cranFileExists(context.Background(), "acct1/org/proj/cranreg",
					"bin/windows/contrib/4.4/demo_1.0.0.zip")
			},
			wantPaths: []string{"/pkg/acct1/cranreg/cran/bin/windows/contrib/4.4/demo_1.0.0.zip"},
			want:      false,
		},
		{
			name: "server error is not absence",
			check: func(c *client) (bool, error) {
				return c.cranFileExists(context.Background(), "acct1/org/proj/cranreg", "src/contrib/demo_1.0.0.tar.gz")
			},
			responses: map[string]int{cranBase + "demo_1.0.0.tar.gz": http.StatusInternalServerError},
			wantPaths: []string{cranBase + "demo_1.0.0.tar.gz"},
			wantErr:   true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotPaths []string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotPaths = append(gotPaths, r.URL.Path)
				status, ok := tt.responses[r.URL.Path]
				if !ok {
					status = http.StatusNotFound
				}
				if status == http.StatusTemporaryRedirect {
					w.Header().Set("Location", "/blob")
				}
				w.WriteHeader(status)
			}))
			defer srv.Close()
			c := &client{client: &http.Client{}, url: srv.URL, accountID: "acct1"}

			got, err := tt.check(c)
			if strings.Join(gotPaths, ",") != strings.Join(tt.wantPaths, ",") {
				t.Errorf("paths = %v, want %v", gotPaths, tt.wantPaths)
			}
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("exists = %v, want %v", got, tt.want)
			}
		})
	}
}
