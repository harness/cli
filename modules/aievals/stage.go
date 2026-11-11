// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package aievals

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ValidatorSnippet is the D2 loader invocation. sys.argv[1] is a .harness/evals directory.
var ValidatorSnippet = "import sys\nfrom ai_evals.onboarding.load import load_onboarding_tree\ntree = load_onboarding_tree(sys.argv[1])\n" + metricCatalogSnippet + "\n" + metricValidationSnippet

//go:embed metric_validate.py
var metricValidationSnippet string

// ValidatorArgs is the subprocess the staging step runs.
func ValidatorArgs(python, evalsRoot string) []string {
	return []string{python, "-I", "-c", ValidatorSnippet, evalsRoot}
}

// OnboardArgs is the D2 local/live command.
func OnboardArgs(bin, root, agent, environment string) []string {
	if environment == "" {
		environment = "local"
	}
	return []string{bin, OnboardCommand, "--root", root, "--agent", agent, "--environment", environment}
}

// CommandResult is one subprocess invocation.
type CommandResult struct {
	Stdout string
	Stderr string
	Code   int
}

// CommandFunc runs a subprocess. dir and env may be empty.
type CommandFunc func(name string, args []string, env []string, dir string) (CommandResult, error)

// StageResult reports which destination files were written or left unchanged.
type StageResult struct {
	Wrote      []string
	Identical  []string
	Collisions []string
}

// Stage copies files into a temporary .harness/evals tree, runs the validator,
// and writes only missing or identical destination files.
func Stage(repo string, files map[string]string, python string, run CommandFunc) (StageResult, error) {
	if len(files) == 0 {
		return StageResult{}, fmt.Errorf("no candidate files to stage")
	}
	if run == nil {
		run = defaultCommand
	}
	tmp, err := os.MkdirTemp("", "aievals-stage-")
	if err != nil {
		return StageResult{}, err
	}
	defer os.RemoveAll(tmp)
	evals := filepath.Join(tmp, "repo", ".harness", "evals")
	for rel, body := range files {
		if err := writeTreeFile(evals, rel, []byte(body)); err != nil {
			return StageResult{}, err
		}
	}
	args := ValidatorArgs(python, evals)
	result, err := run(args[0], args[1:], nil, tmp)
	if err != nil {
		return StageResult{}, err
	}
	if result.Code != 0 {
		msg := strings.TrimSpace(result.Stderr)
		if msg == "" {
			msg = strings.TrimSpace(result.Stdout)
		}
		return StageResult{}, fmt.Errorf("onboarding validator failed: %s", msg)
	}
	return commitFiles(filepath.Join(repo, ".harness", "evals"), files)
}

func writeTreeFile(root, rel string, data []byte) error {
	clean := filepath.Clean(rel)
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) || filepath.IsAbs(clean) {
		return fmt.Errorf("candidate path escapes the tree: %s", rel)
	}
	return writeAtomic(filepath.Join(root, clean), data)
}

func commitFiles(destRoot string, files map[string]string) (StageResult, error) {
	var result StageResult
	draftMarker := filepath.Join(destRoot, ".onboard-guide-draft")
	_, draftErr := os.Stat(draftMarker)
	draft := draftErr == nil
	if draftErr != nil && !os.IsNotExist(draftErr) {
		return StageResult{}, draftErr
	}
	type pending struct {
		rel  string
		body string
	}
	var writes []pending
	rels := make([]string, 0, len(files))
	for rel := range files {
		rels = append(rels, rel)
	}
	sort.Strings(rels)
	for _, rel := range rels {
		body := files[rel]
		dest := filepath.Join(destRoot, filepath.Clean(rel))
		existing, err := os.ReadFile(dest)
		if err != nil {
			if os.IsNotExist(err) {
				writes = append(writes, pending{rel, body})
				continue
			}
			return StageResult{}, err
		}
		if string(existing) == body {
			result.Identical = append(result.Identical, rel)
			continue
		}
		if draft {
			writes = append(writes, pending{rel, body})
			continue
		}
		result.Collisions = append(result.Collisions, rel)
	}
	for _, item := range writes {
		if err := writeTreeFile(destRoot, item.rel, []byte(item.body)); err != nil {
			return result, err
		}
		result.Wrote = append(result.Wrote, item.rel)
	}
	if len(result.Collisions) > 0 {
		return result, fmt.Errorf("destination differs and was left unchanged: %s", strings.Join(result.Collisions, ", "))
	}
	if draft {
		if _, stagedDraft := files[".onboard-guide-draft"]; !stagedDraft {
			if err := os.Remove(draftMarker); err != nil && !os.IsNotExist(err) {
				return result, err
			}
		}
	}
	return result, nil
}
