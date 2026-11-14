// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package pipeline

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/harness/cli/v3/pkg/client"
	"github.com/harness/cli/v3/pkg/cmdctx"
	"github.com/harness/cli/v3/pkg/hlog"
	"github.com/harness/cli/v3/pkg/logstream"
)

const executePipelineBodyFnID = "execute_body"
const executeInputSetBodyFnID = "execute_inputset_body"
const executeDynamicBodyFnID = "execute_dynamic_body"
const executeRetryBodyFnID = "execute_retry_body"

// executePipelineBody builds the runtimeInputYaml request body for pipeline execution.
// --input-file <file>: use pre-filled YAML directly.
// --input key=val: fetch the pipeline's runtime input template, substitute values, send result.
func executePipelineBody(ctx *cmdctx.Ctx) (any, error) {
	inputFile := cmdctx.GetString(ctx.FlagValues, "input-file")
	inputPairs := cmdctx.GetStringSlice(ctx.FlagValues, "input")
	inputSetIDs := cmdctx.GetStringSlice(ctx.FlagValues, "input-set")
	if len(inputSetIDs) > 0 {
		hlog.Debug("execute pipeline: input-set flags provided", "inputSetIDs", inputSetIDs)
		body, err := executeInputSetBody(ctx)
		if err != nil {
			return nil, err
		}
		return body, nil
	}
	inputs, err := parseKeyValuePairs(inputPairs)
	if err != nil {
		return nil, err
	}

	if inputFile == "" && len(inputs) == 0 && len(inputSetIDs) == 0 {
		hlog.Debug("execute pipeline: no runtime inputs provided")
		return nil, nil
	}

	var yamlStr string

	if inputFile != "" {
		// User provided a pre-filled runtimeInputYaml file — use it directly.
		yamlStr, err = readInputFile(inputFile)
		if err != nil {
			return nil, err
		}
		if len(inputs) > 0 {
			// Merge --input overrides on top of the file.
			yamlStr, err = mergeIntoTemplate(yamlStr, inputs)
			if err != nil {
				return nil, err
			}
		}
	} else {
		// Fetch the pipeline's runtime input template and substitute --input values.
		yamlStr, err = resolveFromTemplate(ctx, inputs)
		if err != nil {
			return nil, err
		}
	}

	hlog.Debug("execute pipeline runtimeInputYaml:\n" + yamlStr)
	return &cmdctx.RawBody{ContentType: "application/yaml", Content: yamlStr}, nil
}

// resolveFromTemplate fetches the pipeline's runtime input template and substitutes inputs.
// The pipeline id is taken from ctx.Id, which is correct for commands whose id is the
// pipeline itself (e.g. execute pipeline). Commands with a compound id such as
// "<pipeline>/<execution-id>" must call resolveFromTemplateForPipeline directly.
func resolveFromTemplate(ctx *cmdctx.Ctx, inputs map[string]string) (string, error) {
	return resolveFromTemplateForPipeline(ctx, ctx.Id, inputs)
}

// resolveFromTemplateForPipeline fetches the given pipeline's runtime input template and
// substitutes inputs. The pipeline id is passed explicitly so callers with a compound id
// (e.g. retry, which is invoked as "<pipeline>/<execution-id>") can supply the correct value.
func resolveFromTemplateForPipeline(ctx *cmdctx.Ctx, pipelineID string, inputs map[string]string) (string, error) {
	c := client.New(ctx)
	params := map[string]string{
		"pipelineIdentifier": pipelineID,
		"orgIdentifier":      ctx.Auth.OrgID,
		"projectIdentifier":  ctx.Auth.ProjectID,
	}
	branch := cmdctx.GetString(ctx.FlagValues, "branch")
	if branch != "" {
		params["branch"] = branch
	}

	hlog.Debug("fetching runtime input template", "pipeline", pipelineID)
	raw, _, err := c.Post("/pipeline/api/inputSets/template", params, map[string]any{})
	if err != nil {
		return "", fmt.Errorf("fetching runtime input template: %w", err)
	}

	templateYaml := extractTemplateYaml(raw)
	if templateYaml == "" {
		if len(inputs) > 0 {
			return "", fmt.Errorf("pipeline has no runtime inputs, but --input was given")
		}
		hlog.Debug("pipeline has no runtime inputs")
		return "", nil
	}
	hlog.Debug("runtime input template:\n" + templateYaml)

	return mergeIntoTemplate(templateYaml, inputs)
}

