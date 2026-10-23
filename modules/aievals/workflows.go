// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package aievals

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/harness/cli/v3/pkg/auth"
	"github.com/harness/cli/v3/pkg/cmdctx"
	"github.com/harness/cli/v3/pkg/hbase"
)

const (
	setupWorkflowID  = "setup"
	runWorkflowID    = "run"
	uploadWorkflowID = "upload"
)

// StatusError carries a process exit code from the runner or a batched question.
type StatusError struct {
	Code int
	Msg  string
}

func (e *StatusError) Error() string {
	if e.Msg != "" {
		return e.Msg
	}
	return fmt.Sprintf("exit %d", e.Code)
}

// SetupOptions controls skill placement and staging.
type SetupOptions struct {
	Repo          string
	SkillDir      string
	AcceptMedium  bool
	Answers       map[string]string
	IsPty         bool
	Stdout        io.Writer
	Stderr        io.Writer
	Python        string
	Run           CommandFunc
	ReadLine      func() (string, error)
	SkipValidator bool
}

// Setup writes the skill and stages high-confidence files.
func Setup(opts SetupOptions) (int, error) {
	stderr := opts.Stderr
	if stderr == nil {
		stderr = os.Stderr
	}
	if _, err := PlaceSkill(opts.Repo, opts.SkillDir, opts.Stdout); err != nil {
		return 1, err
	}
	inf, err := Infer(opts.Repo)
	if err != nil {
		return 1, err
	}
	accept := opts.AcceptMedium
	needsMedium := len(inf.Medium()) > 0 && !accept
	needsLow := unansweredLow(inf, opts.Answers)
	if opts.IsPty && opts.ReadLine != nil && (needsMedium || needsLow) {
		line, err := opts.ReadLine()
		if err != nil {
			return 1, err
		}
		if strings.EqualFold(strings.TrimSpace(line), "yes") {
			accept = true
			needsMedium = false
		}
	}
	if medium := FormatMediumConfirmation(inf); medium != "" && !accept {
		fmt.Fprint(stderr, medium)
	}
	pendingLow := unansweredLow(inf, opts.Answers)
	if low := FormatLowQuestion(inf); low != "" && pendingLow {
		fmt.Fprint(stderr, low)
	}
	files := ApplyFiles(inf, accept, opts.Answers)
	if len(files) == 0 {
		return 2, &StatusError{Code: 2, Msg: "onboarding did not find a high-confidence tree to stage"}
	}
	if opts.SkipValidator {
		if _, err := commitFiles(filepath.Join(opts.Repo, ".harness", "evals"), files); err != nil {
			return 1, err
		}
	} else {
		python := opts.Python
		if python == "" {
			layout, err := EnsureRunner(RunnerHome(), nil, opts.Run)
			if err != nil {
				return 1, err
			}
			python = layout.Python
		}
		if _, err := Stage(opts.Repo, files, python, opts.Run); err != nil {
			return 1, err
		}
	}
	if (len(inf.Medium()) > 0 && !accept) || pendingLow {
		return 2, &StatusError{Code: 2, Msg: "onboarding needs an answer before staging the remaining values"}
	}
	return 0, nil
}

func unansweredLow(inf Inference, answers map[string]string) bool {
	for _, ev := range inf.Low() {
		if answers[ev.Field] == "" {
			return true
		}
	}
	return false
}

// RunAgent verifies the pinned runner and invokes onboard-run.
func RunAgent(home, root, agent, environment string, resolved *auth.ResolvedAuth, stderr io.Writer, run CommandFunc) (int, error) {
	if run == nil {
		run = defaultCommand
	}
	layout, err := VerifyRunner(home, run)
	if err != nil {
		return 1, err
	}
	env, notice := RunnerProcessEnv(resolved)
	if notice != "" && stderr != nil {
		fmt.Fprintln(stderr, notice)
	}
	args := OnboardArgs(layout.AIEvals, root, agent, environment)
	result, err := run(args[0], args[1:], env, filepath.Dir(root))
	if err != nil {
		return 1, err
	}
	if result.Stdout != "" && stderr != nil {
		fmt.Fprint(stderr, result.Stdout)
	}
	if result.Code != 0 {
		msg := strings.TrimSpace(result.Stderr)
		if msg == "" {
			msg = strings.TrimSpace(result.Stdout)
		}
		return result.Code, &StatusError{Code: result.Code, Msg: msg}
	}
	return 0, nil
}

func setupWorkflow(ctx *cmdctx.Ctx) error {
	repo := cmdctx.GetString(ctx.FlagValues, "repo")
	if repo == "" {
		wd, err := os.Getwd()
		if err != nil {
			return err
		}
		repo = wd
	}
	code, err := Setup(SetupOptions{
		Repo:         repo,
		SkillDir:     cmdctx.GetString(ctx.FlagValues, "skill-dir"),
		AcceptMedium: cmdctx.GetBool(ctx.FlagValues, "yes"),
		IsPty:        ctx.IsPty,
		Stdout:       os.Stdout,
		Stderr:       os.Stderr,
		ReadLine:     readOneLine,
	})
	return finish(code, err)
}

func runWorkflow(ctx *cmdctx.Ctx) error {
	root := cmdctx.GetString(ctx.FlagValues, "root")
	if root == "" {
		root = ".harness/evals"
	}
	if !filepath.IsAbs(root) {
		wd, err := os.Getwd()
		if err != nil {
			return err
		}
		root = filepath.Join(wd, root)
	}
	code, err := RunAgent(RunnerHome(), root, cmdctx.GetString(ctx.FlagValues, "agent"), cmdctx.GetString(ctx.FlagValues, "environment"), ctx.ScopedAuth(), os.Stderr, nil)
	return finish(code, err)
}

func uploadWorkflow(ctx *cmdctx.Ctx) error {
	root := cmdctx.GetString(ctx.FlagValues, "root")
	if root == "" {
		root = ".harness/evals"
	}
	if !filepath.IsAbs(root) {
		wd, err := os.Getwd()
		if err != nil {
			return err
		}
		root = filepath.Join(wd, root)
	}
	repo := filepath.Dir(filepath.Dir(root))
	layout, err := VerifyRunner(RunnerHome(), nil)
	if err != nil {
		return err
	}
	args := ValidatorArgs(layout.Python, root)
	result, err := defaultCommand(args[0], args[1:], nil, repo)
	if err != nil {
		return err
	}
	if result.Code != 0 {
		return fmt.Errorf("onboarding validator failed: %s", strings.TrimSpace(result.Stderr))
	}
	payload, err := BuildArchive(repo, DefaultLimits())
	if err != nil {
		return err
	}
	text, err := UploadArchive(ctx.ScopedAuth(), payload, nil)
	if err != nil {
		return err
	}
	fmt.Fprint(os.Stdout, text)
	return nil
}

func finish(code int, err error) error {
	if err == nil {
		return nil
	}
	if code == 2 {
		fmt.Fprintln(os.Stderr, err.Error())
		hbase.Exit(2)
	}
	return err
}

func readOneLine() (string, error) {
	var line string
	_, err := fmt.Fscanln(os.Stdin, &line)
	return line, err
}
