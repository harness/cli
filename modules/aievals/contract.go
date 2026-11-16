// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package aievals

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"regexp"
	"strings"
)

// SchemaVersionV1 is the generic onboarding document D2 loads in contract.py.
// The runner handshake in SchemaVersion stays provisional until release-runtime.
const SchemaVersionV1 = "onboarding/v1"

// stageConfirmMessage is the reason a valid candidate does not write.
// Confirmation is the --confirm flag so the JSON stays the D2 document.
const stageConfirmMessage = "stage requires --confirm after the user accepts the candidate. Nothing was written."

// These patterns are copied from D2 service/ai_evals/onboarding/contract.py.
// Do not add Go-only aliases. env_name and secret_ref are rejected on purpose.
var (
	envNamePattern     = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	secretRefPattern   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:@/-]{0,255}$`)
	headerNamePattern  = regexp.MustCompile(`^[!#$%&'*+\-.0-9A-Z^_` + "`" + `a-z|~]+$`)
	inputSelector      = regexp.MustCompile(`^(?:[A-Za-z_][A-Za-z0-9_]*|[0-9]+)(?:\.(?:[A-Za-z_][A-Za-z0-9_]*|[0-9]+))*$`)
	placeholderPattern = regexp.MustCompile(`\{\{\s*([^{}]+?)\s*\}\}`)
	placeholderBody    = regexp.MustCompile(`^(?:input|metadata|env)(?:\.(?:[A-Za-z_][A-Za-z0-9_]*|[0-9]+))*$`)
	camelBoundary      = regexp.MustCompile(`([a-z0-9])([A-Z])`)
	tokenSplit         = regexp.MustCompile(`[^a-z0-9]+`)
	queryInputTemplate = regexp.MustCompile(`\{\{\s*(?:input|metadata)\b`)
	secretShapedValue  = regexp.MustCompile(`(?i)(?:` +
		`\bbearer\s+\S+|\bbasic\s+\S+|\bakia[0-9a-z]{16}|\basia[0-9a-z]{16}|` +
		`\bsk-[A-Za-z0-9]|\bsk_live_[A-Za-z0-9]|\bghp_[A-Za-z0-9]|\bxox[baprs]-[A-Za-z0-9]|` +
		`\b(?:x-api-key|api[_-]?key|access[_-]?token|aws_secret_access_key|password|secret|token|authorization)\s*[:=]\s*\S+` +
		`)`)
)

var sensitiveTokens = map[string]bool{
	"auth": true, "authorization": true, "bearer": true, "cookie": true, "cookies": true,
	"credential": true, "credentials": true, "key": true, "passwd": true, "password": true,
	"secret": true, "secrets": true, "sig": true, "signature": true, "token": true,
}

// gluedCredentialAliases matches D2's exact normalized set. Membership is
// equality after separator removal, not a substring search.
var gluedCredentialAliases = map[string]bool{
	"accesskey": true, "accesstoken": true, "apikey": true, "apisecret": true,
	"authkey": true, "authtoken": true, "awsaccesskeyid": true, "awssecretaccesskey": true,
	"bearertoken": true, "clientidsecret": true, "clientsecret": true, "idtoken": true,
	"passcode": true, "passphrase": true, "passwordhash": true, "privatekey": true,
	"refreshtoken": true, "secretkey": true, "sessiontoken": true, "signingkey": true,
	"webhooksecret": true, "xamzsignature": true,
}

var confidenceLabels = map[string]bool{"high": true, "medium": true, "low": true}

var executableFields = map[string]bool{
	"code": true, "command": true, "commands": true, "entrypoint": true, "eval": true,
	"exec": true, "module_path": true, "payload": true, "program": true, "python": true,
	"script": true, "shell": true,
}

var v1DocumentFields = map[string]bool{
	"schema_version": true, "identifier": true, "name": true, "target": true,
	"environments": true, "evals": true, "suite": true, "candidate": true,
	// Staging supplements. They are removed before the agent document is written.
	"metrics": true, "cases": true,
}

var v1TargetFields = map[string]bool{
	"method": true, "request_template": true, "response_path": true, "headers": true,
	"query_params": true, "auth_headers": true, "timeout_ms": true,
}

