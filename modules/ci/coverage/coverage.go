// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package coverage

import (
	"github.com/harness/cli/v3/modules/ci/coverage/cmd/analyze"
	"github.com/harness/cli/v3/modules/ci/coverage/cmd/upload"
	"github.com/harness/cli/v3/modules/ci/coverage/cmd/waituntil"
	"github.com/harness/cli/v3/pkg/registry"
)

// Register wires the coverage workflows declared in ci.spec.yaml.
func Register(reg registry.ModuleRegistrar) {
	reg.RegisterWorkflow(analyze.HandlerID, analyze.Handler)
	reg.RegisterWorkflow(upload.HandlerID, upload.Handler)
	reg.RegisterWorkflow(waituntil.HandlerID, waituntil.Handler)
}
