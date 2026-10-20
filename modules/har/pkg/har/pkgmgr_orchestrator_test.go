// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package har

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/harness/cli/modules/har/pkg/har/genapi/ar_v3"
)

func scanItem(name string, status ar_v3.BulkScanResultItemScanStatus) ar_v3.BulkScanResultItem {
	return ar_v3.BulkScanResultItem{PackageName: &name, ScanStatus: &status}
}

func TestPkgmgrEvaluateBatchesKeepsEarlierBatchesOnFailure(t *testing.T) {
	artifacts := make([]ar_v3.ArtifactScanInput, 5)
	errBatch := errors.New("batch failed")

	tests := []struct {
		name      string
		failOn    int
		wantScans int
		wantErr   bool
	}{
		{"all batches succeed", 0, 5, false},
		{"first batch fails", 1, 0, true},
		{"last batch fails", 3, 4, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scans, err := pkgmgrEvaluateBatches(artifacts, 2, func(batch []ar_v3.ArtifactScanInput, batchNum int) ([]ar_v3.BulkScanResultItem, error) {
				if batchNum == tt.failOn {
					return nil, errBatch
				}
				return make([]ar_v3.BulkScanResultItem, len(batch)), nil
			})
			if len(scans) != tt.wantScans {
				t.Errorf("got %d scans, want %d", len(scans), tt.wantScans)
			}
			if (err != nil) != tt.wantErr {
				t.Errorf("err = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestWriteFirewallSummary(t *testing.T) {
	counts := countScanStatuses([]ar_v3.BulkScanResultItem{
		scanItem("a", ar_v3.BulkScanResultItemScanStatusBLOCKED),
		scanItem("b", ar_v3.BulkScanResultItemScanStatusALLOWED),
		scanItem("c", ar_v3.BulkScanResultItemScanStatusWARN),
		{},
	})
	if counts != (scanStatusCounts{Allowed: 1, Warn: 1, Blocked: 1, Unknown: 1}) {
		t.Fatalf("counts = %+v", counts)
	}

	var complete, partial bytes.Buffer
	writeFirewallSummary(&complete, counts, 4, false)
	writeFirewallSummary(&partial, counts, 10, true)

	for _, want := range []string{"Evaluated: 4\n", "Blocked: 1", "Unknown: 1"} {
		if !strings.Contains(complete.String(), want) {
			t.Errorf("complete summary missing %q:\n%s", want, complete.String())
		}
	}
	if strings.Contains(complete.String(), "incomplete") {
		t.Errorf("complete summary marked incomplete:\n%s", complete.String())
	}
	for _, want := range []string{"Evaluated: 4 of 10 resolved dependencies", "incomplete"} {
		if !strings.Contains(partial.String(), want) {
			t.Errorf("partial summary missing %q:\n%s", want, partial.String())
		}
	}
}

func TestPkgmgrFailure(t *testing.T) {
	if got := pkgmgrFailure("npm", "install", scanStatusCounts{Blocked: 2, Allowed: 5}).Error(); got != "npm install failed: 2 package(s) blocked by firewall" {
		t.Errorf("blocked: got %q", got)
	}
	if got := pkgmgrFailure("npm", "install", scanStatusCounts{Allowed: 5}).Error(); got != "npm install failed" {
		t.Errorf("none blocked: got %q", got)
	}
}
