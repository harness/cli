// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package gitops

import (
	"reflect"
	"strings"
	"testing"

	"github.com/harness/cli/v3/pkg/cmdctx"
)

func TestBulkSyncBody(t *testing.T) {
	bad := []string{"justname", "a/b/extra", "/app", "agent/", ""}
	for _, target := range bad {
		ctx := &cmdctx.Ctx{FlagValues: map[string]any{"target": []string{"ok/app", target}}}
		if _, err := bulkSyncBody(ctx); err == nil || !strings.Contains(err.Error(), "<agent/app>") {
			t.Errorf("target %q: err = %v, want <agent/app> error", target, err)
		}
	}

	ctx := &cmdctx.Ctx{FlagValues: map[string]any{
		"target":      []string{"account.ag/app1", "ag2/app2"},
		"prune":       true,
		"sync-option": []string{"CreateNamespace=true"},
	}}
	got, err := bulkSyncBody(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"applicationTargets": []map[string]any{
			{"agentIdentifier": "account.ag", "applicationName": "app1"},
			{"agentIdentifier": "ag2", "applicationName": "app2"},
		},
		"prune":       true,
		"syncOptions": map[string]any{"items": []string{"CreateNamespace=true"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}
