// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package aievals

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/harness/cli/v3/pkg/auth"
	"github.com/harness/cli/v3/pkg/hbase"
)

// RunnerLayout is the isolated environment created once for ai-evals-runner.
type RunnerLayout struct {
	Home    string
	Python  string
	AIEvals string
}

type handshake struct {
	Package      string `json:"package"`
	Version      string `json:"version"`
	Schema       string `json:"schema"`
	Command      string `json:"command"`
	SourceDigest string `json:"source_digest,omitempty"`
}

func wantedHandshake() handshake {
	return handshake{
		Package: RunnerPackage,
		Version: RunnerVersion,
		Schema:  SchemaVersion,
		Command: OnboardCommand,
	}
}

// RunnerHome is the pinned environment directory under the CLI home.
func RunnerHome() string {
	return filepath.Join(hbase.GetHarnessHomeDir(), "aievals-runner")
}

// Remediation is the exact text printed when Python or the pinned package is missing.
func Remediation(home string) string {
	python := filepath.Join(home, "bin", "python")
	return fmt.Sprintf(`ai-evals-runner %s is not installed in %s.
The runner is pinned to %s==%s (schema %s) and will not upgrade.
Install uv (https://docs.astral.sh/uv/) or pipx, then run exactly:
  uv venv %s
  uv pip install --python %s '%s==%s'
or:
  PIPX_HOME=%s PIPX_BIN_DIR=%s pipx install '%s==%s'
Do not install a second SDK. ai-evals-runner already depends on harness-evals.`,
		RunnerVersion, home, RunnerPackage, RunnerVersion, SchemaVersion,
		home, python, RunnerPackage, RunnerVersion,
		home, filepath.Join(home, "bin"), RunnerPackage, RunnerVersion)
}

// PinMismatchRemediation is printed when a different runner version is already present.
func PinMismatchRemediation(home, found string) string {
	return fmt.Sprintf("found %s %s in %s; will not upgrade. Reinstall only %s==%s into a clean directory.\n%s",
		RunnerPackage, found, home, RunnerPackage, RunnerVersion, Remediation(home))
}

// EnsureRunnerFromSource installs a persistent snapshot of a local runner
// checkout. It is intentionally separate from EnsureRunner so package-index
// bootstrap cannot silently resolve a developer path.
func EnsureRunnerFromSource(home, source string, look func(string) (string, error), run CommandFunc) (RunnerLayout, error) {
	layout, _, err := ensureRunnerFromSource(home, source, look, run)
	return layout, err
}

