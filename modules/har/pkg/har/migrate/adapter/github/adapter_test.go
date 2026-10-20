// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package github

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/harness/cli/modules/har/pkg/har/migrate/tree"
	"github.com/harness/cli/modules/har/pkg/har/migrate/types"
	"github.com/harness/cli/modules/har/pkg/har/migrate/util"
)

// fakeGitHub serves the REST API and npm registry from one server; tarball
// downloads redirect to a separate "signed URL" server, as GitHub does.
type fakeGitHub struct {
	api        *httptest.Server
	signed     *httptest.Server
	ownerType  string
	pkgNames   []string
	signedAuth []string
}

func newFakeGitHub(t *testing.T, ownerType string, pkgNames []string) *fakeGitHub {
	t.Helper()
	f := &fakeGitHub{ownerType: ownerType, pkgNames: pkgNames}
	f.signed = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.signedAuth = append(f.signedAuth, r.Header.Get("Authorization"))
		_, _ = io.WriteString(w, "tarball:"+r.URL.Path)
	}))
	f.api = httptest.NewServer(http.HandlerFunc(f.serveAPI))
	t.Cleanup(func() {
		f.api.Close()
		f.signed.Close()
	})
	return f
}

func (f *fakeGitHub) serveAPI(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != "Bearer tok" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	path := r.URL.EscapedPath()
	switch {
	case path == "/users/acme":
		writeJSON(w, map[string]string{"type": f.ownerType})
	case path == "/orgs/acme/packages" || path == "/users/acme/packages":
		if (f.ownerType == "Organization") != strings.HasPrefix(path, "/orgs/") {
			http.NotFound(w, r)
			return
		}
		var page int
		_, _ = fmt.Sscanf(r.URL.Query().Get("page"), "%d", &page)
		start, end := (page-1)*pageSize, page*pageSize
		pkgs := []ghPackage{}
		for i := start; i < end && i < len(f.pkgNames); i++ {
			pkgs = append(pkgs, ghPackage{Name: f.pkgNames[i]})
		}
		writeJSON(w, pkgs)
	case strings.HasPrefix(path, "/@acme%2f"):
		name := strings.TrimPrefix(path, "/@acme%2f")
		writeJSON(w, map[string]interface{}{
			"name": "@acme/" + name,
			"versions": map[string]interface{}{
				"1.0.0": map[string]interface{}{"dist": map[string]string{"tarball": f.api.URL + "/download/@acme/" + name + "/1.0.0/sha1"}},
				"1.1.0": map[string]interface{}{"dist": map[string]string{"tarball": f.api.URL + "/download/@acme/" + name + "/1.1.0/sha2"}},
			},
			"time": map[string]string{"1.0.0": "2026-01-01T00:00:00Z", "1.1.0": "2026-06-01T00:00:00Z"},
		})
	case strings.HasPrefix(path, "/download/"):
		signedHost := strings.Replace(f.signed.URL, "127.0.0.1", "localhost", 1)
		http.Redirect(w, r, signedHost+strings.TrimPrefix(path, "/download"), http.StatusFound)
	default:
		http.NotFound(w, r)
	}
}

func writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func (f *fakeGitHub) adapter() *adapter {
	return &adapter{client: newClient(context.Background(), f.api.URL, f.api.URL, "tok", false)}
}

func TestGetFilesAndDownload_OrgOwner(t *testing.T) {
	f := newFakeGitHub(t, "Organization", []string{"widgets"})
	a := f.adapter()

	files, err := a.GetFiles("acme")
	if err != nil {
		t.Fatalf("GetFiles: %v", err)
	}
	want := []types.File{
		{Registry: "acme", Name: "widgets-1.0.0.tgz", Uri: "/@acme/widgets/-/@acme/widgets-1.0.0.tgz", LastModified: "2026-01-01T00:00:00Z"},
		{Registry: "acme", Name: "widgets-1.1.0.tgz", Uri: "/@acme/widgets/-/@acme/widgets-1.1.0.tgz", LastModified: "2026-06-01T00:00:00Z"},
	}
	if len(files) != len(want) {
		t.Fatalf("got %d files, want %d: %+v", len(files), len(want), files)
	}
	for i := range want {
		if files[i] != want[i] {
			t.Errorf("file %d = %+v, want %+v", i, files[i], want[i])
		}
	}

	body, _, err := a.DownloadFile("acme", files[1].Uri)
	if err != nil {
		t.Fatalf("DownloadFile: %v", err)
	}
	defer body.Close()
	got, _ := io.ReadAll(body)
	if string(got) != "tarball:/@acme/widgets/1.1.0/sha2" {
		t.Errorf("downloaded %q", got)
	}
	if len(f.signedAuth) != 1 || f.signedAuth[0] != "" {
		t.Errorf("token must not be forwarded to the redirect host, got Authorization headers %q", f.signedAuth)
	}
}

