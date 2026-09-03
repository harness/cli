// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package vibeapps

import "github.com/harness/cli/pkg/registry"

func ModuleInit(reg registry.ModuleRegistrar) {
	reg.RegisterWorkflow(pushVibeappSourceWorkflowID, pushVibeappSourceWorkflow)
	reg.RegisterWorkflow(pullVibeappWorkflowID, pullVibeappWorkflow)
	reg.RegisterWorkflow(getVibeappDeploymentLogWorkflowID, getVibeappDeploymentLogWorkflow)
	reg.RegisterFollowFn(vibeappDeployFollowFnID, vibeappDeployFollowFn)
}
