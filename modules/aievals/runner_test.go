// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package aievals

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/harness/cli/v3/pkg/auth"
	"github.com/harness/cli/v3/pkg/cmdctx"
	"github.com/harness/cli/v3/pkg/hbase"
)

func TestVerifyRunnerRejectsVersionMismatchWithFreshHandshake(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Dir(pythonBin(home)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pythonBin(home), []byte("python"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bin(home, "ai-evals"), []byte("runner"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := writeAtomic(handshakePath(home), []byte(`{"package":"ai-evals-runner","version":"0.7.12","schema":"onboarding/v1","command":"onboard-run"}`)); err != nil {
		t.Fatal(err)
	}
	_, err := VerifyRunner(home, func(name string, args []string, env []string, dir string) (CommandResult, error) {
		if len(args) == 2 && strings.Contains(args[1], "importlib.metadata") {
			return CommandResult{Stdout: RunnerVersion + "\n"}, nil
		}
		return CommandResult{Code: 0}, nil
	})
	if err == nil || !strings.Contains(err.Error(), "0.7.12") {
		t.Fatalf("VerifyRunner() error = %v, want stale-handshake rejection", err)
	}
}

func TestVerifyRunnerRejectsInstalledMismatchWithMatchingHandshake(t *testing.T) {
	for _, recorded := range []bool{true, false} {
		name := "without handshake"
		if recorded {
			name = "with matching handshake"
		}
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			if err := os.MkdirAll(filepath.Dir(pythonBin(home)), 0o755); err != nil {
				t.Fatal(err)
			}
			for _, path := range []string{pythonBin(home), bin(home, "ai-evals")} {
				if err := os.WriteFile(path, nil, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			if recorded {
				if err := writeHandshake(home); err != nil {
					t.Fatal(err)
				}
			}
			const wrongVersion = "999.0.0"
			_, err := VerifyRunner(home, func(executable string, _ []string, _ []string, _ string) (CommandResult, error) {
				if executable != pythonBin(home) {
					t.Fatal("runner invoked despite installed package mismatch")
				}
				return CommandResult{Stdout: wrongVersion + "\n"}, nil
			})
			if err == nil || !strings.Contains(err.Error(), wrongVersion) {
				t.Fatalf("VerifyRunner() error = %v, want installed version rejection", err)
			}
			if !recorded {
				if _, err := readHandshake(home); !os.IsNotExist(err) {
					t.Fatalf("invalid install should not be recorded; read error = %v", err)
				}
			}
		})
	}
}

func TestVerifyRunnerRecordsMissingHandshakeAfterPinnedProbe(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Dir(pythonBin(home)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pythonBin(home), []byte("python"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bin(home, "ai-evals"), []byte("runner"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyRunner(home, func(name string, args []string, env []string, dir string) (CommandResult, error) {
		if len(args) == 2 && strings.Contains(args[1], "importlib.metadata") {
			return CommandResult{Stdout: RunnerVersion + "\n"}, nil
		}
		return CommandResult{Code: 0}, nil
	}); err != nil {
		t.Fatalf("VerifyRunner() error = %v", err)
	}
	if _, err := readHandshake(home); err != nil {
		t.Fatalf("VerifyRunner() did not record handshake: %v", err)
	}
}

func TestSanitizedRunnerEnvDropsAmbientCredentials(t *testing.T) {
	t.Setenv("HARNESS_API_KEY", "ambient-key")
	t.Setenv("HARNESS_TOKEN", "ambient-token")
	t.Setenv("HARNESS_ACCOUNT_ID", "ambient-account")
	t.Setenv("HARNESS_ORG_ID", "ambient-org")
	t.Setenv("HARNESS_PROJECT_ID", "ambient-project")
	values := strings.Join(sanitizedRunnerEnv([]string{"HARNESS_API_KEY=explicit-key"}), "\n")
	for _, leaked := range []string{"ambient-key", "ambient-token", "ambient-account", "ambient-org", "ambient-project"} {
		if strings.Contains(values, leaked) {
			t.Fatalf("sanitizedRunnerEnv() leaked %q", leaked)
		}
	}
	if !strings.Contains(values, "HARNESS_API_KEY=explicit-key") {
		t.Fatalf("sanitizedRunnerEnv() dropped explicit runner credential")
	}
}

func TestRunnerProcessEnvForwardsPipelineTokenAsHarnessToken(t *testing.T) {
	t.Setenv(hbase.EnvPipelineID, "test-pipeline")
	t.Setenv(hbase.EnvAccountID, "account")
	t.Setenv(hbase.EnvOrgID, "org")
	t.Setenv(hbase.EnvProjectID, "project")
	t.Setenv("HARNESS_TOKEN", "st.account.pipeline")
	t.Setenv("HARNESS_BASE_URL", "https://example.test")
	resolved, handled, err := auth.ResolvePipelineAuth(&auth.PipelineAuthConfig{
		TokenEnvVar: "HARNESS_TOKEN", APIURLEnvVar: "HARNESS_BASE_URL",
	}, true, func(_ map[string]string, token string) map[string]string {
		return map[string]string{"x-api-key": token}
	})
	if err != nil || !handled {
		t.Fatalf("ResolvePipelineAuth() handled = %v, error = %v", handled, err)
	}
	env, notice := RunnerProcessEnv(resolved)
	if notice != "" {
		t.Fatalf("RunnerProcessEnv() notice = %q", notice)
	}
	joined := strings.Join(env, "\n")
	if !strings.Contains(joined, "HARNESS_TOKEN=st.account.pipeline") {
		t.Fatalf("RunnerProcessEnv() did not forward pipeline token: %v", env)
	}
	if strings.Contains(joined, "HARNESS_API_KEY=") {
		t.Fatalf("RunnerProcessEnv() forwarded pipeline token as API key: %v", env)
	}
}

func TestMetricCatalogUsesVerifiedRunnerWithoutWritingHandshake(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Dir(pythonBin(home)), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{pythonBin(home), bin(home, "ai-evals")} {
		if err := os.WriteFile(path, nil, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	run := func(name string, args []string, _ []string, _ string) (CommandResult, error) {
		switch {
		case name == pythonBin(home) && len(args) == 2 && strings.Contains(args[1], "importlib.metadata"):
			return CommandResult{Stdout: RunnerVersion + "\n"}, nil
		case name == bin(home, "ai-evals") && len(args) == 2 && args[0] == OnboardCommand && args[1] == "--help":
			return CommandResult{}, nil
		case name == pythonBin(home) && len(args) == 3 && args[0] == "-I" && args[2] == metricCatalogSnippet:
			return CommandResult{Stdout: `[{"kind":"contains","supported":true},{"kind":"custom","supported":false}]`}, nil
		default:
			t.Fatalf("unexpected runner invocation: %s", name)
			return CommandResult{}, nil
		}
	}
	metrics, err := metricCatalog(home, run)
	if err != nil || len(metrics) != 2 || !strings.Contains(string(metrics[1]), `"supported":false`) {
		t.Fatalf("metricCatalog() = (%s, %v), want the complete catalog", metrics, err)
	}
	if _, err := readHandshake(home); !os.IsNotExist(err) {
		t.Fatalf("read-only metric catalog wrote a handshake or failed to inspect it: %v", err)
	}
}

func TestDefaultCommandContextStopsChildOnCancellation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell cancellation probe uses sh")
	}
	parent, deadline := context.WithTimeout(context.Background(), 5*time.Second)
	defer deadline()
	ctx, cancel := context.WithCancelCause(parent)
	defer cancel(nil)
	cause := &cmdctx.TimeoutError{Secs: 5}
	marker := filepath.Join(t.TempDir(), "started")
	done := make(chan error, 1)
	go func() {
		_, err := defaultCommandContext(ctx, "sh", []string{"-c", `printf started > "$1"; exec sleep 30`, "sh", marker}, nil, "")
		done <- err
	}()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for !fileExists(marker) {
		select {
		case err := <-done:
			t.Fatalf("child exited before startup: %v", err)
		case <-ctx.Done():
			t.Fatal("child did not start")
		case <-tick.C:
		}
	}
	cancel(cause)
	select {
	case err := <-done:
		if !errors.Is(err, cause) || !cmdctx.IsTimeout(err) {
			t.Fatalf("defaultCommandContext() error = %v, want the CLI timeout cause", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("child still running after cancellation")
	}
}
