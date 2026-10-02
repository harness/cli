// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package migratable

import (
	"context"
	"io"
	stdlog "log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/harness/cli/modules/har/pkg/har/migrate/types"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/crane"
	"github.com/google/go-containerregistry/pkg/registry"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/random"
	"github.com/rs/zerolog"
)

// ociStubAdapter points the OCI copy at a real (in-process) registry. Only the
// three methods the OCI path touches are meaningful; the rest come from
// noopCranAdapter.
type ociStubAdapter struct {
	noopCranAdapter
	repo string // full <host>/<repo> reference, no tag
}

func (a *ociStubAdapter) GetOCIImagePath(_ string, _ string, _ string) (string, error) {
	return a.repo, nil
}

// Anonymous auth: the in-process registry accepts everything. Returning a nil
// keychain (noopCranAdapter's behaviour) would panic inside crane.
func (a *ociStubAdapter) GetKeyChain(string) (authn.Keychain, error) {
	return authn.NewMultiKeychain(), nil
}

// Insecure so crane speaks http to the test server.
func (a *ociStubAdapter) GetConfig() types.RegistryConfig {
	return types.RegistryConfig{Insecure: true}
}

func ociOpts() []crane.Option { return []crane.Option{crane.Insecure} }

// startOCIRegistry runs an in-process OCI registry and returns its host:port.
func startOCIRegistry(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(registry.New(registry.Logger(nopStdLogger())))
	t.Cleanup(srv.Close)
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatalf("parse test registry url: %v", err)
	}
	return u.Host
}

