// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package ci

import "github.com/harness/cli/v3/pkg/registry"

// ModuleInit registers ci workflows. Commands are declared in ci.spec.yaml.
func ModuleInit(reg registry.ModuleRegistrar) {
	reg.RegisterWorkflow(executeAnnotationHandlerID, executeAnnotationHandler)
	reg.RegisterWorkflow(executeErrorValidateHandlerID, executeErrorValidateHandler)
	reg.RegisterWorkflow(executeErrorEvaluateHandlerID, executeErrorEvaluateHandler)
	reg.RegisterWorkflow(executeCoverageAnalyzeHandlerID, executeCoverageAnalyzeHandler)
	reg.RegisterWorkflow(executeCoverageUploadHandlerID, executeCoverageUploadHandler)
}
