// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package har

import (
	"path/filepath"
	"testing"

	"github.com/harness/cli/v3/pkg/cmdctx"
)

func TestPipConfigPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	globalPath := filepath.Join(home, ".config", "pip", "pip.conf")

	tests := []struct {
		name     string
		provided bool
		value    bool
		want     string
	}{
		{"flag omitted defaults to global", false, false, globalPath},
		{"explicit --global", true, true, globalPath},
		{"explicit --global=false", true, false, "pip.conf"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := &cmdctx.Ctx{
				FlagValues:    map[string]any{"global": tt.value},
				ProvidedFlags: map[string]bool{"global": tt.provided},
			}
			got, err := pipConfigPath(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Errorf("pipConfigPath() = %q, want %q", got, tt.want)
			}
		})
	}
}
