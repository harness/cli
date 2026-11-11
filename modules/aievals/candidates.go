// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package aievals

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
)

// ScanCandidate is one optional scanner hint. It is JSON for the coding agent.
// Secret values are never included; auth_env is an environment-variable name.
type ScanCandidate struct {
	Identity            string   `json:"identity"`
	Method              string   `json:"method"`
	Path                string   `json:"path"`
	Port                string   `json:"port,omitempty"`
	Endpoint            string   `json:"endpoint,omitempty"`
	RequestField        string   `json:"request_field,omitempty"`
	RequestFields       string   `json:"request_fields,omitempty"`
	ResponsePath        string   `json:"response_path,omitempty"`
	AuthHeader          string   `json:"auth_header,omitempty"`
	AuthEnv             string   `json:"auth_env,omitempty"`
	AgentID             string   `json:"agent_id,omitempty"`
	ServiceRoot         string   `json:"service_root,omitempty"`
	Source              string   `json:"source,omitempty"`
	Adapter             string   `json:"adapter,omitempty"`
	InputText           string   `json:"input_text,omitempty"`
	Expected            string   `json:"expected,omitempty"`
	RequiredQueryParams string   `json:"required_query_params,omitempty"`
	RequiredPathParams  string   `json:"required_path_params,omitempty"`
	Defaults            []string `json:"defaults,omitempty"`
	Notes               []string `json:"notes,omitempty"`
}

// ScanReport is the stdout of `harness scan eval_onboarding`.
type ScanReport struct {
	Schema     string          `json:"schema"`
	HasSignal  bool            `json:"has_signal"`
	Notes      []string        `json:"notes,omitempty"`
	Candidates []ScanCandidate `json:"candidates"`
}

// SampleCase is one dataset row the agent already confirmed with the user.
type SampleCase struct {
	ID                string           `json:"id"`
	Input             json.RawMessage  `json:"input"`
	Expected          any              `json:"expected,omitempty"`
	Context           any              `json:"context,omitempty"`
	ExpectedTools     []string         `json:"expected_tools,omitempty"`
	ExpectedToolCalls []map[string]any `json:"expected_tool_calls,omitempty"`
	Tags              []string         `json:"tags,omitempty"`
	Metadata          map[string]any   `json:"metadata,omitempty"`
}

// Candidate is the validate-and-write input. Flags and a JSON file use the same fields.
type Candidate struct {
	AgentID       string            `json:"agent_id,omitempty"`
	EndpointURL   string            `json:"endpoint_url,omitempty"`
	Method        string            `json:"method,omitempty"`
	RequestField  string            `json:"request_field,omitempty"`
	RequestFields string            `json:"request_fields,omitempty"`
	ResponsePath  string            `json:"response_path,omitempty"`
	AuthHeader    string            `json:"auth_header,omitempty"`
	AuthEnv       string            `json:"auth_env,omitempty"`
	InputText     string            `json:"input_text,omitempty"`
	Expected      string            `json:"expected,omitempty"`
	Cases         []SampleCase      `json:"cases,omitempty"`
	Metrics       []MetricSelection `json:"metrics,omitempty"`
}

// WriteOptions validates a candidate and writes the .harness/evals tree.
type WriteOptions struct {
	Repo      string
	Candidate Candidate
	Python    string
	Run       CommandFunc
	Stdout    io.Writer
	Stderr    io.Writer
}

var envNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
var identifierPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

var forbiddenCandidateKeys = map[string]bool{
	"secret": true, "token": true, "api_key": true, "apikey": true,
	"password": true, "auth_value": true, "credential": true, "api_key_value": true,
}

// Scan prints nothing and writes nothing. A route that is not an exact supported
// hint returns exit code 2. An empty repository returns an empty candidate list.
func Scan(repo, route string) (ScanReport, int, error) {
	inf, err := Infer(repo)
	if err != nil {
		return ScanReport{}, 1, err
	}
	report := ScanReport{Schema: SchemaVersion, Candidates: []ScanCandidate{}}
	if len(inf.Notes) > 0 {
		report.Notes = append([]string{}, inf.Notes...)
	}
	chosen := inf.RouteCandidates
	if strings.TrimSpace(route) != "" {
		match, ok := selectAdapterRoute(inf.RouteCandidates, route)
		if !ok {
			return report, 2, &StatusError{Code: 2, Msg: routeSelectionError(route, inf.RouteCandidates)}
		}
		chosen = []RouteCandidate{match}
	}
	for _, candidate := range chosen {
		detailed, err := InferWithRoute(repo, candidate.Identity())
		if err != nil {
			return ScanReport{}, 1, err
		}
		report.Candidates = append(report.Candidates, scanCandidateFrom(candidate, detailed))
	}
	report.HasSignal = len(report.Candidates) > 0
	return report, 0, nil
}