var v1Methods = map[string]bool{
	"GET": true, "POST": true, "PUT": true, "PATCH": true, "DELETE": true,
}

var legacyCandidateFields = map[string]bool{
	"agent_id": true, "endpoint_url": true, "method": true, "request_field": true,
	"request_fields": true, "response_path": true, "auth_header": true, "auth_env": true,
	"input_text": true, "expected": true, "cases": true, "metrics": true,
}

func unsupportedSchemaVersion(value any) error {
	shown := "null"
	if value != nil {
		shown = fmt.Sprintf("%v", value)
		if text, ok := value.(string); ok {
			shown = "'" + text + "'"
		}
	}
	return fmt.Errorf("schema_version %s is not supported. Set schema_version to '%s', or remove schema_version to use the legacy onboarding tree.", shown, SchemaVersionV1)
}

func absentSchemaVersion(reason string) error {
	return fmt.Errorf("schema_version is absent. Set schema_version to '%s'. %s", SchemaVersionV1, reason)
}

func credentialTokens(name string) []string {
	spaced := camelBoundary.ReplaceAllString(name, "${1} ${2}")
	var out []string
	for _, part := range tokenSplit.Split(strings.ToLower(spaced), -1) {
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func isCredentialName(name string) bool {
	for _, token := range credentialTokens(name) {
		if sensitiveTokens[token] || gluedCredentialAliases[token] {
			return true
		}
	}
	normalized := tokenSplit.ReplaceAllString(strings.ToLower(name), "")
	return gluedCredentialAliases[normalized]
}

func secretShaped(value string) bool {
	return secretShapedValue.MatchString(value)
}

func rejectSecretShaped(label string) error {
	return fmt.Errorf("%s: credential_value: credential value is not accepted", label)
}

func rejectLiteralCredential(label string) error {
	return fmt.Errorf("%s is a credential field. Use source env or source harness_secret. The literal value is not accepted.", label)
}

// decodeJSONValue preserves integers. encoding/json would otherwise turn every
// number into float64 and stringify a typed literal during YAML staging.
func decodeJSONValue(data []byte) (any, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	if decoder.More() {
		return nil, fmt.Errorf("candidate file contains trailing JSON")
	}
	return normalizeJSONNumbers(value), nil
}

func normalizeJSONNumbers(value any) any {
	switch typed := value.(type) {
	case json.Number:
		if integer, err := typed.Int64(); err == nil {
			return integer
		}
		floatValue, err := typed.Float64()
		if err != nil || math.IsNaN(floatValue) || math.IsInf(floatValue, 0) {
			return typed
		}
		return floatValue
	case map[string]any:
		for key, child := range typed {
			typed[key] = normalizeJSONNumbers(child)
		}
		return typed
	case []any:
		for i, child := range typed {
			typed[i] = normalizeJSONNumbers(child)
		}
		return typed
	default:
		return value
	}
}

func validateV1Document(doc map[string]any) error {
	version, ok := doc["schema_version"]
	if !ok {
		return absentSchemaVersion("A stage file needs schema_version.")
	}
	if version != SchemaVersionV1 {
		return unsupportedSchemaVersion(version)
	}
	for key := range doc {
		if !v1DocumentFields[key] {
			return absentSchemaVersion(fmt.Sprintf("Field %q is not part of %s. Use target.request_template value sources (literal, input, env, harness_secret), target.query_params, and target.auth_headers.", key, SchemaVersionV1))
		}
	}
	identifier, _ := doc["identifier"].(string)
	if !identifierPattern.MatchString(strings.TrimSpace(identifier)) {
		return fmt.Errorf("identifier must be a stable identifier")
	}
	name, _ := doc["name"].(string)
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("name is required")
	}
	target, ok := doc["target"].(map[string]any)
	if !ok {
		return fmt.Errorf("target is required")
	}
	if err := validateV1Target(target); err != nil {
		return err
	}
	if err := validateV1Environments(doc["environments"]); err != nil {
		return err
	}
	if err := validateV1Evals(doc["evals"]); err != nil {
		return err
	}
	if err := validateV1Suite(doc["suite"]); err != nil {
		return err
	}
	if _, ok := doc["candidate"]; ok {
		if err := validateCandidate(doc["candidate"], "candidate"); err != nil {
			return err
		}
	}
	return nil
}

func validateV1Target(target map[string]any) error {
	for key := range target {
		if !v1TargetFields[key] {
			return fmt.Errorf("target.%s is not part of %s", key, SchemaVersionV1)
		}
	}
	method, _ := target["method"].(string)
	method = strings.ToUpper(strings.TrimSpace(method))
	if method == "" {
		method = "POST"
	}
	target["method"] = method
	if !v1Methods[method] {
		return fmt.Errorf("target.method %s is not supported by %s. Use GET, POST, PUT, PATCH, or DELETE.", method, SchemaVersionV1)
	}
	template, ok := target["request_template"].(map[string]any)
	if !ok || len(template) == 0 {
		return fmt.Errorf("target.request_template is required")
	}
	if err := validateTemplateNode(template, "target.request_template", false); err != nil {
		return err
	}
	response, _ := target["response_path"].(string)
	if strings.TrimSpace(response) == "" {
		return fmt.Errorf("target.response_path is required")
	}
	if headers, ok := target["headers"]; ok && headers != nil {
		headerMap, ok := headers.(map[string]any)
		if !ok {
			return fmt.Errorf("target.headers must be a mapping")
		}
		if err := validateV1Headers(headerMap, "target.headers"); err != nil {
			return err
		}
	}
	if _, ok := target["query_params"]; ok {
		if err := validateQueryParams(target["query_params"], "target.query_params"); err != nil {
			return err
		}
	}
	headers, _ := target["headers"].(map[string]any)
	if _, ok := target["auth_headers"]; ok {
		if err := validateAuthHeaders(target["auth_headers"], "target.auth_headers", headers); err != nil {
			return err
		}
	}
	if raw, ok := target["timeout_ms"]; ok && raw != nil {
		switch raw.(type) {
		case int64, int, float64:
		default:
			return fmt.Errorf("target.timeout_ms must be a number")
		}
	}
	return nil
}

func validateV1Headers(headers map[string]any, label string) error {
	for name, value := range headers {
		headerLabel := label + "." + name
		if isCredentialName(name) && !approvedSecretSource(value) {
			return fmt.Errorf("%s cannot carry a literal credential. Use source env or source harness_secret. The literal value is not accepted.", headerLabel)
		}
		if err := validateTemplateNode(value, headerLabel, true); err != nil {
			return err
		}
	}
	return nil
}

func approvedSecretSource(value any) bool {
	object, ok := value.(map[string]any)
	if !ok {
		return false
	}
	source, _ := object["source"].(string)
	return source == "env" || source == "harness_secret"
}

func validateTemplateNode(node any, label string, header bool) error {
	object, ok := node.(map[string]any)
	if ok {
		if _, hasSource := object["source"]; hasSource {
			return validateSource(object, label, header)
		}
		for key, value := range object {
			child := label + "." + key
			if isCredentialName(key) {
				if !approvedSecretSource(value) {
					return rejectLiteralCredential(child)
				}
				if err := validateSource(value.(map[string]any), child, header); err != nil {
					return err
				}
				continue
			}
			if err := validateTemplateNode(value, child, header); err != nil {
				return err
			}
		}
		return nil
	}
	if list, ok := node.([]any); ok {
		for index, value := range list {
			if err := validateTemplateNode(value, fmt.Sprintf("%s[%d]", label, index), header); err != nil {
				return err
			}
		}
		return nil
	}
	return ensureJSONScalar(node, label)
}

func validateSource(node map[string]any, label string, header bool) error {
	if err := rejectLegacySourceFields(node, label); err != nil {
		return err
	}
	source, _ := node["source"].(string)
	switch source {
	case "literal":
		if len(node) != 2 || !hasKeys(node, "source", "value") {
			return fmt.Errorf("%s literal source requires only source and value", label)
		}
		value := node["value"]
		if header && !isString(value) {
			return fmt.Errorf("%s literal value must be a string", label)
		}
		return ensureJSONValue(value, label+".value")
	case "input":
		if _, both := node["template"]; both {
			if _, also := node["selector"]; also {
				return fmt.Errorf("%s input source requires template or selector, not both", label)
			}
		}
		allowed := map[string]bool{"source": true}
		if _, ok := node["template"]; ok {
			allowed["template"] = true
		}
		if _, ok := node["selector"]; ok {
			allowed["selector"] = true
		}
		if !sameKeys(node, allowed) || (node["template"] != nil && node["selector"] != nil && hasKey(node, "template") && hasKey(node, "selector")) {
			return fmt.Errorf("%s input source requires template or selector, not both", label)
		}
		if template, ok := node["template"]; ok {
			return validateTemplateString(template, label+".template")
		}
		if selector, ok := node["selector"]; ok {
			text, _ := selector.(string)
			if inputSelector.FindString(text) != text || text == "" {
				return fmt.Errorf("%s.selector must be a dotted or indexed input path such as items.0 or user.name", label)
			}
		}
		return nil
	case "env":
		if !sameKeys(node, map[string]bool{"source": true, "name": true}) {
			return fmt.Errorf("%s env source requires only source and name", label)
		}
		name, _ := node["name"].(string)
		if envNamePattern.FindString(name) != name {
			return fmt.Errorf("%s.name must be an environment variable name", label)
		}
		return nil
	case "harness_secret":
		if _, ok := node["value"]; ok {
			return fmt.Errorf("%s is a Harness secret and cannot contain a value. Declare an environment variable mapping or a .env variable name.", label)
		}
		if _, ok := node["ref"]; !ok || !subsetKeys(node, "source", "ref", "env") {
			return fmt.Errorf("%s harness_secret source requires ref and optional env", label)
		}
		ref, _ := node["ref"].(string)
		if secretRefPattern.FindString(ref) != ref {
			return fmt.Errorf("%s.ref must be a Harness secret reference", label)
		}
		if env, ok := node["env"]; ok {
			name, _ := env.(string)
			if envNamePattern.FindString(name) != name {
				return fmt.Errorf("%s.env must be an environment variable name", label)
			}
		}
		return nil
	default:
		return fmt.Errorf("%s source must be literal, input, env, or harness_secret", label)
	}
}

func rejectLegacySourceFields(node map[string]any, label string) error {
	if _, ok := node["env_name"]; ok || hasKey(node, "secret_ref") {
		return fmt.Errorf("%s uses a legacy source field. Use source env with name, or source harness_secret with ref and optional env. env_name and secret_ref are not accepted.", label)
	}
	return nil
}

func validateTemplateString(value any, label string) error {
	text, ok := value.(string)
	if !ok || text == "" {
		return fmt.Errorf("%s must be a string", label)
	}
	matches := placeholderPattern.FindAllStringSubmatch(text, -1)
	if len(matches) == 0 {
		return fmt.Errorf("%s must contain an {{input}}, {{metadata.*}}, or {{env.*}} placeholder", label)
	}
	for _, match := range matches {
		body := strings.TrimSpace(match[1])
		if placeholderBody.FindString(body) != body {
			return fmt.Errorf("%s placeholder %q is not supported. Use {{input}}, {{input.<selector>}}, {{metadata.<selector>}}, or {{env.<name>}}.", label, match[0])
		}
	}
	return nil
}

func ensureJSONScalar(value any, label string) error {
	switch typed := value.(type) {
	case string:
		if secretShaped(typed) {
			return rejectSecretShaped(label)
		}
		return nil
	case nil, bool, int, int64:
		return nil
	case float64:
		if math.IsNaN(typed) || math.IsInf(typed, 0) {
			return fmt.Errorf("%s must be a finite JSON number", label)
		}
		return nil
	case json.Number:
		return fmt.Errorf("%s must be a finite JSON number", label)
	default:
		return fmt.Errorf("%s contains an unsupported value", label)
	}
}

func ensureJSONValue(value any, label string) error {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			childLabel := label + "." + key
			if isCredentialName(key) {
				return rejectLiteralCredential(childLabel)
			}
			if err := ensureJSONValue(child, childLabel); err != nil {
				return err
			}
		}
		return nil
	case []any:
		for index, child := range typed {
			if err := ensureJSONValue(child, fmt.Sprintf("%s[%d]", label, index)); err != nil {
				return err
			}
		}
		return nil
	default:
		return ensureJSONScalar(value, label)
	}
}

