// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package aievals

import "github.com/harness/cli/v3/pkg/registry"

// ModuleInit registers the stable onboarding workflows.
func ModuleInit(reg registry.ModuleRegistrar) {
	reg.RegisterWorkflow(setupWorkflowID, setupWorkflow)
	reg.RegisterWorkflow(scanWorkflowID, scanWorkflow)
	reg.RegisterWorkflow(stageWorkflowID, stageWorkflow)
	reg.RegisterWorkflow(runWorkflowID, runWorkflow)
}
