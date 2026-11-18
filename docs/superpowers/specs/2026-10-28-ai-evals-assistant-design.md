# AI Evals Assistant Design

## Goal

Replace the one-time, execution-oriented AI Evals onboarding prompt with a reusable assistant that helps users discover, build, run, inspect, and manage AI Evals through explicit major decisions.

## Scope

Phase 1 changes two surfaces:

- The AI Evals UI provides a persistent assistant launcher and a concise copyable bootstrap prompt.
- The Harness CLI provides a read-only planning operation that reports repository and Harness findings without staging files, executing runs, uploading, or mutating the active profile.

The existing provisional onboarding workflow remains available for compatibility, but the UI no longer makes it the default copied experience.

## Interaction model

The copied bootstrap prompt installs or invokes a versioned `aievals-assistant` skill. The assistant:

1. Verifies the CLI, active profile, and current account, organization, and project scope.
2. Runs read-only discovery against the repository and existing Harness resources.
3. Explains what it found in plain language.
4. Presents choices for consequential decisions, including a recommendation and the supporting evidence.
5. Applies the chosen approach using existing deterministic CLI operations.
6. Continues as a reusable management assistant after the initial setup.

Read-only discovery and small local scaffolding proceed automatically. The assistant pauses before these major decisions:

- Reuse an existing target versus create a new target.
- Configure an existing endpoint versus create an adapter endpoint when none is found.
- Reuse, infer, or author a dataset.
- Reuse, recommend, or create metrics and metric sets.
- Run locally versus run in a named live environment.
- Upload/import generated artifacts into Harness.

The assistant never changes CLI profiles automatically. It reports the selected profile and asks the user to select or authenticate a different profile when the scope is unsuitable.

## CLI contract

Add a read-only planning operation for AI Evals onboarding. Its output is structured enough for the assistant to consume and human-readable enough to inspect directly. It reports:

- Supported route candidates and ambiguity.
- Existing local AI Evals files.
- Existing remote targets, evals, datasets, metric sets, and suites for the selected scope.
- Endpoint/configuration gaps.
- Recommended next actions and their confidence.

Planning never writes `.harness/evals`, runs an agent, uploads an archive, or makes authentication/profile changes.

Applying decisions continues to use the existing setup, run, and upload primitives. The assistant passes an explicit route or configuration selection rather than relying on a guessed route.

## UI contract

The persistent Agent-list launcher and the true-empty Agent-list launcher use the same bootstrap prompt. The UI removes operational detail from the copied-prompt guide:

- No account, organization, or project text.
- No PAT or LLM-key instructions.
- No local, live, or upload command blocks.

The assistant discovers scope and explains follow-up actions during the conversation. The empty-state launcher remains fail-closed behind `AI_EVALS_ONBOARDING_V2`; the persistent launcher is available when that flag is enabled even after targets exist.

## Endpoint gap behavior

When no callable endpoint is found, the assistant explains the missing capability and offers these choices:

- Help create an adapter endpoint after approval.
- Configure a known endpoint supplied by the user.
- Stop and preserve the read-only plan.

The assistant does not edit application code before the user chooses endpoint creation.

## Compatibility

- Preserve `setup eval_onboarding`, `run eval_onboarding`, and `upload eval_onboarding`.
- Keep the existing schema and runner handshake unchanged in Phase 1.
- Install the new assistant skill under a separate identifier; do not overwrite a user-modified legacy onboarding skill.
- Preserve current profile resolution order.

## Verification

Tests cover:

- Read-only planning causes no staged files, execution, upload, or profile mutation.
- Ambiguous candidates include evidence and require an explicit selection before apply.
- Major decision prompts are present in the assistant skill.
- Endpoint absence offers creation/configuration/stop choices without writing application code.
- The persistent and empty-state launchers render the same bootstrap prompt while legacy operational UI text is absent.
- Existing onboarding workflow tests continue to pass.
