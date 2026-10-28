// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package aievals

// Skill rule sentences are copied verbatim into the embedded skill.
// TODO(onboarding-schema): provisional scan and staging contract.
const (
	SkillRuleOrder       = "Discover routes only through supported, service-scoped Spring/JAX-RS/Micronaut, FastAPI, and confirmed Express adapters."
	SkillRuleUnsupported = "Unsupported syntax cannot be selected with --route; use guided/manual configuration. Ambiguous supported routes require --route; --yes never authorizes either."
	SkillRuleHigh        = "Auto-stage high-confidence values."
	SkillRuleMedium      = "Batch medium-confidence confirmations into one confirmation."
	SkillRuleLow         = "Ask one batched question for low-confidence gaps."
	SkillRuleSecrets     = "Infer secret names only, never values."
	SkillRuleStage       = "Stage candidate files in a temporary tree, invoke the D2 Python validator, compare destinations, and atomically write only missing or identical files."
	SkillRuleNoScript    = "Do not write a standalone Python runner."
	SkillRuleNoEvalYAML  = "Do not create eval.yaml."

	// RunnerPackage is the distribution selected for this phase: an isolated
	// pinned uv or pipx environment. A signed artifact and a runner bundled
	// inside the CLI module were not selected.
	RunnerPackage  = "ai-evals-runner"
	RunnerVersion  = "0.7.7"
	SchemaVersion  = "provisional-onboarding-1"
	OnboardCommand = "onboard-run"
)

func SkillRules() []string {
	return []string{
		SkillRuleOrder,
		SkillRuleUnsupported,
		SkillRuleHigh,
		SkillRuleMedium,
		SkillRuleLow,
		SkillRuleSecrets,
		SkillRuleStage,
		SkillRuleNoScript,
		SkillRuleNoEvalYAML,
	}
}