// startOCIRegistryWithBrokenTag behaves like startOCIRegistry except that
// manifest reads for brokenTag answer MANIFEST_UNKNOWN, mimicking a tag whose
// manifest was garbage-collected at the source.
func startOCIRegistryWithBrokenTag(t *testing.T, brokenTag string) string {
	t.Helper()
	inner := registry.New(registry.Logger(nopStdLogger()))
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Only reads are broken: the tag must still be seedable (PUT) and must
		// still appear in tags/list, exactly like a tag whose manifest was
		// garbage-collected out from under it.
		readingBrokenManifest := (r.Method == http.MethodGet || r.Method == http.MethodHead) &&
			strings.HasSuffix(r.URL.Path, "/manifests/"+brokenTag)
		if readingBrokenManifest {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"errors":[{"code":"MANIFEST_UNKNOWN","message":"manifest unknown"}]}`))
			return
		}
		inner.ServeHTTP(w, r)
	})
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatalf("parse test registry url: %v", err)
	}
	return u.Host
}

func pushTag(t *testing.T, repo, tag string, img v1.Image) {
	t.Helper()
	if err := crane.Push(img, repo+":"+tag, ociOpts()...); err != nil {
		t.Fatalf("seed push %s:%s: %v", repo, tag, err)
	}
}

func imageDigest(t *testing.T, img v1.Image) string {
	t.Helper()
	d, err := img.Digest()
	if err != nil {
		t.Fatalf("digest: %v", err)
	}
	return d.String()
}

// tagDigests maps tag -> digest for every tag currently in repo.
func tagDigests(t *testing.T, repo string) map[string]string {
	t.Helper()
	out := map[string]string{}
	tags, err := crane.ListTags(repo, ociOpts()...)
	if err != nil {
		return out
	}
	for _, tag := range tags {
		d, err := crane.Digest(repo+":"+tag, ociOpts()...)
		if err != nil {
			continue
		}
		out[tag] = d
	}
	return out
}

func newOCIPackageJob(srcRepo, dstRepo string, overwrite bool, stats *types.TransferStats) *Package {
	return &Package{
		srcRegistry:  "src-reg",
		destRegistry: "dst-reg",
		srcAdapter:   &ociStubAdapter{repo: srcRepo},
		destAdapter:  &ociStubAdapter{repo: dstRepo},
		artifactType: types.DOCKER,
		logger:       zerolog.Nop(),
		pkg:          types.Package{Name: "demo/app"},
		stats:        stats,
		config:       &types.Config{Concurrency: 2, Overwrite: overwrite},
		mapping:      &types.RegistryMapping{},
		registry:     types.RegistryInfo{Path: "dst-reg"},
	}
}

func TestMigrateOCICopiesEveryTag(t *testing.T) {
	srcHost, dstHost := startOCIRegistry(t), startOCIRegistry(t)
	srcRepo := srcHost + "/demo/app"
	dstRepo := dstHost + "/demo/app"

	imgA, _ := random.Image(256, 1)
	imgB, _ := random.Image(256, 1)
	pushTag(t, srcRepo, "v1", imgA)
	pushTag(t, srcRepo, "v2", imgB)
	pushTag(t, srcRepo, "latest", imgA)

	stats := &types.TransferStats{}
	newOCIPackageJob(srcRepo, dstRepo, false, stats).migrateOCI(context.Background(), zerolog.Nop())

	got := tagDigests(t, dstRepo)
	want := map[string]string{
		"v1":     imageDigest(t, imgA),
		"v2":     imageDigest(t, imgB),
		"latest": imageDigest(t, imgA),
	}
	for tag, wantDigest := range want {
		if got[tag] != wantDigest {
			t.Errorf("destination %s = %q, want %q", tag, got[tag], wantDigest)
		}
	}

	if len(stats.Snapshot()) != 1 {
		t.Fatalf("stats = %+v, want exactly one image-level stat", stats.Snapshot())
	}
	if s := stats.Snapshot()[0]; s.Status != types.StatusSuccess {
		t.Errorf("stat = %+v, want StatusSuccess", s)
	}
}

// A tag re-pointed at a different image upstream must be corrected at the
// destination even with overwrite=false. crane's name-based no-clobber cannot
// see this, which is why the per-tag digest comparison exists.
func TestMigrateOCIUpdatesMovedTagWithoutOverwrite(t *testing.T) {
	srcHost, dstHost := startOCIRegistry(t), startOCIRegistry(t)
	srcRepo := srcHost + "/demo/app"
	dstRepo := dstHost + "/demo/app"

	imgA, _ := random.Image(256, 1)
	imgB, _ := random.Image(256, 1)

	pushTag(t, srcRepo, "latest", imgA)
	stats := &types.TransferStats{}
	newOCIPackageJob(srcRepo, dstRepo, false, stats).migrateOCI(context.Background(), zerolog.Nop())

	if got, want := tagDigests(t, dstRepo)["latest"], imageDigest(t, imgA); got != want {
		t.Fatalf("after first migration latest = %q, want %q", got, want)
	}

	// Upstream moves the tag to a different image.
	pushTag(t, srcRepo, "latest", imgB)

	stats2 := &types.TransferStats{}
	newOCIPackageJob(srcRepo, dstRepo, false, stats2).migrateOCI(context.Background(), zerolog.Nop())

	got, want := tagDigests(t, dstRepo)["latest"], imageDigest(t, imgB)
	if got != want {
		t.Errorf("latest = %q, want %q (moved tag was not updated at the destination)", got, want)
	}
}

// One tag whose source manifest is gone must not take the rest of the image
// down with it.
func TestMigrateOCISkipsOrphanedTagAndMigratesRest(t *testing.T) {
	srcHost := startOCIRegistryWithBrokenTag(t, "broken")
	dstHost := startOCIRegistry(t)
	srcRepo := srcHost + "/demo/app"
	dstRepo := dstHost + "/demo/app"

	imgA, _ := random.Image(256, 1)
	imgB, _ := random.Image(256, 1)
	imgC, _ := random.Image(256, 1)
	pushTag(t, srcRepo, "v1", imgA)
	pushTag(t, srcRepo, "v2", imgB)
	// Seeded before the handler starts refusing it: the tag is listed, but its
	// manifest can no longer be read.
	pushTag(t, srcRepo, "broken", imgC)

	stats := &types.TransferStats{}
	newOCIPackageJob(srcRepo, dstRepo, false, stats).migrateOCI(context.Background(), zerolog.Nop())

	got := tagDigests(t, dstRepo)
	for tag, want := range map[string]string{"v1": imageDigest(t, imgA), "v2": imageDigest(t, imgB)} {
		if got[tag] != want {
			t.Errorf("healthy tag %s = %q, want %q (orphaned sibling aborted the image)", tag, got[tag], want)
		}
	}
	if _, ok := got["broken"]; ok {
		t.Errorf("broken tag should not have been copied, got %v", got["broken"])
	}

	snap := stats.Snapshot()
	if len(snap) != 1 {
		t.Fatalf("stats = %+v, want exactly one image-level stat", snap)
	}
	if snap[0].Status != types.StatusSuccess {
		t.Errorf("stat = %+v, want StatusSuccess (healthy tags migrated)", snap[0])
	}
}

// Already-in-sync tags are skipped rather than re-pushed, and an image where
// everything was already present still reports Success.
func TestMigrateOCISkipsTagsAlreadyInSync(t *testing.T) {
	srcHost, dstHost := startOCIRegistry(t), startOCIRegistry(t)
	srcRepo := srcHost + "/demo/app"
	dstRepo := dstHost + "/demo/app"

	imgA, _ := random.Image(256, 1)
	pushTag(t, srcRepo, "v1", imgA)
	pushTag(t, dstRepo, "v1", imgA)

	stats := &types.TransferStats{}
	newOCIPackageJob(srcRepo, dstRepo, false, stats).migrateOCI(context.Background(), zerolog.Nop())

	snap := stats.Snapshot()
	if len(snap) != 1 || snap[0].Status != types.StatusSuccess {
		t.Errorf("stats = %+v, want one StatusSuccess stat", snap)
	}
}

// A source repository that exists but holds no tags is reported as a skip, not
// a success, so an empty source can't be mistaken for a completed migration.
func TestMigrateOCITaglessRepositoryIsSkip(t *testing.T) {
	// A repo with zero tags cannot be created by pushing, so serve an empty
	// tags/list directly.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/v2/":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{}`))
		case strings.HasSuffix(r.URL.Path, "/tags/list"):
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"name":"demo/app","tags":[]}`))
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"errors":[{"code":"MANIFEST_UNKNOWN","message":"manifest unknown"}]}`))
		}
	}))
	t.Cleanup(srv.Close)
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatalf("parse url: %v", err)
	}

	dstHost := startOCIRegistry(t)
	stats := &types.TransferStats{}
	newOCIPackageJob(u.Host+"/demo/app", dstHost+"/demo/app", false, stats).
		migrateOCI(context.Background(), zerolog.Nop())

	snap := stats.Snapshot()
	if len(snap) != 1 {
		t.Fatalf("stats = %+v, want exactly one stat", snap)
	}
	if snap[0].Status != types.StatusSkip || snap[0].Reason != types.SkipReasonNoContent {
		t.Errorf("stat = %+v, want StatusSkip/%s", snap[0], types.SkipReasonNoContent)
	}
}

// nopStdLogger silences the in-process registry's request logging.
func nopStdLogger() *stdlog.Logger { return stdlog.New(io.Discard, "", 0) }
