// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package migratable

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/harness/cli/modules/har/pkg/har/migrate/types"
	"github.com/rs/zerolog"
)

// noopAdapter provides zero-value implementations of every adapter.Adapter
// method so tests only need to override what they exercise.
type noopAdapter struct{}

func (noopAdapter) GetKeyChain(string) (authn.Keychain, error) { return nil, nil }
func (noopAdapter) GetConfig() types.RegistryConfig            { return types.RegistryConfig{} }
func (noopAdapter) ValidateCredentials() (bool, error)         { return false, nil }
func (noopAdapter) GetRegistry(context.Context, string) (types.RegistryInfo, error) {
	return types.RegistryInfo{}, nil
}
func (noopAdapter) CreateRegistryIfDoesntExist(string) (bool, error) { return false, nil }
func (noopAdapter) GetPackages(string, types.ArtifactType, *types.TreeNode) ([]types.Package, error) {
	return nil, nil
}
func (noopAdapter) GetVersions(types.Package, *types.TreeNode, string, string, types.ArtifactType) ([]types.Version, error) {
	return nil, nil
}
func (noopAdapter) GetFiles(string) ([]types.File, error) { return nil, nil }
func (noopAdapter) DownloadFile(string, string) (io.ReadCloser, http.Header, error) {
	return nil, nil, nil
}
func (noopAdapter) UploadFile(string, io.ReadCloser, *types.File, http.Header, string, string, types.ArtifactType, map[string]interface{}) error {
	return nil
}
func (noopAdapter) GetOCIImagePath(string, string, string) (string, error) { return "", nil }
func (noopAdapter) AddNPMTag(string, string, string, string) error         { return nil }
func (noopAdapter) VersionExists(context.Context, types.Package, string, string, string, types.ArtifactType) (bool, error) {
	return false, nil
}
func (noopAdapter) FileExists(context.Context, string, string, string, *types.File, types.ArtifactType) (bool, error) {
	return false, nil
}
func (noopAdapter) GetAllFilesForVersion(context.Context, string, string, string) ([]string, error) {
	return nil, nil
}
func (noopAdapter) CreateVersion(string, string, string, types.ArtifactType, []*types.PackageFiles, map[string]interface{}) error {
	return nil
}
func (noopAdapter) SearchFiles(string) ([]types.SearchedFile, error) { return nil, nil }
func (noopAdapter) BuildExistingIndex(context.Context, string, int) (*types.ExistingIndex, error) {
	return nil, nil
}

// fileFakeSrc serves file content keyed by URI.
type fileFakeSrc struct {
	noopAdapter
	content map[string][]byte
	failURI string // if set, DownloadFile returns an error for this URI
}

func (s *fileFakeSrc) DownloadFile(_ string, uri string) (io.ReadCloser, http.Header, error) {
	if s.failURI != "" && s.failURI == uri {
		return nil, nil, errors.New("download error")
	}
	b, ok := s.content[uri]
	if !ok {
		return nil, nil, errors.New("not found")
	}
	return io.NopCloser(strings.NewReader(string(b))), http.Header{}, nil
}

// fileFakeDest records uploads and can be configured to return specific errors.
type fileFakeDest struct {
	noopAdapter
	uploaded  []string
	uploadErr error
}

func (d *fileFakeDest) UploadFile(
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
	d.uploaded = append(d.uploaded, f.Name)
	return nil
}

func newTerraformFileJob(src *fileFakeSrc, dest *fileFakeDest, f *types.File, pkg, version string, stats *types.TransferStats) *File {
	return &File{
		srcRegistry:  "src-reg",
		destRegistry: "dst-reg",
		srcAdapter:   src,
		destAdapter:  dest,
		artifactType: types.TERRAFORM,
		logger:       zerolog.Nop(),
		pkg:          types.Package{Name: pkg},
		version:      types.Version{Name: version},
		file:         f,
		stats:        stats,
		config:       &types.Config{Concurrency: 1, DryRun: false},
		mapping:      &types.RegistryMapping{},
	}
}

