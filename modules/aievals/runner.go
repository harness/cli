// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package aievals

import (
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
	if err == nil && recorded == wantedHandshake() {
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
	recorded, err := readHandshake(home)
	if err != nil {
		return RunnerLayout{}, fmt.Errorf("%s", Remediation(home))
	}
	want := wantedHandshake()
	if recorded != want {
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