func routeSelectionError(route string, candidates []RouteCandidate) string {
	msg := fmt.Sprintf("route %q is not a supported AST-detected route and cannot be selected with --route; use guided/manual configuration instead", route)
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
	out := ScanCandidate{
		Identity:    candidate.Identity(),
		Method:      candidate.Method,
		Path:        candidate.Path,
		ServiceRoot: candidate.ServiceRoot,
		Source:      candidate.Source,
		Adapter:     candidate.Adapter,
		AgentID:     inf.AgentID,
	}
	if out.AgentID == "" {
		out.AgentID = candidate.Agent
	}
	var defaults []string
	set := func(field string, dest *string) {
		ev, ok := inf.ByField(field)
		if !ok || ev.Value == "" {
			return
		}
		*dest = ev.Value
		if ev.Source == "default" {
			defaults = append(defaults, field)
		}
	}
	set("method", &out.Method)
	set("endpoint_path", &out.Path)
	set("port", &out.Port)
	set("request_field", &out.RequestField)
	set("request_fields", &out.RequestFields)
	set("response_path", &out.ResponsePath)
	set("auth_header", &out.AuthHeader)
	set("auth_env", &out.AuthEnv)
	set("agent_id", &out.AgentID)
	set("test_input_text", &out.InputText)
	set("required_query_params", &out.RequiredQueryParams)
	set("required_path_params", &out.RequiredPathParams)
	if ev, ok := inf.ByField("test_expected"); ok && ev.Source != "gap" {
		out.Expected = ev.Value
	}
	if out.Port != "" && out.Path != "" {
		out.Endpoint = fmt.Sprintf("http://127.0.0.1:%s%s", out.Port, ensureSlash(out.Path))
	}
	if len(defaults) > 0 {
		out.Defaults = defaults
	}
	if len(inf.Notes) > 0 {
		out.Notes = append([]string{}, inf.Notes...)
	}
	return out
}

// AsCandidate converts a scan hint into validate-and-write input.
func (c ScanCandidate) AsCandidate() Candidate {
	return Candidate{
		AgentID:       c.AgentID,
		EndpointURL:   c.Endpoint,
		Method:        c.Method,
		RequestField:  c.RequestField,
		RequestFields: c.RequestFields,
		ResponsePath:  c.ResponsePath,
		AuthHeader:    c.AuthHeader,
		AuthEnv:       c.AuthEnv,
		InputText:     c.InputText,
		Expected:      c.Expected,
	}
}

// LoadCandidateFile reads the temporary JSON file the agent writes.
// Forbidden keys are rejected so a secret value cannot ride along.
func LoadCandidateFile(path string) (Candidate, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Candidate{}, err
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return Candidate{}, fmt.Errorf("candidate file: %w", err)
	}
	for key := range raw {
		if forbiddenCandidateKeys[strings.ToLower(key)] {
			return Candidate{}, fmt.Errorf("candidate file contains %q; pass secret names only, never secret values", key)
		}
	}
	var candidate Candidate
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.UseNumber()
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&candidate); err != nil {
		return Candidate{}, fmt.Errorf("candidate file does not match the onboarding candidate schema")
	}
	if metrics, ok := raw["metrics"]; ok {
		if string(metrics) == "null" {
			return Candidate{}, fmt.Errorf("metrics must be a non-empty list when provided")
		}
		if err := rejectMetricSecrets(metrics); err != nil {
			return Candidate{}, err
		}
	}
	return candidate, nil
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
	if override.Metrics != nil {
		c.Metrics = override.Metrics
	}
	return c
}

