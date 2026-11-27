// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package nexus

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	adp "github.com/harness/cli/modules/har/pkg/har/migrate/adapter"
	"github.com/harness/cli/modules/har/pkg/har/migrate/types"
)

// fakeMavenRepo serves repository "repo": tree maps a folder path ("" is the root) to
// its entries (folders end in "/"), metadata maps a maven-metadata.xml path to its
// content, and search maps "group:name" or "group:name:version" to its result pages.
type fakeMavenRepo struct {
	tree      map[string][]string
	metadata  map[string]string
	search    map[string][][]NexusComponent
	truncated map[string]bool
	// hook runs first for every request and reports whether it answered it.
	hook func(w http.ResponseWriter, r *http.Request) bool
}

func (f fakeMavenRepo) serve(t *testing.T) *client {
	t.Helper()
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		const browse, download = "/service/rest/repository/browse/repo/", "/repository/repo/"
		if f.hook != nil && f.hook(w, r) {
			return
		}
		switch {
		case r.URL.Path == "/service/rest/v1/repositories":
			_, _ = w.Write([]byte(`[{"name":"repo","format":"maven2","type":"hosted"}]`))
		case strings.HasPrefix(r.URL.Path, browse):
			dir := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, browse), "/")
			entries, ok := f.tree[dir]
			if !ok {
				http.NotFound(w, r)
				return
			}
			var b strings.Builder
			b.WriteString(`<html><body><table><tr><td><a href="../">Parent Directory</a></td></tr>`)
			for _, e := range entries {
				if name, isFolder := strings.CutSuffix(e, "/"); isFolder {
					fmt.Fprintf(&b, `<tr><td><a href="%s/">%s</a></td></tr>`, name, name)
				} else {
					fmt.Fprintf(&b, `<tr><td><a href="%s%s%s/%s">%s</a></td></tr>`, srv.URL, download, dir, e, e)
				}
			}
			b.WriteString(`</table>`)
			if f.truncated[dir] {
				b.WriteString(`<p>There may be additional items to display.</p>`)
			}
			b.WriteString(`</body></html>`)
			_, _ = w.Write([]byte(b.String()))
		case strings.HasPrefix(r.URL.Path, download):
			md, ok := f.metadata[strings.TrimPrefix(r.URL.Path, download)]
			if !ok {
				http.NotFound(w, r)
				return
			}
			_, _ = w.Write([]byte(md))
		case r.URL.Path == "/service/rest/v1/search":
			q := r.URL.Query()
			key := q.Get("maven.groupId") + ":" + q.Get("maven.artifactId")
			if v := q.Get("version") + q.Get("maven.baseVersion"); v != "" {
				key += ":" + v
			}
			pages := f.search[key]
			idx, _ := strconv.Atoi(q.Get("continuationToken"))
			resp := NexusSearchResponse{Items: []NexusComponent{}}
			if idx < len(pages) {
				resp.Items = pages[idx]
				if idx+1 < len(pages) {
					resp.ContinuationToken = strconv.Itoa(idx + 1)
				}
			}
			_ = json.NewEncoder(w).Encode(resp)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return &client{
		client:          srv.Client(),
		url:             srv.URL,
		listPageTimeout: 5 * time.Second,
		listRetryBase:   time.Millisecond,
		progressEvery:   time.Hour,
	}
}

func artifactMetadata(artifactID string, versions ...string) string {
	var v strings.Builder
	for _, s := range versions {
		v.WriteString("<version>" + s + "</version>")
	}
	return "<metadata><groupId>g</groupId><artifactId>" + artifactID +
		"</artifactId><versioning><versions>" + v.String() + "</versions></versioning></metadata>"
}

func comp(id, group, name, version string) NexusComponent {
	path := strings.ReplaceAll(group, ".", "/") + "/" + name + "/" + version + "/" + name + "-" + version + ".jar"
	return NexusComponent{ID: id, Group: group, Name: name, Version: version,
		Assets: []NexusAsset{{Path: path, FileSize: 1}}}
}

type parallelListing struct {
	ids, progress, warnings []string
	// unlisted holds "name path" per artifact passed to ListOptions.Unlisted.
	unlisted []string
}

