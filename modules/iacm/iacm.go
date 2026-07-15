// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package iacm

import "github.com/harness/cli/pkg/registry"

// ModuleInit registers iacm workflows. Commands are declared in iacm.spec.yaml.
func ModuleInit(reg registry.ModuleRegistrar) {
	reg.RegisterWorkflow(executeWorkspaceHandlerID, executeWorkspaceHandler)
}