// extractTemplateYaml pulls data.inputSetTemplateYaml out of the API response.
func extractTemplateYaml(raw any) string {
	m, ok := raw.(map[string]any)
	if !ok {
		return ""
	}
	data, ok := m["data"].(map[string]any)
	if !ok {
		return ""
	}
	s, _ := data["inputSetTemplateYaml"].(string)
	return strings.TrimSpace(s)
}

// mergeIntoTemplate substitutes user key=val pairs into a YAML template that contains
// <+input> placeholders. Any placeholder whose leaf key or sibling "name" field matches
// a user key is replaced with that value. Unmatched placeholders are left as-is; a user
// key that replaced nothing is an error.
func mergeIntoTemplate(templateYaml string, inputs map[string]string) (string, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(templateYaml), &doc); err != nil {
		return "", fmt.Errorf("parsing runtime input template: %w", err)
	}

	m := &inputMerger{
		inputs: make(map[string]string, len(inputs)),
		used:   map[string]bool{},
		fixed:  map[string]bool{},
		stages: map[string]bool{},
	}
	for k, v := range inputs {
		m.inputs[strings.ToLower(k)] = v
	}

	m.walk(&doc)

	if err := m.unmatchedError(inputs); err != nil {
		return "", err
	}

	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(&doc); err != nil {
		return "", fmt.Errorf("serializing runtimeInputYaml: %w", err)
	}
	return buf.String(), nil
}

// inputMerger substitutes <+input> placeholders and records which user keys were used.
// fixed holds keys and variable names whose value is not a placeholder. Inside a stage
// both maps also carry the "<stage>.<key>" form, which is how scoped --input keys are matched.
type inputMerger struct {
	inputs map[string]string
	used   map[string]bool
	fixed  map[string]bool
	stages map[string]bool
	stage  string
}

// walk replaces <+input> scalar values. For mapping nodes it checks both the key name
// and (for variable-style entries) the sibling "name" field.
func (m *inputMerger) walk(node *yaml.Node) {
	if node == nil {
		return
	}
	switch node.Kind {
	case yaml.DocumentNode, yaml.SequenceNode:
		for _, child := range node.Content {
			m.walk(child)
		}
	case yaml.MappingNode:
		keys := make(map[string]int, len(node.Content)/2)
		for i := 0; i+1 < len(node.Content); i += 2 {
			keys[node.Content[i].Value] = i + 1
		}
		for i := 0; i+1 < len(node.Content); i += 2 {
			keyNode := node.Content[i]
			valNode := node.Content[i+1]
			leafKey := strings.ToLower(keyNode.Value)
			siblingName := ""
			if leafKey == "value" || leafKey == "default" {
				if nameIdx, ok := keys["name"]; ok {
					siblingName = strings.ToLower(node.Content[nameIdx].Value)
				}
			}
			if !isInputPlaceholder(valNode) {
				if valNode.Kind == yaml.ScalarNode {
					m.markFixed(leafKey)
					m.markFixed(siblingName)
				}
				m.walkChild(keyNode, valNode)
				continue
			}
			if m.replace(valNode, siblingName, leafKey) {
				continue
			}
			m.walk(valNode)
		}
	}
}

// walkChild walks val, scoping everything under a "stage:" mapping to its identifier.
func (m *inputMerger) walkChild(key, val *yaml.Node) {
	if key.Value != "stage" || val.Kind != yaml.MappingNode {
		m.walk(val)
		return
	}
	prev := m.stage
	for i := 0; i+1 < len(val.Content); i += 2 {
		if val.Content[i].Value == "identifier" {
			m.stage = strings.ToLower(val.Content[i+1].Value)
			m.stages[m.stage] = true
		}
	}
	m.walk(val)
	m.stage = prev
}

