package migrate

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/harness/cli/modules/har/pkg/har/migrate/types"
)

func readResultFile(t *testing.T, path string) []types.FileStat {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read result file: %v", err)
	}
	var records []types.FileStat
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line == "" {
			continue
		}
		var fs types.FileStat
		if err := json.Unmarshal([]byte(line), &fs); err != nil {
			t.Fatalf("result file line is not valid JSON: %q: %v", line, err)
		}
		records = append(records, fs)
	}
	return records
}

// TestRunWritesResultFile verifies the happy path: the JSON-lines result file
// reconciles exactly with the in-memory stats.
func TestRunWritesResultFile(t *testing.T) {
	resultPath := filepath.Join(t.TempDir(), "result.jsonl")
	cfg := &types.Config{
		Concurrency: 2,
		Overwrite:   true,
		ResultFile:  resultPath,
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

	records := readResultFile(t, resultPath)
	if len(records) != 3 {
		t.Fatalf("expected 3 result records, got %d", len(records))
	}
	for _, r := range records {
		if r.Status != types.StatusSuccess {
			t.Errorf("expected all Success in result file, got %+v", r)
		}
	}
}

// TestRunWritesResultFileOnFailure verifies the result file exists (and
// includes MkdirAll'd parent directories) even when the migration fails.
func TestRunWritesResultFileOnFailure(t *testing.T) {
	resultPath := filepath.Join(t.TempDir(), "nested", "result.jsonl")
	cfg := &types.Config{
		Concurrency: 1,
		Overwrite:   true,
		ResultFile:  resultPath,
		Mappings:    []types.RegistryMapping{baseMapping(types.NUGET, "nuget-local")},
	}
	dest := &fakeDestAdapter{failAll: errors.New("boom")}
	svc := newMockBackedService(cfg, dest)

	if err := svc.Run(context.Background()); err == nil {
		t.Fatal("expected non-nil error, got nil")
	}

	records := readResultFile(t, resultPath)
	if len(records) != 3 {
		t.Fatalf("expected 3 result records, got %d", len(records))
	}
	for _, r := range records {
		if r.Status != types.StatusFail {
			t.Errorf("expected all Failed in result file, got %+v", r)
		}
	}
}

// TestRunResultFileRecordsSkipReason verifies a 409-style already-exists
// upload error is recorded in the result file as Skipped with
// reason=already_exists, and does not fail the run on its own.
func TestRunResultFileRecordsSkipReason(t *testing.T) {
	resultPath := filepath.Join(t.TempDir(), "result.jsonl")
	cfg := &types.Config{
		Concurrency: 1,
		Overwrite:   true,
		ResultFile:  resultPath,
		Mappings:    []types.RegistryMapping{baseMapping(types.NUGET, "nuget-local")},
	}
	dest := &fakeDestAdapter{failWith: map[string]error{
		"/foo/company.grpc.pkg/1.0.0/company.grpc.pkg.1.0.0.nupkg": types.ErrArtifactAlreadyExists,
	}}
	svc := newMockBackedService(cfg, dest)

	if err := svc.Run(context.Background()); err != nil {
		t.Fatalf("expected nil error when only already-exists skips occur, got: %v", err)
	}

	records := readResultFile(t, resultPath)
	success, skipped := 0, 0
	for _, r := range records {
		switch r.Status {
		case types.StatusSuccess:
			success++
		case types.StatusSkip:
			skipped++
			if r.Reason != types.SkipReasonAlreadyExists {
				t.Errorf("skip record missing reason %q: %+v", types.SkipReasonAlreadyExists, r)
			}
		}
	}
	if skipped != 1 {
		t.Errorf("expected 1 skipped record, got %d", skipped)
	}
	if success != 2 {
		t.Errorf("expected 2 success records, got %d", success)
	}
}
