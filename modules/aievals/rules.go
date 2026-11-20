// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package aievals

// Stable runtime constants for the eval_onboarding interface.
const (
	// RunnerPackage is the distribution selected for this phase: an isolated
	// pinned uv or pipx environment. A signed artifact and a runner bundled
	// inside the CLI module were not selected.
	RunnerPackage  = "ai-evals-runner"
	RunnerVersion  = "0.7.13"
	SchemaVersion  = "onboarding/v1"
	OnboardCommand = "onboard-run"
)
