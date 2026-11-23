// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package migratable

import (
	"context"
	"strings"
	"testing"

	"github.com/harness/cli/modules/har/pkg/har/migrate/tree"
	"github.com/harness/cli/modules/har/pkg/har/migrate/types"

	"github.com/rs/zerolog"
)

// versionsAdapter reports a version whose path is absent from the package's tree.
type versionsAdapter struct{ noopCranAdapter }

func (versionsAdapter) GetVersions(types.Package, *types.TreeNode, string, string, types.ArtifactType) ([]types.Version, error) {
	return []types.Version{{Name: "1.0.0", Path: "1.0.0"}}, nil
}

func newMissingVersionPackageJob(mapping *types.RegistryMapping) *Package {
	root := tree.TransformToTree([]types.File{{Name: "a-1.0.0.jar", Uri: "g/a/1.0.0/a-1.0.0.jar"}})
	return &Package{
		srcRegistry:  "src-reg",
		destRegistry: "dst-reg",
		srcAdapter:   versionsAdapter{},
		destAdapter:  versionsAdapter{},
		artifactType: types.MAVEN,
		logger:       zerolog.Nop(),
		pkg:          types.Package{Name: "g/a"},
		node:         root,
		stats:        &types.TransferStats{},
		config:       &types.Config{Concurrency: 1},
		mapping:      mapping,
		registry:     types.RegistryInfo{Path: "dst-reg"},
	}
}

// Without a filter nothing could have pruned the version, so a path that does not
// resolve is an adapter bug and must fail instead of reporting an empty success.
func TestMigrateFailsWhenVersionMissingFromTreeWithoutFilters(t *testing.T) {
	job := newMissingVersionPackageJob(&types.RegistryMapping{})

	err := job.Migrate(context.Background())
	if err == nil || !strings.Contains(err.Error(), "1.0.0") {
		t.Fatalf("Migrate error = %v, want a failure naming version 1.0.0", err)
	}
}

func TestMigrateSkipsVersionPrunedByFilters(t *testing.T) {
	job := newMissingVersionPackageJob(&types.RegistryMapping{IncludePatterns: []string{"*.jar"}})

	if err := job.Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate: %v, want the pruned version skipped", err)
	}
}

func TestMigrateRecordsFailureWhenVersionMissingFromTree(t *testing.T) {
	job := newMissingVersionPackageJob(&types.RegistryMapping{})

	if err := job.Migrate(context.Background()); err == nil {
		t.Fatal("Migrate succeeded for a version missing from the tree")
	}
	snap := job.stats.Snapshot()
	if len(snap) != 1 || snap[0].Status != types.StatusFail || !strings.Contains(snap[0].Uri, "1.0.0") {
		t.Errorf("stats = %+v, want one StatusFail stat for version 1.0.0", snap)
	}
}
