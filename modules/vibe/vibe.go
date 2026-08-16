// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package vibe

import "github.com/harness/cli/pkg/registry"

const (
	vibeLaunchHandlerID            = "vibe_launch"
	vibeAppListHandlerID           = "vibe_app_list"
	vibeAppGetHandlerID            = "vibe_app_get"
	vibeDeploymentGetHandlerID     = "vibe_deployment_get"
	vibeDeploymentRetryHandlerID   = "vibe_deployment_retry"
	vibeDeploymentCancelHandlerID  = "vibe_deployment_cancel"
	vibeDeploymentPublishHandlerID = "vibe_deployment_publish"
	vibeDemoStateHandlerID         = "vibe_demo_state"
)

// ModuleInit registers Vibe workflows. Commands are declared in vibe.spec.yaml.
func ModuleInit(reg registry.ModuleRegistrar) {
	reg.RegisterWorkflow(vibeLaunchHandlerID, vibeLaunchHandler)
	reg.RegisterWorkflow(vibeAppListHandlerID, vibeAppListHandler)
	reg.RegisterWorkflow(vibeAppGetHandlerID, vibeAppGetHandler)
	reg.RegisterWorkflow(vibeDeploymentGetHandlerID, vibeDeploymentGetHandler)
	reg.RegisterWorkflow(vibeDeploymentRetryHandlerID, vibeDeploymentRetryHandler)
	reg.RegisterWorkflow(vibeDeploymentCancelHandlerID, vibeDeploymentCancelHandler)
	reg.RegisterWorkflow(vibeDeploymentPublishHandlerID, vibeDeploymentPublishHandler)
	reg.RegisterWorkflow(vibeDemoStateHandlerID, vibeDemoStateHandler)
}
