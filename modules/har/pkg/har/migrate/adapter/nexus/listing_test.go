// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package nexus

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	adp "github.com/harness/cli/modules/har/pkg/har/migrate/adapter"
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
	srv, _ := nexusComponentsServer(t, listPageAttempts)
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

func TestListingRetriesTransientFailures(t *testing.T) {
	srv, listings := nexusComponentsServer(t, 2)
	a := newTestAdapter(t, srv.URL)

	files, err := a.GetFiles("repo")
	if err != nil {
		t.Fatalf("GetFiles: %v, want the 502s retried", err)
	}
	if len(files) != 5 {
		t.Errorf("files = %d, want 5", len(files))
	}
	if got := listings.Load(); got != 5 {
		t.Errorf("listing requests = %d, want 5 (two failed attempts, then three pages)", got)
	}
}

func TestListingDoesNotRetryClientErrors(t *testing.T) {
	var listings atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/service/rest/v1/repositories" {
			_, _ = w.Write([]byte(`[{"name":"repo","format":"maven2","type":"hosted"}]`))
			return
		}
		listings.Add(1)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(srv.Close)
	a := newTestAdapter(t, srv.URL)

	if _, err := a.GetFiles("repo"); err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("GetFiles error = %v, want the 401", err)
	}
	if got := listings.Load(); got != 1 {
		t.Errorf("listing requests = %d, want 1 (a 401 will not heal)", got)
	}
}

func TestListingRetriesAStalledPage(t *testing.T) {
	var listings atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/service/rest/v1/repositories":
			_, _ = w.Write([]byte(`[{"name":"repo","format":"maven2","type":"hosted"}]`))
		case listings.Add(1) == 1:
			select {
			case <-r.Context().Done():
			case <-time.After(10 * time.Second):
			}
		default:
			_, _ = w.Write([]byte(componentPages["p3"]))
		}
	}))
	t.Cleanup(srv.Close)
	a := newTestAdapter(t, srv.URL)
	a.client.listPageTimeout = 100 * time.Millisecond

	files, err := a.GetFiles("repo")
	if err != nil {
		t.Fatalf("GetFiles: %v, want the stalled request abandoned and retried", err)
	}
	if len(files) != 1 {
		t.Errorf("files = %d, want 1", len(files))
	}
}

func TestListingReportsTheStalledRequestWhileItIsInFlight(t *testing.T) {
	var listings atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/service/rest/v1/repositories":
			_, _ = w.Write([]byte(`[{"name":"repo","format":"maven2","type":"hosted"}]`))
		case listings.Add(1) == 1:
			select {
			case <-r.Context().Done():
			case <-time.After(10 * time.Second):
			}
		default:
			_, _ = w.Write([]byte(componentPages["p3"]))
		}
	}))
	t.Cleanup(srv.Close)
	a := newTestAdapter(t, srv.URL)
	a.client.listPageTimeout = 300 * time.Millisecond
	a.client.progressEvery = 20 * time.Millisecond

	var mu sync.Mutex
	var lines []string
	progress := func(msg string) {
		mu.Lock()
		defer mu.Unlock()
		lines = append(lines, msg)
	}
	if _, err := a.GetFilesWithProgress("repo", progress); err != nil {
		t.Fatalf("GetFilesWithProgress: %v", err)
	}

	mu.Lock()
	returned := len(lines)
	inFlight := 0
	for _, l := range lines {
		if strings.Contains(l, "fetching page 1 (request running") && strings.Contains(l, "attempt 1/") {
			inFlight++
		}
	}
	mu.Unlock()
	if inFlight == 0 {
		t.Errorf("no status line named the stalled page while its request was in flight; got %q", lines)
	}

	time.Sleep(100 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if len(lines) != returned {
		t.Errorf("%d status line(s) printed after the listing returned", len(lines)-returned)
	}
}

func TestDebugLogsEachNexusAPICallWithoutSecrets(t *testing.T) {
	srv, _ := nexusComponentsServer(t, 0)
	a := newTestAdapter(t, strings.Replace(srv.URL, "http://", "http://admin:s3cret@", 1))

	var out strings.Builder
	prev := log.Logger
	log.Logger = zerolog.New(&out)
	t.Cleanup(func() { log.Logger = prev })

	if _, err := a.GetFiles("repo"); err != nil {
		t.Fatalf("GetFiles: %v", err)
	}

	got := out.String()
	for _, want := range []string{
		`"message":"Nexus API request"`,
		`"message":"Nexus API response"`,
		`/service/rest/v1/components?continuationToken=p2&repository=repo`,
		`"status":200`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("debug log is missing %s:\n%s", want, got)
		}
	}
	if strings.Contains(got, "s3cret") {
		t.Errorf("debug log leaks the endpoint password:\n%s", got)
	}
}

