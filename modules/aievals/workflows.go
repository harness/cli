// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package aievals

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/harness/cli/v3/pkg/auth"
	"github.com/harness/cli/v3/pkg/cmdctx"
	"github.com/harness/cli/v3/pkg/hbase"
	"go.yaml.in/yaml/v3"
)

const (
	setupWorkflowID  = "setup"
	scanWorkflowID   = "scan"
	stageWorkflowID  = "stage"
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

// SetupOptions controls skill placement. Setup does not scan or write eval files.
type SetupOptions struct {
	Repo         string
	SkillDir     string
	RunnerSource string
	Stdout       io.Writer
	Stderr       io.Writer
	// Deprecated: retained so older callers cannot bypass validation by accident.
	// Setup no longer stages files, and this flag still fails closed.
	SkipValidator bool
}

// Setup installs or prints the skill. It does not scan and does not write eval files.
func Setup(opts SetupOptions) (int, error) {
	if opts.SkipValidator {
		return 1, fmt.Errorf("SkipValidator is deprecated and cannot bypass pinned schema validation")
	}
	if opts.RunnerSource != "" {
		cliHome := hbase.GetHarnessHomeDir()
		if os.Getenv(hbase.EnvCLIHome) == "" || filepath.Clean(cliHome) == filepath.Clean(hbase.ExpandHomeDir(hbase.HarnessHome)) {
			return 1, fmt.Errorf("--runner-source requires %s to be set to an isolated developer home; it will not use ~/.harness", hbase.EnvCLIHome)
		}
		_, alreadyCompatible, err := ensureRunnerFromSource(RunnerHome(), opts.RunnerSource, nil, nil)
		if err != nil {
			return 1, err
		}
		if alreadyCompatible && opts.Stdout != nil {
			fmt.Fprintf(opts.Stdout, "ai-evals-runner %s (schema %s) is already compatible; reusing the isolated source installation.\n", RunnerVersion, SchemaVersion)
		}
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

// ValidateReportOutputPath keeps runner-produced result data out of the
// repository tree, where it could be staged, archived, or committed with
// source changes. D2 receives the original path string after this check.
func ValidateReportOutputPath(root, reportPath string) error {
	if reportPath == "" {
		return nil
	}
	if strings.TrimSpace(reportPath) != reportPath ||
		strings.ContainsRune(reportPath, '\x00') ||
		!filepath.IsAbs(reportPath) ||
		filepath.Clean(reportPath) != reportPath {
		return fmt.Errorf("--report-json must be a clean absolute path outside the repository source tree")
	}

	parent := filepath.Dir(reportPath)
	parentInfo, err := os.Stat(parent)
	if err != nil || !parentInfo.IsDir() {
		return fmt.Errorf("--report-json parent directory must already exist")
	}
	if info, err := os.Lstat(reportPath); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return fmt.Errorf("--report-json must name a regular file or a new file")
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("--report-json destination cannot be inspected safely")
	}

	repo := filepath.Dir(filepath.Dir(root))
	repo, err = filepath.Abs(repo)
	if err != nil {
		return fmt.Errorf("--report-json destination cannot be inspected safely")
	}
	repo, err = filepath.EvalSymlinks(repo)
	if err != nil {
		return fmt.Errorf("--report-json destination cannot be inspected safely")
	}
	resolvedParent, err := filepath.EvalSymlinks(parent)
	if err != nil || pathWithin(resolvedParent, repo) {
		return fmt.Errorf("--report-json must be outside the repository source tree")
	}
	return nil
}

func pathWithin(path, root string) bool {
	relative, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return relative == "." || (relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)))
}

func RunAgent(home, root, agent, environment string, resolved *auth.ResolvedAuth, stderr io.Writer, run CommandFunc) (int, error) {
	return RunAgentWithReport(home, root, agent, environment, "", resolved, stderr, nil, run)
}

// RunAgentWithReport verifies the runner and optionally asks D2 to emit one
// local report. The report path is announced only after the runner succeeds.
func RunAgentWithReport(home, root, agent, environment, reportPath string, resolved *auth.ResolvedAuth, stderr, stdout io.Writer, run CommandFunc) (int, error) {
	if run == nil {
		run = defaultCommand
	}
	if reportPath != "" {
		if environment != "" && environment != "local" {
			return 1, fmt.Errorf("--report-json is supported only with --environment local")
		}
		if err := ValidateReportOutputPath(root, reportPath); err != nil {
			return 1, err
		}
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
	args := OnboardArgsWithReport(layout.AIEvals, root, agent, environment, reportPath)
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
	if reportPath != "" {
		if info, err := os.Stat(reportPath); err != nil || !info.Mode().IsRegular() {
			return 1, fmt.Errorf("runner exited successfully but did not produce the requested report")
		}
		if stdout != nil {
			fmt.Fprintf(stdout, "Report %s\n", reportPath)
		}
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
		RunnerSource: cmdctx.GetString(ctx.FlagValues, "runner-source"),
		Stdout:       os.Stdout,
		Stderr:       os.Stderr,
	})
	return finish(code, err)
}

