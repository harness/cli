// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package vibeapps

import "github.com/harness/cli/v3/pkg/registry"

func ModuleInit(reg registry.ModuleRegistrar) {
	reg.RegisterWorkflow(pushVibeappSourceWorkflowID, pushVibeappSourceWorkflow)
	reg.RegisterWorkflow(pullVibeappWorkflowID, pullVibeappWorkflow)
	reg.RegisterWorkflow(getVibeappDeploymentLogWorkflowID, getVibeappDeploymentLogWorkflow)
	reg.RegisterWorkflow(vibeappDeployWorkflowID, vibeappDeployWorkflow)
	reg.RegisterFollowFn(vibeappRunFollowFnID, vibeappRunFollowFn)
}
