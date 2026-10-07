// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package migratable

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path"
	"strings"
	"testing"

	"github.com/harness/cli/modules/har/pkg/har/migrate/tree"
	"github.com/harness/cli/modules/har/pkg/har/migrate/types"
	"github.com/harness/cli/modules/har/pkg/har/migrate/util"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/rs/zerolog"
)

// noopCranAdapter provides zero-value implementations of every
// adapter.Adapter method so tests only need to override what they exercise.
type noopCranAdapter struct{}

func (noopCranAdapter) GetKeyChain(string) (authn.Keychain, error) { return nil, nil }
func (noopCranAdapter) GetConfig() types.RegistryConfig            { return types.RegistryConfig{} }
func (noopCranAdapter) ValidateCredentials() (bool, error)         { return false, nil }
func (noopCranAdapter) GetRegistry(context.Context, string) (types.RegistryInfo, error) {
	return types.RegistryInfo{}, nil
}
func (noopCranAdapter) CreateRegistryIfDoesntExist(string) (bool, error) { return false, nil }
func (noopCranAdapter) GetPackages(string, types.ArtifactType, *types.TreeNode) ([]types.Package, error) {
	return nil, nil
}
func (noopCranAdapter) GetVersions(types.Package, *types.TreeNode, string, string, types.ArtifactType) ([]types.Version, error) {
	return nil, nil
}
func (noopCranAdapter) GetFiles(string) ([]types.File, error) { return nil, nil }
func (noopCranAdapter) DownloadFile(string, string) (io.ReadCloser, http.Header, error) {
	return nil, nil, fmt.Errorf("not implemented")
}
func (noopCranAdapter) UploadFile(string, io.ReadCloser, *types.File, http.Header, string, string, types.ArtifactType, map[string]interface{}) error {
	return fmt.Errorf("not implemented")
}
func (noopCranAdapter) GetOCIImagePath(string, string, string) (string, error) { return "", nil }
func (noopCranAdapter) AddNPMTag(string, string, string, string) error         { return nil }
func (noopCranAdapter) VersionExists(context.Context, types.Package, string, string, string, types.ArtifactType) (bool, error) {
	return false, nil
}
func (noopCranAdapter) FileExists(context.Context, string, string, string, *types.File, types.ArtifactType) (bool, error) {
	return false, nil
}
func (noopCranAdapter) GetAllFilesForVersion(context.Context, string, string, string) ([]string, error) {
	return nil, nil
}
func (noopCranAdapter) CreateVersion(string, string, string, types.ArtifactType, []*types.PackageFiles, map[string]interface{}) error {
	return nil
}
func (noopCranAdapter) SearchFiles(string) ([]types.SearchedFile, error) { return nil, nil }
func (noopCranAdapter) BuildExistingIndex(context.Context, string, int) (*types.ExistingIndex, error) {
	return nil, nil
}

type cranFakeSrc struct {
	noopCranAdapter
	content map[string][]byte
}

func (s *cranFakeSrc) DownloadFile(_ string, uri string) (io.ReadCloser, http.Header, error) {
	b, ok := s.content[uri]
	if !ok {
		return nil, nil, fmt.Errorf("download %q: not found", uri)
	}
	return io.NopCloser(strings.NewReader(string(b))), http.Header{}, nil
}

type cranFakeDest struct {
	noopCranAdapter
	uploadedUris []string
	uploadErr    error
	exists       bool
	existsErr    error
	headURI      string
}

func (d *cranFakeDest) UploadFile(
	_ string,
	file io.ReadCloser,
	f *types.File,
	_ http.Header,
	_ string,
	_ string,
	_ types.ArtifactType,
	_ map[string]interface{},
) error {
	if file != nil {
		_, _ = io.Copy(io.Discard, file)
		_ = file.Close()
	}
	if d.uploadErr != nil {
		return d.uploadErr
	}
	d.uploadedUris = append(d.uploadedUris, f.Uri)
	return nil
}

func (d *cranFakeDest) FileExists(
	_ context.Context,
	_, _, _ string,
	file *types.File,
	_ types.ArtifactType,
) (bool, error) {
	if file != nil {
		d.headURI = file.Uri
	}
	return d.exists, d.existsErr
}

