// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package har

import "github.com/harness/cli/v3/pkg/cmdctx"

const (
	executeArtifactNpmInstallHandlerID    = "execute_artifact_npm_install"
	executeArtifactNpmCiHandlerID         = "execute_artifact_npm_ci"
	executeArtifactPipInstallHandlerID    = "execute_artifact_pip_install"
	executeArtifactMvnInstallHandlerID    = "execute_artifact_mvn_install"
	executeArtifactDotnetRestoreHandlerID = "execute_artifact_dotnet_restore"
)

func executeArtifactNpmInstallHandler(ctx *cmdctx.Ctx) error {
	_, nativeArgs := pkgmgrParseArgs(ctx.Args)
	return pkgmgrExecute(ctx, &npmClient{}, "install", nativeArgs)
}

func executeArtifactNpmCiHandler(ctx *cmdctx.Ctx) error {
	_, nativeArgs := pkgmgrParseArgs(ctx.Args)
	return pkgmgrExecute(ctx, &npmClient{}, "ci", nativeArgs)
}

func executeArtifactPipInstallHandler(ctx *cmdctx.Ctx) error {
	_, nativeArgs := pkgmgrParseArgs(ctx.Args)
	return pkgmgrExecute(ctx, &pipClient{}, "install", nativeArgs)
}

func executeArtifactMvnInstallHandler(ctx *cmdctx.Ctx) error {
	_, nativeArgs := pkgmgrParseArgs(ctx.Args)
	return pkgmgrExecute(ctx, &mavenClient{}, "install", nativeArgs)
}

func executeArtifactDotnetRestoreHandler(ctx *cmdctx.Ctx) error {
	_, nativeArgs := pkgmgrParseArgs(ctx.Args)
	return pkgmgrExecute(ctx, &nugetClient{}, "restore", nativeArgs)
}
