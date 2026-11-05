---
name: aievals
description: Conversational Harness AI Evals skill. The coding agent scans the repository and checks with the user before every write, run, or upload. The Harness CLI validates and writes the .harness/evals tree, runs the eval, and uploads it. This file can be served as an MCP prompt or resource.
version: 1
schema: provisional-onboarding-1
---

# AI Evals

TODO(onboarding-schema): `setup`, `scan`, `stage`, `run`, and `upload` on `eval_onboarding` are provisional names.

This file is the single AI Evals skill. The Harness CLI embeds it, the product copy-prompt includes it, and a Harness MCP server can serve this same file as a prompt or resource.

The coding agent scans the repository. The slightest sign of an agent counts. Nothing is written, run, or uploaded without the user's yes. Infer secret names only, never values.

Do not write a standalone Python runner.
Do not create eval.yaml.

Use the Harness CLI command named for each step. A Harness MCP equivalent may be used for the same step when one is available. Do not invent MCP tool names.

When the prompt header includes a `harness auth login` command, use that command for this account, organization, and project. If no header was provided, ask for the account, organization, and project before authenticating. Do not change profiles automatically. To use an existing profile such as `qa`, pass `--profile <name>` on commands that need Harness credentials, or set `HARNESS_PROFILE`. An explicit `--profile` takes precedence over `HARNESS_PROFILE`.

## Commands

- `harness setup eval_onboarding` installs or prints this skill. It does not scan and it does not write eval files. Optional `--skill-dir` chooses the destination. With no coding-agent skills directory and no `--skill-dir`, it prints the skill to stdout.
- `harness scan eval_onboarding` is an optional hint. It prints candidate JSON and writes nothing. `--route '<METHOD path>'` narrows the hint. The agent's own reading of the repository is the source of truth. If the hint and the agent disagree, show both and let the user choose.
- Write the .harness/evals tree only with the Harness CLI validate-and-write command `harness stage eval_onboarding`. Pass `--file <path>` pointing at a temporary JSON file, or pass flags. Flags override the same fields in the file.
- `harness run eval_onboarding --agent <identifier> [--environment local|<live-name>]` runs a staged agent. `local` is the default. The command checks the pinned runner and does not upgrade it. A PAT or SAT is passed to the runner. An SSO access token is not.
- `harness upload eval_onboarding` validates locally, uploads `.harness/evals`, and prints Agent and Eval links. Git sync is not applied by the upload.

`stage` accepts `--agent`, `--endpoint`, `--method`, `--request-field`, `--request-fields`, `--response-path`, `--auth-header`, `--auth-env`, `--input-text`, and `--expected`. `--auth-header` is a header name. `--auth-env` is an environment-variable name. Never pass a secret value. A header name without an environment-variable name is omitted from the tree.

Example candidate file:

```json
{
  "agent_id": "support",
  "endpoint_url": "http://127.0.0.1:8080/api/chat",
  "method": "POST",
  "request_field": "text",
  "response_path": "output.text",
  "auth_header": "Authorization",
  "auth_env": "SUPPORT_TOKEN",
  "input_text": "hello",
  "expected": "ok"
}
```

The tree the command writes is `.harness/evals/agents/<identifier>/agent.yaml`, `.harness/evals/datasets/<identifier>.jsonl`, and `.harness/evals/metric-sets/<identifier>.yaml`. The default metric is exact match. If `stage` reports that a destination differs and was left unchanged, show that message and ask how to proceed. Do not overwrite the file by hand.

## Harness CLI setup

Show a step only when the next action needs it. Skip a step that is already done.

If `harness version` fails, install the CLI and run `harness version` again:

```bash
curl -fsSL https://raw.githubusercontent.com/harness/cli/main/install.sh | sh
```

Before the first `eval_onboarding` command, enable the module if it is not enabled:

```bash
harness install module aievals
```

Before `run` or `upload`, authenticate with a Harness personal access token or API key. Give the token to the CLI when it asks. Do not paste the token into chat or write it into the repository.

An LLM provider key is required only when the user chooses an LLM-judge metric. Set that key in the local environment. Heuristic metrics, including exact match, do not need one. Never put the key in chat or in the repository.

## Stage 1. Discover

Scan the repository yourself. Any framework counts. A route, a chat handler, an agent class, a prompt call, or a server that returns model output is enough to present a candidate. You may run `harness scan eval_onboarding` as a hint. Do not treat the hint as the only place an agent can be found, and do not write files in this stage.

Present what you found: endpoint, method, port, request field, response path, and auth header name. Also mention a sample case when you saw one. If several candidates are plausible, list them and ask the user to pick. Do not pick one by file order.

If nothing is found, say so, then ask one question at a time:

1. What local URL should the eval call?
2. Which HTTP method should the eval use?
3. Where does the request text go in the body?
4. Where is the reply text in the response?
5. Is an auth header name required? Reply with the header name, or none. Do not send a secret value.

Restate the values you will use, including any the user just gave.

Checkpoint: Use this endpoint, pick another candidate, or set one up?

## Stage 2. Stage files

After the user confirms an endpoint, show the proposed `.harness/evals` files and the sample cases before writing anything. Show the agent identifier, method, endpoint, request field, response path, auth header name or none, the dataset rows, and the metric. Use a real case when one exists. Otherwise show one minimal row and say that the expected value still needs a real answer.

Checkpoint: Write these files?

Only after yes, write them with `harness stage eval_onboarding`. Do not write the tree with a standalone script or by hand.

## Stage 3. Local run

Checkpoint: Run this eval locally?

Only after yes, run `harness run eval_onboarding --agent <identifier> --environment local`. Explain the result in plain language: what was called, what passed or failed, and any diagnostic the command printed. Do not hide a non-zero exit. Do not rewrite files unless the user asks.

Checkpoint: What should we do next: run against a live agent, upload to Harness, run on the platform, view results, or add cases or metrics?

## Later stages

Do only the stage the user picks. Ask before each action. After it finishes, ask the next-step checkpoint again.

### Run against a live agent

Ask for the live environment name. Show the command you will run.

Checkpoint: Run this eval against that live environment?

Then run `harness run eval_onboarding --agent <identifier> --environment <live-name>`. Explain the result the same way as the local run.

### Upload to Harness

Say that this validates the tree and uploads it, and that the CLI will print Agent and Eval links.

Checkpoint: Upload this eval to Harness?

Then run `harness upload eval_onboarding`. Show the links it prints.

### Run on the platform

Use the Eval link from the upload. Do not invent a separate CLI command for a platform run.

Checkpoint: Run it on the platform?

### View results

Use the Agent and Eval links the upload printed. Scores are on the eval's runs.

Checkpoint: Open the result links?

### Add cases or metrics

Ask what to add. Show the updated cases or metric, then ask before writing.

Checkpoint: Write these updated files?

Then call `harness stage eval_onboarding` again. Keep exact match unless the user asks for another metric. Switch to an LLM-judge metric only when the user chooses one, and only then show the LLM provider key step.
