// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package aievals

import (
	"encoding/json"
	"fmt"
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
	calls := 0
	runnerCalls := 0
	catalogCalls := 0
	run := func(string, []string, []string, string) (CommandResult, error) {
		calls++
		return CommandResult{}, nil
	}
	ensureRunner := func(string, func(string) (string, error), CommandFunc) (RunnerLayout, error) {
		runnerCalls++
		return RunnerLayout{}, nil
	}
	readCatalog := func(string, CommandFunc) ([]MetricCatalogEntry, error) {
		catalogCalls++
		return nil, nil
	}
	code, err := WriteCandidate(WriteOptions{
		Repo: repo,
		Candidate: Candidate{
			AgentID: "guide", EndpointURL: "http://127.0.0.1:8080/analyze",
			Method: "POST", RequestField: "query", ResponsePath: "reasoning",
		},
		Python:            "validator",
		Run:               run,
		EnsureRunner:      ensureRunner,
		ReadMetricCatalog: readCatalog,
		Stdout:            io.Discard,
		Stderr:            io.Discard,
	})
	if code != 2 || err == nil || err.Error() != stageConfirmMessage {
		t.Fatalf("code=%d err=%v", code, err)
	}
	if calls != 0 || runnerCalls != 0 || catalogCalls != 0 {
		t.Fatalf("unconfirmed candidate made subprocess=%d runner=%d catalog=%d calls", calls, runnerCalls, catalogCalls)
	}
	assertNoEvalFiles(t, repo)

	document := onboardingV1FixtureDocument()
	document["metrics"] = []any{map[string]any{"kind": "contains"}}
	code, err = WriteCandidate(WriteOptions{
		Repo:              repo,
		Candidate:         Candidate{Wire: document, Metrics: []MetricSelection{{Kind: "contains"}}},
		Python:            "",
		Run:               run,
		EnsureRunner:      ensureRunner,
		ReadMetricCatalog: readCatalog,
		Stdout:            io.Discard,
		Stderr:            io.Discard,
	})
	if code != 2 || err == nil || err.Error() != stageConfirmMessage {
		t.Fatalf("v1 code=%d err=%v", code, err)
	}
	if calls != 0 || runnerCalls != 0 || catalogCalls != 0 {
		t.Fatalf("unconfirmed v1 candidate made subprocess=%d runner=%d catalog=%d calls", calls, runnerCalls, catalogCalls)
	}
	assertNoEvalFiles(t, repo)
}