func cranFileTree(uris ...string) *types.TreeNode {
	files := make([]types.File, 0, len(uris))
	for _, u := range uris {
		files = append(files, types.File{Name: path.Base(u), Uri: u, Size: 10})
	}
	return tree.TransformToTree(files)
}

func filesForCranPackage(node *types.TreeNode, pkgName string) []types.File {
	all, err := tree.GetAllFiles(node)
	if err != nil {
		return nil
	}
	flat := make([]types.File, 0, len(all))
	for _, f := range all {
		if f != nil {
			flat = append(flat, *f)
		}
	}
	return util.BuildCranPackageFilesMap(flat)[pkgName]
}

func newCranPackageJob(src *cranFakeSrc, dest *cranFakeDest, node *types.TreeNode, stats *types.TransferStats) *Package {
	pkgName := "jsonlite"
	return &Package{
		srcRegistry:  "src-reg",
		destRegistry: "dst-reg",
		srcAdapter:   src,
		destAdapter:  dest,
		artifactType: types.CRAN,
		logger:       zerolog.Nop(),
		pkg:          types.Package{Name: pkgName, Path: "/"},
		node:         node,
		files:        filesForCranPackage(node, pkgName),
		stats:        stats,
		config:       &types.Config{Concurrency: 1, DryRun: false, Overwrite: false},
		mapping:      &types.RegistryMapping{},
		registry:     types.RegistryInfo{Path: "cran-dest"},
	}
}

func TestPackageMigrateCRANRemapsArchivePath(t *testing.T) {
	srcURI := "/src/contrib/Archive/jsonlite/1.7.0/jsonlite_1.7.0.tar.gz"
	node := cranFileTree(srcURI)
	src := &cranFakeSrc{content: map[string][]byte{srcURI: []byte("pkg")}}
	dest := &cranFakeDest{}
	stats := &types.TransferStats{}

	job := newCranPackageJob(src, dest, node, stats)
	if err := job.migrateCran(context.Background()); err != nil {
		t.Fatalf("migrateCran() error: %v", err)
	}
	if len(dest.uploadedUris) != 1 || dest.uploadedUris[0] != "src/contrib/jsonlite_1.7.0.tar.gz" {
		t.Errorf("uploaded = %v, want [src/contrib/jsonlite_1.7.0.tar.gz]", dest.uploadedUris)
	}
}

func TestPackageMigrateCRANUploadsAllPlatformsForSameVersion(t *testing.T) {
	srcTar := "/src/contrib/jsonlite_1.8.0.tar.gz"
	winZip := "/bin/windows/contrib/4.4/jsonlite_1.8.0.zip"
	node := cranFileTree(srcTar, winZip)
	src := &cranFakeSrc{content: map[string][]byte{
		srcTar: []byte("src"),
		winZip: []byte("win"),
	}}
	dest := &cranFakeDest{}
	stats := &types.TransferStats{}

	job := newCranPackageJob(src, dest, node, stats)
	if err := job.migrateCran(context.Background()); err != nil {
		t.Fatalf("migrateCran() error: %v", err)
	}
	if len(dest.uploadedUris) != 2 {
		t.Fatalf("uploaded = %v, want 2 files for version 1.8.0", dest.uploadedUris)
	}
}

func TestPackageMigrateCRANSkipsIndexAndOtherPackages(t *testing.T) {
	node := cranFileTree(
		"/src/contrib/jsonlite_1.8.0.tar.gz",
		"/src/contrib/Archive/jsonlite/1.7.0/jsonlite_1.7.0.tar.gz",
		"/src/contrib/data.table_1.14.0.tar.gz",
		"/src/contrib/PACKAGES",
	)
	src := &cranFakeSrc{content: map[string][]byte{
		"/src/contrib/jsonlite_1.8.0.tar.gz": []byte("live"),
	}}
	dest := &cranFakeDest{}
	stats := &types.TransferStats{}

	job := newCranPackageJob(src, dest, node, stats)
	if err := job.migrateCran(context.Background()); err != nil {
		t.Fatalf("migrateCran() error: %v", err)
	}
	if len(dest.uploadedUris) != 1 {
		t.Errorf("uploaded count = %d, want 1 (jsonlite 1.8.0 only)", len(dest.uploadedUris))
	}
}

