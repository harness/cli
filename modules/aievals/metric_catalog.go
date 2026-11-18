// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package aievals

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"strings"
)

//go:embed metric_catalog.py
var metricCatalogSnippet string

type MetricCatalogEntry struct {
	Kind              string         `json:"kind"`
	Name              string         `json:"name"`
	Dimension         string         `json:"dimension"`
	Category          string         `json:"category"`
	DefaultThreshold  *float64       `json:"default_threshold"`
	RequiresLLM       bool           `json:"requires_llm"`
	RequiresEmbedding bool           `json:"requires_embedding"`
	RequiresProvider  bool           `json:"requires_provider"`
	Description       string         `json:"description"`
	Implementation    string         `json:"implementation"`
	ExecutionScope    string         `json:"execution_scope"`
	OptionsSchema     map[string]any `json:"options_schema"`
	MetricType        string         `json:"type"`
	ReviewedContract  bool           `json:"configured_contract_supported"`
	EvalInputs        MetricInputs   `json:"eval_inputs"`
	Applicability     MetricModes    `json:"applicability"`
	Supported         bool           `json:"supported"`
	UnsupportedReason string         `json:"unsupported_reason,omitempty"`
}

// MetricInputs is SDK-declared evidence, not a claim that a candidate has it.
// The advisor intersects it with authored cases before recommending a metric.
type MetricInputs struct {
	Required []string   `json:"required"`
	Optional []string   `json:"optional"`
	AnyOf    [][]string `json:"any_of"`
}

// MetricModes is discovery metadata. It avoids recommending online-only modes
// for the offline local runner without constructing providers.
type MetricModes struct {
	Modes        []string `json:"modes"`
	OnlineScopes []string `json:"online_scopes"`
}

type MetricCatalogReport struct {
	Schema  string               `json:"schema"`
	Source  string               `json:"source"`
	Metrics []MetricCatalogEntry `json:"metrics"`
}

type MetricSelection struct {
	Kind      string         `json:"kind"`
	Threshold *float64       `json:"threshold,omitempty"`
	ScoreName string         `json:"score_name,omitempty"`
	Options   map[string]any `json:"options,omitempty"`
	Config    map[string]any `json:"config,omitempty"`
}

func rejectMetricSecrets(data []byte) error {
	var value any
	if err := json.Unmarshal(data, &value); err != nil {
		return fmt.Errorf("metric configuration must be valid JSON")
	}
	var walk func(any) error
	walk = func(node any) error {
		switch node := node.(type) {
		case map[string]any:
			for key, value := range node {
				normalized := strings.ToLower(strings.NewReplacer("_", "", "-", "").Replace(key))
				switch normalized {
				case "secret", "secrets", "token", "accesstoken", "refreshtoken", "apikey", "embeddingapikey", "password", "credential", "credentials", "clientsecret", "authorization", "cookie", "privatekey", "modulepath", "llm", "embedding":
					return fmt.Errorf("metric configuration contains a credential or runtime-only field; use provider environment variables, never secret values or code modules")
				}
				if err := walk(value); err != nil {
					return err
				}
			}
		case []any:
			for _, value := range node {
				if err := walk(value); err != nil {
					return err
				}
			}
		}
		return nil
	}
	return walk(value)
}

func ReadMetricCatalog(python string, run CommandFunc) ([]MetricCatalogEntry, error) {
	if run == nil {
		run = defaultCommand
	}
	result, err := run(python, []string{"-I", "-c", metricCatalogSnippet}, nil, "")
	if err != nil {
		return nil, err
	}
	if result.Code != 0 {
		return nil, fmt.Errorf("could not read Harness Evals metric catalog (exit %d)", result.Code)
	}
	var entries []MetricCatalogEntry
	if err := json.Unmarshal([]byte(result.Stdout), &entries); err != nil {
		return nil, fmt.Errorf("could not decode Harness Evals metric catalog: %w", err)
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("Harness Evals SDK returned an empty metric catalog")
	}
	return entries, nil
}

func MetricCatalog(home string, run CommandFunc) (MetricCatalogReport, error) {
	layout, err := VerifyRunner(home, run)
	if err != nil {
		return MetricCatalogReport{}, err
	}
	entries, err := ReadMetricCatalog(layout.Python, run)
	if err != nil {
		return MetricCatalogReport{}, err
	}
	return MetricCatalogReport{Schema: SchemaVersion, Source: "harness_evals.catalog", Metrics: entries}, nil
}

