package migrate

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/harness/cli/modules/har/pkg/har/migrate/adapter/jfrog"
	"github.com/harness/cli/modules/har/pkg/har/migrate/adapter/mock_jfrog"
	"github.com/harness/cli/modules/har/pkg/har/migrate/types"
)

func TestRunZeroPackageGuard(t *testing.T) {
	run := func(mapping types.RegistryMapping) ([]string, error) {
		cfg := &types.Config{Concurrency: 1, Overwrite: true, Mappings: []types.RegistryMapping{mapping}}
		dest := &fakeDestAdapter{}
		err := newMockBackedService(cfg, dest).Run(context.Background())
		return dest.uploadedURIs(), err
	}

	_, err := run(baseMapping(types.PUPPET, "raw-local"))
	if err == nil || !strings.Contains(err.Error(), "resolved 0 packages for artifactType PUPPET") {
		t.Fatalf("expected zero-package error for PUPPET on a raw repository, got: %v", err)
	}

	filteredOut := baseMapping(types.RAW, "raw-local")
	filteredOut.IncludePatterns = []string{"/nothing-matches/*"}
	uploads, err := run(filteredOut)
	if err != nil || len(uploads) != 0 {
		t.Fatalf("filters leaving no files should be an empty success, got uploads=%v err=%v", uploads, err)
	}

	client := mock_jfrog.NewMockClient()
	client.(interface {
		SetSearchedFiles(string, []types.SearchedFile)
	}).SetSearchedFiles("cran-local", []types.SearchedFile{
		{Path: "src/contrib", Name: "PACKAGES", Created: "2025-01-01T00:00:00Z"},
		{Path: "src/contrib", Name: "PACKAGES.gz", Created: "2025-01-01T00:00:00Z"},
	})
	after := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	indexOnlyWindow := baseMapping(types.CRAN, "cran-local")
	indexOnlyWindow.DateFilter = &types.DateFilter{Match: types.DateFilterMatchAny, CreatedAfter: &after}
	cfg := &types.Config{Concurrency: 1, Overwrite: true, Mappings: []types.RegistryMapping{indexOnlyWindow}}
	src := jfrog.NewAdapterWithClient(types.RegistryConfig{Type: types.MOCK_JFROG, Endpoint: "http://mock"}, client)
	svc := &MigrationService{config: cfg, source: src, destination: &fakeDestAdapter{}}
	if err := svc.Run(context.Background()); err != nil {
		t.Fatalf("a date window holding only index files should be an empty success, got: %v", err)
	}
}

// The mock source has listings but no content for these registries' package files,
// so every download fails.
func TestRunCountsSourceDownloadFailures(t *testing.T) {
	cases := []struct {
		artifactType types.ArtifactType
		registry     string
		wantFailed   int
	}{
		{types.RAW, "raw-local", 4},
		{types.PYTHON, "python-local", 2},
	}
	for _, tc := range cases {
		t.Run(string(tc.artifactType), func(t *testing.T) {
			resultPath := filepath.Join(t.TempDir(), "result.jsonl")
			cfg := &types.Config{
				Concurrency: 1,
				Overwrite:   true,
				ResultFile:  resultPath,
				Mappings:    []types.RegistryMapping{baseMapping(tc.artifactType, tc.registry)},
			}
			err := newMockBackedService(cfg, &fakeDestAdapter{}).Run(context.Background())
			if err == nil {
				t.Fatal("expected Run to fail when source downloads fail")
			}

			failed := 0
			for _, rec := range readResultFile(t, resultPath) {
				if rec.Status != types.StatusFail {
					t.Errorf("unexpected non-failed record: %+v", rec)
					continue
				}
				if strings.Contains(rec.Error, "panic") {
					t.Errorf("download failure surfaced as a panic: %s", rec.Error)
				}
				failed++
			}
			if failed != tc.wantFailed {
				t.Errorf("failed records = %d, want %d", failed, tc.wantFailed)
			}
		})
	}
}
