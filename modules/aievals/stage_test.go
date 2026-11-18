// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package aievals

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCommitFilesChecksCollisionsBeforeWriting(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "agents"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "agents", "existing.yaml"), []byte("user content"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := commitFiles(root, map[string]string{
		"agents/existing.yaml": "generated content",
		"agents/new.yaml":      "new content",
	})
	if err == nil {
		t.Fatal("commitFiles() accepted a collision")
	}
	if _, statErr := os.Stat(filepath.Join(root, "agents", "new.yaml")); !os.IsNotExist(statErr) {
		t.Fatalf("collision wrote another pending file: %v", statErr)
	}
	if content, readErr := os.ReadFile(filepath.Join(root, "agents", "existing.yaml")); readErr != nil || string(content) != "user content" {
		t.Fatalf("collision changed existing file: %q (%v)", content, readErr)
	}
}

func TestWriteTreeFileRejectsSymlinkedParent(t *testing.T) {
	// macOS temp directories can have a symlink above the test root.
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "agents")); err != nil {
		t.Skipf("symlink creation unavailable: %v", err)
	}
	err = writeTreeFile(root, "agents/agent.yaml", []byte("must not write"))
	if err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("writeTreeFile() error = %v, want symlink rejection", err)
	}
	if _, statErr := os.Stat(filepath.Join(outside, "agent.yaml")); !os.IsNotExist(statErr) {
		t.Fatalf("symlink target was written: %v", statErr)
	}
}

func TestEmbeddedMetricValidationWithHarnessEvals0294(t *testing.T) {
	python := os.Getenv("AIEVALS_METRIC_CATALOG_PYTHON")
	if python == "" {
		t.Skip("set AIEVALS_METRIC_CATALOG_PYTHON to Python with harness-evals==0.29.4")
	}

	// Use a minimal loaded tree so the test exercises the real SDK factory
	// without requiring the separately released runner package or credentials.
	const tree = `
from importlib.metadata import version
from types import SimpleNamespace as ns
import json
import sys
assert version("harness-evals") == "0.29.4"
metric = ns(module_path=None, config=json.loads(sys.argv[1]), kind="schema_validation",
            metric_type="heuristic", score_name="schema", threshold=0, dimension="correctness")
evaluation = ns(metric_set=ns(entries=[metric]))
tree = ns(agents={"agent": ns(evals={"eval": evaluation})})
`
	for _, tc := range []struct {
		name, config string
		wantError    bool
	}{
		{"with required schema", `{"options":{"schema":{"type":"object"}}}`, false},
		{"without required schema", `{}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := exec.Command(python, "-I", "-c", tree+metricValidationSnippet, tc.config)
			cmd.Env = []string{}
			output, err := cmd.CombinedOutput()
			if tc.wantError {
				if err == nil || !strings.Contains(string(output), "'schema'") {
					t.Fatalf("embedded validator did not report the missing schema: %v", err)
				}
			} else if err != nil {
				t.Fatalf("embedded validator rejected valid schema options: %v", err)
			}
		})
	}
}
