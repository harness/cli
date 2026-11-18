// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package aievals

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"go.yaml.in/yaml/v3"
)

// EvidenceRecord is one scanner observation. Source is a path, not a program.
type EvidenceRecord struct {
	Source     string `json:"source"`
	Summary    string `json:"summary"`
	Confidence any    `json:"confidence,omitempty"`
	Locator    string `json:"locator,omitempty"`
}

// ScanTarget is the generic typed hint inside a scan candidate.
// Values are D2 source nodes. Names are included; secret values are not.
type ScanTarget struct {
	Method          string         `json:"method,omitempty"`
	ResponsePath    string         `json:"response_path,omitempty"`
	RequestTemplate map[string]any `json:"request_template,omitempty"`
	QueryParams     map[string]any `json:"query_params,omitempty"`
	AuthHeaders     map[string]any `json:"auth_headers,omitempty"`
	Headers         map[string]any `json:"headers,omitempty"`
}

// ScanCandidate is one optional scanner hint. It is JSON for the coding agent.
// The JSON object is an onboarding/v1 candidate, not a .harness/evals tree.
type ScanCandidate struct {
	ID         string           `json:"id"`
	Confidence string           `json:"confidence"`
	Evidence   []EvidenceRecord `json:"evidence"`
	Target     ScanTarget       `json:"target"`

	agentID       string
	endpoint      string
	method        string
	requestField  string
	requestFields string
	responsePath  string
	authHeader    string
	authEnv       string
	inputText     string
	expected      string
}

// ScanReport is the stdout of `harness execute eval_onboarding:scan`.
type ScanReport struct {
	SchemaVersion string          `json:"schema_version"`
	HasSignal     bool            `json:"has_signal"`
	Notes         []string        `json:"notes,omitempty"`
	Candidates    []ScanCandidate `json:"candidates"`
}

// SampleCase is one dataset row the agent already confirmed with the user.
type SampleCase struct {
	ID                string           `json:"id"`
	Input             json.RawMessage  `json:"input"`
	Expected          any              `json:"expected,omitempty"`
	ExpectedOutput    any              `json:"expected_output,omitempty"`
	Context           any              `json:"context,omitempty"`
	ExpectedTools     []string         `json:"expected_tools,omitempty"`
	ExpectedToolCalls []map[string]any `json:"expected_tool_calls,omitempty"`
	Tags              []string         `json:"tags,omitempty"`
	Metadata          map[string]any   `json:"metadata,omitempty"`
	PrecomputedOutput any              `json:"precomputed_output,omitempty"`

	expectedPresent          bool
	expectedOutputPresent    bool
	precomputedOutputPresent bool
}

var sampleCaseFields = map[string]bool{
	"id": true, "input": true, "expected": true, "expected_output": true,
	"context": true, "expected_tools": true, "expected_tool_calls": true,
	"tags": true, "metadata": true, "precomputed_output": true,
}

