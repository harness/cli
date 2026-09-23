package migrate

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/harness/cli/modules/har/pkg/har/migrate/adapter/jfrog"
	"github.com/harness/cli/modules/har/pkg/har/migrate/adapter/mock_jfrog"
	"github.com/harness/cli/modules/har/pkg/har/migrate/types"

	"github.com/google/go-containerregistry/pkg/authn"
)

// fakeDestAdapter is a destination Adapter that records uploads and can be
// instructed to fail (or already-exist) per URI. Source-side methods are
// unused and return zero values.
type fakeDestAdapter struct {
	mu       sync.Mutex
	uploads  []string         // URIs successfully uploaded
	failWith map[string]error // uri -> error to return from UploadFile
	failAll  error            // when non-nil, every upload fails
}

func (f *fakeDestAdapter) GetKeyChain(string) (authn.Keychain, error) { return nil, nil }
func (f *fakeDestAdapter) GetConfig() types.RegistryConfig            { return types.RegistryConfig{} }
func (f *fakeDestAdapter) ValidateCredentials() (bool, error)         { return true, nil }
func (f *fakeDestAdapter) GetRegistry(context.Context, string) (types.RegistryInfo, error) {
	return types.RegistryInfo{Type: "HAR", Path: "dst-reg"}, nil
}
func (f *fakeDestAdapter) CreateRegistryIfDoesntExist(string) (bool, error) { return false, nil }
func (f *fakeDestAdapter) GetPackages(string, types.ArtifactType, *types.TreeNode) ([]types.Package, error) {
	return nil, nil
}
func (f *fakeDestAdapter) GetVersions(types.Package, *types.TreeNode, string, string, types.ArtifactType) ([]types.Version, error) {
	return nil, nil
}
func (f *fakeDestAdapter) GetFiles(string) ([]types.File, error) { return nil, nil }
func (f *fakeDestAdapter) SearchFiles(string) ([]types.SearchedFile, error) {
	return nil, nil
}
func (f *fakeDestAdapter) DownloadFile(string, string) (io.ReadCloser, http.Header, error) {
	return nil, nil, errors.New("fakeDestAdapter: DownloadFile not supported")
}
func (f *fakeDestAdapter) UploadFile(_ string, _ io.ReadCloser, file *types.File, _ http.Header, _, _ string, _ types.ArtifactType, _ map[string]interface{}) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failAll != nil {
		return f.failAll
	}
	if err, ok := f.failWith[file.Uri]; ok {
		return err
	}
	f.uploads = append(f.uploads, file.Uri)
	return nil
}
func (f *fakeDestAdapter) GetOCIImagePath(string, string, string) (string, error) { return "", nil }
func (f *fakeDestAdapter) AddNPMTag(string, string, string, string) error         { return nil }
func (f *fakeDestAdapter) VersionExists(context.Context, types.Package, string, string, string, types.ArtifactType) (bool, error) {
	return false, nil
}
func (f *fakeDestAdapter) FileExists(context.Context, string, string, string, *types.File, types.ArtifactType) (bool, error) {
	return false, nil
}
func (f *fakeDestAdapter) GetAllFilesForVersion(context.Context, string, string, string) ([]string, error) {
	return nil, nil
}
func (f *fakeDestAdapter) BuildExistingIndex(context.Context, string, int) (*types.ExistingIndex, error) {
	return nil, nil
}
func (f *fakeDestAdapter) CreateVersion(string, string, string, types.ArtifactType, []*types.PackageFiles, map[string]interface{}) error {
	return nil
}

func (f *fakeDestAdapter) uploadedURIs() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.uploads))
	copy(out, f.uploads)
	return out
}

// newMockBackedService builds a MigrationService with the real jfrog adapter
// backed by the mock client as source and the given fake destination.
func newMockBackedService(cfg *types.Config, dest *fakeDestAdapter) *MigrationService {
	src := jfrog.NewAdapterWithClient(
		types.RegistryConfig{Type: types.MOCK_JFROG, Endpoint: "http://mock"},
		mock_jfrog.NewMockClient(),
	)
	return &MigrationService{config: cfg, source: src, destination: dest}
}

func baseMapping(at types.ArtifactType, srcReg string) types.RegistryMapping {
	return types.RegistryMapping{
		ArtifactType:        at,
		SourceRegistry:      srcReg,
		DestinationRegistry: "dst-reg",
	}
}