func TestGetFiles_UserOwnerPaginates(t *testing.T) {
	names := make([]string, pageSize+1)
	for i := range names {
		names[i] = fmt.Sprintf("pkg%03d", i)
	}
	f := newFakeGitHub(t, "User", names)

	files, err := f.adapter().GetFiles("acme")
	if err != nil {
		t.Fatalf("GetFiles: %v", err)
	}
	if len(files) != 2*len(names) {
		t.Errorf("got %d files, want %d", len(files), 2*len(names))
	}
}

func TestDownloadFile_UnknownURI(t *testing.T) {
	f := newFakeGitHub(t, "Organization", nil)
	if _, _, err := f.adapter().DownloadFile("acme", "/@acme/x/-/@acme/x-1.0.0.tgz"); err == nil {
		t.Fatal("expected error for a URI GetFiles never returned")
	}
}

func TestSearchFiles_DateFilterSelectsGetFilesURIs(t *testing.T) {
	f := newFakeGitHub(t, "Organization", []string{"widgets"})
	a := f.adapter()

	files, err := a.GetFiles("acme")
	if err != nil {
		t.Fatalf("GetFiles: %v", err)
	}
	searched, err := a.SearchFiles("acme")
	if err != nil {
		t.Fatalf("SearchFiles: %v", err)
	}
	after := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	mapping := &types.RegistryMapping{DateFilter: &types.DateFilter{Match: types.DateFilterMatchAny, CreatedAfter: &after}}

	kept := util.FilterFilesByDate(files, util.CreateMapOfFilteredFile(searched, mapping))
	if len(kept) != 1 || kept[0].Uri != "/@acme/widgets/-/@acme/widgets-1.1.0.tgz" {
		t.Errorf("date filter kept %+v, want only widgets 1.1.0", kept)
	}
}

func TestGetPackagesAndVersions_FromFilteredTree(t *testing.T) {
	f := newFakeGitHub(t, "Organization", []string{"widgets", "gadgets"})
	a := f.adapter()

	files, err := a.GetFiles("acme")
	if err != nil {
		t.Fatalf("GetFiles: %v", err)
	}
	var kept []types.File
	for _, file := range files {
		if file.Name != "gadgets-1.0.0.tgz" {
			kept = append(kept, file)
		}
	}
	root := tree.TransformToTree(kept)

	pkgs, err := a.GetPackages("acme", types.NPM, root)
	if err != nil {
		t.Fatalf("GetPackages: %v", err)
	}
	if len(pkgs) != 2 || pkgs[0].Name != "@acme/gadgets" || pkgs[1].Name != "@acme/widgets" {
		t.Fatalf("packages = %+v, want @acme/gadgets and @acme/widgets", pkgs)
	}

	wantVersions := map[string][]string{"@acme/gadgets": {"1.1.0"}, "@acme/widgets": {"1.0.0", "1.1.0"}}
	for _, pkg := range pkgs {
		pkgNode, err := tree.GetNodeForPath(root, pkg.Path)
		if err != nil {
			t.Fatalf("package path %q not in tree: %v", pkg.Path, err)
		}
		versions, err := a.GetVersions(pkg, pkgNode, "acme", pkg.Name, types.NPM)
		if err != nil {
			t.Fatalf("GetVersions(%s): %v", pkg.Name, err)
		}
		var got []string
		for _, v := range versions {
			got = append(got, v.Name)
			if _, err := tree.GetNodeForPath(pkgNode, v.Path); err != nil {
				t.Errorf("%s@%s path %q not in tree: %v", pkg.Name, v.Name, v.Path, err)
			}
		}
		if strings.Join(got, ",") != strings.Join(wantVersions[pkg.Name], ",") {
			t.Errorf("%s versions = %v, want %v", pkg.Name, got, wantVersions[pkg.Name])
		}
	}
}

func TestRegistryURLs(t *testing.T) {
	for _, endpoint := range []string{"https://api.github.com", "https://github.com/"} {
		api, npm, err := registryURLs(endpoint)
		if err != nil || api != githubAPIURL || npm != githubNpmURL {
			t.Errorf("registryURLs(%q) = %q, %q, %v", endpoint, api, npm, err)
		}
	}
	if _, _, err := registryURLs("https://ghes.example.com/api/v3"); err == nil {
		t.Error("expected GitHub Enterprise Server endpoint to be rejected")
	}
}