func TestListingStopsOnRepeatedContinuationToken(t *testing.T) {
	var listings atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/service/rest/v1/repositories" {
			_, _ = w.Write([]byte(`[{"name":"repo","format":"maven2","type":"hosted"}]`))
			return
		}
		listings.Add(1)
		_, _ = w.Write([]byte(`{"items":[],"continuationToken":"same"}`))
	}))
	t.Cleanup(srv.Close)
	a := newTestAdapter(t, srv.URL)

	if _, err := a.GetFiles("repo"); err == nil || !strings.Contains(err.Error(), "again") {
		t.Fatalf("GetFiles error = %v, want the repeated token reported", err)
	}
	if got := listings.Load(); got != 2 {
		t.Errorf("listing requests = %d, want 2", got)
	}
}

type batchView struct {
	packages []string
	libA     []string
	files    int
}

func collectBatches(t *testing.T, a *adapter, size int, stopAfter int) ([]batchView, error) {
	t.Helper()
	var got []batchView
	err := a.ListBatches(context.Background(), "repo", size, nil, func(batch adp.Adapter, files []types.File) error {
		pkgs, err := batch.GetPackages("repo", types.MAVEN, nil)
		if err != nil {
			t.Errorf("GetPackages: %v", err)
		}
		versions, err := batch.GetVersions(types.Package{}, nil, "repo", "com.acme/lib-a", types.MAVEN)
		if err != nil {
			t.Errorf("GetVersions: %v", err)
		}
		v := batchView{files: len(files)}
		for _, p := range pkgs {
			v.packages = append(v.packages, p.Name)
		}
		for _, ver := range versions {
			v.libA = append(v.libA, ver.Name)
		}
		sort.Strings(v.packages)
		got = append(got, v)
		if stopAfter > 0 && len(got) == stopAfter {
			return io.ErrClosedPipe
		}
		return nil
	})
	return got, err
}

func TestListBatchesScopesEachBatchToItsComponents(t *testing.T) {
	srv, listings := nexusComponentsServer(t, 0)
	a := newTestAdapter(t, srv.URL)

	// Pages hold 2, 1 and 1 components: the first page fills a batch, the last two
	// are flushed together as the remainder.
	got, err := collectBatches(t, a, 2, 0)
	if err != nil {
		t.Fatalf("ListBatches: %v", err)
	}

	want := []batchView{
		{packages: []string{"com.acme/lib-a"}, libA: []string{"1.0", "2.0"}, files: 3},
		{packages: []string{"com.acme/lib-a", "com.acme/lib-b"}, libA: []string{"3.0"}, files: 2},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("batches = %+v, want %+v", got, want)
	}
	if n := listings.Load(); n != 3 {
		t.Errorf("listing requests = %d, want 3 (batch adapters must not re-list)", n)
	}
}

func TestListBatchesStopsListingWhenCallbackFails(t *testing.T) {
	srv, listings := nexusComponentsServer(t, 0)
	a := newTestAdapter(t, srv.URL)

	got, err := collectBatches(t, a, 1, 1)
	if !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("ListBatches error = %v, want the callback's error", err)
	}
	if len(got) != 1 {
		t.Errorf("batches = %d, want 1", len(got))
	}
	if n := listings.Load(); n != 1 {
		t.Errorf("listing requests = %d, want 1 (no pages after the failure)", n)
	}
}

func TestListBatchesDeliversEarlierBatchesBeforeAListingError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/service/rest/v1/repositories":
			_, _ = w.Write([]byte(`[{"name":"repo","format":"maven2","type":"hosted"}]`))
		case r.URL.Query().Get("continuationToken") == "":
			_, _ = w.Write([]byte(componentPages[""]))
		default:
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	t.Cleanup(srv.Close)
	a := newTestAdapter(t, srv.URL)

	got, err := collectBatches(t, a, 1, 0)
	if err == nil || !strings.Contains(err.Error(), "page 2") {
		t.Fatalf("ListBatches error = %v, want a failure on page 2", err)
	}
	if len(got) != 1 {
		t.Errorf("batches = %d, want the first page's batch delivered before the error", len(got))
	}
}
