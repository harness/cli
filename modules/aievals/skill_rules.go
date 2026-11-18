// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package aievals

// Skill rule sentences are copied verbatim into the embedded skill.
const (
	SkillRuleScan       = "The coding agent scans the repository. The slightest sign of an agent counts."
	SkillRuleConfirm    = "Nothing is written, run, or uploaded without the user's yes."
	SkillRuleSecrets    = "Infer secret names only, never values."
	SkillRuleNoScript   = "Do not write a standalone Python runner."
	SkillRuleNoEvalYAML = "Do not create eval.yaml."
	SkillRuleStage      = "Write the .harness/evals tree only with the Harness CLI validate-and-write command `harness stage eval_onboarding`."
	SkillRuleMCP        = "Check once whether the Harness MCP server is connected, and reuse that choice for the platform stages. Do not invent MCP tool names."
)

// SkillRules exposes the stable rule list to callers that need to render or
// validate the canonical skill without parsing its Markdown.
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
