// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package nexus

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/harness/cli/modules/har/pkg/har/migrate/tree"
	"github.com/harness/cli/modules/har/pkg/har/migrate/types"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
)

func newTestAdapter(t *testing.T, endpoint string) *adapter {
	t.Helper()
	a, err := newAdapter(types.RegistryConfig{
		Endpoint:    endpoint,
		Credentials: types.CredentialsConfig{Username: "u", Password: "p"},
	})
	if err != nil {
		t.Fatalf("newAdapter: %v", err)
	}
	return a.(*adapter)
}

func nexusRepoServer(t *testing.T, repoJSON string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/service/rest/v1/repositories/docker/hosted/") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(repoJSON))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestGetOCIImagePathPrefersSourcePackageHostname(t *testing.T) {
	// Endpoint is unreachable: a hostname override must not call the Nexus API.
	a := newTestAdapter(t, "http://127.0.0.1:1")

	got, err := a.GetOCIImagePath("docker-repo", "docker.example.com", "team/app")
	if err != nil {
		t.Fatalf("GetOCIImagePath: %v", err)
	}
	if want := "docker.example.com/team/app"; got != want {
		t.Errorf("image path = %q, want %q", got, want)
	}
}

func TestGetOCIImagePathDiscoversConnectorPort(t *testing.T) {
	srv := nexusRepoServer(t, `{"name":"docker-repo","docker":{"httpPort":5000}}`)
	a := newTestAdapter(t, srv.URL)

	got, err := a.GetOCIImagePath("docker-repo", "", "team/app")
	if err != nil {
		t.Fatalf("GetOCIImagePath: %v", err)
	}
	if want := "127.0.0.1:5000/team/app"; got != want {
		t.Errorf("image path = %q, want %q", got, want)
	}
}

// A repository without connector ports (proxy/subdomain setups) must surface an
// error that points at sourcePackageHostname, not an empty path.
func TestGetOCIImagePathWithoutPortsExplainsWorkaround(t *testing.T) {
	srv := nexusRepoServer(t, `{"name":"docker-repo","docker":{"v1Enabled":false}}`)
	a := newTestAdapter(t, srv.URL)

	got, err := a.GetOCIImagePath("docker-repo", "", "team/app")
	if err == nil {
		t.Fatalf("GetOCIImagePath = %q, want error", got)
	}
	if got != "" {
		t.Errorf("image path = %q, want empty on error", got)
	}
	for _, want := range []string{"no Docker connector port configured", "sourcePackageHostname"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}

func TestKeychainMatchesHostIgnoringPort(t *testing.T) {
	tests := []struct {
		name                  string
		endpoint              string
		sourcePackageHostname string
		registry              string
		wantAuth              bool
	}{
		{"endpoint with REST port, docker connector port", "https://nexus.example.com:8081", "", "nexus.example.com:5000", true},
		{"endpoint without port", "https://nexus.example.com", "", "nexus.example.com:5000", true},
		{"host compare is case-insensitive", "https://Nexus.Example.com:8081", "", "nexus.example.com:5000", true},
		{"hostname override with port", "https://nexus.example.com:8081", "docker.example.com:5000", "docker.example.com:5000", true},
		{"hostname override replaces endpoint host", "https://nexus.example.com:8081", "docker.example.com", "nexus.example.com:5000", false},
		{"unrelated registry stays anonymous", "https://nexus.example.com:8081", "", "ghcr.io", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := newTestAdapter(t, tt.endpoint)
			kc, err := a.GetKeyChain(tt.sourcePackageHostname)
			if err != nil {
				t.Fatalf("GetKeyChain: %v", err)
			}
			reg, err := name.NewRegistry(tt.registry)
			if err != nil {
				t.Fatalf("NewRegistry(%q): %v", tt.registry, err)
			}
			auth, err := kc.Resolve(reg)
			if err != nil {
				t.Fatalf("Resolve: %v", err)
			}
			if gotAuth := auth != authn.Anonymous; gotAuth != tt.wantAuth {
				t.Errorf("authenticated = %v, want %v", gotAuth, tt.wantAuth)
			}
		})
	}
}

// Maven versions must resolve to their own directory in the file tree; a bare
// version string misses it and the whole package is silently skipped.
func TestMavenVersionPathsResolveInFileTree(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/service/rest/v1/components" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"items":[
			{"group":"org.apache.commons","name":"commons-lang3","version":"3.12.0","assets":[{"fileSize":10},{"fileSize":5}]},
			{"group":"org.apache.commons","name":"commons-lang3","version":"3.14.0","assets":[{"fileSize":7}]},
			{"group":"junit","name":"junit","version":"4.13.2","assets":[]}]}`))
	}))
	t.Cleanup(srv.Close)
	a := newTestAdapter(t, srv.URL)

	var files []types.File
	for _, uri := range []string{
		"org/apache/commons/commons-lang3/3.12.0/commons-lang3-3.12.0.jar",
		"org/apache/commons/commons-lang3/3.12.0/commons-lang3-3.12.0.pom",
		"org/apache/commons/commons-lang3/3.14.0/commons-lang3-3.14.0.jar",
		"junit/junit/4.13.2/junit-4.13.2.jar",
	} {
		files = append(files, types.File{Name: uri[strings.LastIndex(uri, "/")+1:], Uri: uri})
	}
	root := tree.TransformToTree(files)

	versions, err := a.GetVersions(types.Package{}, root, "maven-releases", "org.apache.commons/commons-lang3", types.MAVEN)
	if err != nil {
		t.Fatalf("GetVersions: %v", err)
	}
	if len(versions) != 2 {
		t.Fatalf("got %d versions, want 2: %+v", len(versions), versions)
	}
	wantFiles := map[string]int{"3.12.0": 2, "3.14.0": 1}
	for _, v := range versions {
		node, err := tree.GetNodeForPath(root, v.Path)
		if err != nil {
			t.Fatalf("version %s path %q not found in tree: %v", v.Name, v.Path, err)
		}
		got, err := tree.GetAllFiles(node)
		if err != nil {
			t.Fatalf("GetAllFiles(%s): %v", v.Name, err)
		}
		if len(got) != wantFiles[v.Name] {
			t.Errorf("version %s resolves to %d file(s), want %d", v.Name, len(got), wantFiles[v.Name])
		}
	}
}