// UnmarshalJSON keeps dataset values as JSON data instead of narrowing them
// to strings or float64. The presence bits distinguish an authored null from
// an omitted optional field, which D2 carries through its dataset loader.
func (item *SampleCase) UnmarshalJSON(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var raw map[string]json.RawMessage
	if err := decoder.Decode(&raw); err != nil {
		return err
	}
	if raw == nil {
		return fmt.Errorf("sample case must be a JSON object")
	}
	for key := range raw {
		if !sampleCaseFields[key] {
			return rejectUnknown("cases[]." + publicUnknownKey(key))
		}
	}
	*item = SampleCase{}
	if encoded, ok := raw["id"]; ok {
		if err := json.Unmarshal(encoded, &item.ID); err != nil {
			return err
		}
	}
	if encoded, ok := raw["input"]; ok {
		if !json.Valid(encoded) {
			return fmt.Errorf("sample case input must be valid JSON")
		}
		item.Input = append(json.RawMessage(nil), encoded...)
	}
	decodeAny := func(key string) (any, bool, error) {
		encoded, ok := raw[key]
		if !ok {
			return nil, false, nil
		}
		value, err := decodeJSONValue(encoded)
		return value, true, err
	}
	var err error
	if item.Expected, item.expectedPresent, err = decodeAny("expected"); err != nil {
		return err
	}
	if item.ExpectedOutput, item.expectedOutputPresent, err = decodeAny("expected_output"); err != nil {
		return err
	}
	if item.Context, _, err = decodeAny("context"); err != nil {
		return err
	}
	if item.PrecomputedOutput, item.precomputedOutputPresent, err = decodeAny("precomputed_output"); err != nil {
		return err
	}
	if encoded, ok := raw["expected_tools"]; ok {
		if err := json.Unmarshal(encoded, &item.ExpectedTools); err != nil {
			return err
		}
	}
	if encoded, ok := raw["expected_tool_calls"]; ok {
		value, err := decodeJSONValue(encoded)
		if err != nil {
			return err
		}
		if value == nil {
			item.ExpectedToolCalls = nil
		} else {
			calls, ok := value.([]any)
			if !ok {
				return fmt.Errorf("sample case expected_tool_calls must be a list")
			}
			item.ExpectedToolCalls = make([]map[string]any, len(calls))
			for index, call := range calls {
				body, ok := call.(map[string]any)
				if !ok {
					return fmt.Errorf("sample case expected_tool_calls[%d] must be an object", index)
				}
				item.ExpectedToolCalls[index] = body
			}
		}
	}
	if encoded, ok := raw["tags"]; ok {
		if err := json.Unmarshal(encoded, &item.Tags); err != nil {
			return err
		}
	}
	if encoded, ok := raw["metadata"]; ok {
		value, err := decodeJSONValue(encoded)
		if err != nil {
			return err
		}
		if value != nil {
			var ok bool
			item.Metadata, ok = value.(map[string]any)
			if !ok {
				return fmt.Errorf("sample case metadata must be an object")
			}
		}
	}
	return nil
}

func (item SampleCase) MarshalJSON() ([]byte, error) {
	row := map[string]any{"id": item.ID}
	if item.Input != nil {
		row["input"] = json.RawMessage(item.Input)
	}
	if item.expectedPresent || item.Expected != nil {
		row["expected"] = item.Expected
	}
	if item.expectedOutputPresent || item.ExpectedOutput != nil {
		row["expected_output"] = item.ExpectedOutput
	}
	if item.Context != nil {
		row["context"] = item.Context
	}
	if item.ExpectedTools != nil {
		row["expected_tools"] = item.ExpectedTools
	}
	if item.ExpectedToolCalls != nil {
		row["expected_tool_calls"] = item.ExpectedToolCalls
	}
	if item.Tags != nil {
		row["tags"] = item.Tags
	}
	if item.Metadata != nil {
		row["metadata"] = item.Metadata
	}
	if item.precomputedOutputPresent || item.PrecomputedOutput != nil {
		row["precomputed_output"] = item.PrecomputedOutput
	}
	return json.Marshal(row)
}

// Candidate is the validate-and-write input.
// Wire is set when the file is an onboarding/v1 document. Flat fields remain
// for flags and for a legacy file that maps onto that document.
type Candidate struct {
	AgentID       string         `json:"agent_id,omitempty"`
	EndpointURL   string         `json:"endpoint_url,omitempty"`
	Method        string         `json:"method,omitempty"`
	RequestField  string         `json:"request_field,omitempty"`
	RequestFields string         `json:"request_fields,omitempty"`
	ResponsePath  string         `json:"response_path,omitempty"`
	AuthHeader    string         `json:"auth_header,omitempty"`
	AuthEnv       string         `json:"auth_env,omitempty"`
	InputText     string         `json:"input_text,omitempty"`
	Expected      string         `json:"expected,omitempty"`
	Metrics       []any          `json:"metrics,omitempty"`
	Cases         []SampleCase   `json:"cases,omitempty"`
	Wire          map[string]any `json:"-"`
}

// WriteOptions validates a candidate and writes the .harness/evals tree.
// Confirm must be set, from `harness execute eval_onboarding:stage --confirm`, or the
// command writes nothing.
type WriteOptions struct {
	Repo           string
	Candidate      Candidate
	Confirm        bool
	Python         string
	Run            CommandFunc
	VerifyRunner   func(string, CommandFunc) (RunnerLayout, error)
	ResolveMetrics func(string, CommandFunc) (map[string]MetricDefinition, error)
	Stdout         io.Writer
	Stderr         io.Writer
}

// MetricDefinition supplies the installed SDK's classification for staged metrics.
type MetricDefinition struct {
	Type      string `json:"type"`
	Dimension string `json:"dimension"`
	Supported bool   `json:"supported"`
}