func applySelectedMetrics(files map[string]string, agent string, selections []MetricSelection, catalog []MetricCatalogEntry) error {
	byKind := make(map[string]MetricCatalogEntry, len(catalog))
	for _, entry := range catalog {
		byKind[entry.Kind] = entry
	}
	var body strings.Builder
	fmt.Fprintf(&body, "identifier: %s\nname: %s\nentries:\n", yamlString(agent), yamlString(displayName(agent)))
	seen := map[string]bool{}
	for _, selection := range selections {
		entry, ok := byKind[selection.Kind]
		if !ok {
			return fmt.Errorf("metric kind %q is not in the installed SDK catalog; run harness scan eval_onboarding --metrics", selection.Kind)
		}
		if !entry.Supported {
			return fmt.Errorf("metric %q is unavailable for onboarding: %s", entry.Kind, entry.UnsupportedReason)
		}
		score := selection.ScoreName
		if score == "" {
			score = entry.Kind
		}
		if !identifierPattern.MatchString(score) || seen[score] {
			return fmt.Errorf("metric score_name must be a unique stable identifier")
		}
		seen[score] = true
		threshold := selection.Threshold
		if threshold == nil {
			threshold = entry.DefaultThreshold
		}
		if threshold == nil || math.IsNaN(*threshold) || math.IsInf(*threshold, 0) {
			return fmt.Errorf("metric %q needs a finite threshold", entry.Kind)
		}
		config := map[string]any{}
		for key, value := range selection.Config {
			switch key {
			case "metadata":
				if err := validateProviderMetadata(value); err != nil {
					return err
				}
				config[key] = value
			case "criteria", "rubric", "prompt_instructions", "allowed_topics", "role_description":
				config[key] = value
			default:
				return fmt.Errorf("metric config key %q is not supported; use options for constructor arguments", key)
			}
		}
		if len(selection.Options) > 0 {
			config["options"] = selection.Options
		}
		encoded, err := json.Marshal(config)
		if err != nil {
			return fmt.Errorf("metric config must be JSON-serializable")
		}
		if err := rejectMetricSecrets(encoded); err != nil {
			return err
		}
		fmt.Fprintf(&body, "  - metric:\n      identifier: %s\n      name: %s\n      kind: %s\n      type: %s\n      dimension: %s\n    threshold: %s\n    score_name: %s\n    config: %s\n",
			yamlString(score), yamlString(entry.Name), yamlString(entry.Kind), yamlString(entry.MetricType), yamlString(entry.Dimension), fmt.Sprint(*threshold), yamlString(score), encoded)
	}
	files[fmt.Sprintf("metric-sets/%s.yaml", agent)] = body.String()
	return nil
}

func validateConstructorOptions(python string, run CommandFunc, entry MetricCatalogEntry, selection MetricSelection) error {
	if run == nil {
		run = defaultCommand
	}
	options := map[string]any{}
	for key, value := range selection.Options {
		options[key] = value
	}
	for _, key := range []string{"criteria", "rubric", "prompt_instructions", "allowed_topics", "role_description"} {
		if value, ok := selection.Config[key]; ok {
			options[key] = value
		}
	}
	schema, err := json.Marshal(entry.OptionsSchema)
	if err != nil {
		return fmt.Errorf("invalid or missing constructor options for metric %s", entry.Kind)
	}
	instance, err := json.Marshal(options)
	if err != nil {
		return fmt.Errorf("invalid or missing constructor options for metric %s", entry.Kind)
	}
	snippet := "import json,sys\nfrom jsonschema import Draft202012Validator\nschema=json.loads(sys.argv[1])\ninstance=json.loads(sys.argv[2])\nerr=next(Draft202012Validator(schema).iter_errors(instance), None)\nif err:\n    raise SystemExit('invalid or missing constructor options for metric')\n"
	result, err := run(python, []string{"-I", "-c", snippet, string(schema), string(instance)}, nil, "")
	if err != nil {
		return err
	}
	if result.Code != 0 {
		return fmt.Errorf("invalid or missing constructor options for metric %s", entry.Kind)
	}
	return nil
}

func validateProviderMetadata(value any) error {
	metadata, ok := value.(map[string]any)
	if !ok {
		return fmt.Errorf("metric config.metadata must be an object of non-secret provider settings")
	}
	for key, value := range metadata {
		switch key {
		case "provider", "model", "embedding_model", "region", "llm_gateway_path", "x_source", "x_source_class":
			if _, ok := value.(string); !ok {
				return fmt.Errorf("metric provider setting %q must be a string", key)
			}
		case "temperature", "max_tokens":
			switch value.(type) {
			case float64, float32, int, int64, json.Number:
			default:
				return fmt.Errorf("metric provider setting %q must be a number", key)
			}
		case "bedrock", "use_llm_gateway":
			if _, ok := value.(bool); !ok {
				return fmt.Errorf("metric provider setting %q must be a boolean", key)
			}
		case "base_url":
			raw, ok := value.(string)
			parsed, err := url.Parse(raw)
			if !ok || err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Hostname() == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
				return fmt.Errorf("metric provider base_url must be an HTTP(S) URL without credentials, query parameters, or fragments")
			}
		default:
			return fmt.Errorf("metric provider setting %q is not supported; credentials must come from the provider environment", key)
		}
	}
	return nil
}