func TestFileMigrateTerraformModuleSuccess(t *testing.T) {
	f := &types.File{Name: "vpc-1.0.0.tar.gz", Uri: "/hashicorp/vpc/aws/1.0.0/vpc-1.0.0.tar.gz", Size: 10}
	src := &fileFakeSrc{content: map[string][]byte{f.Uri: []byte("module")}}
	dest := &fileFakeDest{}
	stats := &types.TransferStats{}

	job := newTerraformFileJob(src, dest, f, "hashicorp/vpc/aws", "1.0.0", stats)
	if err := job.Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate() error: %v", err)
	}
	if len(dest.uploaded) != 1 || dest.uploaded[0] != f.Name {
		t.Errorf("uploaded = %v, want [%s]", dest.uploaded, f.Name)
	}
	if len(stats.FileStats) != 1 || stats.FileStats[0].Status != types.StatusSuccess {
		t.Errorf("stat = %+v, want StatusSuccess", stats.FileStats)
	}
}

func TestFileMigrateTerraformProviderSuccess(t *testing.T) {
	f := &types.File{Name: "terraform-provider-aws_2.0.0_linux_amd64.zip", Uri: "/hashicorp/aws/2.0.0/terraform-provider-aws_2.0.0_linux_amd64.zip", Size: 20}
	src := &fileFakeSrc{content: map[string][]byte{f.Uri: []byte("provider")}}
	dest := &fileFakeDest{}
	stats := &types.TransferStats{}

	job := newTerraformFileJob(src, dest, f, "hashicorp/aws", "2.0.0", stats)
	if err := job.Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate() error: %v", err)
	}
	if len(dest.uploaded) != 1 {
		t.Errorf("uploaded = %v, want 1 file", dest.uploaded)
	}
	if stats.FileStats[0].Status != types.StatusSuccess {
		t.Errorf("stat status = %v, want StatusSuccess", stats.FileStats[0].Status)
	}
}

func TestFileMigrateTerraformAlreadyExists(t *testing.T) {
	f := &types.File{Name: "vpc-1.0.0.tar.gz", Uri: "/hashicorp/vpc/aws/1.0.0/vpc-1.0.0.tar.gz", Size: 10}
	src := &fileFakeSrc{content: map[string][]byte{f.Uri: []byte("module")}}
	dest := &fileFakeDest{uploadErr: types.ErrArtifactAlreadyExists}
	stats := &types.TransferStats{}

	job := newTerraformFileJob(src, dest, f, "hashicorp/vpc/aws", "1.0.0", stats)
	if err := job.Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate() error: %v", err)
	}
	if len(stats.FileStats) != 1 || stats.FileStats[0].Status != types.StatusSkip {
		t.Errorf("stat = %+v, want StatusSkip", stats.FileStats)
	}
}

func TestFileMigrateTerraformUploadError(t *testing.T) {
	f := &types.File{Name: "vpc-1.0.0.tar.gz", Uri: "/hashicorp/vpc/aws/1.0.0/vpc-1.0.0.tar.gz", Size: 10}
	src := &fileFakeSrc{content: map[string][]byte{f.Uri: []byte("module")}}
	dest := &fileFakeDest{uploadErr: errors.New("server error")}
	stats := &types.TransferStats{}

	job := newTerraformFileJob(src, dest, f, "hashicorp/vpc/aws", "1.0.0", stats)
	if err := job.Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate() error: %v", err)
	}
	if len(stats.FileStats) != 1 || stats.FileStats[0].Status != types.StatusFail {
		t.Errorf("stat = %+v, want StatusFail", stats.FileStats)
	}
}

func TestFileMigrateTerraformDownloadError(t *testing.T) {
	f := &types.File{Name: "vpc-1.0.0.tar.gz", Uri: "/hashicorp/vpc/aws/1.0.0/vpc-1.0.0.tar.gz", Size: 10}
	src := &fileFakeSrc{content: map[string][]byte{}, failURI: f.Uri}
	dest := &fileFakeDest{}
	stats := &types.TransferStats{}

	job := newTerraformFileJob(src, dest, f, "hashicorp/vpc/aws", "1.0.0", stats)
	err := job.Migrate(context.Background())
	if err == nil {
		t.Fatal("expected error from download failure, got nil")
	}
}