// Scan prints nothing and writes nothing. A route that is not an exact supported
// hint returns exit code 2. An empty repository returns an empty candidate list.
func Scan(repo, route string) (ScanReport, int, error) {
	inf, err := Infer(repo)
	if err != nil {
		return ScanReport{}, 1, err
	}
	report := ScanReport{SchemaVersion: SchemaVersionV1, Candidates: []ScanCandidate{}}
	if len(inf.Notes) > 0 {
		report.Notes = append([]string{}, inf.Notes...)
	}
	chosen := inf.RouteCandidates
	if strings.TrimSpace(route) != "" {
		match, ok := selectAdapterRoute(inf.RouteCandidates, route)
		if !ok {
			return report, 2, fmt.Errorf("%s", routeSelectionError(route, inf.RouteCandidates))
		}
		chosen = []RouteCandidate{match}
	}
	for _, candidate := range chosen {
		detailed, err := InferWithRoute(repo, candidate.internalIdentity())
		if err != nil {
			return ScanReport{}, 1, err
		}
		report.Candidates = append(report.Candidates, scanCandidateFrom(candidate, detailed))
	}
	report.HasSignal = len(report.Candidates) > 0
	return report, 0, nil
}

func routeSelectionError(route string, candidates []RouteCandidate) string {
	msg := fmt.Sprintf("route %q is not a supported AST-detected route and cannot be selected with --route; use guided/manual configuration instead", sanitizeSourcePath(route))
	if len(candidates) == 0 {
		return msg
	}
	identities := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		identities = append(identities, candidate.Identity())
	}
	return msg + ": supported candidates are " + strings.Join(identities, ", ")
}

func scanCandidateFrom(candidate RouteCandidate, inf Inference) ScanCandidate {
	out := ScanCandidate{ID: candidate.Identity(), Confidence: "high"}
	out.agentID = inf.AgentID
	if out.agentID == "" {
		out.agentID = candidate.Agent
	}
	set := func(field string, dest *string) {
		ev, ok := inf.ByField(field)
		if !ok || ev.Value == "" {
			return
		}
		*dest = ev.Value
	}
	set("method", &out.method)
	var path, port string
	set("endpoint_path", &path)
	set("port", &port)
	set("request_field", &out.requestField)
	set("request_fields", &out.requestFields)
	set("response_path", &out.responsePath)
	set("auth_header", &out.authHeader)
	set("auth_env", &out.authEnv)
	set("agent_id", &out.agentID)
	set("test_input_text", &out.inputText)
	if ev, ok := inf.ByField("test_expected"); ok && ev.Source != "gap" {
		out.expected = ev.Value
	}
	if port != "" && path != "" {
		out.endpoint = fmt.Sprintf("http://127.0.0.1:%s%s", port, ensureSlash(path))
	}
	if out.method == "" {
		out.method = candidate.Method
	}
	if path == "" {
		path = candidate.Path
	}
	source := sanitizeSourcePath(candidate.Source)
	if strings.TrimSpace(source) == "" {
		source = "repository"
	}
	summary := strings.TrimSpace(out.method + " " + path)
	if summary == "" {
		summary = candidate.Route()
	}
	if ev, ok := inf.ByField("required_query_params"); ok && ev.Value != "" {
		summary += "; required query parameters " + ev.Value
	}
	out.Evidence = []EvidenceRecord{{
		Source:     source,
		Summary:    summary,
		Locator:    source,
		Confidence: "high",
	}}
	out.Target.Method = out.method
	out.Target.ResponsePath = out.responsePath
	fields := requestFieldList(out.requestField, out.requestFields)
	if len(fields) > 0 {
		out.Target.RequestTemplate = map[string]any{}
		for _, field := range fields {
			if inputSelector.FindString(field) != field {
				continue
			}
			out.Target.RequestTemplate[field] = map[string]any{"source": "input", "selector": field}
		}
	}
	if out.authHeader != "" && out.authEnv != "" && envNamePattern.MatchString(out.authEnv) {
		out.Target.AuthHeaders = map[string]any{
			canonicalHeader(out.authHeader): map[string]any{"source": "env", "name": out.authEnv},
		}
	}
	return out
}