func listParallel(t *testing.T, c *client, concurrency int) (parallelListing, error) {
	t.Helper()
	var got parallelListing
	var inFn atomic.Int32
	opts := adp.ListOptions{
		Concurrency: concurrency,
		Progress:    func(s string) { got.progress = append(got.progress, s) },
		Warn:        func(s string) { got.warnings = append(got.warnings, s) },
		Unlisted:    func(name, path string) { got.unlisted = append(got.unlisted, name+" "+path) },
	}
	err := c.streamMavenParallel(context.Background(), "repo", opts,
		func(items []NexusComponent) error {
			if inFn.Add(1) > 1 {
				t.Error("fn called concurrently")
			}
			defer inFn.Add(-1)
			time.Sleep(time.Millisecond)
			for _, it := range items {
				got.ids = append(got.ids, it.ID)
			}
			return nil
		})
	sort.Strings(got.ids)
	return got, err
}

func TestParallelListingFindsEveryArtifactLayout(t *testing.T) {
	c := fakeMavenRepo{
		tree: map[string][]string{
			"":    {"com/", "org/"},
			"com": {"acme/"},
			// lib has listed versions plus a nested group folder that is not a version.
			"com/acme":                  {"lib/", "plugins/"},
			"com/acme/lib":              {"1.0/", "2.0/", "ext/", "maven-metadata.xml", "maven-metadata.xml.sha1"},
			"com/acme/lib/ext":          {"tool/"},
			"com/acme/lib/ext/tool":     {"1.0/"},
			"com/acme/lib/ext/tool/1.0": {"tool-1.0.jar"},
			// A plugin group carries metadata without an artifactId.
			"com/acme/plugins":           {"my-plugin/", "maven-metadata.xml"},
			"com/acme/plugins/my-plugin": {"1.0/", "maven-metadata.xml"},
			// An artifact without metadata is recognised from its version folder.
			"org":              {"x/"},
			"org/x":            {"nometa/", "ghost/"},
			"org/x/nometa":     {"1.0/"},
			"org/x/nometa/1.0": {"nometa-1.0.jar", "nometa-1.0.pom"},
			"org/x/ghost":      {"1.0/", "maven-metadata.xml"},
		},
		metadata: map[string]string{
			"com/acme/lib/maven-metadata.xml":               artifactMetadata("lib", "1.0", "2.0"),
			"com/acme/plugins/maven-metadata.xml":           `<metadata><plugins><plugin><prefix>my</prefix></plugin></plugins></metadata>`,
			"com/acme/plugins/my-plugin/maven-metadata.xml": artifactMetadata("my-plugin", "1.0"),
			"org/x/ghost/maven-metadata.xml":                artifactMetadata("ghost", "1.0"),
		},
		search: map[string][][]NexusComponent{
			// Over-matches and a repeated component across pages must be dropped.
			"com.acme:lib": {
				{comp("lib-1", "com.acme", "lib", "1.0"), comp("other", "com.acme", "lib-extra", "1.0")},
				{comp("lib-1", "com.acme", "lib", "1.0"), comp("lib-2", "com.acme", "lib", "2.0")},
			},
			"com.acme.lib.ext:tool":      {{comp("tool-1", "com.acme.lib.ext", "tool", "1.0")}},
			"com.acme.plugins:my-plugin": {{comp("plugin-1", "com.acme.plugins", "my-plugin", "1.0")}},
			"org.x:nometa":               {{comp("nometa-1", "org.x", "nometa", "1.0")}},
		},
	}.serve(t)

	got, err := listParallel(t, c, 4)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"lib-1", "lib-2", "nometa-1", "plugin-1", "tool-1"}
	if strings.Join(got.ids, ",") != strings.Join(want, ",") {
		t.Fatalf("ids = %v, want %v", got.ids, want)
	}
	if len(got.warnings) != 1 || !strings.Contains(got.warnings[0], "org.x:ghost") {
		t.Fatalf("warnings = %q, want one naming org.x:ghost", got.warnings)
	}
	if strings.Join(got.unlisted, ",") != "org.x:ghost org/x/ghost" {
		t.Fatalf("unlisted = %q, want only org.x:ghost", got.unlisted)
	}
}

