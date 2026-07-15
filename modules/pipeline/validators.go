// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package pipeline

import (
	"fmt"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/harness/cli/pkg/cmdctx"
)

const validatePipelineCreateID = "validate_pipeline_create"

// validatePipelineCreate checks that the identifier in the pipeline YAML matches
// the positional <id> argument (ctx.Id), if one was provided. Mismatches are
// almost always a mistake — the API ignores ctx.Id and uses the YAML identifier,
// so the created pipeline would have a different ID than the user specified.
func validatePipelineCreate(ctx *cmdctx.Ctx, req cmdctx.EndpointRequest) error {
	if ctx.Id == "" {
		return nil
	}
	body, ok := req.Body.(string)
	if !ok || body == "" {
		return nil
	}

	var doc struct {
		Pipeline struct {
			Identifier string `yaml:"identifier"`
		} `yaml:"pipeline"`
	}
	if err := yaml.Unmarshal([]byte(body), &doc); err != nil {
		return nil // malformed YAML will be caught by the API
	}
	yamlID := strings.TrimSpace(doc.Pipeline.Identifier)
	if yamlID == "" {
		return nil
	}
	if yamlID != ctx.Id {
		return fmt.Errorf("identifier mismatch: argument is %q but pipeline YAML has identifier %q", ctx.Id, yamlID)
	}
	return nil
}
