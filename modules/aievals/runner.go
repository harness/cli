// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package aievals

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	"github.com/harness/cli/v3/pkg/hbase"
)

type handshake struct {
	Package string `json:"package"`
	Version string `json:"version"`
	Schema  string `json:"schema"`
	Command string `json:"command"`
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
	python := filepath.Join(home, runnerBinName(), pythonName())
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
		home, filepath.Join(home, runnerBinName()), RunnerPackage, RunnerVersion)
}

// PinMismatchRemediation is printed when a different runner version is already present.
func PinMismatchRemediation(home, found string) string {
	return fmt.Sprintf("found %s %s in %s; will not upgrade. Reinstall only %s==%s into a clean directory.\n%s",
		RunnerPackage, found, home, RunnerPackage, RunnerVersion, Remediation(home))
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
		env := []string{"PIPX_HOME=" + home, "PIPX_BIN_DIR=" + filepath.Join(home, runnerBinName())}
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
	if err == nil && recorded == wantedHandshake() {
		return nil
	}
	return writeHandshake(home)
}

// VerifyRunner checks the recorded handshake and the installed package version
// before a run. It does not install or upgrade.
func VerifyRunner(home string, run CommandFunc) (RunnerLayout, error) {
	return verifyRunner(home, run, true)
}

func verifyRunner(home string, run CommandFunc, persistHandshake bool) (RunnerLayout, error) {
	if run == nil {
		run = defaultCommand
	}
	layout, ok := installedLayout(home)
	if !ok {
		return RunnerLayout{}, fmt.Errorf("%s", Remediation(home))
	}
	recorded, err := readHandshake(home)
	missing := os.IsNotExist(err)
	if err != nil && !missing {
		return RunnerLayout{}, fmt.Errorf("%s", Remediation(home))
	}
	if !missing && recorded != wantedHandshake() {
		return RunnerLayout{}, fmt.Errorf("%s", PinMismatchRemediation(home, recorded.Version))
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
	// A manually installed runner has no handshake until both probes succeed.
	if missing && persistHandshake {
		if err := writeHandshake(home); err != nil {
			return RunnerLayout{}, err
		}
	}
	return layout, nil
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

func installedLayout(home string) (RunnerLayout, bool) {
	candidates := []RunnerLayout{
		{Home: home, Python: pythonBin(home), AIEvals: bin(home, "ai-evals")},
		{Home: home, Python: filepath.Join(home, "venvs", RunnerPackage, runnerBinName(), pythonName()), AIEvals: filepath.Join(home, "venvs", RunnerPackage, runnerBinName(), "ai-evals"+exeSuffix())},
	}
	for _, layout := range candidates {
		if fileExists(layout.Python) && fileExists(layout.AIEvals) {
			return layout, true
		}
	}
	return RunnerLayout{}, false
}

func pythonBin(home string) string {
	return filepath.Join(home, runnerBinName(), pythonName())
}

func runnerBinName() string {
	if runtime.GOOS == "windows" {
		return "Scripts"
	}
	return "bin"
}

func pythonName() string {
	if runtime.GOOS == "windows" {
		return "python.exe"
	}
	return "python"
}

func exeSuffix() string {
	if runtime.GOOS == "windows" {
		return ".exe"
	}
	return ""
}

func bin(home, name string) string {
	return filepath.Join(home, runnerBinName(), name+exeSuffix())
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func handshakePath(home string) string {
	return filepath.Join(home, "handshake.json")
}

func writeHandshake(home string) error {
	data, err := json.Marshal(wantedHandshake())
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

// Keep the 0.7.13 pin usable without trusting older python-dotenv versions
// that silently ignore PYTHON_DOTENV_DISABLED. Test it without reading a file.
const onboardBootstrap = `from io import StringIO
from dotenv import load_dotenv
if load_dotenv(stream=StringIO('AIEVALS_DOTENV_PROBE=1'), override=True):
    raise SystemExit('The pinned runner does not honor PYTHON_DOTENV_DISABLED; use the approved runner environment.')
from ai_evals.runner.cli import app
app()`

var runnerEnvName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func selectedRunnerEnv(names []string) ([]string, error) {
	var env []string
	for _, name := range names {
		if !runnerEnvName.MatchString(name) {
			return nil, fmt.Errorf("--env requires an exported variable name, not a value or assignment")
		}
		upper := strings.ToUpper(name)
		if strings.HasPrefix(upper, "HARNESS_") || strings.HasPrefix(upper, "PYTHON") ||
			strings.HasPrefix(upper, "LD_") || strings.HasPrefix(upper, "DYLD_") {
			return nil, fmt.Errorf("--env cannot pass Harness credentials or process configuration")
		}
		if value, ok := os.LookupEnv(name); ok {
			env = append(env, name+"="+value)
		} else {
			return nil, fmt.Errorf("--env variable %s is not exported", name)
		}
	}
	return env, nil
}

func defaultCommand(name string, args []string, env []string, dir string) (CommandResult, error) {
	return defaultCommandContext(context.Background(), name, args, env, dir)
}

func defaultCommandContext(ctx context.Context, name string, args []string, env []string, dir string) (CommandResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	cmd := exec.CommandContext(ctx, name, args...)
	if dir != "" {
		cmd.Dir = dir
	}
	cmd.Env = sanitizedRunnerEnv(env)
	out, err := cmd.Output()
	result := CommandResult{Stdout: string(out)}
	if err == nil {
		return result, nil
	}
	if cause := context.Cause(ctx); cause != nil {
		return result, cause
	}
	if exit, ok := err.(*exec.ExitError); ok {
		result.Code = exit.ExitCode()
		result.Stderr = string(exit.Stderr)
		return result, nil
	}
	return result, err
}

func sanitizedRunnerEnv(extra []string) []string {
	// The pinned runner binaries use absolute paths, so they don't need the
	// caller's PATH, HOME, or provider environment to start.
	env := []string{"PYTHON_DOTENV_DISABLED=1"}
	for _, name := range []string{"SYSTEMROOT", "WINDIR", "TEMP", "TMP", "TMPDIR", "LANG", "LC_ALL", "LC_CTYPE"} {
		if value, ok := os.LookupEnv(name); ok {
			env = append(env, name+"="+value)
		}
	}
	return append(env, extra...)
}
