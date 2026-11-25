// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package code

import (
	"errors"

	"github.com/harness/cli/v3/pkg/cmdctx"
)

const validateRepoLabelUpdateID = "validate_repo_label_update"

func validateRepoLabelUpdate(_ *cmdctx.Ctx, req cmdctx.EndpointRequest) error {
	if body, ok := req.Body.(map[string]any); ok && len(body) == 0 {
		return errors.New("nothing to update: pass at least one of --new-key, --color, --type or --description")
	}
	return nil
}