func TestParallelListingRelistsACappedArtifactPerVersion(t *testing.T) {
	capped := make([]NexusComponent, searchResultCap)
	for i := range capped {
		capped[i] = comp("old-"+strconv.Itoa(i), "g", "big", "x")
	}
	c := fakeMavenRepo{
		tree: map[string][]string{
			"":      {"g/"},
			"g":     {"big/"},
			"g/big": {"1.0/", "2.0/", "maven-metadata.xml"},
		},
		metadata: map[string]string{"g/big/maven-metadata.xml": artifactMetadata("big", "1.0", "2.0")},
		search: map[string][][]NexusComponent{
			"g:big":     {capped},
			"g:big:1.0": {{comp("big-1", "g", "big", "1.0")}},
			"g:big:2.0": {{comp("big-2", "g", "big", "2.0")}},
		},
	}
	got, err := listParallel(t, c.serve(t), 2)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(got.ids, "big-1") || !slices.Contains(got.ids, "big-2") {
		t.Fatalf("per-version components missing from %d ids", len(got.ids))
	}
	if len(got.unlisted) != 0 {
		t.Fatalf("unlisted = %q, want none", got.unlisted)
	}

	delete(c.search, "g:big:1.0")
	if got, err = listParallel(t, c.serve(t), 2); err != nil {
		t.Fatal(err)
	}
	if strings.Join(got.unlisted, ",") != "g:big:1.0 g/big/1.0" {
		t.Fatalf("unlisted = %q, want the version search found nothing for", got.unlisted)
	}

	c.search["g:big:2.0"] = [][]NexusComponent{capped}
	if _, err := listParallel(t, c.serve(t), 2); err == nil || !strings.Contains(err.Error(), "version 2.0 reached") {
		t.Fatalf("err = %v, want a capped-version error", err)
	}
}

func TestParallelListingFailsOnATruncatedBrowsePage(t *testing.T) {
	c := fakeMavenRepo{
		tree:      map[string][]string{"": {"g/"}, "g": {"a/"}, "g/a": {"1.0/"}, "g/a/1.0": {"a.jar"}},
		truncated: map[string]bool{"g": true},
	}.serve(t)
	if _, err := listParallel(t, c, 2); err == nil || !strings.Contains(err.Error(), "only part of this folder") {
		t.Fatalf("err = %v, want a truncation error", err)
	}
}

func TestParallelListingRejectsAnEmptyBrowseTree(t *testing.T) {
	c := fakeMavenRepo{tree: map[string][]string{"": {}}}.serve(t)
	if _, err := listParallel(t, c, 2); err == nil || !strings.Contains(err.Error(), "no Maven artifacts") {
		t.Fatalf("err = %v, want an empty-tree error", err)
	}
}

// smallMavenRepo holds 5 components in 3 artifacts. Every /v1/components request
// is counted, since the parallel listing must never fall back to it.
func smallMavenRepo(components *atomic.Int32) fakeMavenRepo {
	return fakeMavenRepo{
		tree: map[string][]string{
			"":    {"g/"},
			"g":   {"a/", "b/", "c/"},
			"g/a": {"1/", "2/", "maven-metadata.xml"},
			"g/b": {"1/", "maven-metadata.xml"},
			"g/c": {"1/", "2/", "maven-metadata.xml"},
		},
		metadata: map[string]string{
			"g/a/maven-metadata.xml": artifactMetadata("a", "1", "2"),
			"g/b/maven-metadata.xml": artifactMetadata("b", "1"),
			"g/c/maven-metadata.xml": artifactMetadata("c", "1", "2"),
		},
		search: map[string][][]NexusComponent{
			"g:a": {{comp("a1", "g", "a", "1")}, {comp("a2", "g", "a", "2")}},
			"g:b": {{comp("b1", "g", "b", "1")}},
			"g:c": {{comp("c1", "g", "c", "1"), comp("c2", "g", "c", "2")}},
		},
		hook: func(_ http.ResponseWriter, r *http.Request) bool {
			if r.URL.Path == "/service/rest/v1/components" {
				components.Add(1)
			}
			return false
		},
	}
}

func parallelAdapter(t *testing.T, repo fakeMavenRepo) *adapter {
	t.Helper()
	c := repo.serve(t)
	a := newTestAdapter(t, c.url)
	a.client.listPageTimeout, a.client.listRetryBase, a.client.progressEvery = c.listPageTimeout, c.listRetryBase, c.progressEvery
	return a
}