func requestFieldList(primary, listed string) []string {
	if listed != "" {
		var out []string
		seen := map[string]bool{}
		for _, part := range strings.Split(listed, ",") {
			part = strings.TrimSpace(part)
			if part == "" || seen[part] {
				continue
			}
			seen[part] = true
			out = append(out, part)
		}
		if len(out) > 0 {
			return out
		}
	}
	if strings.TrimSpace(primary) != "" {
		return []string{strings.TrimSpace(primary)}
	}
	return nil
}

// AsCandidate converts a scan hint into the legacy flag shape.
// Stage maps that shape onto onboarding/v1 when every field is unambiguous.
func (c ScanCandidate) AsCandidate() Candidate {
	return Candidate{
		AgentID:       c.agentID,
		EndpointURL:   c.endpoint,
		Method:        c.method,
		RequestField:  c.requestField,
		RequestFields: c.requestFields,
		ResponsePath:  c.responsePath,
		AuthHeader:    c.authHeader,
		AuthEnv:       c.authEnv,
		InputText:     c.inputText,
		Expected:      c.expected,
	}
}

// LoadCandidateFile reads the temporary JSON file the agent writes.
// An onboarding/v1 document is accepted as itself. A legacy flat file is
// accepted only when every field maps onto that document. Anything else fails
// before a tree is written. Secret values are never copied into the error.
func LoadCandidateFile(path string) (Candidate, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Candidate{}, err
	}
	decoded, err := decodeJSONValue(data)
	if err != nil {
		return Candidate{}, fmt.Errorf("candidate file: %w", err)
	}
	raw, ok := decoded.(map[string]any)
	if !ok {
		return Candidate{}, fmt.Errorf("candidate file must be a JSON object")
	}
	if _, ok := raw["schema_version"]; ok {
		return loadV1Candidate(raw)
	}
	for key := range raw {
		if !legacyCandidateFields[key] {
			return Candidate{}, rejectUnknown("candidate." + publicUnknownKey(key))
		}
	}
	var candidate Candidate
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&candidate); err != nil {
		return Candidate{}, fmt.Errorf("candidate file does not match the onboarding candidate schema")
	}
	// Reuse the normalized v1 number representation for legacy metric fields.
	candidate.Metrics, _ = raw["metrics"].([]any)
	return candidate, nil
}

func loadV1Candidate(raw map[string]any) (Candidate, error) {
	version := raw["schema_version"]
	if version != SchemaVersionV1 {
		return Candidate{}, unsupportedSchemaVersion(version)
	}
	for key := range raw {
		if !v1DocumentFields[key] {
			return Candidate{}, rejectUnknown("candidate." + publicUnknownKey(key))
		}
	}
	candidate := Candidate{Wire: raw}
	if err := validateV1Document(raw); err != nil {
		return Candidate{}, err
	}
	if err := attachStagingSupplements(&candidate, raw); err != nil {
		return Candidate{}, err
	}
	return candidate, nil
}

func attachStagingSupplements(candidate *Candidate, raw map[string]any) error {
	if cases, ok := raw["cases"]; ok && cases != nil {
		encoded, err := json.Marshal(cases)
		if err != nil {
			return err
		}
		decoder := json.NewDecoder(bytes.NewReader(encoded))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&candidate.Cases); err != nil {
			return fmt.Errorf("candidate file does not match the onboarding candidate schema")
		}
	}
	return nil
}

// WithOverrides replaces fields that the flags set. Empty overrides keep the file value.
func (c Candidate) WithOverrides(override Candidate) Candidate {
	if override.AgentID != "" {
		c.AgentID = override.AgentID
	}
	if override.EndpointURL != "" {
		c.EndpointURL = override.EndpointURL
	}
	if override.Method != "" {
		c.Method = override.Method
	}
	if override.RequestField != "" {
		c.RequestField = override.RequestField
	}
	if override.RequestFields != "" {
		c.RequestFields = override.RequestFields
	}
	if override.ResponsePath != "" {
		c.ResponsePath = override.ResponsePath
	}
	if override.AuthHeader != "" {
		c.AuthHeader = override.AuthHeader
	}
	if override.AuthEnv != "" {
		c.AuthEnv = override.AuthEnv
	}
	if override.InputText != "" {
		c.InputText = override.InputText
	}
	if override.Expected != "" {
		c.Expected = override.Expected
	}
	if len(override.Cases) > 0 {
		c.Cases = override.Cases
	}
	return c
}

