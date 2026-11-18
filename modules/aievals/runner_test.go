// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package aievals

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/harness/cli/v3/pkg/auth"
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
	env, notice := RunnerProcessEnv(&auth.ResolvedAuth{
		Source:    auth.SourcePipeline,
		PATToken:  "st.account.pipeline",
		TokenKind: auth.TokenKindST,
		AccountID: "account",
		OrgID:     "org",
		ProjectID: "project",
		APIUrl:    "https://example.test",
	})
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

func TestDefaultCommandContextStopsChildOnCancellation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell cancellation probe uses sh")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := defaultCommandContext(ctx, "sh", []string{"-c", "sleep 10"}, nil, "")
	if err == nil {
		t.Fatal("defaultCommandContext() returned nil for canceled context")
	}
}
