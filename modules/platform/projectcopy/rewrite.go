// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package projectcopy

import (
	"encoding/json"
	"reflect"
	"regexp"
	"strings"

	"go.yaml.in/yaml/v3"
)

var scopedRef = regexp.MustCompile(`^(account|org)\.[A-Za-z_][A-Za-z0-9_]{0,127}$`)

var rootDrop = map[string]bool{
	"createdAt": true, "lastModifiedAt": true, "createdBy": true, "lastModifiedBy": true,
	"status": true, "harnessManaged": true, "entityValidityDetails": true, "connectorStats": true,
	"gitDetails": true, "isFavorite": true, "isDraft": true, "cacheResponse": true,
	"errorMessage": true, "connectorMetaData": true, "governanceMetadata": true,
	"yamlVersion": true, "entityValidity": true,
}

type rewriteOpts struct {
	Org, Project, SourceOrg, SourceProject string
	DisableTriggers                        bool
}

func rewriteYAML(doc string, opts rewriteOpts) (string, error) {
	var node any
	if err := yaml.Unmarshal([]byte(doc), &node); err != nil {
		return "", err
	}
	node = yamlToPlain(node)
	rewriteTree(node, opts)
	out, err := yaml.Marshal(node)
	if err != nil {
		return "", err
	}
	return string(out), nil
}

func sameDocument(a, b string) bool {
	var left, right any
	if yaml.Unmarshal([]byte(a), &left) != nil || yaml.Unmarshal([]byte(b), &right) != nil {
		return false
	}
	return reflect.DeepEqual(yamlToPlain(left), yamlToPlain(right))
}

func yamlToPlain(v any) any {
	switch n := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(n))
		for k, child := range n {
			out[k] = yamlToPlain(child)
		}
		return out
	case map[any]any:
		out := make(map[string]any, len(n))
		for k, child := range n {
			out[toString(k)] = yamlToPlain(child)
		}
		return out
	case []any:
		out := make([]any, len(n))
		for i := range n {
			out[i] = yamlToPlain(n[i])
		}
		return out
	default:
		return n
	}
}

func toString(v any) string {
	s, _ := v.(string)
	return s
}

func rewriteTree(v any, opts rewriteOpts) {
	switch n := v.(type) {
	case map[string]any:
		if cur, ok := n["projectIdentifier"].(string); ok && cur == opts.SourceProject {
			n["projectIdentifier"] = opts.Project
		}
		if cur, ok := n["orgIdentifier"].(string); ok && cur == opts.SourceOrg {
			n["orgIdentifier"] = opts.Org
		}
		if opts.DisableTriggers {
			if _, ok := n["pipelineIdentifier"]; ok {
				if _, ok := n["enabled"]; ok {
					n["enabled"] = false
				}
			}
		}
		for k, child := range n {
			if s, ok := child.(string); ok && (k == "yaml" || k == "yamlPipeline" || k == "inputSetYaml") && strings.Contains(s, "projectIdentifier") {
				if rewritten, err := rewriteYAML(s, opts); err == nil {
					n[k] = rewritten
					continue
				}
			}
			rewriteTree(child, opts)
		}
	case []any:
		for i := range n {
			rewriteTree(n[i], opts)
		}
	}
}

func cleanRoot(obj map[string]any) map[string]any {
	cloned := cloneMap(obj)
	for key := range rootDrop {
		delete(cloned, key)
	}
	delete(cloned, "yaml")
	delete(cloned, "yamlPipeline")
	delete(cloned, "inputSetYaml")
	return cloned
}

func cloneMap(obj map[string]any) map[string]any {
	data, err := json.Marshal(obj)
	if err != nil {
		return map[string]any{}
	}
	var out map[string]any
	if json.Unmarshal(data, &out) != nil || out == nil {
		return map[string]any{}
	}
	return out
}

func canonicalJSON(v any) string {
	data, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(data)
}

func walk(v any, fn func(key string, value any)) {
	switch n := v.(type) {
	case map[string]any:
		for k, child := range n {
			fn(k, child)
			walk(child, fn)
		}
	case []any:
		for _, child := range n {
			walk(child, fn)
		}
	}
}

func stringField(m map[string]any, keys ...string) string {
	for _, key := range keys {
		if s, ok := m[key].(string); ok && s != "" {
			return s
		}
	}
	return ""
}

func isRemote(m map[string]any) bool {
	if strings.EqualFold(stringField(m, "storeType", "store_type"), "REMOTE") {
		return true
	}
	if git, ok := m["gitDetails"].(map[string]any); ok {
		return strings.EqualFold(stringField(git, "storeType"), "REMOTE")
	}
	return false
}

func asMap(v any) (map[string]any, bool) {
	m, ok := v.(map[string]any)
	return m, ok && m != nil
}

func asSlice(v any) ([]any, bool) {
	s, ok := v.([]any)
	return s, ok
}
