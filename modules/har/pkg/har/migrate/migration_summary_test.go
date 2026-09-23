package migrate

import (
	"bytes"
	"context"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/harness/cli/modules/har/pkg/har/migrate/types"
)

// TestRunSummaryModeSucceeds verifies the --summary flag path (Config.Summary)
// takes the printSummary branch instead of the full table and still completes
// a clean migration without error.
func TestRunSummaryModeSucceeds(t *testing.T) {
	cfg := &types.Config{
		Concurrency: 1,
		Overwrite:   true,
		Summary:     true,
		Mappings:    []types.RegistryMapping{baseMapping(types.NUGET, "nuget-local")},
	}
	dest := &fakeDestAdapter{}
	svc := newMockBackedService(cfg, dest)

	stdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe() error: %v", err)
	}
	os.Stdout = w

	runErr := svc.Run(context.Background())

	w.Close()
	os.Stdout = stdout
	var buf bytes.Buffer
	_, _ = io.Copy(&buf, r)

	if runErr != nil {
		t.Fatalf("expected nil error for clean migration, got: %v", runErr)
	}
	out := buf.String()
	if !strings.Contains(out, "Migration Summary") {
		t.Errorf("stdout %q does not contain summary header", out)
	}
	if !strings.Contains(out, "Success :") || !strings.Contains(out, "Total   :") {
		t.Errorf("stdout %q missing expected summary lines", out)
	}
}

// TestPrintSummaryCounts verifies printSummary tallies each status correctly.
func TestPrintSummaryCounts(t *testing.T) {
	stats := []types.FileStat{
		{Status: types.StatusSuccess},
		{Status: types.StatusSuccess},
		{Status: types.StatusSkip},
		{Status: types.StatusFail},
	}

	stdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe() error: %v", err)
	}
	os.Stdout = w

	printSummary(stats)

	w.Close()
	os.Stdout = stdout
	var buf bytes.Buffer
	_, _ = io.Copy(&buf, r)
	out := buf.String()

	if !strings.Contains(out, "Success :  2") {
		t.Errorf("stdout %q missing success count of 2", out)
	}
	if !strings.Contains(out, "Skipped :  1") {
		t.Errorf("stdout %q missing skipped count of 1", out)
	}
	if !strings.Contains(out, "Failed  :  1") {
		t.Errorf("stdout %q missing failed count of 1", out)
	}
	if !strings.Contains(out, "Total   :  4") {
		t.Errorf("stdout %q missing total count of 4", out)
	}
}
