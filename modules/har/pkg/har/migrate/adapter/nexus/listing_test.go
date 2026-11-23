// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package nexus

import (
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/harness/cli/modules/har/pkg/har/migrate/types"
)

// componentPages are served three at a time by nexusComponentsServer. com.acme/lib-a
// has versions on the first and last page, so a lookup must see every page.
var componentPages = map[string]string{
	"": `{"items":[
		{"group":"com.acme","name":"lib-a","version":"1.0","assets":[
			{"path":"com/acme/lib-a/1.0/lib-a-1.0.pom","fileSize":10,"checksum":{"sha1":"a1","sha256":"b1"}},
			{"path":"com/acme/lib-a/1.0/lib-a-1.0.jar","fileSize":20,"checksum":{"sha1":"a2","sha256":"b2"}}]},
		{"group":"com.acme","name":"lib-a","version":"2.0","assets":[
			{"path":"com/acme/lib-a/2.0/lib-a-2.0.jar","fileSize":30}]}],
		"continuationToken":"p2"}`,
	"p2": `{"items":[
		{"group":"com.acme","name":"lib-b","version":"1.0","assets":[
			{"path":"com/acme/lib-b/1.0/lib-b-1.0.jar","fileSize":40}]}],
		"continuationToken":"p3"}`,
	"p3": `{"items":[
		{"group":"com.acme","name":"lib-a","version":"3.0","assets":[
			{"path":"com/acme/lib-a/3.0/lib-a-3.0.jar","fileSize":50}]}]}`,
}

// nexusComponentsServer serves a hosted maven repo named "repo" and counts the
// /components listing requests. failFirst makes that many listing requests answer 502.
func nexusComponentsServer(t *testing.T, failFirst int32) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var listings, failures atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/service/rest/v1/repositories":
			_, _ = w.Write([]byte(`[{"name":"repo","format":"maven2","type":"hosted"}]`))
		case r.URL.Path == "/service/rest/v1/components" && r.Method == http.MethodPost:
			_, _ = io.Copy(io.Discard, r.Body)
			w.WriteHeader(http.StatusCreated)
		case r.URL.Path == "/service/rest/v1/components":
			listings.Add(1)
			if failures.Add(1) <= failFirst {
				w.WriteHeader(http.StatusBadGateway)
				return
			}
			page, ok := componentPages[r.URL.Query().Get("continuationToken")]
			if !ok {
				http.NotFound(w, r)
				return
			}
			_, _ = w.Write([]byte(page))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &listings
}

func TestListingIsOnePassSharedByAllCallers(t *testing.T) {
	srv, listings := nexusComponentsServer(t, 0)
	a := newTestAdapter(t, srv.URL)

	// Packages migrate concurrently and each asks for its versions.
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := a.GetVersions(types.Package{}, nil, "repo", "com.acme/lib-a", types.MAVEN); err != nil {
				t.Errorf("GetVersions: %v", err)
			}
		}()
	}
	wg.Wait()

	files, err := a.GetFiles("repo")
	if err != nil {
		t.Fatalf("GetFiles: %v", err)
	}
	pkgs, err := a.GetPackages("repo", types.MAVEN, nil)
	if err != nil {
		t.Fatalf("GetPackages: %v", err)
	}
	versions, err := a.GetVersions(types.Package{}, nil, "repo", "com.acme/lib-a", types.MAVEN)
	if err != nil {
		t.Fatalf("GetVersions: %v", err)
	}

	if got := listings.Load(); got != 3 {
		t.Errorf("listing requests = %d, want 3 (one pass over three pages)", got)
	}
	if len(files) != 5 {
		t.Errorf("files = %d, want 5 across all pages", len(files))
	}
	var pkgNames, versionPaths []string
	for _, p := range pkgs {
		pkgNames = append(pkgNames, p.Name)
	}
	for _, v := range versions {
		versionPaths = append(versionPaths, v.Path)
	}
	sort.Strings(pkgNames)
	sort.Strings(versionPaths)
	if want := []string{"com.acme/lib-a", "com.acme/lib-b"}; !reflect.DeepEqual(pkgNames, want) {
		t.Errorf("packages = %v, want %v", pkgNames, want)
	}
	if want := []string{"com/acme/lib-a/1.0", "com/acme/lib-a/2.0", "com/acme/lib-a/3.0"}; !reflect.DeepEqual(versionPaths, want) {
		t.Errorf("lib-a version paths = %v, want %v (versions live on the first and last page)", versionPaths, want)
	}
	if versions[0].Size == 0 {
		t.Errorf("version size = 0, want the sum of its asset sizes")
	}
}

func TestFailedListingIsNotCached(t *testing.T) {
	srv, _ := nexusComponentsServer(t, 1)
	a := newTestAdapter(t, srv.URL)

	if _, err := a.GetFiles("repo"); err == nil {
		t.Fatal("GetFiles succeeded against a failing listing")
	}
	files, err := a.GetFiles("repo")
	if err != nil {
		t.Fatalf("GetFiles retry: %v, want the failure not to be remembered", err)
	}
	if len(files) != 5 {
		t.Errorf("files = %d, want 5", len(files))
	}
}

func TestUploadInvalidatesCachedListing(t *testing.T) {
	srv, listings := nexusComponentsServer(t, 0)
	a := newTestAdapter(t, srv.URL)
	if _, err := a.GetFiles("repo"); err != nil {
		t.Fatalf("GetFiles: %v", err)
	}

	err := a.UploadFile("repo", io.NopCloser(strings.NewReader("x")), &types.File{Name: "lib-a-4.0.jar"},
		nil, "lib-a", "4.0", types.MAVEN, map[string]interface{}{"groupId": "com.acme"})
	if err != nil {
		t.Fatalf("UploadFile: %v", err)
	}
	if _, err := a.GetFiles("repo"); err != nil {
		t.Fatalf("GetFiles: %v", err)
	}

	if got := listings.Load(); got != 6 {
		t.Errorf("listing requests = %d, want 6 (second pass after the upload)", got)
	}
}
