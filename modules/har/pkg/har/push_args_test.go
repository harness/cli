// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package har

import (
	"strings"
	"testing"

	"github.com/harness/cli/v3/pkg/cmdctx"
)

func TestRejectExtraArgs(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		max     int
		wantErr string
	}{
		{name: "none", args: nil, max: 1},
		{name: "at limit", args: []string{"a.rpm"}, max: 1},
		{name: "one over", args: []string{"a.rpm", "b.rpm"}, max: 1, wantErr: `unexpected argument "b.rpm"`},
		{name: "reports first extra", args: []string{"a", "b", "c", "d"}, max: 2, wantErr: `unexpected argument "c"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := rejectExtraArgs(&cmdctx.Ctx{Args: tt.args}, tt.max)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("got %v, want error containing %q", err, tt.wantErr)
			}
		})
	}
}
