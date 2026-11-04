// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package har

import "github.com/harness/cli/v3/pkg/registry"

// ModuleInit registers har workflows. Commands are declared in har.spec.yaml.
func ModuleInit(reg registry.ModuleRegistrar) {
	reg.RegisterWorkflow("push_artifact_generic", withPackageHost(pushGenericArtifact))
	reg.RegisterWorkflow("push_artifact_debian", withPackageHost(pushDebianArtifact))
	reg.RegisterWorkflow("push_artifact_conan", withPackageHost(pushConanArtifact))
	reg.RegisterWorkflow("push_artifact_maven", withPackageHost(pushMavenArtifact))
	reg.RegisterWorkflow("push_artifact_npm", withPackageHost(pushNpmArtifact))
	reg.RegisterWorkflow("push_artifact_python", withPackageHost(pushPythonArtifact))
	reg.RegisterWorkflow("push_artifact_nuget", withPackageHost(pushNugetArtifact))
	reg.RegisterWorkflow("push_artifact_rpm", withPackageHost(pushRpmArtifact))
	reg.RegisterWorkflow("push_artifact_cargo", withPackageHost(pushCargoArtifact))
	reg.RegisterWorkflow("push_artifact_go", withPackageHost(pushGoArtifact))
	reg.RegisterWorkflow("push_artifact_conda", withPackageHost(pushCondaArtifact))
	reg.RegisterWorkflow("push_artifact_dart", withPackageHost(pushDartArtifact))
	reg.RegisterWorkflow("push_artifact_composer", withPackageHost(pushComposerArtifact))
	reg.RegisterWorkflow("push_artifact_swift", withPackageHost(pushSwiftArtifact))
	reg.RegisterWorkflow("push_artifact_ruby", withPackageHost(pushRubyArtifact))
	reg.RegisterWorkflow("push_artifact_puppet", withPackageHost(pushPuppetArtifact))
	reg.RegisterWorkflow("push_artifact_helm", pushHelmArtifact)
	reg.RegisterWorkflow("push_artifact_docker", pushDockerArtifact)
	reg.RegisterWorkflow("push_artifact_terraform", withPackageHost(pushTerraformArtifact))
	reg.RegisterWorkflow(pullArtifactHandlerID, withPackageHost(pullArtifactHandler))
	reg.RegisterWorkflow(executeArtifactFirewallScanHandlerID, executeArtifactFirewallScanHandler)
	reg.RegisterWorkflow(executeRegistryFirewallScanHandlerID, executeRegistryFirewallScanHandler)
	reg.RegisterWorkflow(executeRegistryMigrateHandlerID, withPackageHost(executeRegistryMigrateHandler))
	reg.RegisterWorkflow(configureRegistryNpmHandlerID, withPackageHost(configureNpm))
	reg.RegisterWorkflow(configureRegistryMavenHandlerID, withPackageHost(configureMaven))
	reg.RegisterWorkflow(configureRegistryPipHandlerID, withPackageHost(configurePip))
	reg.RegisterWorkflow(configureRegistryNugetHandlerID, withPackageHost(configureNuget))
	reg.RegisterWorkflow(executeArtifactNpmInstallHandlerID, executeArtifactNpmInstallHandler)
	reg.RegisterWorkflow(executeArtifactNpmCiHandlerID, executeArtifactNpmCiHandler)
	reg.RegisterWorkflow(executeArtifactPipInstallHandlerID, executeArtifactPipInstallHandler)
	reg.RegisterWorkflow(executeArtifactMvnInstallHandlerID, executeArtifactMvnInstallHandler)
	reg.RegisterWorkflow(executeArtifactDotnetRestoreHandlerID, executeArtifactDotnetRestoreHandler)
	reg.RegisterWorkflow(bulkDeleteArtifactHandlerID, bulkDeleteArtifactHandler)
	reg.RegisterWorkflow(deleteArtifactHandlerID, deleteArtifactHandler)
	reg.RegisterWorkflow(deleteArtifactVersionHandlerID, deleteArtifactVersionHandler)
	reg.RegisterWorkflow(executeArtifactDownloadHandlerID, executeArtifactDownloadHandler)
}
