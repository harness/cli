// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package har

import "github.com/harness/cli/pkg/registry"

// ModuleInit registers har workflows. Commands are declared in har.spec.yaml.
func ModuleInit(reg registry.ModuleRegistrar) {
	reg.RegisterWorkflow("push_artifact_maven", pushMavenArtifact)
	reg.RegisterWorkflow("push_artifact_npm", pushNpmArtifact)
	reg.RegisterWorkflow("push_artifact_python", pushPythonArtifact)
	reg.RegisterWorkflow("push_artifact_nuget", pushNugetArtifact)
	reg.RegisterWorkflow("push_artifact_rpm", pushRpmArtifact)
	reg.RegisterWorkflow("push_artifact_cargo", pushCargoArtifact)
	reg.RegisterWorkflow("push_artifact_go", pushGoArtifact)
	reg.RegisterWorkflow("push_artifact_conda", pushCondaArtifact)
	reg.RegisterWorkflow("push_artifact_dart", pushDartArtifact)
	reg.RegisterWorkflow("push_artifact_composer", pushComposerArtifact)
	reg.RegisterWorkflow("push_artifact_swift", pushSwiftArtifact)
	reg.RegisterWorkflow("push_artifact_puppet", pushPuppetArtifact)
	reg.RegisterWorkflow("push_artifact_helm", pushHelmArtifact)
	reg.RegisterWorkflow("push_artifact_docker", pushDockerArtifact)
	reg.RegisterWorkflow(pullArtifactHandlerID, pullArtifactHandler)
	reg.RegisterWorkflow(executeArtifactFirewallScanHandlerID, executeArtifactFirewallScanHandler)
	reg.RegisterWorkflow(executeRegistryFirewallScanHandlerID, executeRegistryFirewallScanHandler)
	reg.RegisterWorkflow(executeRegistryMigrateHandlerID, executeRegistryMigrateHandler)
	reg.RegisterWorkflow(configureRegistryHandlerID, configureRegistryHandler)
}
