// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package migratable

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/harness/cli/modules/har/pkg/har/migrate/types"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/rs/zerolog"
)

// noopComposerAdapter provides zero-value implementations of every
// adapter.Adapter method so tests only need to override what they exercise.
type noopComposerAdapter struct{}

func (noopComposerAdapter) GetKeyChain(string) (authn.Keychain, error) { return nil, nil }
func (noopComposerAdapter) GetConfig() types.RegistryConfig            { return types.RegistryConfig{} }
func (noopComposerAdapter) ValidateCredentials() (bool, error)         { return false, nil }
func (noopComposerAdapter) GetRegistry(context.Context, string) (types.RegistryInfo, error) {
	return types.RegistryInfo{}, nil
}
func (noopComposerAdapter) CreateRegistryIfDoesntExist(string) (bool, error) { return false, nil }
func (noopComposerAdapter) GetPackages(string, types.ArtifactType, *types.TreeNode) ([]types.Package, error) {
	return nil, nil
}
func (noopComposerAdapter) GetVersions(types.Package, *types.TreeNode, string, string, types.ArtifactType) ([]types.Version, error) {
	return nil, nil
}
func (noopComposerAdapter) GetFiles(string) ([]types.File, error) { return nil, nil }
func (noopComposerAdapter) DownloadFile(string, string) (io.ReadCloser, http.Header, error) {
	return nil, nil, nil
}
func (noopComposerAdapter) UploadFile(string, io.ReadCloser, *types.File, http.Header, string, string, types.ArtifactType, map[string]interface{}) error {
	return nil
}
func (noopComposerAdapter) GetOCIImagePath(string, string, string) (string, error) { return "", nil }
func (noopComposerAdapter) AddNPMTag(string, string, string, string) error         { return nil }
func (noopComposerAdapter) VersionExists(context.Context, types.Package, string, string, string, types.ArtifactType) (bool, error) {
	return false, nil
}
func (noopComposerAdapter) FileExists(context.Context, string, string, string, *types.File, types.ArtifactType) (bool, error) {
	return false, nil
}
func (noopComposerAdapter) GetAllFilesForVersion(context.Context, string, string, string) ([]string, error) {
	return nil, nil
}
func (noopComposerAdapter) CreateVersion(string, string, string, types.ArtifactType, []*types.PackageFiles, map[string]interface{}) error {
	return nil
}
func (noopComposerAdapter) SearchFiles(string) ([]types.SearchedFile, error) { return nil, nil }
func (noopComposerAdapter) BuildExistingIndex(context.Context, string, int) (*types.ExistingIndex, error) {
	return nil, nil
}

type composerFakeSrc struct {
	noopComposerAdapter
	content        map[string][]byte
	versions       []types.Version
	getVersionsErr error
}

func (s *composerFakeSrc) DownloadFile(_ string, uri string) (io.ReadCloser, http.Header, error) {
	b, ok := s.content[uri]
	if !ok {
		return nil, nil, fmt.Errorf("download %q: not found", uri)
	}
	return io.NopCloser(strings.NewReader(string(b))), http.Header{}, nil
}

func (s *composerFakeSrc) GetVersions(_ types.Package, _ *types.TreeNode, _, _ string, artifactType types.ArtifactType) ([]types.Version, error) {
	if artifactType != types.COMPOSER {
		return nil, fmt.Errorf("unexpected artifact type %s", artifactType)
	}
	if s.getVersionsErr != nil {
		return nil, s.getVersionsErr
	}
	return s.versions, nil
}

type composerFakeDest struct {
	noopComposerAdapter
	uploads []string
}

func (d *composerFakeDest) UploadFile(
	_ string,
	_ io.ReadCloser,
	f *types.File,
	_ http.Header,
	_ string,
	_ string,
	_ types.ArtifactType,
	_ map[string]interface{},
) error {
	d.uploads = append(d.uploads, f.Name)
	return nil
}

func newComposerJob(src *composerFakeSrc, dest *composerFakeDest, stats *types.TransferStats) *Package {
	return &Package{
		srcRegistry:  "src-reg",
		destRegistry: "dst-reg",
		srcAdapter:   src,
		destAdapter:  dest,
		artifactType: types.COMPOSER,
		logger:       zerolog.Nop(),
		pkg: types.Package{
			Name: "harness/migtest",
			Path: "/",
		},
		node:   &types.TreeNode{Name: "/", Key: "/"},
		stats:  stats,
		config: &types.Config{Concurrency: 1, DryRun: false, Overwrite: false},
	}
}

// TestMigrateComposerGetVersionsErrorRecordsStat is a regression guard: a
// GetVersions failure for a Composer package must be recorded in transfer
// stats (matching hc's AddPackageErrorToStat), not silently dropped.
func TestMigrateComposerGetVersionsErrorRecordsStat(t *testing.T) {
	src := &composerFakeSrc{getVersionsErr: fmt.Errorf("source unavailable")}
	dest := &composerFakeDest{}
	stats := &types.TransferStats{}

	job := newComposerJob(src, dest, stats)
	if err := job.migrateComposer(context.Background()); err == nil {
		t.Fatal("expected GetVersions error to be returned")
	}
	if len(stats.FileStats) != 1 || stats.FileStats[0].Status != types.StatusFail {
		t.Fatalf("stats = %+v, want 1 StatusFail entry for the GetVersions error", stats.FileStats)
	}
	if stats.FileStats[0].Name != "harness/migtest" {
		t.Errorf("stat.Name = %q, want package name %q", stats.FileStats[0].Name, "harness/migtest")
	}
}

// TestMigrateComposerNoVersionsFoundRecordsStat is a regression guard: a
// Composer package that resolves to zero versions must be recorded as a
// failure in transfer stats, not silently skipped.
func TestMigrateComposerNoVersionsFoundRecordsStat(t *testing.T) {
	src := &composerFakeSrc{versions: nil}
	dest := &composerFakeDest{}
	stats := &types.TransferStats{}

	job := newComposerJob(src, dest, stats)
	if err := job.migrateComposer(context.Background()); err != nil {
		t.Fatalf("migrateComposer should record failure in stats, not return error: %v", err)
	}
	if len(stats.FileStats) != 1 || stats.FileStats[0].Status != types.StatusFail {
		t.Fatalf("stats = %+v, want 1 StatusFail entry for the empty version list", stats.FileStats)
	}
}
