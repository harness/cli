// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package aievals

import (
	"bytes"
	"os"
	"os/exec"
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
	for _, rel := range []string{".cursor", ".cursor/skills", ".cursor/skills/aievals"} {
		t.Run(rel, func(t *testing.T) {
			repo := t.TempDir()
			outside := t.TempDir()
			link := filepath.Join(repo, filepath.FromSlash(rel))
			if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(outside, link); err != nil {
				t.Skipf("symlink creation unavailable: %v", err)
			}
			var stdout bytes.Buffer
			path, err := PlaceSkill(repo, "", &stdout)
			if err != nil || path != "" || stdout.String() != SkillText() {
				t.Fatalf("symlinked destination did not fall back to stdout: path=%q err=%v", path, err)
			}
			if entries, err := os.ReadDir(outside); err != nil || len(entries) != 0 {
				t.Fatalf("symlink target was changed: %v (%v)", entries, err)
			}
		})
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
	skillPath := "skill\tpath\\with'quote\n"
	module := FormatSkillModule("source\nlabel", "body", skillPath)
	const marker = "// Do not edit by hand. Regenerate with: "
	_, command, ok := strings.Cut(module, marker)
	line, _, _ := strings.Cut(command, "\n")
	if !ok || !strings.Contains(line, "-source-label 'source label'") || strings.ContainsAny(line, "\t\r") {
		t.Fatalf("regeneration command was split or contains raw control characters: %q", line)
	}
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash is unavailable to check the regeneration command's arguments")
	}
	// Stub Go to inspect the argument that the one-line command actually passes.
	line = strings.Replace(line, "<this-file>", "output.ts", 1)
	got, err := exec.Command(bash, "--noprofile", "--norc", "-c", `go() { printf '%s' "$4"; }; `+line).Output()
	if err != nil || string(got) != skillPath {
		t.Fatalf("regeneration -skill argument = %q (%v), want %q", got, err, skillPath)
	}
}
