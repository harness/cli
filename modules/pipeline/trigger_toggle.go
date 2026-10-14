// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package pipeline

import (
	"bytes"
	"fmt"
	"strconv"

	"go.yaml.in/yaml/v3"
)

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