// WriteCandidate validates an onboarding/v1 candidate and writes the tree after confirmation.
// Legacy fields first map onto the same document. If Python is supplied directly
// or via VerifyRunner, Stage validates the temporary tree before any files are
// committed; otherwise only in-process validation is performed.
func WriteCandidate(opts WriteOptions) (int, error) {
	if opts.Candidate.Wire == nil {
		normalized, err := normalizeCandidate(opts.Candidate)
		if err != nil {
			return 2, err
		}
		document, err := legacyCandidateDocument(opts.Repo, normalized)
		if err != nil {
			return 2, err
		}
		caseValue, err := sampleCasesValue(normalized.Cases)
		if err != nil {
			return 2, err
		}
		document["cases"] = caseValue
		opts.Candidate.Wire = document
		opts.Candidate.Cases = normalized.Cases
	}
	return writeV1Candidate(opts)
}

func normalizeCandidate(candidate Candidate) (Candidate, error) {
	candidate.EndpointURL = strings.TrimSpace(candidate.EndpointURL)
	candidate.Method = strings.ToUpper(strings.TrimSpace(candidate.Method))
	candidate.RequestField = strings.TrimSpace(candidate.RequestField)
	candidate.RequestFields = strings.TrimSpace(candidate.RequestFields)
	candidate.ResponsePath = strings.TrimSpace(candidate.ResponsePath)
	candidate.AgentID = strings.TrimSpace(candidate.AgentID)
	if candidate.AgentID != "" && !identifierPattern.MatchString(candidate.AgentID) {
		return Candidate{}, fmt.Errorf("agent_id must be a stable identifier")
	}
	// Input text and expected stay verbatim. They are sample case content,
	// and trimming them would change the staged dataset.
	header := strings.TrimSpace(candidate.AuthHeader)
	if strings.EqualFold(header, "none") || strings.EqualFold(header, "no") {
		header = ""
	}
	if header != "" && headerNamePattern.FindString(header) != header {
		return Candidate{}, &StatusError{Code: 2, Msg: "auth_header must be a header name, not a secret value"}
	}
	envName := strings.TrimSpace(candidate.AuthEnv)
	if envName != "" && !envNamePattern.MatchString(envName) {
		return Candidate{}, &StatusError{Code: 2, Msg: "auth_env must be an environment-variable name, not a secret value"}
	}
	candidate.AuthHeader = header
	candidate.AuthEnv = envName
	if candidate.RequestField == "" && candidate.RequestFields != "" {
		candidate.RequestField = strings.TrimSpace(strings.Split(candidate.RequestFields, ",")[0])
	}
	if len(candidate.Metrics) == 0 {
		return Candidate{}, &StatusError{Code: 2, Msg: "metrics are required; choose the metrics to stage instead of defaulting to exact match"}
	}
	for _, field := range requestFieldList(candidate.RequestField, candidate.RequestFields) {
		if inputSelector.FindString(field) != field {
			return Candidate{}, &StatusError{Code: 2, Msg: "request_field does not map to onboarding/v1. Use a selector such as text or items.0, or set target.request_template with source input."}
		}
	}
	if candidate.EndpointURL == "" || candidate.Method == "" || candidate.ResponsePath == "" || candidate.RequestField == "" {
		return Candidate{}, &StatusError{Code: 2, Msg: "endpoint_url, method, request_field, and response_path are required. Set schema_version to 'onboarding/v1' and use target.request_template, target.response_path, and environments.local.endpoint."}
	}
	if err := validateEndpoint(candidate.EndpointURL, "endpoint_url"); err != nil {
		return Candidate{}, &StatusError{Code: 2, Msg: "endpoint_url does not map to onboarding/v1. " + err.Error()}
	}
	if inputSelector.FindString(candidate.ResponsePath) != candidate.ResponsePath {
		return Candidate{}, &StatusError{Code: 2, Msg: "response_path does not map to onboarding/v1. Use a dotted or indexed selector such as output.text."}
	}
	if candidate.Method == "HEAD" {
		return Candidate{}, &StatusError{Code: 2, Msg: "method HEAD is not supported by onboarding/v1. Use GET, POST, PUT, PATCH, or DELETE."}
	}
	switch candidate.Method {
	case "GET", "POST", "PUT", "PATCH", "DELETE":
	default:
		return Candidate{}, &StatusError{Code: 2, Msg: "method must be an HTTP method"}
	}
	if candidate.AuthHeader != "" && candidate.AuthEnv == "" {
		return Candidate{}, &StatusError{Code: 2, Msg: "auth_header without auth_env does not map to onboarding/v1. Declare target.auth_headers.<Header> as {\"source\":\"env\",\"name\":\"ENV_NAME\"} or {\"source\":\"harness_secret\",\"ref\":\"REF\",\"env\":\"ENV_NAME\"}. The literal value is not accepted."}
	}
	if len(candidate.Cases) == 0 {
		return Candidate{}, fmt.Errorf("cases: missing_cases: staging requires an explicit non-empty case list; ask the user to add a sample case first")
	}
	for i, item := range candidate.Cases {
		if strings.TrimSpace(item.ID) == "" {
			return Candidate{}, &StatusError{Code: 2, Msg: fmt.Sprintf("cases[%d].id: required_case_id: case id is required", i)}
		}
		input := strings.TrimSpace(string(item.Input))
		if len(input) == 0 || !json.Valid(item.Input) {
			return Candidate{}, &StatusError{Code: 2, Msg: fmt.Sprintf("cases[%d].input: invalid_case_input: input must be valid JSON", i)}
		}
	}
	return candidate, nil
}