func TestPackageMigrateCRANAlreadyExists(t *testing.T) {
	srcURI := "/src/contrib/jsonlite_1.8.0.tar.gz"
	node := cranFileTree(srcURI)
	src := &cranFakeSrc{content: map[string][]byte{srcURI: []byte("pkg")}}
	dest := &cranFakeDest{uploadErr: types.ErrArtifactAlreadyExists}
	stats := &types.TransferStats{}

	job := newCranPackageJob(src, dest, node, stats)
	if err := job.migrateCran(context.Background()); err != nil {
		t.Fatalf("migrateCran() error: %v", err)
	}
	if len(stats.FileStats) != 1 || stats.FileStats[0].Status != types.StatusSkip {
		t.Errorf("stat = %+v, want StatusSkip", stats.FileStats)
	}
	if stats.FileStats[0].Reason != types.SkipReasonAlreadyExists {
		t.Errorf("Reason = %q, want %q", stats.FileStats[0].Reason, types.SkipReasonAlreadyExists)
	}
}

func TestPackageMigrateCRANSkipsUnrecognizedPaths(t *testing.T) {
	srcURI := "/not/a/cran/path.txt"
	node := cranFileTree(srcURI)
	src := &cranFakeSrc{content: map[string][]byte{srcURI: []byte("x")}}
	dest := &cranFakeDest{}
	stats := &types.TransferStats{}

	job := newCranPackageJob(src, dest, node, stats)
	if err := job.migrateCran(context.Background()); err != nil {
		t.Fatalf("migrateCran() error: %v", err)
	}
	if len(dest.uploadedUris) != 0 {
		t.Errorf("expected no uploads for unrecognized path, got %v", dest.uploadedUris)
	}
	if len(stats.FileStats) != 0 {
		t.Errorf("stats = %+v, want no entries for skipped unrecognized path", stats.FileStats)
	}
}

func TestPackageMigrateCRANSkipsWhenHeadExists(t *testing.T) {
	srcURI := "/src/contrib/Archive/jsonlite/1.7.0/jsonlite_1.7.0.tar.gz"
	node := cranFileTree(srcURI)
	src := &cranFakeSrc{content: map[string][]byte{srcURI: []byte("pkg")}}
	dest := &cranFakeDest{exists: true}
	stats := &types.TransferStats{}

	job := newCranPackageJob(src, dest, node, stats)
	if err := job.migrateCran(context.Background()); err != nil {
		t.Fatalf("migrateCran() error: %v", err)
	}
	if dest.headURI != "src/contrib/jsonlite_1.7.0.tar.gz" {
		t.Errorf("HEAD uri = %q, want remapped contrib path", dest.headURI)
	}
	if len(dest.uploadedUris) != 0 {
		t.Errorf("expected no uploads after HEAD skip, got %v", dest.uploadedUris)
	}
	if len(stats.FileStats) != 1 || stats.FileStats[0].Status != types.StatusSkip {
		t.Errorf("stat = %+v, want StatusSkip", stats.FileStats)
	}
	if stats.FileStats[0].Reason != types.SkipReasonAlreadyExists {
		t.Errorf("Reason = %q, want %q", stats.FileStats[0].Reason, types.SkipReasonAlreadyExists)
	}
}

func TestPackageMigrateCRANProceedsWhenHeadErrors(t *testing.T) {
	srcURI := "/src/contrib/jsonlite_1.8.0.tar.gz"
	node := cranFileTree(srcURI)
	src := &cranFakeSrc{content: map[string][]byte{srcURI: []byte("pkg")}}
	dest := &cranFakeDest{existsErr: errors.New("head failed")}
	stats := &types.TransferStats{}

	job := newCranPackageJob(src, dest, node, stats)
	if err := job.migrateCran(context.Background()); err != nil {
		t.Fatalf("migrateCran() error: %v", err)
	}
	if len(dest.uploadedUris) != 1 {
		t.Errorf("expected upload after HEAD error, got %v", dest.uploadedUris)
	}
}
