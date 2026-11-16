// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package aievals

import (
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestScanEmitsV1CandidateAndWritesNothing(t *testing.T) {
	repo := copyFixture(t, "java-support")
	report, code, err := Scan(repo, "")
	if err != nil || code != 0 || len(report.Candidates) != 1 {
		t.Fatalf("code=%d err=%v candidates=%d", code, err, len(report.Candidates))
	}
	assertNoEvalFiles(t, repo)
	if _, err := os.Stat(filepath.Join(repo, ".harness")); !os.IsNotExist(err) {
		t.Fatalf("scan created .harness: %v", err)
	}
	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	if report.SchemaVersion != SchemaVersionV1 || strings.Contains(string(encoded), SchemaVersion) {
		t.Fatalf("scan schema = %s json=%s", report.SchemaVersion, encoded)
	}
	candidate := report.Candidates[0]
	if candidate.ID == "" || candidate.Confidence != "high" || len(candidate.Evidence) != 1 {
		t.Fatalf("candidate = %+v", candidate)
	}
	if candidate.Evidence[0].Source == "" || !strings.Contains(candidate.Evidence[0].Summary, "POST /api/chat") {
		t.Fatalf("evidence = %+v", candidate.Evidence)
	}
	text, ok := candidate.Target.RequestTemplate["text"].(map[string]any)
	if !ok || text["source"] != "input" || text["selector"] != "text" {
		t.Fatalf("request template = %#v", candidate.Target.RequestTemplate)
	}
	auth, ok := candidate.Target.AuthHeaders["Authorization"].(map[string]any)
	if !ok || auth["source"] != "env" || auth["name"] != "SUPPORT_TOKEN" {
		t.Fatalf("auth = %#v", candidate.Target.AuthHeaders)
	}
	if strings.Contains(string(encoded), "pat.") || strings.Contains(string(encoded), "sk-") {
		t.Fatalf("scan JSON carried a secret-shaped value: %s", encoded)
	}
}

func TestStageWithoutConfirmWritesNothing(t *testing.T) {
	repo := t.TempDir()
	run := func(string, []string, []string, string) (CommandResult, error) {
		t.Fatal("unconfirmed candidate invoked the validator")
		return CommandResult{}, nil
	}
	code, err := WriteCandidate(WriteOptions{
		Repo: repo,
		Candidate: Candidate{
			AgentID: "guide", EndpointURL: "http://127.0.0.1:8080/analyze",
			Method: "POST", RequestField: "query", ResponsePath: "reasoning",
		},
		Python: "validator",
		Run:    run,
		Stdout: io.Discard,
		Stderr: io.Discard,
	})
	if code != 2 || err == nil || err.Error() != stageConfirmMessage {
		t.Fatalf("code=%d err=%v", code, err)
	}
	assertNoEvalFiles(t, repo)

	document := onboardingV1FixtureDocument()
	code, err = WriteCandidate(WriteOptions{
		Repo:      repo,
		Candidate: Candidate{Wire: document},
		Python:    "validator",
		Run:       run,
		Stdout:    io.Discard,
		Stderr:    io.Discard,
	})
	if code != 2 || err == nil || err.Error() != stageConfirmMessage {
		t.Fatalf("v1 code=%d err=%v", code, err)
	}
	assertNoEvalFiles(t, repo)
}

func TestStageRejectsSchemaGrammarAndSecretsBeforeWrite(t *testing.T) {
	repo := t.TempDir()
	run := func(string, []string, []string, string) (CommandResult, error) {
		t.Fatal("rejected candidate invoked the validator")
		return CommandResult{}, nil
	}
	secret := "sk-abc123value"
	cases := []struct {
		name   string
		mutate func(map[string]any)
		want   string
		absent string
	}{
		{
			name:   "unsupported version",
			mutate: func(doc map[string]any) { doc["schema_version"] = "onboarding/v2" },
			want:   "Set schema_version to 'onboarding/v1'",
		},
		{
			name:   "unknown field",
			mutate: func(doc map[string]any) { doc["toolResults"] = "stub" },
			want:   "toolResults",
		},
		{
			name: "legacy source alias",
			mutate: func(doc map[string]any) {
				target := doc["target"].(map[string]any)
				template := target["request_template"].(map[string]any)
				template["region"] = map[string]any{"source": "env", "env_name": "REGION"}
			},
			want: "env_name and secret_ref are not accepted",
		},
		{
			name: "secret-shaped literal",
			mutate: func(doc map[string]any) {
				target := doc["target"].(map[string]any)
				template := target["request_template"].(map[string]any)
				nested := template["nested"].(map[string]any)
				value := nested["value"].(map[string]any)
				value["note"] = secret
			},
			want:   "credential_value",
			absent: secret,
		},
		{
			name: "credential query",
			mutate: func(doc map[string]any) {
				target := doc["target"].(map[string]any)
				params := target["query_params"].(map[string]any)
				params["api_key"] = map[string]any{"source": "literal", "value": "ordinary"}
			},
			want:   "credential query parameter",
			absent: "ordinary",
		},
		{
			name: "literal auth header",
			mutate: func(doc map[string]any) {
				target := doc["target"].(map[string]any)
				headers := target["auth_headers"].(map[string]any)
				headers["X-Request-Id"] = "literal-token"
			},
			want:   "not a literal value",
			absent: "literal-token",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			doc := onboardingV1FixtureDocument()
			tc.mutate(doc)
			code, err := WriteCandidate(WriteOptions{
				Repo: repo, Candidate: Candidate{Wire: doc}, Confirm: true,
				Python: "validator", Run: run, Stdout: io.Discard, Stderr: io.Discard,
			})
			if code != 2 || err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("code=%d err=%v", code, err)
			}
			if tc.absent != "" && strings.Contains(err.Error(), tc.absent) {
				t.Fatalf("error echoed the value: %v", err)
			}
			assertNoEvalFiles(t, repo)
		})
	}

	path := filepath.Join(t.TempDir(), "legacy.json")
	if err := os.WriteFile(path, []byte(`{"endpoint_url":"http://127.0.0.1:8080/chat","method":"POST","request_field":"text","response_path":"output.text","auth_header":"Authorization"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadCandidateFile(path); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadCandidateFile(path)
	if err != nil {
		t.Fatal(err)
	}
	code, err := WriteCandidate(WriteOptions{
		Repo: repo, Candidate: loaded, Confirm: true, Python: "validator", Run: run,
		Stdout: io.Discard, Stderr: io.Discard,
	})
	if code != 2 || err == nil || !strings.Contains(err.Error(), "auth_header without auth_env") {
		t.Fatalf("legacy code=%d err=%v", code, err)
	}
	assertNoEvalFiles(t, repo)
}

func TestV1FixtureKeepsTypedValuesAndProjectsPinnedRunner(t *testing.T) {
	doc := onboardingV1FixtureDocument()
	if err := validateV1Document(doc); err != nil {
		t.Fatal(err)
	}
	if isCredentialName("keyboard") || isCredentialName("author") || isCredentialName("task-name") || secretShaped("task-name") || secretShaped("desk-lamp") {
		t.Fatal("ordinary words were classified as credentials")
	}
	if !isCredentialName("secretkey") || !isCredentialName("api_key") || !secretShaped("Bearer token") {
		t.Fatal("D2 credential grammar was not applied")
	}
	repo := t.TempDir()
	var checked []byte
	code, err := WriteCandidate(WriteOptions{
		Repo: repo, Candidate: Candidate{Wire: doc}, Confirm: true, Python: "validator",
		Stdout: io.Discard, Stderr: io.Discard,
		Run: func(_ string, args []string, _ []string, _ string) (CommandResult, error) {
			body, err := os.ReadFile(filepath.Join(args[len(args)-1], "agents", "support-agent", "agent.yaml"))
			if err != nil {
				return CommandResult{}, err
			}
			checked = body
			return CommandResult{Code: 0}, nil
		},
	})
	if err != nil || code != 0 {
		t.Fatalf("code=%d err=%v", code, err)
	}
	committed, err := os.ReadFile(filepath.Join(repo, ".harness", "evals", "agents", "support-agent", "agent.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(committed), "schema_version: onboarding/v1") || !strings.Contains(string(committed), "source: input") {
		t.Fatalf("committed tree is not the v1 document:\n%s", committed)
	}
	if strings.Contains(string(checked), "source: input") {
		t.Fatalf("pinned projection still contains source input:\n%s", checked)
	}
	if strings.Contains(string(committed), "super-secret") || strings.Contains(string(checked), "SUPPORT_TOKEN_VALUE") {
		t.Fatal("staged YAML contained a secret value")
	}
}

func TestD2LoadsGeneratedOnboardingV1Candidate(t *testing.T) {
	root := os.Getenv("D2_ONBOARDING_ROOT")
	if root == "" {
		t.Skip("D2_ONBOARDING_ROOT is unset")
	}
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("test file path is unavailable")
	}
	script := filepath.Join(filepath.Dir(file), "..", "..", "scripts", "check_d4_onboarding_v1.py")
	fixture := filepath.Join(t.TempDir(), "candidate.json")
	encoded, err := json.Marshal(onboardingV1FixtureDocument())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fixture, encoded, 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("poetry", "run", "python", script, fixture)
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("D2 loader failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "onboarding/v1 candidate loaded") {
		t.Fatalf("D2 output = %s", out)
	}
}

func onboardingV1FixtureDocument() map[string]any {
	return map[string]any{
		"schema_version": SchemaVersionV1,
		"identifier":     "support-agent",
		"name":           "Support Agent",
		"candidate": map[string]any{
			"id":         "support-agent",
			"confidence": "high",
			"evidence": []any{
				map[string]any{
					"source":     "src/chat.py",
					"summary":    "POST /chat returns JSON",
					"locator":    "chat:40",
					"confidence": 0.9,
				},
			},
		},
		"target": map[string]any{
			"method":        "POST",
			"response_path": "output.text",
			"headers": map[string]any{
				"Content-Type": map[string]any{"source": "literal", "value": "application/json"},
			},
			"request_template": map[string]any{
				"message": map[string]any{"source": "input", "selector": "items.0"},
				"region":  map[string]any{"source": "env", "name": "REGION"},
				"nested": map[string]any{"source": "literal", "value": map[string]any{
					"flag":     true,
					"count":    2,
					"empty":    nil,
					"items":    []any{1, "task-name", false},
					"child":    map[string]any{"ok": true, "author": "ada", "keyboard": "keys"},
					"passage":  "text",
					"deskLamp": "desk-lamp",
				}},
			},
			"query_params": map[string]any{
				"q":      map[string]any{"source": "literal", "value": "hello world"},
				"flag":   map[string]any{"source": "literal", "value": true},
				"empty":  map[string]any{"source": "literal", "value": nil},
				"region": map[string]any{"source": "env", "name": "REGION"},
			},
			"auth_headers": map[string]any{
				"Authorization": map[string]any{"source": "harness_secret", "ref": "account.supportToken", "env": "SUPPORT_TOKEN"},
				"X-Request-Id":  map[string]any{"source": "env", "name": "REQUEST_ID"},
			},
		},
		"environments": map[string]any{
			"local": map[string]any{
				"endpoint": "https://agent.example/chat",
				"secrets":  map[string]any{"account.supportToken": "SUPPORT_TOKEN"},
			},
		},
		"evals": []any{
			map[string]any{
				"identifier": "answer-quality",
				"name":       "Answer Quality",
				"dataset":    "datasets/answers.jsonl",
				"metric_set": "metric-sets/quality.yaml",
			},
		},
		"suite": map[string]any{
			"identifier":    "support-suite",
			"name":          "Support Suite",
			"pass_strategy": "all_must_pass",
			"evals":         []any{"answer-quality"},
		},
	}
}
