// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package ci

import (
	"github.com/harness/cli/v3/modules/ci/annotations"
	"github.com/harness/cli/v3/modules/ci/coverage"
	cierrors "github.com/harness/cli/v3/modules/ci/errors"
	"github.com/harness/cli/v3/pkg/registry"
)

// ModuleInit registers ci workflows. Commands are declared in ci.spec.yaml.
func ModuleInit(reg registry.ModuleRegistrar) {
	reg.RegisterWorkflow(annotations.HandlerID, annotations.Handler)
	reg.RegisterWorkflow(cierrors.ValidateHandlerID, cierrors.ValidateHandler)
	reg.RegisterWorkflow(cierrors.EvaluateHandlerID, cierrors.EvaluateHandler)
	coverage.Register(reg)
	reg.RegisterWorkflow(pushCiArtifactHandlerID, pushCiArtifactHandler)
	reg.RegisterWorkflow(pullCiArtifactHandlerID, pullCiArtifactHandler)
}