// TestRunReturnsErrorOnUploadFailures verifies bug 1: per-coordinate upload
// failures are recorded as StatusFail stats, and Run now fails the process
// instead of always returning nil.
func TestRunReturnsErrorOnUploadFailures(t *testing.T) {
	cfg := &types.Config{
		Concurrency: 1,
		Overwrite:   true,
		Mappings:    []types.RegistryMapping{baseMapping(types.NUGET, "nuget-local")},
	}
	dest := &fakeDestAdapter{failAll: errors.New("boom: destination unavailable")}
	svc := newMockBackedService(cfg, dest)

	err := svc.Run(context.Background())
	if err == nil {
		t.Fatal("expected non-nil error when uploads fail, got nil")
	}
	if !strings.Contains(err.Error(), "failed to migrate") {
		t.Errorf("error %q does not mention failed artifact count", err.Error())
	}
}

// TestRunSucceedsOnCleanMigration is the corresponding happy-path check: a
// migration with no failures still returns nil (bug 1's fix must not make
// successful runs fail).
func TestRunSucceedsOnCleanMigration(t *testing.T) {
	cfg := &types.Config{
		Concurrency: 1,
		Overwrite:   true,
		Mappings:    []types.RegistryMapping{baseMapping(types.NUGET, "nuget-local")},
	}
	dest := &fakeDestAdapter{}
	svc := newMockBackedService(cfg, dest)

	if err := svc.Run(context.Background()); err != nil {
		t.Fatalf("expected nil error for clean migration, got: %v", err)
	}
	// nuget-local: 1.0.0.nupkg, 2.0.0.nupkg, 2.0.0.snupkg parse as package files.
	uploads := dest.uploadedURIs()
	if len(uploads) != 3 {
		t.Fatalf("expected 3 uploads, got %d: %v", len(uploads), uploads)
	}
}

// TestRunPackageFiltersVersionSelector verifies bug 2 end-to-end: a
// packageFilters entry naming a specific version narrows the migrated files
// to that version only.
func TestRunPackageFiltersVersionSelector(t *testing.T) {
	cfg := &types.Config{
		Concurrency: 1,
		Overwrite:   true,
		Mappings: []types.RegistryMapping{{
			ArtifactType:        types.NUGET,
			SourceRegistry:      "nuget-local",
			DestinationRegistry: "dst-reg",
			PackageFilters: []types.PackageSelector{
				{Package: "company.grpc.pkg", Versions: []string{"1.0.0"}},
			},
		}},
	}
	dest := &fakeDestAdapter{}
	svc := newMockBackedService(cfg, dest)

	if err := svc.Run(context.Background()); err != nil {
		t.Fatalf("expected nil error, got: %v", err)
	}
	uploads := dest.uploadedURIs()
	if len(uploads) != 1 || !strings.Contains(uploads[0], "1.0.0") {
		t.Fatalf("expected only the 1.0.0 version to upload, got: %v", uploads)
	}
}

// TestRunPackageFiltersFileSelector verifies bug 2 end-to-end at file
// granularity: a packageFilters entry naming a specific file narrows the
// migrated files to that file only, across versions.
func TestRunPackageFiltersFileSelector(t *testing.T) {
	cfg := &types.Config{
		Concurrency: 1,
		Overwrite:   true,
		Mappings: []types.RegistryMapping{{
			ArtifactType:        types.NUGET,
			SourceRegistry:      "nuget-local",
			DestinationRegistry: "dst-reg",
			PackageFilters: []types.PackageSelector{
				{Package: "company.grpc.pkg", Files: []string{"company.grpc.pkg.2.0.0.snupkg"}},
			},
		}},
	}
	dest := &fakeDestAdapter{}
	svc := newMockBackedService(cfg, dest)

	if err := svc.Run(context.Background()); err != nil {
		t.Fatalf("expected nil error, got: %v", err)
	}
	uploads := dest.uploadedURIs()
	if len(uploads) != 1 || !strings.Contains(uploads[0], "snupkg") {
		t.Fatalf("expected only the .snupkg file to upload, got: %v", uploads)
	}
}

// TestRunPackageFiltersUnmatchedPackageMigratesNothing verifies bug 2: when
// packageFilters is set and the source package is not named in it, nothing
// migrates (the allow-list blocks it).
func TestRunPackageFiltersUnmatchedPackageMigratesNothing(t *testing.T) {
	cfg := &types.Config{
		Concurrency: 1,
		Overwrite:   true,
		Mappings: []types.RegistryMapping{{
			ArtifactType:        types.NUGET,
			SourceRegistry:      "nuget-local",
			DestinationRegistry: "dst-reg",
			PackageFilters: []types.PackageSelector{
				{Package: "some-other-package"},
			},
		}},
	}
	dest := &fakeDestAdapter{}
	svc := newMockBackedService(cfg, dest)

	if err := svc.Run(context.Background()); err != nil {
		t.Fatalf("expected nil error, got: %v", err)
	}
	if uploads := dest.uploadedURIs(); len(uploads) != 0 {
		t.Fatalf("expected no uploads, got: %v", uploads)
	}
}
