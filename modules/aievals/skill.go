// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package aievals

import (
	_ "embed"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

//go:embed skill/SKILL.md
var skillText string

const skillFileName = "SKILL.md"

// SkillSourcePath is the canonical skill file consumed by the CLI and UI
// generator. Keeping the source coordinate here lets this slice stand alone.
const SkillSourcePath = "modules/aievals/skill/SKILL.md"

// SkillText is the versioned static skill embedded in this package.
func SkillText() string {
	return skillText
}

// PlaceSkill writes the skill to an explicit path, a detected coding-agent
// directory under repo, or stdout when neither exists. It never prompts.
func PlaceSkill(repo, explicit string, stdout io.Writer) (string, error) {
	if stdout == nil {
		stdout = os.Stdout
	}
	if explicit != "" {
		return writeSkill(explicit)
	}
	if dest, ok := detectSkillDir(repo); ok {
		return writeSkill(dest)
	}
	if _, err := fmt.Fprint(stdout, skillText); err != nil {
		return "", err
	}
	return "", nil
}

func detectSkillDir(repo string) (string, bool) {
	if repo == "" {
		return "", false
	}
	for _, dir := range []string{".cursor", ".claude"} {
		info, err := os.Stat(filepath.Join(repo, dir))
		if err != nil || !info.IsDir() {
			continue
		}
		return filepath.Join(repo, dir, "skills", "aievals", skillFileName), true
	}
	return "", false
}

func writeSkill(dest string) (string, error) {
	path := dest
	if !strings.HasSuffix(dest, skillFileName) {
		path = filepath.Join(dest, skillFileName)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	if err := writeSkillAtomic(path, []byte(skillText)); err != nil {
		return "", err
	}
	return path, nil
}

func writeSkillAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".aievals-skill-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpName)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	cleanup = false
	return nil
}
