// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package platform

import "github.com/harness/cli/v3/pkg/registry"

const resolvePrincipalIDFnID = "resolve_principal_id"

func ModuleInit(reg registry.ModuleRegistrar) {
	reg.RegisterFlagResolveFn(resolvePrincipalIDFnID, resolvePrincipalID)
}