func TestParallelListingServesAllListingConsumersFromOnePass(t *testing.T) {
	var components, searches atomic.Int32
	repo := smallMavenRepo(&components)
	countComponents := repo.hook
	repo.hook = func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path == "/service/rest/v1/search" {
			searches.Add(1)
		}
		return countComponents(w, r)
	}
	a := parallelAdapter(t, repo)

	files, err := a.GetFilesWithProgress("repo", adp.ListOptions{Concurrency: 3})
	if err != nil {
		t.Fatal(err)
	}
	listed := searches.Load()
	pkgs, err := a.GetPackages("repo", types.MAVEN, nil)
	if err != nil {
		t.Fatal(err)
	}
	versions, err := a.GetVersions(types.Package{}, nil, "repo", "g/a", types.MAVEN)
	if err != nil {
		t.Fatal(err)
	}

	if len(files) != 5 || len(pkgs) != 3 || len(versions) != 2 {
		t.Fatalf("files=%d packages=%d versions of g/a=%d, want 5, 3, 2", len(files), len(pkgs), len(versions))
	}
	if n := searches.Load(); n != listed {
		t.Errorf("searches grew from %d to %d: GetPackages/GetVersions re-listed instead of reusing the listing", listed, n)
	}
	if n := components.Load(); n != 0 {
		t.Errorf("/v1/components requests = %d, want 0", n)
	}
}

func TestParallelListingInBatches(t *testing.T) {
	var components atomic.Int32
	a := parallelAdapter(t, smallMavenRepo(&components))

	var sizes []int
	uris := map[string]bool{}
	versionsOfA := 0
	err := a.ListBatches(context.Background(), "repo", 2, adp.ListOptions{Concurrency: 3},
		func(batch adp.Adapter, files []types.File) error {
			sizes = append(sizes, len(files))
			for _, f := range files {
				if uris[f.Uri] {
					t.Errorf("%s delivered in more than one batch", f.Uri)
				}
				uris[f.Uri] = true
			}
			v, err := batch.GetVersions(types.Package{}, nil, "repo", "g/a", types.MAVEN)
			versionsOfA += len(v)
			return err
		})
	if err != nil {
		t.Fatal(err)
	}

	if len(uris) != 5 || versionsOfA != 2 {
		t.Fatalf("files=%d versions of g/a=%d across batches, want 5 and 2", len(uris), versionsOfA)
	}
	for i, n := range sizes[:len(sizes)-1] {
		if n < 2 {
			t.Errorf("batch %d has %d file(s), want at least batchSize 2 (sizes %v)", i, n, sizes)
		}
	}
	if n := components.Load(); n != 0 {
		t.Errorf("/v1/components requests = %d, want 0", n)
	}
}

