// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package aievals

import (
	"encoding/json"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

func TestDecodeJSONValueRejectsTrailingJSON(t *testing.T) {
	if _, err := decodeJSONValue([]byte(`{"ok":true}]`)); err == nil {
		t.Fatal("decodeJSONValue accepted a trailing delimiter")
	}
}

func TestDecodeJSONValuePreservesLargeInteger(t *testing.T) {
	value, err := decodeJSONValue([]byte(`{"id":12345678901234567890}`))
	if err != nil {
		t.Fatalf("decodeJSONValue() error = %v", err)
	}
	encoded, err := marshalJSON(value)
	if err != nil {
		t.Fatalf("marshalJSON() error = %v", err)
	}
	if !strings.Contains(string(encoded), "12345678901234567890") {
		t.Fatalf("large integer was changed: %s", encoded)
	}
}

func TestV1TargetPreservesUnsignedIntegerInYAML(t *testing.T) {
	const want = uint64(12345678901234567890)
	decoded, err := decodeJSONValue([]byte(`{"method":"POST","response_path":"output.text",
		"request_template":{"item_id":12345678901234567890},"query_params":{"item_id":12345678901234567890}}`))
	if err != nil {
		t.Fatal(err)
	}
	if err := validateV1Target(decoded.(map[string]any)); err != nil {
		t.Fatal(err)
	}
	encoded, err := yaml.Marshal(decoded)
	if err != nil {
		t.Fatal(err)
	}
	var reloaded map[string]any
	if err := yaml.Unmarshal(encoded, &reloaded); err != nil {
		t.Fatal(err)
	}
	if err := validateV1Target(reloaded); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"request_template", "query_params"} {
		if got := reloaded[field].(map[string]any)["item_id"]; got != want {
			t.Errorf("%s.item_id = %v (%T), want exact unsigned integer", field, got, got)
		}
	}
}

func TestValidateV1TargetRejectsNonStringMethod(t *testing.T) {
	err := validateV1Target(map[string]any{
		"method":           true,
		"request_template": map[string]any{"input": "hello"},
		"response_path":    "output.text",
	})
	if err == nil || !strings.Contains(err.Error(), "target.method") {
		t.Fatalf("validateV1Target() error = %v, want a method type error", err)
	}
}

func TestValidateEndpointRejectsSecretQueryValue(t *testing.T) {
	err := validateEndpoint("https://example.test/chat?note=Bearer%20secret", "environment.endpoint")
	if err == nil || strings.Contains(err.Error(), "Bearer secret") {
		t.Fatalf("validateEndpoint() error = %v, want a redacted credential-value error", err)
	}
}

func TestValidateEndpointRejectsMalformedQuery(t *testing.T) {
	for _, query := range []string{"count=1&part=%ZZ", "count=1;part=2"} {
		err := validateEndpoint("https://example.test/chat?"+query, "environment.endpoint")
		if err == nil || !strings.Contains(err.Error(), "invalid query string") || strings.Contains(err.Error(), query) {
			t.Fatalf("validateEndpoint() error = %v, want a sanitized invalid-query error", err)
		}
	}
	if err := validateEndpoint("https://example.test/chat?count=1&part=two", "environment.endpoint"); err != nil {
		t.Fatal(err)
	}
}

func TestCredentialErrorsRedactKeysAndValues(t *testing.T) {
	err := validateLiteralValue(
		map[string]any{"password": "super-secret-value"},
		"target.request_template",
	)
	if err == nil {
		t.Fatal("validateLiteralValue() accepted a credential field")
	}
	if strings.Contains(err.Error(), "password") || strings.Contains(err.Error(), "super-secret-value") {
		t.Fatalf("credential error leaked input: %v", err)
	}
}

func TestValidateV1SeparatesAgentAndStageDocuments(t *testing.T) {
	agent := CanonicalOnboardingV1Candidate()
	stage := cloneValue(agent).(map[string]any)
	stage["cases"] = []any{map[string]any{"id": "smoke", "input": "hello"}}
	if err := validateV1Document(stage); err != nil {
		t.Fatalf("stage document with cases: %v", err)
	}
	if err := validateV1AgentDocument(agent); err != nil {
		t.Fatalf("agent document without cases: %v", err)
	}
	if err := validateV1Document(agent); err == nil || !strings.Contains(err.Error(), "cases") {
		t.Fatalf("stage accepted the agent document without cases: %v", err)
	}
	stage["evals"].([]any)[0].(map[string]any)["dataset"] = "../answers.jsonl"
	if err := validateV1Document(stage); err == nil || !strings.Contains(err.Error(), "dataset") {
		t.Fatalf("stage accepted a dataset outside the tree: %v", err)
	}
}

func TestSanitizeSourcePathPreservesNeighbors(t *testing.T) {
	if got := sanitizeSourcePath("src/auth/chat.py"); got != "src/[redacted]/chat.py" {
		t.Fatalf("sanitizeSourcePath() = %q", got)
	}
}

func TestScanStagedValueAllowsSchemaSecretReferences(t *testing.T) {
	if err := scanStagedValue(CanonicalOnboardingV1Candidate(), "candidate"); err != nil {
		t.Fatalf("scanStagedValue() rejected valid secret references: %v", err)
	}
}

func TestScanStagedValueRejectsNestedCaseCredentialFields(t *testing.T) {
	err := scanStagedValue(
		map[string]any{
			"metadata": map[string]any{
				"secrets": map[string]any{"ordinary_name": "ordinary-value"},
			},
		},
		"cases[0]",
	)
	if err == nil {
		t.Fatal("scanStagedValue() accepted a nested case credential container")
	}
	if strings.Contains(err.Error(), "ordinary_name") || strings.Contains(err.Error(), "ordinary-value") {
		t.Fatalf("nested credential error leaked input: %v", err)
	}
}

// Keep JSON-number encoding in one test helper so the test never converts the
// value through float64 while checking the preservation contract.
func marshalJSON(value any) ([]byte, error) {
	return json.Marshal(value)
}
