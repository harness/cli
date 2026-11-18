// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package aievals

import (
	"os"
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
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "agents")); err != nil {
		t.Skipf("symlink creation unavailable: %v", err)
	}
	err := writeTreeFile(root, "agents/agent.yaml", []byte("must not write"))
	if err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("writeTreeFile() error = %v, want symlink rejection", err)
	}
	if _, statErr := os.Stat(filepath.Join(outside, "agent.yaml")); !os.IsNotExist(statErr) {
		t.Fatalf("symlink target was written: %v", statErr)
	}
}
