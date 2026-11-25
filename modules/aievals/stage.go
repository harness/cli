// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package aievals

import (
	"bytes"
	_ "embed"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// ValidatorSnippet is the D2 loader invocation. sys.argv[1] is a .harness/evals directory.
var ValidatorSnippet = "import sys\nfrom ai_evals.onboarding.load import load_onboarding_tree\ntree = load_onboarding_tree(sys.argv[1])\n" + metricValidationSnippet

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
// and writes only missing or identical destination files. It validates the
// same bytes it writes, including v1 candidates supplied by the CLI.
func Stage(repo string, files map[string]string, python string, run CommandFunc) (StageResult, error) {
	if len(files) == 0 {
		return StageResult{}, fmt.Errorf("no candidate files to stage")
	}
	if run == nil {
		run = stageCommand
	}
	tmp, err := os.MkdirTemp("", "aievals-stage-")
	if err != nil {
		return StageResult{}, err
	}
	defer os.RemoveAll(tmp)
	// The OS temp location can have a system symlink (for example macOS /var).
	// Resolve our private temp directory, not any user-selected destination.
	stagingDir, err := filepath.EvalSymlinks(tmp)
	if err != nil {
		return StageResult{}, err
	}
	evals := filepath.Join(stagingDir, "repo", ".harness", "evals")
	for rel, body := range files {
		if err := writeTreeFile(evals, rel, []byte(body)); err != nil {
			return StageResult{}, err
		}
	}
	args := ValidatorArgs(python, evals)
	result, err := run(args[0], args[1:], nil, stagingDir)
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
	dest := filepath.Join(root, clean)
	parent := filepath.Dir(dest)
	if err := ensureNoSymlink(parent); err != nil {
		return err
	}
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return err
	}
	if err := ensureNoSymlink(parent); err != nil {
		return err
	}
	return writeAtomic(dest, data)
}

func commitFiles(destRoot string, files map[string]string) (StageResult, error) {
	var result StageResult
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
		result.Collisions = append(result.Collisions, rel)
	}
	if len(result.Collisions) > 0 {
		return result, fmt.Errorf("destination differs and was left unchanged: %s", strings.Join(result.Collisions, ", "))
	}
	for _, item := range writes {
		if err := ensureNoSymlink(filepath.Join(destRoot, filepath.Dir(item.rel))); err != nil {
			return result, err
		}
	}
	for _, item := range writes {
		if err := writeTreeFile(destRoot, item.rel, []byte(item.body)); err != nil {
			return result, err
		}
		result.Wrote = append(result.Wrote, item.rel)
	}
	return result, nil
}

func ensureNoSymlink(path string) error {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	volume := filepath.VolumeName(absolute)
	current := volume + string(filepath.Separator)
	rest := strings.TrimPrefix(absolute, current)
	for _, part := range strings.Split(rest, string(filepath.Separator)) {
		if part == "" {
			continue
		}
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("staging path contains a symlink: %s", current)
		}
	}
	return nil
}

func stageCommand(name string, args []string, env []string, dir string) (CommandResult, error) {
	cmd := exec.Command(name, args...)
	if dir != "" {
		cmd.Dir = dir
	}
	if len(env) > 0 {
		cmd.Env = append(os.Environ(), env...)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	result := CommandResult{Stdout: stdout.String(), Stderr: stderr.String()}
	if err == nil {
		return result, nil
	}
	if exit, ok := err.(*exec.ExitError); ok {
		result.Code = exit.ExitCode()
		return result, nil
	}
	return result, err
}