func validateQueryParams(raw any, label string) error {
	if raw == nil {
		return nil
	}
	object, ok := raw.(map[string]any)
	if !ok {
		return fmt.Errorf("%s must be a mapping of query parameter names", label)
	}
	for key, value := range object {
		if key == "" {
			return fmt.Errorf("%s names must be non-empty strings", label)
		}
		item := label + "." + key
		if isCredentialName(key) {
			return fmt.Errorf("%s is a credential query parameter. Put credentials in auth_headers using source env or source harness_secret. The value is not accepted.", item)
		}
		if err := validateQueryValue(value, item); err != nil {
			return err
		}
	}
	return nil
}

func validateQueryValue(value any, label string) error {
	object, ok := value.(map[string]any)
	if ok {
		if _, hasSource := object["source"]; hasSource {
			source, _ := object["source"].(string)
			switch source {
			case "harness_secret":
				return fmt.Errorf("%s cannot reference a Harness secret. Put credentials in auth_headers. The value is not accepted.", label)
			case "input", "metadata":
				return queryTemplateError(label)
			case "env":
				return validateSource(object, label, false)
			case "literal":
				if !sameKeys(object, map[string]bool{"source": true, "value": true}) {
					return fmt.Errorf("%s literal source requires only source and value", label)
				}
				literal := object["value"]
				if text, ok := literal.(string); ok && queryInputTemplate.MatchString(text) {
					return queryTemplateError(label)
				}
				if err := ensureJSONValue(literal, label+".value"); err != nil {
					return err
				}
				return ensureQueryScalar(literal, label+".value")
			default:
				return fmt.Errorf("%s source must be literal or env. Query parameters do not render input or metadata templates.", label)
			}
		}
	}
	if text, ok := value.(string); ok && queryInputTemplate.MatchString(text) {
		return queryTemplateError(label)
	}
	if err := ensureJSONValue(value, label); err != nil {
		return err
	}
	return ensureQueryScalar(value, label)
}