// WriteCandidate validates the candidate, runs the pinned onboarding validator,
// and writes only a tree the validator accepts.
func WriteCandidate(opts WriteOptions) (int, error) {
	stdout := opts.Stdout
	if stdout == nil {
		stdout = os.Stdout
	}
	stderr := opts.Stderr
	if stderr == nil {
		stderr = os.Stderr
	}
	candidate, err := normalizeCandidate(opts.Candidate)
	if err != nil {
		return 2, err
	}
	if candidate.AuthHeader != "" && candidate.AuthEnv == "" {
		fmt.Fprintln(stderr, "Auth header name was omitted from the tree because no environment-variable name was given. Pass --auth-env with the variable name, never the secret value.")
	}
	inf := candidateInference(opts.Repo, candidate)
	files := ApplyFiles(inf, true, nil)
	if len(files) == 0 {
		return 2, &StatusError{Code: 2, Msg: "candidate did not produce a tree"}
	}
	if err := applySampleCases(files, inf.AgentID, candidate.Cases); err != nil {
		return 2, err
	}
	python := opts.Python
	if python == "" {
		layout, err := EnsureRunner(RunnerHome(), nil, opts.Run)
		if err != nil {
			return 1, err
		}
		python = layout.Python
	}
	if len(candidate.Metrics) > 0 {
		catalog, err := ReadMetricCatalog(python, opts.Run)
		if err != nil {
			return 1, err
		}
		if err := applySelectedMetrics(files, inf.AgentID, candidate.Metrics, catalog); err != nil {
			return 2, err
		}
	}
	result, err := Stage(opts.Repo, files, python, opts.Run)
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
	if candidate.Metrics != nil && len(candidate.Metrics) == 0 {
		return Candidate{}, fmt.Errorf("metrics must be a non-empty list when provided")
	}
	if len(candidate.Metrics) > 0 {
		encoded, err := json.Marshal(candidate.Metrics)
		if err != nil {
			return Candidate{}, fmt.Errorf("metrics must be JSON-serializable")
		}
		if err := rejectMetricSecrets(encoded); err != nil {
			return Candidate{}, err
		}
	}
	// Input text and expected stay verbatim. They are sample case content,
	// and trimming them would change the staged dataset.
	header := strings.TrimSpace(candidate.AuthHeader)
	if strings.EqualFold(header, "none") || strings.EqualFold(header, "no") {
		header = ""
	}
	if header != "" && (strings.Contains(header, " ") || strings.Contains(header, ":")) {
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
	if candidate.EndpointURL == "" || candidate.Method == "" || candidate.ResponsePath == "" || candidate.RequestField == "" {
		return Candidate{}, &StatusError{Code: 2, Msg: "endpoint_url, method, request_field, and response_path are required"}
	}
	switch candidate.Method {
	case "GET", "POST", "PUT", "PATCH", "DELETE", "HEAD":
	default:
		return Candidate{}, &StatusError{Code: 2, Msg: "method must be an HTTP method"}
	}
	for i, item := range candidate.Cases {
		if strings.TrimSpace(item.ID) == "" {
			return Candidate{}, &StatusError{Code: 2, Msg: fmt.Sprintf("case %d id is required", i+1)}
		}
		input := strings.TrimSpace(string(item.Input))
		if len(input) == 0 || !json.Valid(item.Input) || input[0] != '{' {
			return Candidate{}, &StatusError{Code: 2, Msg: fmt.Sprintf("case %s input must be a JSON object", item.ID)}
		}
	}
	return candidate, nil
}

func candidateInference(repo string, candidate Candidate) Inference {
	var ev []FieldEvidence
	port, path := splitEndpointURL(candidate.EndpointURL)
	if path == "" {
		path = "/"
	}
	if port == "" {
		port = "8080"
	}
	add(&ev, "endpoint_path", path, "candidate", ConfidenceHigh)
	add(&ev, "endpoint_url", candidate.EndpointURL, "candidate", ConfidenceHigh)
	add(&ev, "port", port, "candidate", ConfidenceHigh)
	add(&ev, "method", candidate.Method, "candidate", ConfidenceHigh)
	add(&ev, "request_field", candidate.RequestField, "candidate", ConfidenceHigh)
	if candidate.RequestFields != "" {
		add(&ev, "request_fields", candidate.RequestFields, "candidate", ConfidenceHigh)
	}
	add(&ev, "response_path", candidate.ResponsePath, "candidate", ConfidenceHigh)
	if candidate.InputText != "" {
		add(&ev, "test_input_text", candidate.InputText, "candidate", ConfidenceHigh)
	}
	if candidate.Expected != "" {
		add(&ev, "test_expected", candidate.Expected, "candidate", ConfidenceHigh)
	} else {
		gap := "expected value was not evidenced for " + candidate.ResponsePath
		add(&ev, "expected_gap", gap, "gap", ConfidenceHigh)
	}
	if candidate.AuthHeader != "" {
		add(&ev, "auth_header", canonicalHeader(candidate.AuthHeader), "candidate", ConfidenceHigh)
	}
	if candidate.AuthEnv != "" {
		add(&ev, "auth_env", candidate.AuthEnv, "candidate", ConfidenceHigh)
	}
	agent := candidate.AgentID
	if agent == "" {
		agent = agentFromDir(repo)
	}
	add(&ev, "agent_id", agent, "candidate", ConfidenceHigh)
	return Inference{Language: "candidate", AgentID: agent, Evidence: ev, HasSignal: true}
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
