// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package aievals

// Skill rule sentences are copied verbatim into the embedded skill.
// TODO(onboarding-schema): provisional command names until the CLI owner approves them.
const (
	SkillRuleScan       = "The coding agent scans the repository. The slightest sign of an agent counts."
	SkillRuleConfirm    = "Nothing is written, run, or uploaded without the user's yes."
	SkillRuleSecrets    = "Infer secret names only, never values."
	SkillRuleNoScript   = "Do not write a standalone Python runner."
	SkillRuleNoEvalYAML = "Do not create eval.yaml."
	SkillRuleStage      = "Write the .harness/evals tree only with the Harness CLI validate-and-write command `harness stage eval_onboarding`."
	SkillRuleMCP        = "A Harness MCP equivalent may be used for the same step when one is available. Do not invent MCP tool names."

	// RunnerPackage is the distribution selected for this phase: an isolated
	// pinned uv or pipx environment. A signed artifact and a runner bundled
	// inside the CLI module were not selected.
	RunnerPackage  = "ai-evals-runner"
	RunnerVersion  = "0.7.7"
	SchemaVersion  = "provisional-onboarding-1"
	OnboardCommand = "onboard-run"

	// SkillSourcePath is the canonical skill file. The CLI embeds it, the UI
	// generator copies it, and an MCP server can serve the same bytes.
	SkillSourcePath = "modules/aievals/skill/SKILL.md"
)

func SkillRules() []string {
	return []string{
		SkillRuleScan,
		SkillRuleConfirm,
		SkillRuleSecrets,
		SkillRuleNoScript,
		SkillRuleNoEvalYAML,
		SkillRuleStage,
		SkillRuleMCP,
	}
}