func queryTemplateError(label string) error {
	return fmt.Errorf("%s cannot use an input or metadata template. Query parameters accept a literal scalar or source env. Per-item templates are not rendered on the query string.", label)
}

func ensureQueryScalar(value any, label string) error {
	switch value.(type) {
	case map[string]any, []any:
		return fmt.Errorf("%s must be a literal scalar or source env. Objects and lists are not query parameters.", label)
	default:
		return ensureJSONScalar(value, label)
	}
}

func validateAuthHeaders(raw any, label string, existing map[string]any) error {
	if raw == nil {
		return nil
	}
	object, ok := raw.(map[string]any)
	if !ok {
		return fmt.Errorf("%s must be a mapping of header names", label)
	}
	occupied := map[string]string{}
	for name := range existing {
		occupied[strings.ToLower(name)] = name
	}
	for name, value := range object {
		if headerNamePattern.FindString(name) != name {
			return fmt.Errorf("%s key %q must be an HTTP header name", label, name)
		}
		folded := strings.ToLower(name)
		if previous, ok := occupied[folded]; ok {
			return fmt.Errorf("%s.%s duplicates header %q. Name each auth header once.", label, name, previous)
		}
		body, ok := value.(map[string]any)
		if !ok {
			return fmt.Errorf("%s.%s must reference an environment variable or a named Harness secret, not a literal value.", label, name)
		}
		source, _ := body["source"].(string)
		if source != "env" && source != "harness_secret" {
			return fmt.Errorf("%s.%s must reference an environment variable or a named Harness secret, not a literal value.", label, name)
		}
		if err := validateSource(body, label+"."+name, true); err != nil {
			return err
		}
		occupied[folded] = name
	}
	return nil
}