func applySampleCases(files map[string]string, agent string, cases []SampleCase) error {
	if len(cases) == 0 {
		return nil
	}
	var body strings.Builder
	for _, item := range cases {
		row, err := json.Marshal(item)
		if err != nil {
			return err
		}
		body.Write(row)
		body.WriteByte('\n')
	}
	files[fmt.Sprintf("datasets/%s.jsonl", agent)] = body.String()
	return nil
}

func legacyCandidateDocument(repo string, candidate Candidate) (map[string]any, error) {
	agent := candidate.AgentID
	if agent == "" {
		agent = agentFromDir(repo)
	}
	if _, err := requireIdentifier(agent, "agent_id"); err != nil {
		return nil, &StatusError{Code: 2, Msg: "agent_id does not map to onboarding/v1. Use a stable identifier, or set identifier on an onboarding/v1 document."}
	}
	fields := requestFieldList(candidate.RequestField, candidate.RequestFields)
	template := map[string]any{}
	for _, field := range fields {
		template[field] = map[string]any{"source": "input", "selector": field}
	}
	target := map[string]any{
		"method":           candidate.Method,
		"response_path":    candidate.ResponsePath,
		"request_template": template,
	}
	if candidate.AuthHeader != "" {
		target["auth_headers"] = map[string]any{
			candidate.AuthHeader: map[string]any{"source": "env", "name": candidate.AuthEnv},
		}
	}
	evalID := agent + "-eval"
	return map[string]any{
		"schema_version": SchemaVersionV1,
		"identifier":     agent,
		"name":           displayName(agent),
		"candidate": map[string]any{
			"id":         agent,
			"confidence": "high",
			"evidence": []any{map[string]any{
				"source":  "cli",
				"summary": "confirmed onboarding candidate",
			}},
		},
		"target": target,
		"environments": map[string]any{
			"local": map[string]any{"endpoint": candidate.EndpointURL, "secrets": map[string]any{}},
		},
		"evals": []any{map[string]any{
			"identifier": evalID,
			"name":       displayName(agent),
			"dataset":    "datasets/" + agent + ".jsonl",
			"metric_set": "metric-sets/" + agent + ".yaml",
		}},
		"suite": map[string]any{
			"identifier":    agent + "-suite",
			"name":          displayName(agent),
			"pass_strategy": "all_must_pass",
			"evals":         []any{evalID},
		},
		"metrics": candidate.Metrics,
	}, nil
}