func (m *inputMerger) markFixed(key string) {
	if key == "" {
		return
	}
	m.fixed[key] = true
	if m.stage != "" {
		m.fixed[m.stage+"."+key] = true
	}
}

// replace sets node to the user value for the first of keys that has one and reports
// whether one was found. Inside a stage every scoped "<stage>.<key>" form is tried
// before any unscoped key.
func (m *inputMerger) replace(node *yaml.Node, keys ...string) bool {
	var candidates []string
	for _, k := range keys {
		if k != "" && m.stage != "" {
			candidates = append(candidates, m.stage+"."+k)
		}
	}
	for _, k := range keys {
		if k != "" {
			candidates = append(candidates, k)
		}
	}
	for _, c := range candidates {
		if v, ok := m.inputs[c]; ok {
			node.Value = v
			node.Tag = "!!str"
			m.used[c] = true
			return true
		}
	}
	return false
}

// unmatchedError reports user keys that replaced nothing, in the order given by sorted key.
func (m *inputMerger) unmatchedError(inputs map[string]string) error {
	var errs []error
	for _, k := range slices.Sorted(maps.Keys(inputs)) {
		lk := strings.ToLower(k)
		switch {
		case m.used[lk]:
		case m.fixed[lk]:
			errs = append(errs, fmt.Errorf("--input %q is not a runtime input in this pipeline", k))
		default:
			if stage, _, scoped := strings.Cut(lk, "."); scoped && !m.stages[stage] {
				errs = append(errs, fmt.Errorf("--input %q: unknown stage %q", k, stage))
				continue
			}
			errs = append(errs, fmt.Errorf("--input %q matched no runtime input", k))
		}
	}
	return errors.Join(errs...)
}

func isInputPlaceholder(node *yaml.Node) bool {
	return node != nil && node.Kind == yaml.ScalarNode &&
		strings.HasPrefix(node.Value, "<+input>")
}

func readInputFile(path string) (string, error) {
	var r io.Reader
	if path == "-" {
		r = os.Stdin
	} else {
		f, err := os.Open(path)
		if err != nil {
			return "", fmt.Errorf("opening --input-file: %w", err)
		}
		defer f.Close()
		r = f
	}
	b, err := io.ReadAll(r)
	if err != nil {
		return "", fmt.Errorf("reading --input-file: %w", err)
	}
	var parsed any
	if err := yaml.Unmarshal(b, &parsed); err != nil {
		return "", fmt.Errorf("--input-file is not valid YAML: %w", err)
	}
	return string(b), nil
}

// executeDynamicBody builds the DynamicPipelineExecuteRequestBody: {"yaml": "<pipeline yaml>"}.
func executeDynamicBody(ctx *cmdctx.Ctx) (any, error) {
	yamlFile := cmdctx.GetString(ctx.FlagValues, "file")
	if yamlFile == "" {
		return nil, fmt.Errorf("-f/--file is required for dynamic pipeline execution")
	}
	yamlStr, err := readInputFile(yamlFile)
	if err != nil {
		return nil, err
	}
	hlog.Debug("execute pipeline:dynamic yaml:\n" + yamlStr)
	return map[string]any{"yaml": yamlStr}, nil
}

