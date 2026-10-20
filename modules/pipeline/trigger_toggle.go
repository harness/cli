// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package pipeline

import (
	"bytes"
	"errors"
	"fmt"
	"net/url"
	"strconv"

	"go.yaml.in/yaml/v3"

	"github.com/harness/cli/v3/pkg/client"
	"github.com/harness/cli/v3/pkg/cmdctx"
	"github.com/harness/cli/v3/pkg/format"
)

const (
	updateTriggerEnableHandlerID  = "update_trigger_enable"
	updateTriggerDisableHandlerID = "update_trigger_disable"
)

func updateTriggerEnableHandler(ctx *cmdctx.Ctx) error  { return toggleTrigger(ctx, true) }
func updateTriggerDisableHandler(ctx *cmdctx.Ctx) error { return toggleTrigger(ctx, false) }

// toggleTrigger flips trigger.enabled by fetching the trigger YAML, editing it and
// PUTting it back: the NG API has no dedicated enable/disable endpoint.
func toggleTrigger(ctx *cmdctx.Ctx, enabled bool) error {
	if len(ctx.IdParts) != 2 {
		return fmt.Errorf("expected <pipeline>/<trigger>, got %q", ctx.Id)
	}
	pipelineID, triggerID := ctx.IdParts[0], ctx.IdParts[1]
	state := "disabled"
	if enabled {
		state = "enabled"
	}

	path := "/pipeline/api/triggers/" + url.PathEscape(triggerID)
	qp := map[string]string{
		"orgIdentifier":     ctx.Auth.OrgID,
		"projectIdentifier": ctx.Auth.ProjectID,
		"targetIdentifier":  pipelineID,
	}
	c := client.New(ctx)

	resp, _, err := c.DoRequest(client.Request{Method: "GET", Path: path, QueryParams: qp})
	if err != nil {
		return err
	}
	doc, ok := triggerYAML(resp)
	if !ok {
		return fmt.Errorf("trigger %s/%s: response has no YAML to update", pipelineID, triggerID)
	}
	updated, changed, err := setTriggerEnabled(doc, enabled)
	if err != nil {
		return fmt.Errorf("trigger %s/%s: %w", pipelineID, triggerID, err)
	}

	w, err := format.OpenWriter(ctx.FormatFlags.OutFile)
	if err != nil {
		return err
	}

	if !changed {
		fmt.Fprintf(w, "Trigger %s is already %s\n", triggerID, state)
		return nil
	}
	_, _, err = c.DoRequest(client.Request{
		Method:          "PUT",
		Path:            path,
		QueryParams:     qp,
		Body:            updated,
		BodyContentType: "application/yaml",
	})
	if errors.Is(err, client.ErrRequestPreviewed) {
		return nil
	}
	if err != nil {
		return err
	}
	fmt.Fprintf(w, "Trigger %s %s\n", triggerID, state)
	return nil
}

// triggerYAML extracts data.yaml from a get-trigger response.
func triggerYAML(resp any) (string, bool) {
	root, _ := resp.(map[string]any)
	data, _ := root["data"].(map[string]any)
	doc, ok := data["yaml"].(string)
	return doc, ok && doc != ""
}

// setTriggerEnabled sets trigger.enabled in a trigger YAML document. It edits the
// parsed node tree so key order and comments survive the round trip. changed is
// false, and doc is returned untouched, when enabled already has the target value.
func setTriggerEnabled(doc string, enabled bool) (out string, changed bool, err error) {
	var root yaml.Node
	if err := yaml.Unmarshal([]byte(doc), &root); err != nil {
		return "", false, fmt.Errorf("parsing trigger YAML: %w", err)
	}
	if root.Kind != yaml.DocumentNode || len(root.Content) == 0 || root.Content[0].Kind != yaml.MappingNode {
		return "", false, fmt.Errorf("trigger YAML is not a mapping")
	}
	trigger := mappingValue(root.Content[0], "trigger")
	if trigger == nil || trigger.Kind != yaml.MappingNode {
		return "", false, fmt.Errorf("trigger YAML has no \"trigger\" mapping")
	}
	node := mappingValue(trigger, "enabled")
	if node == nil || node.Kind != yaml.ScalarNode {
		return "", false, fmt.Errorf("trigger YAML has no scalar \"trigger.enabled\"")
	}

	var current bool
	if node.Tag == "!!bool" && node.Decode(&current) == nil && current == enabled {
		return doc, false, nil
	}
	node.Value = strconv.FormatBool(enabled)
	node.Tag = "!!bool"
	node.Style = 0

	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(&root); err != nil {
		return "", false, fmt.Errorf("encoding trigger YAML: %w", err)
	}
	if err := enc.Close(); err != nil {
		return "", false, fmt.Errorf("encoding trigger YAML: %w", err)
	}
	return buf.String(), true, nil
}

func mappingValue(m *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}
