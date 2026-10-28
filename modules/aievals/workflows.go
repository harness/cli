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
	Repo         string
	SkillDir     string
	AcceptMedium bool
	Answers      map[string]string
	IsPty        bool
	Stdout       io.Writer
	Stderr       io.Writer
	Python       string
	Run          CommandFunc
	ReadLine     func() (string, error)
	Route        string
	// Deprecated: retained for callers compiled against earlier onboarding APIs.
	// Validation is always required before files are committed.
	SkipValidator bool
}

// GuideQuestions is the no-signal fallback, one question at a time.
var GuideQuestions = []string{
	"What local URL should the eval call?",
	"Which HTTP method should the eval use?",
	"Where does the request text go in the body?",
	"Where is the reply text in the response?",
	"Is an auth header name required? Reply with the header name, or none. Do not send a secret value.",
}

var guideFields = []string{"endpoint_url", "method", "request_field", "response_path", "auth_header"}

// LocalRunCommand is the command printed after the fallback guide is complete.
func LocalRunCommand(agent string) string {
	return fmt.Sprintf("harness run eval_onboarding --agent %s --environment local", agent)
}

// Setup writes the skill and stages a draft from any agent signal.
func Setup(opts SetupOptions) (int, error) {
	stderr := opts.Stderr
	if stderr == nil {
		stderr = os.Stderr
	}
	if opts.SkipValidator {
		return 1, fmt.Errorf("SkipValidator is deprecated and cannot bypass pinned schema validation")
	}
	if _, err := PlaceSkill(opts.Repo, opts.SkillDir, opts.Stdout); err != nil {
		return 1, err
	}
	inf, err := Infer(opts.Repo)
	if err != nil {
		return 1, err
	}
	if strings.TrimSpace(opts.Route) != "" {
		if _, ok := selectAdapterRoute(inf.RouteCandidates, opts.Route); !ok {
			var candidates []string
			for _, candidate := range inf.RouteCandidates {
				candidates = append(candidates, candidate.Identity())
			}
			msg := fmt.Sprintf("route %q is not a supported AST-detected route and cannot be selected with --route; use guided/manual configuration instead", opts.Route)
			if len(candidates) > 0 {
				msg += ": supported candidates are " + strings.Join(candidates, ", ")
			}
			fmt.Fprintln(stderr, msg)
			return 2, &StatusError{Code: 2, Msg: msg}
		}
		inf, err = InferWithRoute(opts.Repo, opts.Route)
		if err != nil {
			return 1, err
		}
	}
	if ambiguity := inf.RouteAmbiguity(); ambiguity != nil {
		if strings.TrimSpace(opts.Route) == "" {
			fmt.Fprintln(stderr, ambiguity)
			return 2, &StatusError{Code: 2, Msg: ambiguity.Error()}
		}
	}
	if !inf.HasSignal {
		for _, note := range inf.Notes {
			fmt.Fprintln(stderr, note)
		}
		return setupGuide(opts, stderr)
	}
	for _, note := range inf.Notes {
		fmt.Fprintln(stderr, note)
	}
	// One location question at most. It does not block the draft.
	if q := FormatLowQuestion(inf); q != "" {
		fmt.Fprint(stderr, q)
	}
	files := ApplyFiles(inf, opts.AcceptMedium, opts.Answers)
	if len(files) == 0 {
		if medium := FormatMediumConfirmation(inf); medium != "" {
			msg := medium + "Pass --yes to accept these values before staging."
			fmt.Fprintln(stderr, msg)
			return 2, &StatusError{Code: 2, Msg: msg}
		}
		return setupGuide(opts, stderr)
	}
	if err := writeStaged(opts, files); err != nil {
		return 1, err
	}
	return 0, nil
}

func writeStaged(opts SetupOptions, files map[string]string) error {
	python := opts.Python
	if python == "" {
		layout, err := EnsureRunner(RunnerHome(), nil, opts.Run)
		if err != nil {
			return err
		}
		python = layout.Python
	}
	_, err := Stage(opts.Repo, files, python, opts.Run)
	return err
}

