package har

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/harness/cli/modules/har/pkg/har/migrate/types"
)

func TestUploadExistsPaths(t *testing.T) {
	const base = "/pkg/acct1/reg/"
	tests := []struct {
		name         string
		artifactType types.ArtifactType
		file         *types.File
		artifact     string
		version      string
		metadata     map[string]interface{}
		wantMethod   string
		wantPath     string
	}{
		{
			name:         "conda",
			artifactType: types.CONDA,
			file:         &types.File{Uri: "linux-64/demo-1.0-0.conda"},
			metadata:     map[string]interface{}{"X-Subdir": "linux-64", "X-File-Name": "demo-1.0-0.conda"},
			wantMethod:   http.MethodHead,
			wantPath:     base + "conda/linux-64/demo-1.0-0.conda",
		},
		{
			name:         "swift",
			artifactType: types.SWIFT,
			file:         &types.File{Uri: "acme/demo/1.0.0/demo-1.0.0.zip"},
			artifact:     "acme.demo",
			version:      "1.0.0",
			wantMethod:   http.MethodGet,
			wantPath:     base + "swift/acme/demo/1.0.0",
		},
		{
			name:         "composer dist name has no vendor",
			artifactType: types.COMPOSER,
			file:         &types.File{Uri: "acme/demo/acme-demo-1.0.0.zip"},
			artifact:     "acme/demo",
			version:      "1.0.0",
			wantMethod:   http.MethodGet,
			wantPath:     base + "composer/dist/acme/demo/1.0.0/demo-1.0.0.zip",
		},
		{
			name:         "rpm name with dashes",
			artifactType: types.RPM,
			file:         &types.File{Uri: "x86_64/my-demo-pkg-1.2.3-4.el9.x86_64.rpm"},
			wantMethod:   http.MethodGet,
			wantPath:     base + "rpm/package/my-demo-pkg/1.2.3-4.el9/x86_64/my-demo-pkg-1.2.3-4.el9.x86_64.rpm",
		},
		{
			name:         "conan package file without user and channel",
			artifactType: types.CONAN,
			file:         &types.File{Uri: "_/demo/1.0/_/r1/package/p1/r2/conan_package.tgz"},
			metadata: map[string]interface{}{"layer": "package", "name": "demo", "version": "1.0", "rrev": "r1",
				"pkgid": "p1", "prev": "r2", "filename": "conan_package.tgz"},
			wantMethod: http.MethodGet,
			wantPath: base + "conan/v2/conans/demo/1.0/_/_/revisions/r1/packages/p1/revisions/r2/files/" +
				"conan_package.tgz",
		},
		{
			name:         "debian source file in lib pool",
			artifactType: types.DEBIAN,
			file:         &types.File{Uri: "pool/libdemo_1.0.orig.tar.gz"},
			metadata: map[string]interface{}{"distribution": "stable", "component": "main", "fileType": "src",
				"packageName": "libdemo", "version": "1.0"},
			wantMethod: http.MethodGet,
			wantPath:   base + "debian/pool/main/libd/libdemo/1.0/libdemo_1.0.orig.tar.gz",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotMethod, gotPath, gotAccept string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotMethod, gotPath, gotAccept = r.Method, r.URL.Path, r.Header.Get("Accept")
				w.Header().Set("Location", "/blob")
				w.WriteHeader(http.StatusTemporaryRedirect)
			}))
			defer srv.Close()
			c := &client{client: &http.Client{}, url: srv.URL, accountID: "acct1"}

			got, err := c.uploadExists(context.Background(), "reg", tt.file, tt.artifact, tt.version,
				tt.artifactType, tt.metadata)
			if err != nil {
				t.Fatalf("err = %v", err)
			}
			if !got {
				t.Errorf("exists = false, want true")
			}
			if gotMethod != tt.wantMethod || gotPath != tt.wantPath {
				t.Errorf("request = %s %s, want %s %s", gotMethod, gotPath, tt.wantMethod, tt.wantPath)
			}
			if tt.artifactType == types.SWIFT && gotAccept != "application/vnd.swift.registry.v1+json" {
				t.Errorf("Accept = %q, want the Swift registry media type", gotAccept)
			}
		})
	}
}