// executeRetryBody builds the RerunPipelineRequest body: {"inputs_yaml": "<yaml>"} for the
// v1 retry endpoint. The pipeline id comes from ctx.IdParts[0] since retry is invoked as
// "<pipeline>/<execution-id>". An empty inputs_yaml is valid when the pipeline has no runtime inputs.
func executeRetryBody(ctx *cmdctx.Ctx) (any, error) {
	inputFile := cmdctx.GetString(ctx.FlagValues, "input-file")
	inputPairs := cmdctx.GetStringSlice(ctx.FlagValues, "input")

	inputs, err := parseKeyValuePairs(inputPairs)
	if err != nil {
		return nil, err
	}
	if inputFile == "" && len(inputs) == 0 {
		return map[string]any{"inputs_yaml": ""}, nil
	}
	var yamlStr string
	if inputFile != "" {
		yamlStr, err = readInputFile(inputFile)
		if err != nil {
			return nil, err
		}
		if len(inputs) > 0 {
			yamlStr, err = mergeIntoTemplate(yamlStr, inputs)
			if err != nil {
				return nil, err
			}
		}
	} else {
		var pipelineID string
		if len(ctx.IdParts) > 0 {
			pipelineID = ctx.IdParts[0]
		}
		yamlStr, err = resolveFromTemplateForPipeline(ctx, pipelineID, inputs)
		if err != nil {
			return nil, err
		}
	}

	hlog.Debug("execute retry inputs_yaml:\n" + yamlStr)
	return map[string]any{"inputs_yaml": yamlStr}, nil
}

// executeInputSetBody builds the MergeInputSetRequest body for the /inputSetList endpoint.
func executeInputSetBody(ctx *cmdctx.Ctx) (any, error) {
	inputSetIDs := cmdctx.GetStringSlice(ctx.FlagValues, "input-set")
	inputPairs := cmdctx.GetStringSlice(ctx.FlagValues, "input")
	inputFile := cmdctx.GetString(ctx.FlagValues, "input-file")

	body := map[string]any{
		"inputSetReferences": inputSetIDs,
	}

	inputs, err := parseKeyValuePairs(inputPairs)
	if err != nil {
		return nil, err
	}

	var lastYaml string
	if inputFile != "" {
		lastYaml, err = readInputFile(inputFile)
		if err != nil {
			return nil, err
		}
		if len(inputs) > 0 {
			lastYaml, err = mergeIntoTemplate(lastYaml, inputs)
			if err != nil {
				return nil, err
			}
		}
	} else if len(inputs) > 0 {
		lastYaml, err = resolveFromTemplate(ctx, inputs)
		if err != nil {
			return nil, err
		}
	}

	if lastYaml != "" {
		body["lastYamlToMerge"] = lastYaml
	}

	hlog.Debug("execute inputSetList body", "inputSetReferences", inputSetIDs, "hasLastYaml", lastYaml != "")
	return body, nil
}

const executeFollowFnID = "execute_follow"

// executeFollowFn is the follow_fn for both execute pipeline variants.
// result is the raw API response; item_expr it.data.planExecution holds uuid and metadata.
func executeFollowFn(ctx *cmdctx.Ctx, result any) error {
	m, _ := result.(map[string]any)
	data, _ := m["data"].(map[string]any)
	plan, _ := data["planExecution"].(map[string]any)
	execId, _ := plan["uuid"].(string)
	if execId == "" {
		return fmt.Errorf("--follow: could not extract execution ID from response")
	}
	fmt.Println("\nFollowing log output...")
	return logstream.FollowMulti(ctx, execId, "", "", logstream.MultiStyleMarkers, nil)
}

const executeV1FollowFnID = "execute_v1_follow"

// executeV1FollowFn is the follow_fn for v1 execute endpoints (e.g. retry). Unlike the legacy
// response, v1 nests the execution id at execution_details.execution_id.
func executeV1FollowFn(ctx *cmdctx.Ctx, result any) error {
	m, _ := result.(map[string]any)
	details, _ := m["execution_details"].(map[string]any)
	execId, _ := details["execution_id"].(string)
	if execId == "" {
		return fmt.Errorf("--follow: could not extract execution ID from response")
	}
	fmt.Println("\nFollowing log output...")
	return logstream.FollowMulti(ctx, execId, "", "", logstream.MultiStyleMarkers, nil)
}

func parseKeyValuePairs(pairs []string) (map[string]string, error) {
	if len(pairs) == 0 {
		return nil, nil
	}
	result := make(map[string]string, len(pairs))
	for _, kv := range pairs {
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			return nil, fmt.Errorf("invalid --input value %q: expected key=value format", kv)
		}
		result[k] = v
	}
	return result, nil
}
