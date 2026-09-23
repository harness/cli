// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package endpoint

import (
	"context"
	"testing"

	"github.com/harness/cli/v3/pkg/auth"
	"github.com/harness/cli/v3/pkg/cmdctx"
	"github.com/harness/cli/v3/pkg/spec"
)

func TestBuildRequest_NoAccountID(t *testing.T) {
	tests := []struct {
		name string
		ep   *spec.EndpointSpec
		want bool
	}{
		{name: "default_false", ep: &spec.EndpointSpec{Path: "/items"}, want: false},
		{name: "propagated_true", ep: &spec.EndpointSpec{Path: "/items", NoAccountID: true}, want: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := &cmdctx.Ctx{
				Context: context.Background(),
				Auth: &auth.ResolvedAuth{
					AccountID: "acct",
					OrgID:     "org",
					ProjectID: "proj",
				},
			}
			req, err := BuildRequest(ctx, tc.ep)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if req.NoAccountID != tc.want {
				t.Fatalf("req.NoAccountID = %v, want %v", req.NoAccountID, tc.want)
			}
		})
	}
}
