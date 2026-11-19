// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package aievals

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/harness/cli/v3/pkg/auth"
	"github.com/harness/cli/v3/pkg/cmdctx"
	"github.com/harness/cli/v3/pkg/format"
	"github.com/harness/cli/v3/pkg/hbase"
	"github.com/harness/cli/v3/pkg/spec"
)

const (
	setupWorkflowID   = "setup"
	scanWorkflowID    = "scan"
	metricsWorkflowID = "aievals_onboarding_metrics"
	stageWorkflowID   = "stage"
	runWorkflowID     = "run"
	uploadWorkflowID  = "upload"
)

// SetupOptions controls skill placement. Setup does not scan or write eval files.
type SetupOptions struct {
	Repo     string
	SkillDir string
	Stdout   io.Writer
	Stderr   io.Writer
	// Deprecated: retained so older callers cannot bypass validation by accident.
	// Setup no longer stages files, and this flag still fails closed.
	SkipValidator bool
}

// Setup installs or prints the skill. It does not scan and does not write eval files.
func Setup(opts SetupOptions) (int, error) {
	if opts.SkipValidator {
		return 1, fmt.Errorf("SkipValidator is deprecated and cannot bypass pinned schema validation")
	}
	if _, err := PlaceSkill(opts.Repo, opts.SkillDir, opts.Stdout); err != nil {
		return 1, err
	}
	return 0, nil
}

// RunAgent verifies the pinned runner and invokes onboard-run.
// RunnerV1Remediation is the exact failure when the pinned runner cannot
// execute an onboarding/v1 tree. The tree is left unchanged.
func RunnerV1Remediation() string {
	return fmt.Sprintf("ai-evals-runner %s (schema %s) does not support onboarding/v1. The staged tree was not modified. Query parameters and auth headers were not removed. Install a runner that reports schema %s. Do not edit the candidate to satisfy this runner.", RunnerVersion, SchemaVersion, SchemaVersionV1)
}

func treeRequiresV1Runner(root string) (bool, error) {
	matches, err := filepath.Glob(filepath.Join(root, "agents", "*", "agent.yaml"))
	if err != nil {
		return false, err
	}
	for _, path := range matches {
		data, err := os.ReadFile(path)
		if err != nil {
			return false, err
		}
		var doc map[string]any
		if err := yaml.Unmarshal(data, &doc); err != nil {
			return false, err
		}
		if doc["schema_version"] == SchemaVersionV1 {
			return true, nil
		}
	}
	return false, nil
}

func RunAgent(home, root, agent, environment string, resolved *auth.ResolvedAuth, stderr io.Writer, run CommandFunc) (int, error) {
	return RunAgentContext(context.Background(), home, root, agent, environment, resolved, stderr, run)
}

