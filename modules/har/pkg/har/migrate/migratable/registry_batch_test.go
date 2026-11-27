// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package migratable

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/harness/cli/modules/har/pkg/har/migrate/adapter"
	"github.com/harness/cli/modules/har/pkg/har/migrate/types"

	"github.com/rs/zerolog"
)

// batchSource is what the source adapter hands out for one batch: a single package
// whose version is missing from the tree (so its job fails) when failing is set.
type batchSource struct {
	noopCranAdapter
	pkg      string
	failing  bool
	attempts *atomic.Int32
}

func (s batchSource) GetPackages(string, types.ArtifactType, *types.TreeNode) ([]types.Package, error) {
	if s.pkg == "" {
		return nil, nil
	}
	return []types.Package{{Name: s.pkg}}, nil
}

func (s batchSource) GetVersions(types.Package, *types.TreeNode, string, string, types.ArtifactType) ([]types.Version, error) {
	s.attempts.Add(1)
	if s.failing {
		return []types.Version{{Name: "1.0.0", Path: "1.0.0"}}, nil
	}
	return nil, nil
}

// pagedSource replays batches and then reports listErr, as ListBatches does.
type pagedSource struct {
	noopCranAdapter
	batches []batch
	listErr error
	opts    adapter.ListOptions
}

func (s *pagedSource) GetFilesWithProgress(_ string, opts adapter.ListOptions) ([]types.File, error) {
	s.opts = opts
	return nil, errors.New("unused")
}

func (s *pagedSource) ListBatches(
	_ context.Context, _ string, _ int, opts adapter.ListOptions,
	fn func(adapter.Adapter, []types.File) error,
) error {
	s.opts = opts
	for _, b := range s.batches {
		if err := fn(b.src, b.files); err != nil {
			return err
		}
	}
	return s.listErr
}

type indexCountingDest struct {
	noopCranAdapter
	builds atomic.Int32
}

func (d *indexCountingDest) BuildExistingIndex(context.Context, string, int) (*types.ExistingIndex, error) {
	d.builds.Add(1)
	return types.NewExistingIndex(), nil
}

func newBatchedRegistry(src adapter.Adapter, dest adapter.Adapter) *Registry {
	return &Registry{
		srcRegistry:  "src-reg",
		destRegistry: "dst-reg",
		srcAdapter:   src,
		destAdapter:  dest,
		artifactType: types.MAVEN,
		logger:       zerolog.Nop(),
		stats:        &types.TransferStats{},
		mapping:      &types.RegistryMapping{BatchSize: 1},
		config:       &types.Config{Concurrency: 1},
		registry:     types.RegistryInfo{Path: "dst-reg"},
	}
}

func jarBatch(attempts *atomic.Int32, pkg string, failing bool) batch {
	return batch{
		src:   batchSource{pkg: pkg, failing: failing, attempts: attempts},
		files: []types.File{{Name: "a-1.0.0.jar", Uri: "g/a/1.0.0/a-1.0.0.jar"}},
	}
}

func TestBatchedMigrationContinuesPastFailedPackagesAndIndexesOnce(t *testing.T) {
	var attempts atomic.Int32
	dest := &indexCountingDest{}
	src := &pagedSource{batches: []batch{
		jarBatch(&attempts, "g/a", true),
		jarBatch(&attempts, "g/b", false),
		jarBatch(&attempts, "g/c", false),
	}}

	err := newBatchedRegistry(src, dest).Migrate(context.Background())

	if err == nil || !strings.Contains(err.Error(), "package migration errors") {
		t.Fatalf("Migrate error = %v, want the first batch's package failure reported", err)
	}
	if got := attempts.Load(); got != 3 {
		t.Errorf("batches migrated = %d, want 3 (a failed package must not stop later batches)", got)
	}
	if got := dest.builds.Load(); got != 1 {
		t.Errorf("destination index builds = %d, want 1 for the whole run", got)
	}
}

func TestBatchedMigrationReportsListingErrorAfterMigratingEarlierBatches(t *testing.T) {
	var attempts atomic.Int32
	src := &pagedSource{
		batches: []batch{jarBatch(&attempts, "g/a", false)},
		listErr: errors.New("page 3: giving up after 4 attempts"),
	}

	err := newBatchedRegistry(src, &indexCountingDest{}).Migrate(context.Background())

	if err == nil || !strings.Contains(err.Error(), "page 3") {
		t.Fatalf("Migrate error = %v, want the listing failure", err)
	}
	if got := attempts.Load(); got != 1 {
		t.Errorf("batches migrated = %d, want the batch listed before the failure migrated", got)
	}
}

func TestBatchedMigrationAbortsOnBatchSetupError(t *testing.T) {
	var attempts atomic.Int32
	// Content that resolves to no package means artifactType does not match the
	// source; every later batch would fail the same way.
	unresolved := jarBatch(&attempts, "", false)
	src := &pagedSource{batches: []batch{unresolved, jarBatch(&attempts, "g/b", false)}}

	err := newBatchedRegistry(src, &indexCountingDest{}).Migrate(context.Background())

	if err == nil || !strings.Contains(err.Error(), "resolved 0 packages") {
		t.Fatalf("Migrate error = %v, want the zero-package failure", err)
	}
	if got := attempts.Load(); got != 0 {
		t.Errorf("packages migrated = %d, want the run to stop at the failed batch", got)
	}
}

func TestBatchedMigrationRequiresPagedSource(t *testing.T) {
	err := newBatchedRegistry(noopCranAdapter{}, &indexCountingDest{}).Migrate(context.Background())

	if err == nil || !strings.Contains(err.Error(), "batchSize is not supported") {
		t.Fatalf("Migrate error = %v, want batchSize rejected for a non-paged source", err)
	}
}

func TestListOptionsReachTheSourceWithAndWithoutBatches(t *testing.T) {
	for _, batchSize := range []int{0, 1} {
		src := &pagedSource{}
		r := newBatchedRegistry(src, &indexCountingDest{})
		r.mapping = &types.RegistryMapping{BatchSize: batchSize, ListConcurrency: 8}

		_ = r.Migrate(context.Background())

		if src.opts.Concurrency != 8 || src.opts.Progress == nil || src.opts.Warn == nil || src.opts.Unlisted == nil {
			t.Errorf("batchSize %d: listing options = %+v, want concurrency 8 and every callback", batchSize, src.opts)
			continue
		}
		before := len(r.stats.FileStats)
		src.opts.Unlisted("g:a", "g/a")
		want := types.FileStat{Name: "g:a", Registry: "src-reg", Uri: "g/a", Status: types.StatusSkip,
			Reason: types.SkipReasonNotInSearchIndex}
		if got := r.stats.FileStats[before:]; len(got) != 1 || got[0] != want {
			t.Errorf("batchSize %d: result rows for an unlisted artifact = %+v, want [%+v]", batchSize, got, want)
		}
	}
}