func ensureRunnerFromSource(home, source string, look func(string) (string, error), run CommandFunc) (RunnerLayout, bool, error) {
	defaultHome := filepath.Join(hbase.ExpandHomeDir(hbase.HarnessHome), "aievals-runner")
	if filepath.Clean(home) == filepath.Clean(defaultHome) {
		return RunnerLayout{}, false, fmt.Errorf("--runner-source requires an isolated HARNESS_CLI_HOME; refusing to modify %s", defaultHome)
	}
	manifest, err := validateRunnerSource(source)
	if err != nil {
		return RunnerLayout{}, false, err
	}
	if look == nil {
		look = exec.LookPath
	}
	if run == nil {
		run = defaultCommand
	}
	if layout, ok := installedLayout(home); ok {
		if err := verifySourceRunner(home, manifest); err != nil {
			return RunnerLayout{}, false, err
		}
		if err := requirePinned(home, layout, run); err != nil {
			return RunnerLayout{}, false, err
		}
		return layout, true, nil
	}
	if info, err := os.Lstat(home); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return RunnerLayout{}, false, fmt.Errorf("runner home %s is a symlink; remove it before using --runner-source", home)
		}
		if !info.IsDir() {
			return RunnerLayout{}, false, fmt.Errorf("runner home %s exists but is not a directory", home)
		}
		entries, readErr := os.ReadDir(home)
		if readErr != nil {
			return RunnerLayout{}, false, readErr
		}
		if len(entries) > 0 {
			return RunnerLayout{}, false, incompleteRunnerHomeError(home)
		}
	} else if !os.IsNotExist(err) {
		return RunnerLayout{}, false, err
	}
	if _, err := look("uv"); err != nil {
		return RunnerLayout{}, false, fmt.Errorf("source bootstrap requires uv on PATH; install uv, then retry --runner-source %s", source)
	}

	parent := filepath.Dir(home)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return RunnerLayout{}, false, err
	}
	staging, err := os.MkdirTemp(parent, ".aievals-runner-")
	if err != nil {
		return RunnerLayout{}, false, err
	}
	committed := false
	defer func() {
		if !committed {
			_ = os.RemoveAll(staging)
		}
	}()

	if err := runRunnerCommand(run, "uv", []string{"venv", staging}, nil, ""); err != nil {
		return RunnerLayout{}, false, err
	}
	snapshot := filepath.Join(staging, "source")
	if err := copyRunnerSource(source, snapshot, manifest); err != nil {
		return RunnerLayout{}, false, err
	}
	if err := runRunnerCommand(run, "uv", []string{"pip", "install", "--python", pythonBin(staging), snapshot}, nil, ""); err != nil {
		return RunnerLayout{}, false, err
	}
	layout, ok := installedLayout(staging)
	if !ok {
		return RunnerLayout{}, false, fmt.Errorf("%s", Remediation(home))
	}
	if err := requirePinned(home, layout, run); err != nil {
		return RunnerLayout{}, false, err
	}
	help, err := run(layout.AIEvals, []string{OnboardCommand, "--help"}, nil, "")
	if err != nil {
		return RunnerLayout{}, false, err
	}
	if help.Code != 0 {
		return RunnerLayout{}, false, fmt.Errorf("source %s %s does not provide %s\n%s", RunnerPackage, RunnerVersion, OnboardCommand, Remediation(home))
	}
	if err := writeSourceHandshake(staging, manifest.Fingerprint); err != nil {
		return RunnerLayout{}, false, err
	}
	if info, err := os.Lstat(home); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return RunnerLayout{}, false, fmt.Errorf("runner home %s became a symlink; refusing to replace it", home)
		}
		entries, readErr := os.ReadDir(home)
		if readErr != nil {
			return RunnerLayout{}, false, readErr
		}
		if len(entries) != 0 {
			return RunnerLayout{}, false, incompleteRunnerHomeError(home)
		}
		if err := os.Remove(home); err != nil {
			return RunnerLayout{}, false, err
		}
	} else if !os.IsNotExist(err) {
		return RunnerLayout{}, false, err
	}
	if err := os.Rename(staging, home); err != nil {
		return RunnerLayout{}, false, fmt.Errorf("runner bootstrap could not install atomically at %s: %w; remove the isolated home and retry --runner-source %s", home, err, source)
	}
	committed = true
	layout, ok = installedLayout(home)
	if !ok {
		return RunnerLayout{}, false, fmt.Errorf("%s", Remediation(home))
	}
	return layout, false, nil
}

// EnsureRunner installs the pinned package once. A different installed version
// is reported and left in place.
func EnsureRunner(home string, look func(string) (string, error), run CommandFunc) (RunnerLayout, error) {
	if look == nil {
		look = exec.LookPath
	}
	if run == nil {
		run = defaultCommand
	}
	if layout, ok := installedLayout(home); ok {
		if err := requirePinned(home, layout, run); err != nil {
			return RunnerLayout{}, err
		}
		if err := recordHandshake(home); err != nil {
			return RunnerLayout{}, err
		}
		return layout, nil
	}
	if _, err := look("uv"); err == nil {
		if _, err := run("uv", []string{"venv", home}, nil, ""); err != nil {
			return RunnerLayout{}, err
		}
		python := pythonBin(home)
		if _, err := run("uv", []string{"pip", "install", "--python", python, RunnerPackage + "==" + RunnerVersion}, nil, ""); err != nil {
			return RunnerLayout{}, err
		}
	} else if _, err := look("pipx"); err == nil {
		env := []string{"PIPX_HOME=" + home, "PIPX_BIN_DIR=" + filepath.Join(home, "bin")}
		if _, err := run("pipx", []string{"install", RunnerPackage + "==" + RunnerVersion}, env, ""); err != nil {
			return RunnerLayout{}, err
		}
	} else {
		msg := Remediation(home)
		if _, pyErr := look("python3"); pyErr != nil {
			if _, pyErr = look("python"); pyErr != nil {
				msg = "Python was not found on PATH.\n" + msg
			}
		}
		return RunnerLayout{}, fmt.Errorf("%s", msg)
	}
	layout, ok := installedLayout(home)
	if !ok {
		return RunnerLayout{}, fmt.Errorf("%s", Remediation(home))
	}
	if err := requirePinned(home, layout, run); err != nil {
		return RunnerLayout{}, err
	}
	if err := recordHandshake(home); err != nil {
		return RunnerLayout{}, err
	}
	return layout, nil
}

func recordHandshake(home string) error {
	recorded, err := readHandshake(home)
	if err == nil && compatibleHandshake(recorded, wantedHandshake()) {
		return nil
	}
	return writeHandshake(home)
}