func setupGuide(opts SetupOptions, stderr io.Writer) (int, error) {
	answers := map[string]string{}
	for key, value := range opts.Answers {
		answers[key] = strings.TrimSpace(value)
	}
	interactive := opts.IsPty && opts.ReadLine != nil
	for i, field := range guideFields {
		if answers[field] != "" {
			continue
		}
		if !interactive {
			break
		}
		fmt.Fprintln(stderr, GuideQuestions[i])
		line, err := opts.ReadLine()
		if err != nil {
			return 1, err
		}
		answers[field] = strings.TrimSpace(line)
		if answers["endpoint_url"] == "" {
			continue
		}
		if err := stageGuideAnswers(opts, answers, guideComplete(answers)); err != nil {
			return 1, err
		}
	}
	if answers["endpoint_url"] == "" {
		fmt.Fprintln(stderr, GuideQuestions[0])
		return 2, &StatusError{Code: 2, Msg: "No agent was staged. " + GuideQuestions[0]}
	}
	complete := guideComplete(answers)
	if err := stageGuideAnswers(opts, answers, complete); err != nil {
		return 1, err
	}
	if !complete {
		for i, field := range guideFields {
			if answers[field] == "" {
				fmt.Fprintln(stderr, GuideQuestions[i])
				return 0, nil
			}
		}
	}
	fmt.Fprintln(stderr, LocalRunCommand(agentFromDir(opts.Repo)))
	return 0, nil
}

func guideComplete(answers map[string]string) bool {
	for _, field := range guideFields {
		if strings.TrimSpace(answers[field]) == "" {
			return false
		}
	}
	return true
}

func stageGuideAnswers(opts SetupOptions, answers map[string]string, complete bool) error {
	files := materialize(guideInference(opts.Repo, answers), false, nil)
	if len(files) == 0 {
		return fmt.Errorf("guide answers did not produce a tree")
	}
	if !complete {
		files[".onboard-guide-draft"] = "guided onboarding is incomplete\n"
	}
	return writeStaged(opts, files)
}

func guideInference(repo string, answers map[string]string) Inference {
	var ev []FieldEvidence
	port, path := splitEndpointURL(answers["endpoint_url"])
	if path == "" {
		path = "/"
	}
	if port == "" {
		port = "8080"
	}
	add(&ev, "endpoint_path", path, "guide", ConfidenceHigh)
	add(&ev, "port", port, "guide", ConfidenceHigh)
	method := strings.ToUpper(strings.TrimSpace(answers["method"]))
	if method == "" {
		method = "GET"
	}
	add(&ev, "method", method, "guide", ConfidenceHigh)
	requestField := strings.TrimSpace(answers["request_field"])
	if requestField == "" {
		requestField = "text"
	}
	add(&ev, "request_field", requestField, "guide", ConfidenceHigh)
	responsePath := strings.TrimSpace(answers["response_path"])
	if responsePath == "" {
		responsePath = "output.text"
	}
	add(&ev, "response_path", responsePath, "guide", ConfidenceHigh)
	gap := "expected value was not evidenced for " + responsePath
	add(&ev, "expected_gap", gap, "gap", ConfidenceHigh)
	header := strings.TrimSpace(answers["auth_header"])
	if header != "" && !strings.EqualFold(header, "none") && !strings.EqualFold(header, "no") {
		add(&ev, "auth_header", canonicalHeader(header), "guide", ConfidenceHigh)
	}
	agent := agentFromDir(repo)
	add(&ev, "agent_id", agent, "directory", ConfidenceHigh)
	return Inference{Language: "guide", AgentID: agent, Evidence: ev, HasSignal: true, Notes: []string{gap}}
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
		Route:        cmdctx.GetString(ctx.FlagValues, "route"),
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
