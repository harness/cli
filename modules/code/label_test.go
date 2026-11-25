// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package code

import (
	"testing"

	"github.com/harness/cli/v3/pkg/cmdctx"
)

func TestValidateRepoLabelUpdate(t *testing.T) {
	if err := validateRepoLabelUpdate(nil, cmdctx.EndpointRequest{Body: map[string]any{}}); err == nil {
		t.Fatal("an empty body must be rejected")
	}
	if err := validateRepoLabelUpdate(nil, cmdctx.EndpointRequest{Body: map[string]any{"color": "red"}}); err != nil {
		t.Fatalf("a body with a field must pass: %v", err)
	}
}
