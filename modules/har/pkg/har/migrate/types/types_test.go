// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package types

import (
	"sync"
	"testing"
)

// TransferStats is shared by pointer across every concurrently running file
// job, so an unsynchronised append loses records (and races). Run with -race to
// catch the unsynchronised variant.
func TestTransferStatsAddIsConcurrencySafe(t *testing.T) {
	const writers, perWriter = 16, 64

	stats := &TransferStats{}
	var wg sync.WaitGroup
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < perWriter; i++ {
				stats.Add(FileStat{Name: "artifact", Status: StatusSuccess})
			}
		}(w)
	}
	wg.Wait()

	if got := len(stats.Snapshot()); got != writers*perWriter {
		t.Errorf("recorded %d stats, want %d — records lost to a concurrent append", got, writers*perWriter)
	}
}

// Snapshot must not alias the live slice, otherwise reporting code reading the
// snapshot races with jobs still appending.
func TestTransferStatsSnapshotIsIndependentCopy(t *testing.T) {
	stats := &TransferStats{}
	stats.Add(FileStat{Name: "first"})

	snap := stats.Snapshot()
	stats.Add(FileStat{Name: "second"})

	if len(snap) != 1 {
		t.Errorf("snapshot length = %d, want 1 (must not observe later appends)", len(snap))
	}
	snap[0].Name = "mutated"
	if stats.Snapshot()[0].Name != "first" {
		t.Error("mutating the snapshot changed the underlying stats")
	}
}

func TestTransferStatsNilReceiverIsSafe(t *testing.T) {
	var stats *TransferStats
	stats.Add(FileStat{Name: "artifact"})
	if got := stats.Snapshot(); got == nil || len(got) != 0 {
		t.Errorf("Snapshot() on nil = %v, want empty non-nil slice", got)
	}
}
