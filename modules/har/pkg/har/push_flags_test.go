// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package har

import (
	"testing"

	"github.com/harness/cli/v3/pkg/cmdctx"
)

func TestMaxConcurrentUploadsFlag(t *testing.T) {
	tests := []struct {
		name    string
		flags   map[string]any
		want    int
		wantErr bool
	}{
		{"unset", map[string]any{}, defaultMaxConcurrentUploads, false},
		{"empty", map[string]any{"max-concurrent-uploads": ""}, defaultMaxConcurrentUploads, false},
		{"valid", map[string]any{"max-concurrent-uploads": "3"}, 3, false},
		{"zero", map[string]any{"max-concurrent-uploads": "0"}, defaultMaxConcurrentUploads, false},
		{"negative", map[string]any{"max-concurrent-uploads": "-1"}, defaultMaxConcurrentUploads, false},
		{"non-numeric", map[string]any{"max-concurrent-uploads": "abc"}, 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := maxConcurrentUploadsFlag(&cmdctx.Ctx{FlagValues: tt.flags})
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Fatalf("got %d, want %d", got, tt.want)
			}
		})
	}
}
