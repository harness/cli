// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package migratable

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"testing"

	"github.com/harness/cli/modules/har/pkg/har/migrate/types"

	"github.com/rs/zerolog"
)

type debianFakeDest struct {
	noopComposerAdapter
	existing map[string]bool
	checkErr error
	uploads  []string
}

func (d *debianFakeDest) UploadExists(
	_ context.Context, _ string, f *types.File, _, _ string, _ types.ArtifactType, _ map[string]interface{},
) (bool, error) {
	return d.existing[f.Uri], d.checkErr
}

func (d *debianFakeDest) UploadFile(
	_ string, _ io.ReadCloser, f *types.File, _ http.Header, _, _ string, _ types.ArtifactType,
	_ map[string]interface{},
) error {
	d.uploads = append(d.uploads, f.Uri)
	return nil
}

func TestMigrateDebianDscSkipChecks(t *testing.T) {
	const dsc, orig, debian = "pool/demo_1.0.dsc", "pool/demo_1.0.orig.tar.gz", "pool/demo_1.0.debian.tar.xz"
	tests := []struct {
		name        string
		existing    map[string]bool
		checkErr    error
		wantUploads []string
		wantStatus  map[types.Status]int
	}{
		{
			name:        "nothing at destination",
			wantUploads: []string{dsc, orig, debian},
			wantStatus:  map[types.Status]int{types.StatusSuccess: 3},
		},
		{
			name:        "dsc present still checks each source file",
			existing:    map[string]bool{dsc: true, orig: true},
			wantUploads: []string{debian},
			wantStatus:  map[types.Status]int{types.StatusSkip: 2, types.StatusSuccess: 1},
		},
		{
			name:       "failed check uploads nothing",
			checkErr:   types.NewStatusError(http.StatusForbidden, "forbidden"),
			wantStatus: map[types.Status]int{types.StatusFail: 1},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			src := &composerFakeSrc{content: map[string][]byte{dsc: nil, orig: nil, debian: nil}}
			dest := &debianFakeDest{existing: tt.existing, checkErr: tt.checkErr}
			stats := &types.TransferStats{}
			job := &Package{
				srcRegistry:  "src-reg",
				destRegistry: "dst-reg",
				srcAdapter:   src,
				destAdapter:  dest,
				artifactType: types.DEBIAN,
				logger:       zerolog.Nop(),
				pkg: types.Package{
					Name: "demo_1.0.dsc",
					URL:  dsc,
					Metadata: map[string]string{
						"distribution": "stable",
						"component":    "main",
						"sourceFiles":  "demo_1.0.orig.tar.gz,demo_1.0.debian.tar.xz",
						"directory":    "pool",
						"packageName":  "demo",
						"fullVersion":  "1.0-1",
					},
				},
				stats:  stats,
				config: &types.Config{Concurrency: 1},
			}

			if err := job.migrateDebian(context.Background()); err != nil {
				t.Fatalf("migrateDebian: %v", err)
			}
			if fmt.Sprint(dest.uploads) != fmt.Sprint(tt.wantUploads) {
				t.Errorf("uploads = %v, want %v", dest.uploads, tt.wantUploads)
			}
			gotStatus := map[types.Status]int{}
			for _, s := range stats.FileStats {
				gotStatus[s.Status]++
			}
			if fmt.Sprint(gotStatus) != fmt.Sprint(tt.wantStatus) {
				t.Errorf("stats = %v, want %v", gotStatus, tt.wantStatus)
			}
		})
	}
}