func writeV1Candidate(opts WriteOptions) (int, error) {
	stdout := opts.Stdout
	if stdout == nil {
		stdout = os.Stdout
	}
	if err := validateV1Document(opts.Candidate.Wire); err != nil {
		return 2, err
	}
	cases, err := candidateCases(opts.Candidate)
	if err != nil {
		return 2, err
	}
	opts.Candidate.Cases = cases
	if len(opts.Candidate.Cases) > 0 {
		encoded, err := json.Marshal(opts.Candidate.Cases)
		if err != nil {
			return 2, err
		}
		decoded, err := decodeJSONValue(encoded)
		if err != nil {
			return 2, err
		}
		if err := scanStagedValue(decoded, "cases"); err != nil {
			return 2, err
		}
	}
	if !opts.Confirm {
		return 2, &StatusError{Code: 2, Msg: stageConfirmMessage}
	}
	if opts.Candidate.Wire["metrics"] == nil {
		return 2, fmt.Errorf("metrics are required; choose the metrics to stage instead of defaulting to exact match")
	}
	var result StageResult
	python := opts.Python
	if python == "" && opts.VerifyRunner != nil {
		layout, err := opts.VerifyRunner(opts.Repo, opts.Run)
		if err != nil {
			return 1, err
		}
		python = layout.Python
	}
	var definitions map[string]MetricDefinition
	if opts.ResolveMetrics != nil {
		definitions, err = opts.ResolveMetrics(python, opts.Run)
		if err != nil {
			return 1, err
		}
	}
	files, err := v1TreeFiles(opts.Candidate, definitions)
	if err != nil {
		return 2, err
	}
	if python != "" {
		result, err = Stage(opts.Repo, files, python, opts.Run)
	} else {
		// In-process callers may choose structural validation without a runner.
		result, err = commitFiles(filepath.Join(opts.Repo, ".harness", "evals"), files)
	}
	if err != nil {
		return 1, err
	}
	for _, rel := range result.Wrote {
		fmt.Fprintf(stdout, "wrote %s\n", rel)
	}
	for _, rel := range result.Identical {
		fmt.Fprintf(stdout, "identical %s\n", rel)
	}
	return 0, nil
}

func v1TreeFiles(candidate Candidate, definitions map[string]MetricDefinition) (map[string]string, error) {
	doc, ok := cloneValue(candidate.Wire).(map[string]any)
	if !ok {
		return nil, fmt.Errorf("candidate file must be a JSON object")
	}
	rawMetrics, hasMetrics := doc["metrics"]
	delete(doc, "metrics")
	delete(doc, "cases")
	agent, _ := doc["identifier"].(string)
	encoded, err := yaml.Marshal(doc)
	if err != nil {
		return nil, err
	}
	files := map[string]string{fmt.Sprintf("agents/%s/agent.yaml", agent): string(encoded)}
	body, err := datasetBody(candidate.Cases, candidate.Wire)
	if err != nil {
		return nil, err
	}
	for _, rel := range datasetRefs(doc) {
		if err := addGeneratedTreeFile(files, rel, body, "dataset"); err != nil {
			return nil, err
		}
	}
	if hasMetrics && rawMetrics != nil {
		metricBody, err := metricSetBody(rawMetrics, agent, definitions)
		if err != nil {
			return nil, err
		}
		for _, rel := range metricSetRefs(doc) {
			if err := addGeneratedTreeFile(files, rel, metricBody, "metric_set"); err != nil {
				return nil, err
			}
		}
	}
	return files, nil
}

func addGeneratedTreeFile(files map[string]string, rel, body, field string) error {
	if err := relativeTreePath(rel); err != nil {
		return err
	}
	// Check the same path that will be written, including aliases with ./.
	path := filepath.ToSlash(filepath.Clean(rel))
	if _, exists := files[path]; exists {
		return fmt.Errorf("generated %s path conflicts with another tree file", field)
	}
	files[path] = body
	return nil
}

func datasetBody(cases []SampleCase, doc map[string]any) (string, error) {
	if len(cases) == 0 {
		return "", fmt.Errorf("cases: missing_cases: staging requires an explicit non-empty case list; ask the user to add a sample case first")
	}
	var body strings.Builder
	for _, item := range cases {
		if strings.TrimSpace(item.ID) == "" {
			return "", fmt.Errorf("case id is required")
		}
		row, err := json.Marshal(item)
		if err != nil {
			return "", err
		}
		body.Write(row)
		body.WriteByte('\n')
	}
	return body.String(), nil
}

