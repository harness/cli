// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

// Package rt implements the handlers behind the Resilience Testing commands.
// Load testing is the first feature area under RT; chaos is expected next.
// Plain HTTP calls live in rt.spec.yaml — only what the spec cannot express is here.
package rt

import (
	"github.com/harness/cli/pkg/cmdctx"
	"github.com/harness/cli/pkg/registry"
)

// The load test manager API root. A second RT feature area would have its own root.
const basePath = "/gateway/loadTest/manager/api/v1"

// ModuleInit registers the RT handlers. Commands are declared in rt.spec.yaml.
func ModuleInit(reg registry.ModuleRegistrar) {
	reg.RegisterBodyFn(startRunBodyFnID, startRunBody)
	reg.RegisterBodyFn(encodeScriptBodyFnID, encodeScriptBody)
	reg.RegisterBodyFn(compositeBodyFnID, compositeBody)
	reg.RegisterFollowFn(watchFollowFnID, watchFollowFn)
	reg.RegisterWorkflow(rerunWorkflowID, rerunWorkflow)
	reg.RegisterWorkflow(exportTemplateYamlWorkflowID, exportTemplateYaml)
	reg.RegisterTextFormatter(formatScriptID, formatScript)
	reg.RegisterListTransformFn(usageReportRowsID, usageReportRows)
	reg.RegisterQueryParamsFn(usageWindowParamsID, usageWindowParams)
	reg.RegisterQueryParamsFn(loadTestFilterParamsID, loadTestFilterParams)
	reg.RegisterFlagResolveFn(resolveScriptRevisionID, resolveScriptRevision)
}

// accountIdentifier is added by the client on the way out; org and project are not.
func scopeParams(ctx *cmdctx.Ctx) map[string]string {
	qp := map[string]string{}
	if ctx.Auth == nil {
		return qp
	}
	if ctx.Auth.OrgID != "" {
		qp["organizationIdentifier"] = ctx.Auth.OrgID
	}
	if ctx.Auth.ProjectID != "" {
		qp["projectIdentifier"] = ctx.Auth.ProjectID
	}
	return qp
}

func asMap(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}

func stringField(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	s, _ := m[key].(string)
	return s
}

// JSON decoding gives every number as a float64, so this covers counts and rates alike.
func floatField(m map[string]any, key string) float64 {
	if m == nil {
		return 0
	}
	f, _ := m[key].(float64)
	return f
}
