// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package aievals

import (
	"encoding/json"
	"strings"
	"testing"
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
