// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package aievals

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
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

func TestEmbeddedMetricCatalogWithHarnessEvals0294(t *testing.T) {
	python := os.Getenv("AIEVALS_METRIC_CATALOG_PYTHON")
	if python == "" {
		t.Skip("set AIEVALS_METRIC_CATALOG_PYTHON to a Python executable with harness-evals==0.29.4 installed")
	}

	runPython := func(source string) []byte {
		t.Helper()
		cmd := exec.Command(python, "-I", "-c", source)
		cmd.Env = []string{}
		output, err := cmd.Output()
		if err != nil {
			t.Fatalf("configured Python command failed: %v", err)
		}
		return output
	}

	version := strings.TrimSpace(string(runPython(`import importlib.metadata as metadata; print(metadata.version("harness-evals"))`)))
	if version != "0.29.4" {
		t.Fatalf("configured Python has harness-evals %q, want 0.29.4", version)
	}

	type metricEntry struct {
		Kind              string `json:"kind"`
		Type              string `json:"type"`
		Supported         bool   `json:"supported"`
		UnsupportedReason string `json:"unsupported_reason"`
		OptionsSchema     struct {
			Required   []string `json:"required"`
			Properties map[string]struct {
				Type  json.RawMessage `json:"type"`
				Items struct {
					Type string `json:"type"`
				} `json:"items"`
			} `json:"properties"`
		} `json:"options_schema"`
	}
	var entries []metricEntry
	if err := json.Unmarshal(runPython(metricCatalogSnippet), &entries); err != nil {
		t.Fatalf("embedded metric_catalog.py returned invalid JSON: %v", err)
	}
	byKind := make(map[string]metricEntry, len(entries))
	for _, entry := range entries {
		byKind[entry.Kind] = entry
	}

	for kind, wantType := range map[string]string{"schema_validation": "heuristic", "topic_adherence": "llm"} {
		entry, ok := byKind[kind]
		if !ok || !entry.Supported || entry.Type != wantType {
			t.Fatalf("catalog entry %q = %+v, want supported type %q", kind, entry, wantType)
		}
	}
	jsonSchemaTypes := func(raw json.RawMessage) []string {
		var types []string
		if err := json.Unmarshal(raw, &types); err == nil {
			return types
		}
		var single string
		if err := json.Unmarshal(raw, &single); err == nil && single != "" {
			return []string{single}
		}
		return nil
	}
	assertRequiredOption := func(kind, name, wantTypes, wantItemType string) {
		t.Helper()
		entry := byKind[kind]
		required := false
		for _, option := range entry.OptionsSchema.Required {
			if option == name {
				required = true
				break
			}
		}
		if !required {
			t.Fatalf("catalog entry %q does not mark constructor option %q required", kind, name)
		}
		property, ok := entry.OptionsSchema.Properties[name]
		if !ok || strings.Join(jsonSchemaTypes(property.Type), ",") != wantTypes {
			t.Fatalf("catalog entry %q option %q schema = %+v, want type %q", kind, name, property, wantTypes)
		}
		if wantItemType != "" && property.Items.Type != wantItemType {
			t.Fatalf("catalog entry %q option %q item type = %q, want %q", kind, name, property.Items.Type, wantItemType)
		}
	}
	assertRequiredOption("schema_validation", "schema", "object,string", "")
	assertRequiredOption("topic_adherence", "allowed_topics", "array", "string")

	for kind, want := range map[string]struct {
		typeName string
		reason   string
	}{
		"decision_choice":        {typeName: "unsupported", reason: "decision provider"},
		"mcp_trace_completeness": {typeName: "heuristic", reason: "without a persisted JSON representation"},
	} {
		entry, ok := byKind[kind]
		if !ok || entry.Supported || entry.Type != want.typeName || !strings.Contains(entry.UnsupportedReason, want.reason) {
			t.Fatalf("catalog entry %q = %+v, want unsupported type %q and reason containing %q", kind, entry, want.typeName, want.reason)
		}
	}
}
