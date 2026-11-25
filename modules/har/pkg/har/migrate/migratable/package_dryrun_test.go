// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package migratable

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"sync"
	"testing"

	"github.com/harness/cli/modules/har/pkg/har/migrate/types"

	"github.com/rs/zerolog"
)

// dryRunTripwire fails the test if the migration reaches the network in
// dry-run mode: every call below is a side effect --dry-run promises not to
// perform.
type dryRunTripwire struct {
	noopCranAdapter
	t *testing.T
}

func (a *dryRunTripwire) DownloadFile(_ string, uri string) (io.ReadCloser, http.Header, error) {
	a.t.Errorf("DownloadFile(%q) called during dry-run", uri)
	return nil, nil, nil
}

func (a *dryRunTripwire) UploadFile(
	_ string,
	_ io.ReadCloser,
	f *types.File,
	_ http.Header,
	_ string,
	_ string,
	_ types.ArtifactType,
	_ map[string]interface{},
) error {
	a.t.Errorf("UploadFile(%v) called during dry-run", f)
	return nil
}

func (a *dryRunTripwire) GetOCIImagePath(_, _, name string) (string, error) {
	a.t.Errorf("GetOCIImagePath(%q) called during dry-run", name)
	return "", nil
}

func newDryRunPackageJob(t *testing.T, artifactType types.ArtifactType) *Package {
	t.Helper()
	tripwire := &dryRunTripwire{t: t}
	return &Package{
		srcRegistry:  "src-reg",
		destRegistry: "dst-reg",
		srcAdapter:   tripwire,
		destAdapter:  tripwire,
		artifactType: artifactType,
		logger:       zerolog.Nop(),
		pkg: types.Package{
			Name:    "some-package",
			Version: "1.0.0",
			Path:    "/some/package-1.0.0.pkg",
			URL:     "/some/package-1.0.0.pkg",
		},
		stats:    &types.TransferStats{},
		config:   &types.Config{Concurrency: 1, DryRun: true},
		mapping:  &types.RegistryMapping{},
		registry: types.RegistryInfo{Path: "dest-path"},
	}
}

// Package-level artifact types upload from Package.Migrate rather than from a
// child File job, so each needs its own dry-run guard; a missing one silently
// pushes artifacts on a --dry-run run.
func TestPackageMigrateDryRunPerformsNoTransfer(t *testing.T) {
	cases := []struct {
		name         string
		artifactType types.ArtifactType
		migrate      func(*Package, context.Context) error
	}{
		{"docker", types.DOCKER, (*Package).Migrate},
		{"helm", types.HELM, (*Package).Migrate},
		{"helm_legacy", types.HELM_LEGACY, (*Package).migrateLegacyHelm},
		{"conda", types.CONDA, (*Package).migrateConda},
		{"rpm", types.RPM, (*Package).migrateRPM},
		{"swift", types.SWIFT, (*Package).migrateSwift},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			job := newDryRunPackageJob(t, tc.artifactType)

			if err := tc.migrate(job, context.Background()); err != nil {
				t.Fatalf("dry-run migrate returned error: %v", err)
			}
			if len(job.stats.FileStats) != 0 {
				t.Errorf("stats = %+v, want no transfer records in dry-run", job.stats.FileStats)
			}
		})
	}
}

// File jobs of one version run concurrently and share one DryRunStats, so recording
// into the directory structure has to be safe under concurrent writers.
func TestAddFileToDryRunDirectoryIsConcurrencySafe(t *testing.T) {
	stats := &types.DryRunStats{}
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 25; j++ {
				(&File{
					dryRunStats: stats,
					srcRegistry: "reg",
					pkg:         types.Package{Name: fmt.Sprintf("pkg-%d", i%4)},
					version:     types.Version{Name: "1.0"},
					file:        &types.File{Name: fmt.Sprintf("f-%d-%d", i, j)},
				}).addFileToDryRunDirectory()
			}
		}(i)
	}
	wg.Wait()

	total := 0
	for _, pkg := range stats.Directories["reg"].Packages {
		total += len(pkg.Versions["1.0"].Files)
	}
	if total != 16*25 {
		t.Errorf("recorded %d files, want %d", total, 16*25)
	}
}
