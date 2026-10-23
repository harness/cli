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
		return filepath.Join(repo, dir, "skills", "aievals-onboarding", skillFileName), true
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
	if err := writeAtomic(path, []byte(skillText)); err != nil {
		return "", err
	}
	return path, nil
}
