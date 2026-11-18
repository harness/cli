// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package aievals

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPlaceSkillSelectsOneDetectedAgentDirectory(t *testing.T) {
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, ".cursor"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repo, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	path, err := PlaceSkill(repo, "", nil)
	if err != nil {
		t.Fatalf("PlaceSkill() error = %v", err)
	}
	want := filepath.Join(repo, ".cursor", "skills", "aievals", "SKILL.md")
	if path != want {
		t.Fatalf("PlaceSkill() path = %q, want %q", path, want)
	}
	if _, err := os.Stat(filepath.Join(repo, ".claude", "skills", "aievals", "SKILL.md")); !os.IsNotExist(err) {
		t.Fatalf("PlaceSkill() wrote more than one detected destination: %v", err)
	}
}

func TestPlaceSkillPrintsWhenNoAgentDirectoryExists(t *testing.T) {
	repo := t.TempDir()
	var stdout bytes.Buffer
	path, err := PlaceSkill(repo, "", &stdout)
	if err != nil {
		t.Fatalf("PlaceSkill() error = %v", err)
	}
	if path != "" || stdout.String() != SkillText() {
		t.Fatalf("PlaceSkill() = (%q, %q), want stdout-only skill", path, stdout.String())
	}
	entries, err := os.ReadDir(repo)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("stdout fallback created files: %v", entries)
	}
}

func TestPlaceSkillSkipsSymlinkedDetectedDirectory(t *testing.T) {
	repo := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(repo, ".cursor")); err != nil {
		t.Skipf("symlink creation unavailable: %v", err)
	}
	var stdout bytes.Buffer
	path, err := PlaceSkill(repo, "", &stdout)
	if err != nil {
		t.Fatalf("PlaceSkill() error = %v", err)
	}
	if path != "" || stdout.String() != SkillText() {
		t.Fatalf("symlinked destination was selected: (%q, %q)", path, stdout.String())
	}
	if _, err := os.Stat(filepath.Join(outside, "skills")); !os.IsNotExist(err) {
		t.Fatalf("symlink target was written: %v", err)
	}
}

func TestWriteSkillTreatsOnlyExactSkillFileNameAsFile(t *testing.T) {
	repo := t.TempDir()
	dir := filepath.Join(repo, "team-SKILL.md")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path, err := writeSkill(dir)
	if err != nil {
		t.Fatalf("writeSkill() error = %v", err)
	}
	if path != filepath.Join(dir, "SKILL.md") {
		t.Fatalf("writeSkill() path = %q", path)
	}
}

func TestFormatSkillModuleSanitizesLabelsAndPreservesGeneratorInputs(t *testing.T) {
	module := FormatSkillModule("source\nlabel", "body", "skill\npath")
	header := strings.SplitN(module, "\n\n", 2)[0]
	if strings.Contains(header, "source\nlabel") || strings.Contains(header, "skill\npath") {
		t.Fatalf("generated header contains a raw line break: %q", header)
	}
	if !strings.Contains(module, "-skill 'skill path' -source-label 'source label'") {
		t.Fatalf("regeneration command did not preserve custom inputs: %s", module)
	}
}