func RunAgentContext(ctx context.Context, home, root, agent, environment string, resolved *auth.ResolvedAuth, stderr io.Writer, run CommandFunc) (int, error) {
	if run == nil {
		run = runnerCommand(ctx)
	}
	v1Tree, err := treeRequiresV1Runner(root)
	if err != nil {
		return 1, err
	}
	if v1Tree && SchemaVersion != SchemaVersionV1 {
		return 2, &StatusError{Code: 2, Msg: RunnerV1Remediation()}
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
	if ctx.Id != "" {
		return fmt.Errorf("the AI Evals skill does not accept an ID; use --repo or --skill-dir")
	}
	repo := cmdctx.GetString(ctx.FlagValues, "repo")
	if repo == "" {
		wd, err := os.Getwd()
		if err != nil {
			return err
		}
		repo = wd
	}
	code, err := Setup(SetupOptions{
		Repo:     repo,
		SkillDir: cmdctx.GetString(ctx.FlagValues, "skill-dir"),
		Stdout:   os.Stdout,
		Stderr:   os.Stderr,
	})
	return finish(code, err)
}

//go:embed metric_catalog.py
var metricCatalogSnippet string

func metricCatalog(home string, run CommandFunc) ([]json.RawMessage, error) {
	layout, err := verifyRunner(home, run, false)
	if err != nil {
		return nil, err
	}
	return loadMetricCatalog(layout.Python, run)
}

func loadMetricCatalog(python string, run CommandFunc) ([]json.RawMessage, error) {
	result, err := run(python, []string{"-I", "-c", metricCatalogSnippet}, nil, "")
	if err != nil {
		return nil, err
	}
	if result.Code != 0 {
		return nil, fmt.Errorf("could not read the pinned runner's metric catalog (exit %d)", result.Code)
	}
	var metrics []json.RawMessage
	if err := json.Unmarshal([]byte(result.Stdout), &metrics); err != nil || len(metrics) == 0 {
		return nil, fmt.Errorf("the pinned runner did not return a valid, non-empty metric catalog")
	}
	return metrics, nil
}

func resolveMetricDefinitions(python string, run CommandFunc) (map[string]MetricDefinition, error) {
	metrics, err := loadMetricCatalog(python, run)
	if err != nil {
		return nil, err
	}
	definitions := make(map[string]MetricDefinition, len(metrics))
	for _, raw := range metrics {
		var entry struct {
			Kind string `json:"kind"`
			MetricDefinition
		}
		if err := json.Unmarshal(raw, &entry); err != nil || entry.Kind == "" || entry.Type == "" {
			return nil, fmt.Errorf("the pinned runner returned an invalid metric catalog entry")
		}
		definitions[entry.Kind] = entry.MetricDefinition
	}
	return definitions, nil
}

func scanWorkflow(ctx *cmdctx.Ctx) error {
	repo, err := workflowRepo(ctx)
	if err != nil {
		return err
	}
	report, code, err := Scan(repo, cmdctx.GetString(ctx.FlagValues, "route"))
	if err != nil {
		return finish(code, err)
	}
	return printOnboardingJSON(report)
}

func metricsWorkflow(ctx *cmdctx.Ctx) error {
	if ctx.ParentId != "" {
		return fmt.Errorf("the onboarding metric catalog does not accept a parent ID")
	}
	report, err := metricCatalog(RunnerHome(), runnerCommand(ctx.Context))
	if err != nil {
		return err
	}
	return renderMetricCatalog(ctx, report)
}

func renderMetricCatalog(ctx *cmdctx.Ctx, report []json.RawMessage) error {
	rows := make([]any, len(report))
	for i, raw := range report {
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		var row map[string]any
		if err := decoder.Decode(&row); err != nil || row == nil {
			return fmt.Errorf("the pinned runner returned an invalid metric catalog entry")
		}
		rows[i] = row
	}
	flags := ctx.FormatFlags
	if flags.Format == "" && flags.Columns == "" {
		flags.Format = "json" // Keep the default machine-readable for the onboarding skill.
	}
	table := &spec.TableSpec{Columns: []spec.TableColumn{
		{Header: "KIND", Expr: "it.kind"},
		{Header: "TYPE", Expr: "it.type"},
		{Header: "DIMENSION", Expr: "it.dimension"},
		{Header: "SUPPORTED", Expr: "it.supported"},
	}}
	return format.FormatArrayOutput(flags, ctx.IsPty, rows, "it", table, nil, nil, nil)
}

func printOnboardingJSON(value any) error {
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}

func runnerCommand(ctx context.Context) CommandFunc {
	return func(name string, args []string, env []string, dir string) (CommandResult, error) {
		return defaultCommandContext(ctx, name, args, env, dir)
	}
}

func stageWorkflow(ctx *cmdctx.Ctx) error {
	repo, err := workflowRepo(ctx)
	if err != nil {
		return err
	}
	var candidate Candidate
	if file := cmdctx.GetString(ctx.FlagValues, "file"); file != "" {
		candidate, err = LoadCandidateFile(file)
		if err != nil {
			return err
		}
	}
	override := Candidate{
		AgentID:       cmdctx.GetString(ctx.FlagValues, "agent"),
		EndpointURL:   cmdctx.GetString(ctx.FlagValues, "endpoint"),
		Method:        cmdctx.GetString(ctx.FlagValues, "method"),
		RequestField:  cmdctx.GetString(ctx.FlagValues, "request-field"),
		RequestFields: cmdctx.GetString(ctx.FlagValues, "request-fields"),
		ResponsePath:  cmdctx.GetString(ctx.FlagValues, "response-path"),
		AuthHeader:    cmdctx.GetString(ctx.FlagValues, "auth-header"),
		AuthEnv:       cmdctx.GetString(ctx.FlagValues, "auth-env"),
		InputText:     cmdctx.GetString(ctx.FlagValues, "input-text"),
		Expected:      cmdctx.GetString(ctx.FlagValues, "expected"),
	}
	if candidate.Wire != nil && legacyOverrideSet(override) {
		return fmt.Errorf("onboarding/v1 candidate files are staged as written. Flags such as --endpoint are the legacy mapping and cannot be combined with schema_version %s. Edit the document, then pass --confirm", SchemaVersionV1)
	}
	if candidate.Wire == nil {
		candidate = candidate.WithOverrides(override)
	}
	code, err := WriteCandidate(WriteOptions{
		Repo:      repo,
		Candidate: candidate,
		Confirm:   cmdctx.GetBool(ctx.FlagValues, "confirm"),
		Run:       runnerCommand(ctx.Context),
		VerifyRunner: func(_ string, run CommandFunc) (RunnerLayout, error) {
			return VerifyRunner(RunnerHome(), run)
		},
		ResolveMetrics: resolveMetricDefinitions,
		Stdout:         os.Stdout,
		Stderr:         os.Stderr,
	})
	return finish(code, err)
}

func workflowRepo(ctx *cmdctx.Ctx) (string, error) {
	repo := cmdctx.GetString(ctx.FlagValues, "repo")
	if repo != "" {
		return repo, nil
	}
	return os.Getwd()
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
	code, err := RunAgentContext(ctx.Context, RunnerHome(), root, ctx.Id, cmdctx.GetString(ctx.FlagValues, "environment"), ctx.ScopedAuth(), os.Stderr, nil)
	return finish(code, err)
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