func TestParseRPMFileName(t *testing.T) {
	tests := []struct {
		file                       string
		name, versionRelease, arch string
		ok                         bool
	}{
		{"demo-1.0.0-1.noarch.rpm", "demo", "1.0.0-1", "noarch", true},
		{"my-demo-pkg-1.2.3-4.el9.x86_64.rpm", "my-demo-pkg", "1.2.3-4.el9", "x86_64", true},
		{"demo-1.0.0.noarch.rpm", "", "", "", false},
		{"demo-1.0.0-1.rpm.sig", "", "", "", false},
	}
	for _, tt := range tests {
		name, versionRelease, arch, ok := parseRPMFileName(tt.file)
		if name != tt.name || versionRelease != tt.versionRelease || arch != tt.arch || ok != tt.ok {
			t.Errorf("parseRPMFileName(%q) = %q, %q, %q, %v; want %q, %q, %q, %v", tt.file,
				name, versionRelease, arch, ok, tt.name, tt.versionRelease, tt.arch, tt.ok)
		}
	}
}

func TestDebianFileExistsUsesDistributionIndex(t *testing.T) {
	const dists = "/pkg/acct1/reg/debian/dists/"
	indexes := map[string]string{
		dists + "stable/main/binary-amd64/Packages": "Package: demo\nVersion: 1.0\n" +
			"Filename: pool/main/d/demo/1.0/demo_1.0_amd64.deb\n\nPackage: other\n" +
			"Filename: pool/main/o/other/2.0/other_2.0_amd64.deb\n",
		dists + "stable/main/source/Sources": "Package: demo\nVersion: 1.0\nFiles:\n" +
			" 0123 10 demo_1.0.dsc\n 4567 20 demo_1.0.orig.tar.gz\nChecksums-Sha256:\n 89ab 10 demo_1.0.dsc\n",
	}
	requests := map[string]int{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests[r.URL.Path]++
		body, ok := indexes[r.URL.Path]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()
	c := &client{client: &http.Client{}, url: srv.URL, accountID: "acct1"}

	tests := []struct {
		name, file, dist, fileType string
		want                       bool
	}{
		{"deb listed", "demo_1.0_amd64.deb", "stable", "deb", true},
		{"deb in same index", "other_2.0_amd64.deb", "stable", "deb", true},
		{"deb not listed", "demo_2.0_amd64.deb", "stable", "deb", false},
		{"pool shared but other distribution has no index", "demo_1.0_amd64.deb", "testing", "deb", false},
		{"dsc listed in Sources", "demo_1.0.dsc", "stable", "dsc", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			meta := map[string]interface{}{"distribution": tt.dist, "component": "main", "fileType": tt.fileType}
			got, err := c.uploadExists(context.Background(), "reg", &types.File{Uri: "pool/" + tt.file}, tt.file,
				tt.file, types.DEBIAN, meta)
			if err != nil {
				t.Fatalf("err = %v", err)
			}
			if got != tt.want {
				t.Errorf("exists = %v, want %v", got, tt.want)
			}
		})
	}
	if n := requests[dists+"stable/main/binary-amd64/Packages"]; n != 1 {
		t.Errorf("Packages fetched %d times, want 1", n)
	}
}

type apiKeyTransport struct{}

func (apiKeyTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("x-api-key", "secret")
	return http.DefaultTransport.RoundTrip(r)
}

func TestFetchWithoutLeakingAuthToOtherHost(t *testing.T) {
	var blobKey string
	blob := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		blobKey = r.Header.Get("x-api-key")
		_, _ = w.Write([]byte("Filename: pool/main/d/demo/1.0/demo_1.0_amd64.deb\n"))
	}))
	defer blob.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-api-key") == "" {
			t.Error("registry request is missing the API key")
		}
		http.Redirect(w, r, blob.URL+"/signed", http.StatusTemporaryRedirect)
	}))
	defer srv.Close()
	c := &client{client: &http.Client{Transport: apiKeyTransport{}}, url: srv.URL, accountID: "acct1"}

	got, err := c.uploadExists(context.Background(), "reg", &types.File{Uri: "demo_1.0_amd64.deb"}, "", "",
		types.DEBIAN, map[string]interface{}{"distribution": "stable", "component": "main", "fileType": "deb"})
	if err != nil || !got {
		t.Fatalf("exists = %v, err = %v; want true, nil", got, err)
	}
	if blobKey != "" {
		t.Errorf("API key sent to redirect host")
	}
}
