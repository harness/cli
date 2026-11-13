package migrate

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/harness/cli/modules/har/pkg/har/migrate/types"
)

func TestRunCondaDateFilterKeepsRepodata(t *testing.T) {
	const (
		oldPkg = "/linux-64/demo-pkg-1.0.0-0.tar.bz2"
		newPkg = "/linux-64/demo-pkg-2.0.0-0.conda"
	)
	createdAfter := func(date string) *types.DateFilter {
		ts, err := time.Parse(time.DateOnly, date)
		if err != nil {
			t.Fatal(err)
		}
		return &types.DateFilter{Match: types.DateFilterMatchAny, CreatedAfter: &ts}
	}
	cases := []struct {
		name       string
		dateFilter *types.DateFilter
		want       []string
	}{
		{name: "no date filter migrates every package", want: []string{oldPkg, newPkg}},
		{name: "window after repodata.json still finds in-window package",
			dateFilter: createdAfter("2024-01-01"), want: []string{newPkg}},
		{name: "window with no new packages migrates nothing",
			dateFilter: createdAfter("2025-01-01")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mapping := baseMapping(types.CONDA, "conda-local")
			mapping.DateFilter = tc.dateFilter
			cfg := &types.Config{Concurrency: 1, Overwrite: true, Mappings: []types.RegistryMapping{mapping}}
			dest := &fakeDestAdapter{}
			if err := newMockBackedService(cfg, dest).Run(context.Background()); err != nil {
				t.Fatalf("expected nil error, got: %v", err)
			}
			uploads := dest.uploadedURIs()
			slices.Sort(uploads)
			if !slices.Equal(uploads, tc.want) {
				t.Fatalf("uploads = %v, want %v", uploads, tc.want)
			}
		})
	}
}
