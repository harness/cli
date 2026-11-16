// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package aievals

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"reflect"
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
	golden, err := loadGoldenCandidate(t)
	if err != nil {
		t.Fatal(err)
	}
	goldenTarget := golden["target"].(map[string]any)
	goldenTemplate := goldenTarget["request_template"].(map[string]any)
	if !reflect.DeepEqual(text, goldenTemplate["text"]) {
		t.Fatalf("scan text source = %#v golden = %#v", text, goldenTemplate["text"])
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
		{
			name: "null selector",
			mutate: func(doc map[string]any) {
				target := doc["target"].(map[string]any)
				template := target["request_template"].(map[string]any)
				template["text"] = map[string]any{"source": "input", "selector": nil}
			},
			want: "selector must be a dotted or indexed input path",
		},
		{
			name: "missing threshold",
			mutate: func(doc map[string]any) {
				suite := doc["suite"].(map[string]any)
				suite["pass_strategy"] = "threshold"
			},
			want: "suite.pass_threshold is required for threshold",
		},
		{
			name: "unknown suite reference",
			mutate: func(doc map[string]any) {
				suite := doc["suite"].(map[string]any)
				suite["evals"] = []any{"missing-eval"}
			},
			want: "unresolved suite eval reference",
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

func TestV1StageWritesFieldsAndRunFailsClosed(t *testing.T) {
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
	code, err := WriteCandidate(WriteOptions{
		Repo: repo, Candidate: Candidate{Wire: doc}, Confirm: true, Python: "validator",
		Stdout: io.Discard, Stderr: io.Discard,
		Run: func(string, []string, []string, string) (CommandResult, error) {
			t.Fatal("v1 stage invoked the pinned runner")
			return CommandResult{}, nil
		},
	})
	if err != nil || code != 0 {
		t.Fatalf("code=%d err=%v", code, err)
	}
	agentPath := filepath.Join(repo, ".harness", "evals", "agents", "support-agent", "agent.yaml")
	committed, err := os.ReadFile(agentPath)
	if err != nil {
		t.Fatal(err)
	}
	text := string(committed)
	for _, kept := range []string{"schema_version: onboarding/v1", "source: input", "query_params:", "auth_headers:", "account.supportToken", "hello world"} {
		if !strings.Contains(text, kept) {
			t.Fatalf("staged v1 tree dropped %q:\n%s", kept, text)
		}
	}
	before := append([]byte(nil), committed...)
	run := func(string, []string, []string, string) (CommandResult, error) {
		t.Fatal("old runner was invoked for an onboarding/v1 tree")
		return CommandResult{}, nil
	}
	code, err = RunAgent(t.TempDir(), filepath.Join(repo, ".harness", "evals"), "support-agent", "local", nil, io.Discard, run)
	if code != 2 || err == nil || err.Error() != RunnerV1Remediation() {
		t.Fatalf("run code=%d err=%v", code, err)
	}
	after, err := os.ReadFile(agentPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatal("failed run modified the v1 tree")
	}
}

func TestStrictNestedSecretsAndLegacyRejection(t *testing.T) {
	repo := t.TempDir()
	run := func(string, []string, []string, string) (CommandResult, error) {
		t.Fatal("rejected candidate invoked a command")
		return CommandResult{}, nil
	}
	doc := onboardingV1FixtureDocument()
	envs := doc["environments"].(map[string]any)
	local := envs["local"].(map[string]any)
	local["notes"] = map[string]any{"token": "super-secret"}
	code, err := WriteCandidate(WriteOptions{
		Repo: repo, Candidate: Candidate{Wire: doc}, Confirm: true, Python: "validator", Run: run,
		Stdout: io.Discard, Stderr: io.Discard,
	})
	if code != 2 || err == nil || !strings.Contains(err.Error(), "unknown_field") || strings.Contains(err.Error(), "super-secret") {
		t.Fatalf("unknown nested code=%d err=%v", code, err)
	}
	assertNoEvalFiles(t, repo)

	doc = onboardingV1FixtureDocument()
	doc["metrics"] = []any{map[string]any{"kind": "contains", "config": map[string]any{"criteria": "sk-abc123value"}}}
	code, err = WriteCandidate(WriteOptions{
		Repo: repo, Candidate: Candidate{Wire: doc}, Confirm: true, Python: "validator", Run: run,
		Stdout: io.Discard, Stderr: io.Discard,
	})
	if code != 2 || err == nil || !strings.Contains(err.Error(), "credential_value") || strings.Contains(err.Error(), "sk-abc123value") {
		t.Fatalf("metric secret code=%d err=%v", code, err)
	}
	assertNoEvalFiles(t, repo)

	doc = onboardingV1FixtureDocument()
	suite := doc["suite"].(map[string]any)
	suite["notes"] = "password=hunter2"
	code, err = WriteCandidate(WriteOptions{
		Repo: repo, Candidate: Candidate{Wire: doc}, Confirm: true, Python: "validator", Run: run,
		Stdout: io.Discard, Stderr: io.Discard,
	})
	if code != 2 || err == nil || !strings.Contains(err.Error(), "unknown_field") || strings.Contains(err.Error(), "hunter2") {
		t.Fatalf("suite secret code=%d err=%v", code, err)
	}

	code, err = WriteCandidate(WriteOptions{
		Repo: repo,
		Candidate: Candidate{
			AgentID: "guide", EndpointURL: "http://user:secret@127.0.0.1:8080/chat",
			Method: "POST", RequestField: "text", ResponsePath: "output.text",
		},
		Confirm: true, Python: "validator", Run: run, Stdout: io.Discard, Stderr: io.Discard,
	})
	if code != 2 || err == nil || !strings.Contains(err.Error(), "onboarding/v1") || strings.Contains(err.Error(), "user:secret") {
		t.Fatalf("legacy endpoint code=%d err=%v", code, err)
	}
	assertNoEvalFiles(t, repo)
}

func TestGoldenCandidateSerialization(t *testing.T) {
	got, err := SerializeOnboardingV1Candidate()
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile(filepath.Join("testdata", "onboarding_v1_candidate.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Fatalf("serializer drifted from testdata/onboarding_v1_candidate.json\n%s", got)
	}
}

func onboardingV1FixtureDocument() map[string]any {
	return CanonicalOnboardingV1Candidate()
}

func loadGoldenCandidate(t *testing.T) (map[string]any, error) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "onboarding_v1_candidate.json"))
	if err != nil {
		return nil, err
	}
	decoded, err := decodeJSONValue(data)
	if err != nil {
		return nil, err
	}
	doc, ok := decoded.(map[string]any)
	if !ok {
		return nil, err
	}
	return doc, nil
}