// VerifyRunner checks the recorded handshake and the installed package version
// before a run. It does not install or upgrade.
func VerifyRunner(home string, run CommandFunc) (RunnerLayout, error) {
	if run == nil {
		run = defaultCommand
	}
	layout, ok := installedLayout(home)
	if !ok {
		return RunnerLayout{}, fmt.Errorf("%s", Remediation(home))
	}
	if err := verifyHandshake(home); err != nil {
		return RunnerLayout{}, err
	}
	if err := requirePinned(home, layout, run); err != nil {
		return RunnerLayout{}, err
	}
	help, err := run(layout.AIEvals, []string{OnboardCommand, "--help"}, nil, "")
	if err != nil {
		return RunnerLayout{}, err
	}
	if help.Code != 0 {
		return RunnerLayout{}, fmt.Errorf("pinned %s %s does not provide %s\n%s", RunnerPackage, RunnerVersion, OnboardCommand, Remediation(home))
	}
	return layout, nil
}

func verifyHandshake(home string) error {
	recorded, err := readHandshake(home)
	if err != nil {
		return fmt.Errorf("%s", Remediation(home))
	}
	if !compatibleHandshake(recorded, wantedHandshake()) {
		return fmt.Errorf("%s", PinMismatchRemediation(home, recorded.Version))
	}
	return nil
}

func verifySourceRunner(home string, manifest runnerSourceManifest) error {
	if err := verifyHandshake(home); err != nil {
		return err
	}
	recorded, err := readHandshake(home)
	if err != nil {
		return fmt.Errorf("%s", Remediation(home))
	}
	if recorded.SourceDigest != "" && recorded.SourceDigest != manifest.Fingerprint {
		return fmt.Errorf("%s", SourceMismatchRemediation(home))
	}
	actual, err := runnerSourceFingerprint(filepath.Join(home, "source"), manifest.Contents)
	if err != nil || actual != manifest.Fingerprint {
		return fmt.Errorf("%s", SourceMismatchRemediation(home))
	}
	if recorded.SourceDigest == "" {
		if err := writeSourceHandshake(home, manifest.Fingerprint); err != nil {
			return err
		}
	}
	return nil
}

func compatibleHandshake(got, want handshake) bool {
	return got.Package == want.Package &&
		got.Version == want.Version &&
		got.Schema == want.Schema &&
		got.Command == want.Command
}

// SourceMismatchRemediation explains how to replace an isolated source
// installation without allowing an in-place overwrite of a working runner.
func SourceMismatchRemediation(home string) string {
	return fmt.Sprintf("runner source in %s is not compatible with the requested source; refusing to replace it. Remove this isolated home and retry --runner-source.", home)
}

func incompleteRunnerHomeError(home string) error {
	return fmt.Errorf("runner home %s is incomplete; remove it before using --runner-source", home)
}

func requirePinned(home string, layout RunnerLayout, run CommandFunc) error {
	probed, err := run(layout.Python, []string{"-c", "import importlib.metadata as m; print(m.version('" + RunnerPackage + "'))"}, nil, "")
	if err != nil {
		return err
	}
	if probed.Code != 0 {
		return fmt.Errorf("%s", Remediation(home))
	}
	found := strings.TrimSpace(probed.Stdout)
	if found != RunnerVersion {
		return fmt.Errorf("%s", PinMismatchRemediation(home, found))
	}
	return nil
}

func runRunnerCommand(run CommandFunc, name string, args, env []string, dir string) error {
	result, err := run(name, args, env, dir)
	if err != nil {
		return err
	}
	if result.Code == 0 {
		return nil
	}
	msg := strings.TrimSpace(result.Stderr)
	if msg == "" {
		msg = strings.TrimSpace(result.Stdout)
	}
	if msg == "" {
		msg = fmt.Sprintf("%s exited with status %d", name, result.Code)
	}
	return fmt.Errorf("%s", msg)
}

type runnerSourceManifest struct {
	Contents    []byte
	Fingerprint string
}