func validateCandidate(raw any, label string) error {
	object, ok := raw.(map[string]any)
	if !ok {
		return fmt.Errorf("%s must be a mapping", label)
	}
	if err := rejectExecutableFields(object, label); err != nil {
		return err
	}
	for key := range object {
		switch key {
		case "id", "confidence", "evidence":
		default:
			return fmt.Errorf("%s only allows id, confidence, and evidence", label)
		}
	}
	id, _ := object["id"].(string)
	if strings.TrimSpace(id) == "" {
		return fmt.Errorf("%s.id is required", label)
	}
	if err := validateConfidence(object["confidence"], label+".confidence"); err != nil {
		return err
	}
	evidence, ok := object["evidence"].([]any)
	if !ok || len(evidence) == 0 {
		return fmt.Errorf("%s.evidence must be a non-empty list", label)
	}
	for index, item := range evidence {
		if err := validateEvidence(item, fmt.Sprintf("%s.evidence[%d]", label, index)); err != nil {
			return err
		}
	}
	return nil
}

func validateEvidence(raw any, label string) error {
	object, ok := raw.(map[string]any)
	if !ok {
		return fmt.Errorf("%s must be a mapping", label)
	}
	if err := rejectExecutableFields(object, label); err != nil {
		return err
	}
	for key := range object {
		switch key {
		case "source", "summary", "confidence", "locator":
		default:
			return fmt.Errorf("%s only allows source, summary, confidence, and locator", label)
		}
	}
	source, _ := object["source"].(string)
	summary, _ := object["summary"].(string)
	if strings.TrimSpace(source) == "" {
		return fmt.Errorf("%s.source is required", label)
	}
	if strings.TrimSpace(summary) == "" {
		return fmt.Errorf("%s.summary is required", label)
	}
	if _, ok := object["confidence"]; ok {
		if err := validateConfidence(object["confidence"], label+".confidence"); err != nil {
			return err
		}
	}
	if locator, ok := object["locator"]; ok && locator != nil {
		if _, ok := locator.(string); !ok {
			return fmt.Errorf("%s.locator must be a string", label)
		}
	}
	return nil
}