func TestParallelListingRequestFailures(t *testing.T) {
	cases := []struct {
		name string
		// respond answers the n-th (1-based) request of the failing kind, or returns
		// false to let the fake repo answer it.
		respond   func(w http.ResponseWriter, r *http.Request, n int32) bool
		path      string
		wantErr   string
		wantCalls int32
	}{
		{
			name: "5xx search is retried", path: "/service/rest/v1/search",
			respond: func(w http.ResponseWriter, _ *http.Request, n int32) bool {
				if n <= 2 {
					w.WriteHeader(http.StatusBadGateway)
				}
				return n <= 2
			},
		},
		{
			name: "429 browse is retried", path: "/service/rest/repository/browse/repo/",
			respond: func(w http.ResponseWriter, _ *http.Request, n int32) bool {
				if n == 1 {
					w.WriteHeader(http.StatusTooManyRequests)
				}
				return n == 1
			},
		},
		{
			name: "stalled request times out and is retried", path: "/service/rest/v1/search",
			respond: func(_ http.ResponseWriter, r *http.Request, n int32) bool {
				if n == 1 {
					<-r.Context().Done()
				}
				return n == 1
			},
		},
		{
			name: "gives up after the last attempt", path: "/service/rest/v1/search",
			respond: func(w http.ResponseWriter, _ *http.Request, _ int32) bool {
				w.WriteHeader(http.StatusServiceUnavailable)
				return true
			},
			wantErr: "giving up after 4 attempts", wantCalls: listPageAttempts,
		},
		{
			name: "4xx is not retried", path: "/service/rest/v1/search",
			respond: func(w http.ResponseWriter, _ *http.Request, _ int32) bool {
				w.WriteHeader(http.StatusForbidden)
				return true
			},
			wantErr: "unexpected status code: 403", wantCalls: 1,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var components, calls atomic.Int32
			repo := smallMavenRepo(&components)
			repo.hook = func(w http.ResponseWriter, r *http.Request) bool {
				return r.URL.Path == tc.path && tc.respond(w, r, calls.Add(1))
			}
			c := repo.serve(t)
			c.listPageTimeout = 200 * time.Millisecond

			got, err := listParallel(t, c, 1)
			switch {
			case tc.wantErr == "" && err != nil:
				t.Fatalf("listing failed: %v", err)
			case tc.wantErr == "" && len(got.ids) != 5:
				t.Fatalf("listed %v, want all 5 components", got.ids)
			case tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)):
				t.Fatalf("err = %v, want it to contain %q", err, tc.wantErr)
			case tc.wantCalls > 0 && calls.Load() != tc.wantCalls:
				t.Fatalf("requests = %d, want %d", calls.Load(), tc.wantCalls)
			}
		})
	}
}

func TestParallelListingReportsProgressWhileRunning(t *testing.T) {
	var components atomic.Int32
	repo := smallMavenRepo(&components)
	repo.hook = func(_ http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path == "/service/rest/v1/search" {
			time.Sleep(40 * time.Millisecond)
		}
		return false
	}
	c := repo.serve(t)
	c.progressEvery = 10 * time.Millisecond

	got, err := listParallel(t, c, 1)
	if err != nil {
		t.Fatal(err)
	}
	progress := got.progress
	running := 0
	for _, p := range progress {
		if strings.HasPrefix(p, "Listing repo (1 parallel requests):") {
			running++
		}
	}
	if running == 0 || !strings.HasPrefix(progress[len(progress)-1], "Listed repo: 5 component(s) from 3 artifact(s)") {
		t.Fatalf("progress = %q, want periodic status lines and a final summary", progress)
	}
}

func TestParallelListingRelistsCappedSnapshotsByBaseVersion(t *testing.T) {
	capped := make([]NexusComponent, searchResultCap)
	for i := range capped {
		capped[i] = comp("old-"+strconv.Itoa(i), "g", "snap", "x")
	}
	var baseVersionQueries atomic.Int32
	c := fakeMavenRepo{
		tree: map[string][]string{
			"":       {"g/"},
			"g":      {"snap/"},
			"g/snap": {"1.0-SNAPSHOT/", "maven-metadata.xml"},
		},
		metadata: map[string]string{"g/snap/maven-metadata.xml": artifactMetadata("snap", "1.0-SNAPSHOT")},
		search: map[string][][]NexusComponent{
			"g:snap": {capped},
			"g:snap:1.0-SNAPSHOT": {{
				comp("build-1", "g", "snap", "1.0-20240101.120000-1"),
				comp("build-2", "g", "snap", "1.0-20240102.120000-2"),
				comp("other", "g", "snap", "1.0.1-20240102.120000-1"),
			}},
		},
		hook: func(_ http.ResponseWriter, r *http.Request) bool {
			if q := r.URL.Query(); q.Get("maven.baseVersion") == "1.0-SNAPSHOT" && q.Get("version") == "" {
				baseVersionQueries.Add(1)
			}
			return false
		},
	}.serve(t)

	got, err := listParallel(t, c, 2)
	if err != nil {
		t.Fatal(err)
	}
	if baseVersionQueries.Load() == 0 {
		t.Error("the snapshot folder was not searched by maven.baseVersion")
	}
	if !slices.Contains(got.ids, "build-1") || !slices.Contains(got.ids, "build-2") || slices.Contains(got.ids, "other") {
		t.Errorf("want build-1 and build-2 but not another base version's build; listed %d ids", len(got.ids))
	}
	if len(got.unlisted) != 0 {
		t.Errorf("unlisted = %q, want none", got.unlisted)
	}
}