func validateRunnerSource(source string) (runnerSourceManifest, error) {
	info, err := os.Stat(source)
	if err != nil {
		return runnerSourceManifest{}, fmt.Errorf("runner source %s is not available: %w", source, err)
	}
	if !info.IsDir() {
		return runnerSourceManifest{}, fmt.Errorf("runner source %s is not a directory", source)
	}
	manifestPath := filepath.Join(source, "pyproject.runner.toml")
	manifest, err := os.ReadFile(manifestPath)
	if err != nil {
		return runnerSourceManifest{}, fmt.Errorf("runner source is missing pyproject.runner.toml: %w", err)
	}
	name, ok := runnerPoetryField(string(manifest), "name")
	if !ok || name != RunnerPackage {
		return runnerSourceManifest{}, fmt.Errorf("runner source package %q is not supported; expected %s", name, RunnerPackage)
	}
	version, ok := runnerPoetryField(string(manifest), "version")
	if !ok || version != RunnerVersion {
		return runnerSourceManifest{}, fmt.Errorf("runner source version %s is not supported; expected %s", version, RunnerVersion)
	}
	for _, rel := range []string{
		"service/ai_evals/onboarding/contract.py",
		"service/ai_evals/onboarding/load.py",
		"service/ai_evals/onboarding/runner.py",
	} {
		path := filepath.Join(source, rel)
		entry, err := os.Stat(path)
		if err != nil || !entry.Mode().IsRegular() {
			return runnerSourceManifest{}, fmt.Errorf("runner source is missing required file %s", rel)
		}
	}
	contract, err := os.ReadFile(filepath.Join(source, "service", "ai_evals", "onboarding", "contract.py"))
	if err != nil {
		return runnerSourceManifest{}, err
	}
	expectedSchema := `SCHEMA_VERSION_V1 = "` + SchemaVersionV1 + `"`
	if !strings.Contains(string(contract), expectedSchema) {
		schema := "unknown"
		if marker := strings.Index(string(contract), "SCHEMA_VERSION_V1"); marker >= 0 {
			line := strings.SplitN(string(contract)[marker:], "\n", 2)[0]
			if parts := strings.SplitN(line, "=", 2); len(parts) == 2 {
				schema = strings.Trim(strings.TrimSpace(parts[1]), `"`)
			}
		}
		return runnerSourceManifest{}, fmt.Errorf("runner source schema %s is not supported; expected %s", schema, SchemaVersionV1)
	}
	if err := rejectRunnerSourceSymlinks(source); err != nil {
		return runnerSourceManifest{}, err
	}
	fingerprint, err := runnerSourceFingerprint(source, manifest)
	if err != nil {
		return runnerSourceManifest{}, err
	}
	return runnerSourceManifest{Contents: manifest, Fingerprint: fingerprint}, nil
}

func runnerPoetryField(manifest, field string) (string, bool) {
	inPoetry := false
	for _, line := range strings.Split(manifest, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") {
			inPoetry = trimmed == "[tool.poetry]"
			continue
		}
		if !inPoetry || !strings.HasPrefix(trimmed, field) {
			continue
		}
		rest := strings.TrimSpace(strings.TrimPrefix(trimmed, field))
		if !strings.HasPrefix(rest, "=") {
			continue
		}
		value := strings.TrimSpace(strings.TrimPrefix(rest, "="))
		if len(value) >= 2 && value[0] == '"' && value[len(value)-1] == '"' {
			return value[1 : len(value)-1], true
		}
	}
	return "", false
}

func rejectRunnerSourceSymlinks(source string) error {
	return filepath.WalkDir(source, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == source {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("runner source contains a symlink: %s", filepath.ToSlash(path))
		}
		if entry.IsDir() && runnerSourceExcluded(filepath.Base(path)) {
			return filepath.SkipDir
		}
		return nil
	})
}

func copyRunnerSource(source, destination string, manifest runnerSourceManifest) error {
	err := filepath.WalkDir(source, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return os.MkdirAll(destination, 0o755)
		}
		if runnerSourceExcluded(rel) {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if rel == "pyproject.toml" {
			return nil
		}
		target := filepath.Join(destination, rel)
		if entry.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("runner source contains a symlink: %s", filepath.ToSlash(path))
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("runner source contains unsupported file: %s", filepath.ToSlash(path))
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return writeAtomic(target, data)
	})
	if err != nil {
		return err
	}
	// The runner project uses a separate Poetry manifest so the API project's
	// pyproject.toml cannot accidentally be installed in the source snapshot.
	return writeAtomic(filepath.Join(destination, "pyproject.toml"), manifest.Contents)
}

func runnerSourceFingerprint(root string, manifest []byte) (string, error) {
	digest := sha256.New()
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		if runnerSourceExcluded(rel) {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if rel == "pyproject.toml" {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("runner source contains a symlink: %s", filepath.ToSlash(path))
		}
		if entry.IsDir() {
			return nil
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("runner source contains unsupported file: %s", filepath.ToSlash(path))
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if _, err := digest.Write([]byte(filepath.ToSlash(rel) + "\x00")); err != nil {
			return err
		}
		if _, err := digest.Write(data); err != nil {
			return err
		}
		_, err = digest.Write([]byte{0})
		return err
	})
	if err != nil {
		return "", err
	}
	if _, err := digest.Write([]byte("pyproject.toml\x00")); err != nil {
		return "", err
	}
	if _, err := digest.Write(manifest); err != nil {
		return "", err
	}
	if _, err := digest.Write([]byte{0}); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", digest.Sum(nil)), nil
}