func validateConfidence(value any, label string) error {
	if text, ok := value.(string); ok && confidenceLabels[text] {
		return nil
	}
	switch typed := value.(type) {
	case bool:
		return fmt.Errorf("%s must be high, medium, low, or a number from 0 to 1", label)
	case int:
		if typed < 0 || typed > 1 {
			return fmt.Errorf("%s must be high, medium, low, or a number from 0 to 1", label)
		}
	case int64:
		if typed < 0 || typed > 1 {
			return fmt.Errorf("%s must be high, medium, low, or a number from 0 to 1", label)
		}
	case float64:
		if math.IsNaN(typed) || math.IsInf(typed, 0) || typed < 0 || typed > 1 {
			return fmt.Errorf("%s must be high, medium, low, or a number from 0 to 1", label)
		}
	default:
		return fmt.Errorf("%s must be high, medium, low, or a number from 0 to 1", label)
	}
	return nil
}

func rejectExecutableFields(node any, label string) error {
	switch typed := node.(type) {
	case map[string]any:
		for key, value := range typed {
			if executableFields[key] {
				return fmt.Errorf("%s cannot include executable field %q. Record a source path and summary only.", label, key)
			}
			if err := rejectExecutableFields(value, label); err != nil {
				return err
			}
		}
	case []any:
		for _, value := range typed {
			if err := rejectExecutableFields(value, label); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateV1Environments(raw any) error {
	envs, ok := raw.(map[string]any)
	if !ok {
		return fmt.Errorf("environments is required")
	}
	local, ok := envs["local"].(map[string]any)
	if !ok {
		return fmt.Errorf("environments.local is required")
	}
	for name, body := range envs {
		if !identifierPattern.MatchString(name) {
			return fmt.Errorf("environment name must be a stable identifier")
		}
		spec, ok := body.(map[string]any)
		if !ok {
			return fmt.Errorf("environments.%s is required", name)
		}
		endpoint, _ := spec["endpoint"].(string)
		if endpoint == "" {
			return fmt.Errorf("environments.%s.endpoint is required", name)
		}
		parsed, err := url.Parse(endpoint)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Hostname() == "" {
			return fmt.Errorf("environments.%s.endpoint must be an http(s) URL", name)
		}
		if parsed.User != nil {
			return fmt.Errorf("environments.%s.endpoint must not embed credentials", name)
		}
		for key := range parsed.Query() {
			if isCredentialName(key) {
				return fmt.Errorf("environments.%s.endpoint includes credential query parameter %q. Remove it from the endpoint and declare it under auth_headers with source env or source harness_secret. The value is not accepted.", name, key)
			}
		}
		if secrets, ok := spec["secrets"]; ok && secrets != nil {
			secretMap, ok := secrets.(map[string]any)
			if !ok {
				return fmt.Errorf("environments.%s.secrets must be a mapping", name)
			}
			for ref, target := range secretMap {
				if secretRefPattern.FindString(ref) != ref {
					return fmt.Errorf("environments.%s.secrets key must be a Harness secret reference", name)
				}
				switch mapping := target.(type) {
				case string:
					if envNamePattern.FindString(mapping) != mapping {
						return fmt.Errorf("environments.%s.secrets.%s must be an environment variable name", name, ref)
					}
				case map[string]any:
					if _, hasValue := mapping["value"]; hasValue {
						return fmt.Errorf("environments.%s.secrets.%s cannot contain a value. Map the Harness secret to an environment variable name.", name, ref)
					}
					env, _ := mapping["env"].(string)
					if envNamePattern.FindString(env) != env {
						return fmt.Errorf("environments.%s.secrets.%s.env must be an environment variable name", name, ref)
					}
				default:
					return fmt.Errorf("environments.%s.secrets.%s must map to an environment variable name", name, ref)
				}
			}
		}
		_ = local
	}
	return nil
}

func validateV1Evals(raw any) error {
	evals, ok := raw.([]any)
	if !ok || len(evals) == 0 {
		return fmt.Errorf("evals must be a non-empty list")
	}
	for index, item := range evals {
		body, ok := item.(map[string]any)
		if !ok {
			return fmt.Errorf("evals[%d] must be a mapping", index)
		}
		id, _ := body["identifier"].(string)
		if !identifierPattern.MatchString(id) {
			return fmt.Errorf("evals[%d].identifier must be a stable identifier", index)
		}
		dataset, _ := body["dataset"].(string)
		metricSet, _ := body["metric_set"].(string)
		if strings.TrimSpace(dataset) == "" || strings.TrimSpace(metricSet) == "" {
			return fmt.Errorf("evals[%d] dataset and metric_set are required", index)
		}
	}
	return nil
}

func validateV1Suite(raw any) error {
	suite, ok := raw.(map[string]any)
	if !ok {
		return fmt.Errorf("suite is required")
	}
	id, _ := suite["identifier"].(string)
	if !identifierPattern.MatchString(id) {
		return fmt.Errorf("suite.identifier must be a stable identifier")
	}
	strategy, _ := suite["pass_strategy"].(string)
	if strategy == "" {
		strategy = "all_must_pass"
	}
	if strategy != "all_must_pass" && strategy != "threshold" {
		return fmt.Errorf("suite.pass_strategy is not supported")
	}
	evals, ok := suite["evals"].([]any)
	if !ok || len(evals) == 0 {
		return fmt.Errorf("suite.evals must list eval identifiers")
	}
	return nil
}

func hasKey(node map[string]any, key string) bool {
	_, ok := node[key]
	return ok
}

func hasKeys(node map[string]any, keys ...string) bool {
	if len(node) != len(keys) {
		return false
	}
	for _, key := range keys {
		if _, ok := node[key]; !ok {
			return false
		}
	}
	return true
}

func sameKeys(node map[string]any, allowed map[string]bool) bool {
	if len(node) != len(allowed) {
		return false
	}
	for key := range node {
		if !allowed[key] {
			return false
		}
	}
	return true
}

func subsetKeys(node map[string]any, keys ...string) bool {
	allowed := map[string]bool{}
	for _, key := range keys {
		allowed[key] = true
	}
	for key := range node {
		if !allowed[key] {
			return false
		}
	}
	return true
}

func isString(value any) bool {
	_, ok := value.(string)
	return ok
}

func renderInputPlaceholder(node map[string]any) (string, error) {
	template, hasTemplate := node["template"]
	selector, hasSelector := node["selector"]
	if hasTemplate && hasSelector {
		return "", fmt.Errorf("input source accepts template or selector, not both")
	}
	if hasTemplate {
		text, ok := template.(string)
		if !ok {
			return "", fmt.Errorf("template must be a string")
		}
		return text, nil
	}
	if !hasSelector {
		return "{{input}}", nil
	}
	text, ok := selector.(string)
	if !ok || inputSelector.FindString(text) != text {
		return "", fmt.Errorf("selector must be a dotted or indexed input path such as items.0 or user.name")
	}
	return "{{input." + text + "}}", nil
}