func candidateCases(candidate Candidate) ([]SampleCase, error) {
	if candidate.Cases != nil {
		if len(candidate.Cases) == 0 {
			return nil, fmt.Errorf("cases: empty_cases: staging requires an explicit non-empty case list; ask the user to add a sample case first")
		}
		return candidate.Cases, nil
	}
	raw, ok := candidate.Wire["cases"]
	if !ok || raw == nil {
		return nil, fmt.Errorf("cases: missing_cases: staging requires an explicit non-empty case list; ask the user to add a sample case first")
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		return nil, fmt.Errorf("cases must be a list")
	}
	var cases []SampleCase
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&cases); err != nil {
		return nil, fmt.Errorf("candidate file does not match the onboarding candidate schema")
	}
	if len(cases) == 0 {
		return nil, fmt.Errorf("cases: empty_cases: staging requires an explicit non-empty case list; ask the user to add a sample case first")
	}
	return cases, nil
}

func sampleCasesValue(cases []SampleCase) (any, error) {
	encoded, err := json.Marshal(cases)
	if err != nil {
		return nil, err
	}
	return decodeJSONValue(encoded)
}

func datasetRefs(doc map[string]any) []string {
	return evalRefs(doc, "dataset")
}

func metricSetRefs(doc map[string]any) []string {
	return evalRefs(doc, "metric_set")
}

func metricSetBody(raw any, agent string, definitions map[string]MetricDefinition) (string, error) {
	metrics, ok := raw.([]any)
	if !ok || len(metrics) == 0 {
		return "", fmt.Errorf("metrics: empty_metrics: staging requires an explicit non-empty metric list; choose metrics before staging")
	}
	entries := make([]any, 0, len(metrics))
	for index, rawMetric := range metrics {
		metric, ok := rawMetric.(map[string]any)
		if !ok {
			return "", fmt.Errorf("metrics[%d] must be a mapping", index)
		}
		kind, _ := metric["kind"].(string)
		if strings.TrimSpace(kind) == "" {
			return "", fmt.Errorf("metrics[%d].kind is required", index)
		}
		metricDefinition := map[string]any{
			"name": kind,
			"kind": kind,
		}
		if definitions != nil {
			definition, ok := definitions[kind]
			if !ok || !definition.Supported {
				return "", fmt.Errorf("metrics[%d].kind is not supported by the pinned runner", index)
			}
			metricDefinition["type"] = definition.Type
			if definition.Dimension != "" {
				metricDefinition["dimension"] = definition.Dimension
			}
		}
		entry := map[string]any{"metric": metricDefinition}
		// The candidate is flat, but the pinned loader expects options inside the entry config.
		config := map[string]any{}
		if fields, ok := metric["config"].(map[string]any); ok {
			for key, value := range fields {
				config[key] = value
			}
		}
		if _, exists := config["kind"]; exists {
			return "", fmt.Errorf("metrics[%d].config.kind cannot override the selected kind", index)
		}
		if options, ok := metric["options"].(map[string]any); ok {
			mergedOptions := map[string]any{}
			if nested, ok := config["options"].(map[string]any); ok {
				for key, value := range nested {
					mergedOptions[key] = value
				}
			}
			for key, value := range options {
				mergedOptions[key] = value
			}
			config["options"] = mergedOptions
		}
		if len(config) != 0 {
			entry["config"] = config
		}
		for _, key := range []string{"threshold", "score_name"} {
			if value, ok := metric[key]; ok {
				entry[key] = value
			}
		}
		entries = append(entries, entry)
	}
	data := map[string]any{
		"identifier": agent + "-metrics",
		"name":       displayName(agent) + " Metrics",
		"entries":    entries,
	}
	encoded, err := yaml.Marshal(data)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

func evalRefs(doc map[string]any, field string) []string {
	evals, _ := doc["evals"].([]any)
	var refs []string
	seen := map[string]bool{}
	for _, item := range evals {
		body, _ := item.(map[string]any)
		rel, _ := body[field].(string)
		if rel == "" || seen[rel] {
			continue
		}
		seen[rel] = true
		refs = append(refs, rel)
	}
	return refs
}

func relativeTreePath(rel string) error {
	clean := filepath.Clean(rel)
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) || filepath.IsAbs(clean) || strings.Contains(rel, "\\") {
		return fmt.Errorf("candidate path escapes the tree: %s", rel)
	}
	return nil
}

func legacyOverrideSet(override Candidate) bool {
	return override.AgentID != "" || override.EndpointURL != "" || override.Method != "" ||
		override.RequestField != "" || override.RequestFields != "" || override.ResponsePath != "" ||
		override.AuthHeader != "" || override.AuthEnv != "" || override.InputText != "" || override.Expected != ""
}