func runnerSourceExcluded(path string) bool {
	base := filepath.Base(path)
	switch base {
	case ".git", ".venv", "__pycache__", ".pytest_cache", ".mypy_cache", ".ruff_cache", "dist", "build", "node_modules":
		return true
	}
	return false
}

func installedLayout(home string) (RunnerLayout, bool) {
	candidates := []RunnerLayout{
		{Home: home, Python: pythonBin(home), AIEvals: bin(home, "ai-evals")},
		{Home: home, Python: filepath.Join(home, "venvs", RunnerPackage, "bin", pythonName()), AIEvals: filepath.Join(home, "venvs", RunnerPackage, "bin", "ai-evals")},
	}
	for _, layout := range candidates {
		if fileExists(layout.Python) && fileExists(layout.AIEvals) {
			return layout, true
		}
	}
	return RunnerLayout{}, false
}

func pythonBin(home string) string {
	return filepath.Join(home, "bin", pythonName())
}

func pythonName() string {
	if runtime.GOOS == "windows" {
		return "python.exe"
	}
	return "python"
}

func bin(home, name string) string {
	if runtime.GOOS == "windows" {
		return filepath.Join(home, "bin", name+".exe")
	}
	return filepath.Join(home, "bin", name)
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func handshakePath(home string) string {
	return filepath.Join(home, "handshake.json")
}

func writeHandshake(home string) error {
	return writeHandshakeValue(home, wantedHandshake())
}

func writeSourceHandshake(home, sourceFingerprint string) error {
	recorded := wantedHandshake()
	recorded.SourceDigest = sourceFingerprint
	return writeHandshakeValue(home, recorded)
}

func writeHandshakeValue(home string, recorded handshake) error {
	data, err := json.Marshal(recorded)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return writeAtomic(handshakePath(home), data)
}

func readHandshake(home string) (handshake, error) {
	data, err := os.ReadFile(handshakePath(home))
	if err != nil {
		return handshake{}, err
	}
	var recorded handshake
	if err := json.Unmarshal(data, &recorded); err != nil {
		return handshake{}, err
	}
	return recorded, nil
}

// SSORunnerNotice is printed when the active login cannot be forwarded to the
// Python runner. The D2 runner sends HARNESS_API_KEY as x-api-key and accepts
// PAT, SAT, and pipeline st.* tokens.
const SSORunnerNotice = "The Python runner cannot use this SSO session. onboard-run accepts a PAT or SAT as HARNESS_API_KEY (x-api-key), or a pipeline HARNESS_TOKEN. The SSO access token was not sent to the runner."

// RunnerProcessEnv returns environment assignments for the runner child.
// PAT and SAT are forwarded. An SSO token is omitted.
func RunnerProcessEnv(a *auth.ResolvedAuth) (env []string, notice string) {
	if a == nil {
		return nil, ""
	}
	if a.AuthType == auth.AuthTypeSSO || a.TokenKind == auth.TokenKindJWT {
		return nil, SSORunnerNotice
	}
	token := a.PATToken
	if token == "" {
		return nil, ""
	}
	kind := auth.TokenType(token)
	if kind != auth.TokenKindPAT && kind != auth.TokenKindSAT {
		return nil, SSORunnerNotice
	}
	return []string{
		"HARNESS_API_KEY=" + token,
		"HARNESS_ACCOUNT_ID=" + a.AccountID,
		"HARNESS_ORG_ID=" + a.OrgID,
		"HARNESS_PROJECT_ID=" + a.ProjectID,
		"HARNESS_BASE_URL=" + strings.TrimRight(a.APIUrl, "/"),
	}, ""
}

func defaultCommand(name string, args []string, env []string, dir string) (CommandResult, error) {
	cmd := exec.Command(name, args...)
	if dir != "" {
		cmd.Dir = dir
	}
	if len(env) > 0 {
		cmd.Env = append(os.Environ(), env...)
	}
	out, err := cmd.Output()
	result := CommandResult{Stdout: string(out)}
	if err == nil {
		return result, nil
	}
	if exit, ok := err.(*exec.ExitError); ok {
		result.Code = exit.ExitCode()
		result.Stderr = string(exit.Stderr)
		return result, nil
	}
	return result, err
}
