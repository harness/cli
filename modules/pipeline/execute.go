// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package pipeline

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/harness/cli/pkg/client"
	"github.com/harness/cli/pkg/cmdctx"
	"github.com/harness/cli/pkg/hlog"
	"github.com/harness/cli/pkg/logstream"
	"go.yaml.in/yaml/v3"
)

const executePipelineBodyFnID = "execute_body"
const executeInputSetBodyFnID = "execute_inputset_body"
const executeDynamicBodyFnID = "execute_dynamic_body"

// executePipelineBody builds the runtimeInputYaml request body for pipeline execution.
// --input-file <file>: use pre-filled YAML directly.
// --input key=val: fetch the pipeline's runtime input template, substitute values, send result.
func executePipelineBody(ctx *cmdctx.Ctx) (any, error) {
	inputFile := cmdctx.GetString(ctx.FlagValues, "input-file")
	inputPairs := cmdctx.GetStringSlice(ctx.FlagValues, "input")

	inputs, err := parseKeyValuePairs(inputPairs)
	if err != nil {
		return nil, err
	}

	if inputFile == "" && len(inputs) == 0 {
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
func resolveFromTemplate(ctx *cmdctx.Ctx, inputs map[string]string) (string, error) {
	c := client.New(ctx)
	params := map[string]string{
		"pipelineIdentifier": ctx.Id,
		"orgIdentifier":      ctx.Auth.OrgID,
		"projectIdentifier":  ctx.Auth.ProjectID,
	}
	branch := cmdctx.GetString(ctx.FlagValues, "branch")
	if branch != "" {
		params["branch"] = branch
	}

	hlog.Debug("fetching runtime input template", "pipeline", ctx.Id)
	raw, _, err := c.Post("/pipeline/api/inputSets/template", params, map[string]any{})
	if err != nil {
		return "", fmt.Errorf("fetching runtime input template: %w", err)
	}

	templateYaml := extractTemplateYaml(raw)
	if templateYaml == "" {
		hlog.Debug("pipeline has no runtime inputs, ignoring --input flags")
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
// a user key is replaced with that value. Unmatched placeholders are left as-is.
func mergeIntoTemplate(templateYaml string, inputs map[string]string) (string, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(templateYaml), &doc); err != nil {
		return "", fmt.Errorf("parsing runtime input template: %w", err)
	}

	// Normalise inputs to lowercase for case-insensitive matching.
	normalised := make(map[string]string, len(inputs))
	for k, v := range inputs {
		normalised[strings.ToLower(k)] = v
	}

	substituteNode(&doc, normalised)

	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(&doc); err != nil {
		return "", fmt.Errorf("serializing runtimeInputYaml: %w", err)
	}
	return buf.String(), nil
}

// substituteNode walks a yaml.Node tree and replaces <+input> scalar values.
// For mapping nodes it checks both the key name and (for variable-style entries)
// the sibling "name" field.
func substituteNode(node *yaml.Node, inputs map[string]string) {
	if node == nil {
		return
	}
	switch node.Kind {
	case yaml.DocumentNode:
		for _, child := range node.Content {
			substituteNode(child, inputs)
		}
	case yaml.MappingNode:
		// Collect key→value index for sibling "name" lookups.
		keys := make(map[string]int, len(node.Content)/2)
		for i := 0; i+1 < len(node.Content); i += 2 {
			keys[node.Content[i].Value] = i + 1
		}
		for i := 0; i+1 < len(node.Content); i += 2 {
			keyNode := node.Content[i]
			valNode := node.Content[i+1]
			if isInputPlaceholder(valNode) {
				leafKey := strings.ToLower(keyNode.Value)
				// For "value" or "default" keys, look for sibling "name" key.
				if leafKey == "value" || leafKey == "default" {
					if nameIdx, ok := keys["name"]; ok {
						siblingName := strings.ToLower(node.Content[nameIdx].Value)
						if v, ok := inputs[siblingName]; ok {
							valNode.Value = v
							valNode.Tag = "!!str"
							continue
						}
					}
				}
				if v, ok := inputs[leafKey]; ok {
					valNode.Value = v
					valNode.Tag = "!!str"
					continue
				}
			}
			substituteNode(valNode, inputs)
		}
	case yaml.SequenceNode:
		for _, child := range node.Content {
			substituteNode(child, inputs)
		}
	}
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