func scanWorkflow(ctx *cmdctx.Ctx) error {
	metrics := cmdctx.GetBool(ctx.FlagValues, "metrics")
	if !metrics && (cmdctx.GetString(ctx.FlagValues, "candidate") != "" ||
		cmdctx.GetString(ctx.FlagValues, "goals") != "" ||
		cmdctx.GetBool(ctx.FlagValues, "allow-llm") ||
		cmdctx.GetBool(ctx.FlagValues, "allow-embedding")) {
		return fmt.Errorf("--candidate, --goals, --allow-llm, and --allow-embedding require --metrics")
	}
	if metrics {
		if cmdctx.GetString(ctx.FlagValues, "route") != "" {
			return fmt.Errorf("--metrics cannot be combined with --route")
		}
		report, err := MetricCatalog(RunnerHome(), nil)
		if err != nil {
			return err
		}
		if candidatePath := cmdctx.GetString(ctx.FlagValues, "candidate"); candidatePath != "" {
			candidate, err := LoadCandidateFile(candidatePath)
			if err != nil {
				return err
			}
			goals := strings.Split(cmdctx.GetString(ctx.FlagValues, "goals"), ",")
			for i := range goals {
				goals[i] = strings.TrimSpace(goals[i])
			}
			encoder := json.NewEncoder(os.Stdout)
			encoder.SetIndent("", "  ")
			return encoder.Encode(AdviseMetrics(candidate, report.Metrics, MetricAdviceOptions{
				AllowLLM:       cmdctx.GetBool(ctx.FlagValues, "allow-llm"),
				AllowEmbedding: cmdctx.GetBool(ctx.FlagValues, "allow-embedding"),
				Goals:          goals,
			}))
		}
		encoder := json.NewEncoder(os.Stdout)
		encoder.SetIndent("", "  ")
		return encoder.Encode(report)
	}
	repo, err := workflowRepo(ctx)
	if err != nil {
		return err
	}
	report, code, err := Scan(repo, cmdctx.GetString(ctx.FlagValues, "route"))
	if err != nil {
		return finish(code, err)
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(report); err != nil {
		return err
	}
	return nil
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
		return fmt.Errorf("onboarding/v1 candidate files are staged as written. Flags such as --endpoint are the legacy mapping and cannot be combined with schema_version %s. Edit the document, then pass --confirm.", SchemaVersionV1)
	}
	if candidate.Wire == nil {
		candidate = candidate.WithOverrides(override)
	}
	code, err := WriteCandidate(WriteOptions{
		Repo:      repo,
		Candidate: candidate,
		Confirm:   cmdctx.GetBool(ctx.FlagValues, "confirm"),
		Stdout:    os.Stdout,
		Stderr:    os.Stderr,
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
	code, err := RunAgentWithReport(
		RunnerHome(),
		root,
		cmdctx.GetString(ctx.FlagValues, "agent"),
		cmdctx.GetString(ctx.FlagValues, "environment"),
		cmdctx.GetString(ctx.FlagValues, "report-json"),
		ctx.ScopedAuth(),
		os.Stderr,
		os.Stdout,
		nil,
	)
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
	if err := ValidateUploadTree(root); err != nil {
		return err
	}
	persistPath := cmdctx.GetString(ctx.FlagValues, "persist-report")
	var report map[string]any
	var err error
	if persistPath != "" {
		report, err = ReadPersistReport(persistPath)
		if err != nil {
			return err
		}
	}
	payload, err := BuildArchive(repo, DefaultLimits())
	if err != nil {
		return err
	}
	apiAuth := ctx.ScopedAuth()
	apiURL := cmdctx.GetString(ctx.FlagValues, "api-url")
	apiBasePath := cmdctx.GetString(ctx.FlagValues, "api-base-path")
	if apiURL != "" || apiBasePath != "" {
		scoped := *apiAuth
		if apiURL != "" {
			scoped.APIUrl = apiURL
		}
		apiAuth = &scoped
	}
	imported, err := UploadArchiveWithBasePath(apiAuth, payload, apiBasePath, nil)
	if err != nil {
		return err
	}
	fmt.Fprint(os.Stdout, imported.Text)
	if persistPath == "" {
		fmt.Fprint(os.Stdout, formatManifestMappings(imported.Response.Manifest))
		return nil
	}
	prepared, err := PrepareReportImport(
		payload,
		imported.Response,
		cmdctx.GetString(ctx.FlagValues, "eval-identifier"),
		report,
	)
	if err != nil {
		return err
	}
	fmt.Fprint(os.Stdout, formatManifestMappings([]importManifest{{
		Eval:           importedResource{Identifier: prepared.EvalIdentifier, ID: prepared.EvalID},
		MetricBindings: prepared.Mapping,
	}}))
	text, err := PersistReport(apiAuth, prepared.EvalID, prepared.Body, apiBasePath, nil)
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