func TestD2DatasetCaseShapesAndFieldsSurviveStaging(t *testing.T) {
	fixture, err := os.ReadFile(filepath.Join("testdata", "onboarding_v1_dataset_cases.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cases []SampleCase
	decoder := json.NewDecoder(strings.NewReader(string(fixture)))
	decoder.UseNumber()
	if err := decoder.Decode(&cases); err != nil {
		t.Fatal(err)
	}
	document := onboardingV1FixtureDocument()
	document["cases"] = cases
	repo := t.TempDir()
	candidatePath := filepath.Join(t.TempDir(), "candidate.json")
	candidateBytes, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(candidatePath, candidateBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	candidate, err := LoadCandidateFile(candidatePath)
	if err != nil {
		t.Fatal(err)
	}
	code, err := WriteCandidate(WriteOptions{
		Repo:      repo,
		Candidate: candidate,
		Confirm:   true,
		Stdout:    io.Discard,
		Stderr:    io.Discard,
		Run: func(string, []string, []string, string) (CommandResult, error) {
			t.Fatal("v1 staging without metrics invoked a subprocess")
			return CommandResult{}, nil
		},
	})
	if code != 0 || err != nil {
		t.Fatalf("code=%d err=%v", code, err)
	}
	staged, err := os.ReadFile(filepath.Join(repo, ".harness", "evals", "datasets", "answers.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	wantValue, err := decodeJSONValue(fixture)
	if err != nil {
		t.Fatal(err)
	}
	want, ok := wantValue.([]any)
	if !ok {
		t.Fatalf("fixture is not a list: %#v", wantValue)
	}
	var got []any
	for _, line := range strings.Split(strings.TrimSpace(string(staged)), "\n") {
		value, err := decodeJSONValue([]byte(line))
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, value)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("staged dataset changed D2 row shapes:\n got: %#v\nwant: %#v", got, want)
	}
}

func TestV1UnknownSensitiveFieldsAreRedactedWithD2Code(t *testing.T) {
	document := onboardingV1FixtureDocument()
	document["api_key"] = "raw-sensitive-value"
	target := document["target"].(map[string]any)
	target["request_template"].(map[string]any)["accessToken"] = "another-sensitive-value"

	err := validateV1Document(document)
	if err == nil {
		t.Fatal("expected sensitive unknown field rejection")
	}
	if got, want := err.Error(), "candidate.[redacted]: unknown_field: field is not accepted"; got != want {
		t.Fatalf("error = %q, want %q", got, want)
	}
	if strings.Contains(err.Error(), "api_key") || strings.Contains(err.Error(), "raw-sensitive-value") {
		t.Fatalf("error exposed sensitive unknown field: %v", err)
	}

	data, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "candidate.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadCandidateFile(path); err == nil {
		t.Fatal("expected loader rejection")
	} else if got, want := err.Error(), "candidate.[redacted]: unknown_field: field is not accepted"; got != want {
		t.Fatalf("loader error = %q, want %q", got, want)
	}
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

func TestV1HeadersMatchD2TranslationContract(t *testing.T) {
	repo := t.TempDir()
	run := func(string, []string, []string, string) (CommandResult, error) {
		t.Fatal("rejected candidate invoked the validator")
		return CommandResult{}, nil
	}
	t.Run("ordinary headers render strings", func(t *testing.T) {
		for _, value := range []any{true, int64(42), nil, map[string]any{"nested": "value"}} {
			t.Run(fmt.Sprintf("%T", value), func(t *testing.T) {
				doc := onboardingV1FixtureDocument()
				target := doc["target"].(map[string]any)
				target["headers"].(map[string]any)["X-Request-Id"] = value
				code, err := WriteCandidate(WriteOptions{
					Repo: repo, Candidate: Candidate{Wire: doc}, Confirm: true,
					Python: "validator", Run: run, Stdout: io.Discard, Stderr: io.Discard,
				})
				if code != 2 || err == nil || err.Error() != "target.headers.X-Request-Id must be a string" {
					t.Fatalf("value=%#v code=%d err=%v", value, code, err)
				}
				assertNoEvalFiles(t, repo)
			})
		}
	})
	t.Run("ordinary literal sources render strings", func(t *testing.T) {
		doc := onboardingV1FixtureDocument()
		target := doc["target"].(map[string]any)
		target["headers"].(map[string]any)["X-Request-Id"] = map[string]any{
			"source": "literal",
			"value":  42,
		}
		code, err := WriteCandidate(WriteOptions{
			Repo: repo, Candidate: Candidate{Wire: doc}, Confirm: true,
			Python: "validator", Run: run, Stdout: io.Discard, Stderr: io.Discard,
		})
		if code != 2 || err == nil || err.Error() != "target.headers.X-Request-Id literal value must be a string" {
			t.Fatalf("code=%d err=%v", code, err)
		}
		assertNoEvalFiles(t, repo)
	})
	t.Run("auth headers accept only named sources", func(t *testing.T) {
		for _, source := range []string{"literal", "input"} {
			t.Run(source, func(t *testing.T) {
				doc := onboardingV1FixtureDocument()
				target := doc["target"].(map[string]any)
				target["auth_headers"].(map[string]any)["X-Request-Id"] = map[string]any{
					"source": source,
					"value":  "literal-token",
				}
				code, err := WriteCandidate(WriteOptions{
					Repo: repo, Candidate: Candidate{Wire: doc}, Confirm: true,
					Python: "validator", Run: run, Stdout: io.Discard, Stderr: io.Discard,
				})
				if code != 2 || err == nil || !strings.Contains(err.Error(), "must reference an environment variable or a named Harness secret") {
					t.Fatalf("source=%s code=%d err=%v", source, code, err)
				}
				if strings.Contains(err.Error(), "literal-token") {
					t.Fatalf("auth error echoed literal: %v", err)
				}
				assertNoEvalFiles(t, repo)
			})
		}
	})
	t.Run("secret-shaped values use a redacted path", func(t *testing.T) {
		doc := onboardingV1FixtureDocument()
		target := doc["target"].(map[string]any)
		target["headers"].(map[string]any)["X-Trace-Id"] = "Bearer sk-live-header-secret"
		code, err := WriteCandidate(WriteOptions{
			Repo: repo, Candidate: Candidate{Wire: doc}, Confirm: true,
			Python: "validator", Run: run, Stdout: io.Discard, Stderr: io.Discard,
		})
		if code != 2 || err == nil || !strings.Contains(err.Error(), "target.headers.X-Trace-Id: credential_value") {
			t.Fatalf("code=%d err=%v", code, err)
		}
		if strings.Contains(err.Error(), "sk-live-header-secret") {
			t.Fatalf("header error echoed secret: %v", err)
		}
		assertNoEvalFiles(t, repo)
	})
	t.Run("secret-shaped header names are redacted", func(t *testing.T) {
		doc := onboardingV1FixtureDocument()
		target := doc["target"].(map[string]any)
		target["headers"].(map[string]any)["sk-live"] = true
		code, err := WriteCandidate(WriteOptions{
			Repo: repo, Candidate: Candidate{Wire: doc}, Confirm: true,
			Python: "validator", Run: run, Stdout: io.Discard, Stderr: io.Discard,
		})
		if code != 2 || err == nil || err.Error() != "target.headers.<redacted> must be a string" {
			t.Fatalf("code=%d err=%v", code, err)
		}
		if strings.Contains(err.Error(), "sk-live") {
			t.Fatalf("header error echoed secret-shaped name: %v", err)
		}
		assertNoEvalFiles(t, repo)
	})
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

func TestV1LiteralTemplateDelimitersAreRejectedByPathAndCode(t *testing.T) {
	const template = "{{env.ONBOARDING_SECRET}}"
	tests := []struct {
		name   string
		mutate func(map[string]any)
		want   string
	}{
		{
			name: "body",
			mutate: func(doc map[string]any) {
				target := doc["target"].(map[string]any)
				target["request_template"] = map[string]any{
					"payload": map[string]any{
						"source": "literal",
						"value":  map[string]any{"nested": template},
					},
				}
			},
			want: "target.request_template.payload.value.nested",
		},
		{
			name: "header",
			mutate: func(doc map[string]any) {
				target := doc["target"].(map[string]any)
				target["headers"] = map[string]any{
					"X-Request-Id": map[string]any{"source": "literal", "value": template},
				}
				target["auth_headers"] = map[string]any{}
			},
			want: "target.headers.X-Request-Id.value",
		},
		{
			name: "query",
			mutate: func(doc map[string]any) {
				target := doc["target"].(map[string]any)
				target["query_params"] = map[string]any{
					"q": map[string]any{"source": "literal", "value": template},
				}
			},
			want: "target.query_params.q.value",
		},
		{
			name: "metric",
			mutate: func(doc map[string]any) {
				doc["metrics"] = []any{
					map[string]any{
						"kind": "answer_relevance",
						"options": map[string]any{
							"provider": map[string]any{
								"source": "literal",
								"value":  template,
							},
						},
					},
				}
			},
			want: "metrics[0].options.provider.value",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			doc := onboardingV1FixtureDocument()
			test.mutate(doc)
			err := validateV1Document(doc)
			if err == nil {
				t.Fatal("expected literal template rejection")
			}
			want := test.want + ": literal_template: literal values cannot contain template delimiters. Use the relevant source/template form instead of literal."
			if err.Error() != want {
				t.Fatalf("error = %q, want %q", err, want)
			}
			if strings.Contains(err.Error(), template) {
				t.Fatalf("error echoed template value: %v", err)
			}
		})
	}
}

func TestV1MetricConfigRejectsNestedSensitiveKeysAndValues(t *testing.T) {
	tests := []struct {
		name   string
		config map[string]any
		want   string
	}{
		{
			name: "options key",
			config: map[string]any{
				"api_key": "ordinary-placeholder",
			},
			want: "metrics[0].options.api_key: credential_field: credential field is not accepted. Use source env or source harness_secret.",
		},
		{
			name: "criteria key",
			config: map[string]any{
				"criteria": map[string]any{"token": "ordinary-placeholder"},
			},
			want: "metrics[0].options.criteria.token: credential_field: credential field is not accepted. Use source env or source harness_secret.",
		},
		{
			name: "provider metadata key",
			config: map[string]any{
				"provider": map[string]any{
					"metadata": map[string]any{"client_secret": "ordinary-placeholder"},
				},
			},
			want: "metrics[0].options.provider.metadata.client_secret: credential_field: credential field is not accepted. Use source env or source harness_secret.",
		},
		{
			name: "nested value",
			config: map[string]any{
				"provider": map[string]any{
					"metadata": map[string]any{"region": "Bearer sk-live-metric-secret"},
				},
			},
			want: "metrics[0].options.provider.metadata.region: credential_value: credential value is not accepted",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			doc := onboardingV1FixtureDocument()
			doc["metrics"] = []any{
				map[string]any{
					"kind":    "answer_relevance",
					"options": test.config,
				},
			}
			err := validateV1Document(doc)
			if err == nil {
				t.Fatal("expected metric configuration rejection")
			}
			if err.Error() != test.want {
				t.Fatalf("error = %q, want %q", err, test.want)
			}
			if strings.Contains(err.Error(), "ordinary-placeholder") ||
				strings.Contains(err.Error(), "sk-live-metric-secret") {
				t.Fatalf("error echoed sensitive value: %v", err)
			}
		})
	}

	doc := onboardingV1FixtureDocument()
	doc["metrics"] = []any{
		map[string]any{
			"kind": "answer_relevance",
			"config": map[string]any{
				"criteria": map[string]any{"password": "ordinary-placeholder"},
			},
		},
	}
	err := validateV1Document(doc)
	if err == nil {
		t.Fatal("expected sensitive metric config rejection")
	}
	want := "metrics[0].config.criteria.password: credential_field: credential field is not accepted. Use source env or source harness_secret."
	if err.Error() != want {
		t.Fatalf("error = %q, want %q", err, want)
	}
	if strings.Contains(err.Error(), "ordinary-placeholder") {
		t.Fatalf("error echoed sensitive value: %v", err)
	}
}

func TestV1InputAndEnvSourcesRemainValidBesideLiteralPolicy(t *testing.T) {
	doc := onboardingV1FixtureDocument()
	target := doc["target"].(map[string]any)
	target["request_template"] = map[string]any{
		"input": map[string]any{"source": "input", "selector": "items.0"},
		"env":   map[string]any{"source": "env", "name": "REGION"},
	}
	target["headers"] = map[string]any{
		"X-Request-Id": map[string]any{"source": "env", "name": "REQUEST_ID"},
	}
	target["auth_headers"] = map[string]any{}
	target["query_params"] = map[string]any{
		"region": map[string]any{"source": "env", "name": "REGION"},
	}
	if err := validateV1Document(doc); err != nil {
		t.Fatalf("valid input/env sources rejected: %v", err)
	}
}

func TestLoadV1CandidateUsesRedactedMetricPolicyErrors(t *testing.T) {
	doc := onboardingV1FixtureDocument()
	doc["metrics"] = []any{
		map[string]any{
			"kind": "answer_relevance",
			"options": map[string]any{
				"provider": map[string]any{
					"metadata": map[string]any{"api_key": "ordinary-placeholder"},
				},
			},
		},
	}
	data, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "candidate.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = LoadCandidateFile(path)
	if err == nil {
		t.Fatal("expected sensitive metric configuration rejection")
	}
	want := "metrics[0].options.provider.metadata.api_key: credential_field: credential field is not accepted. Use source env or source harness_secret."
	if err.Error() != want {
		t.Fatalf("error = %q, want %q", err, want)
	}
	if strings.Contains(err.Error(), "ordinary-placeholder") {
		t.Fatalf("error echoed sensitive value: %v", err)
	}
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
