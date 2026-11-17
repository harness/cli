---
name: aievals
description: Conversational Harness AI Evals skill. The coding agent scans the repository and checks with the user before every write, run, or upload. The Harness CLI validates and writes the .harness/evals tree, runs the eval, and uploads it. This file can be served as an MCP prompt or resource.
version: 1
schema: onboarding/v1
---

# AI Evals

TODO(onboarding-schema): `setup`, `scan`, `stage`, `run`, and `upload` on `eval_onboarding` are provisional names.

This file is the single AI Evals skill. The Harness CLI embeds it, the product copy-prompt includes it, and a Harness MCP server can serve this same file as a prompt or resource.

The coding agent scans the repository. The slightest sign of an agent counts. Nothing is written, run, or uploaded without the user's yes. Infer secret names only, never values.
Persisting a completed local result is a separate, optional write. Show the
archive manifest mapping and ask the user before using
`harness upload eval_onboarding --persist-report <path>`.

Do not write a standalone Python runner.
Do not create eval.yaml.

The hosted Harness MCP server (https://mcp.harness.io/mcp) cannot read this repository, call localhost, build the upload zip, or call import-archive. Discover, Stage files, Local run, a live run against an endpoint that is not public, and Upload stay on the coding agent and the Harness CLI. Use the Harness CLI command named for each of those steps.

When the prompt header includes a `harness auth login` command, use that command for this account, organization, and project. If no header was provided, ask for the account, organization, and project before authenticating. Do not change profiles automatically. To use an existing profile such as `qa`, pass `--profile <name>` on commands that need Harness credentials, or set `HARNESS_PROFILE`. An explicit `--profile` takes precedence over `HARNESS_PROFILE`.

## Commands

- `harness setup eval_onboarding` installs or prints this skill. It does not scan and it does not write eval files. Optional `--skill-dir` chooses the destination. With no coding-agent skills directory and no `--skill-dir`, it prints the skill to stdout. For a developer-local runner only, set `HARNESS_CLI_HOME` to an isolated directory and pass `--runner-source <D2-worktree>`; setup copies and installs the checked-out 0.7.9 runner there. Without `--runner-source`, setup never uses a local source path.
- `harness scan eval_onboarding` is an optional hint. It prints an `onboarding/v1` candidate with id, confidence, evidence, and typed value sources. It writes nothing and does not generate a tree. `--route '<METHOD path>'` narrows the hint. The agent's own reading of the repository is the source of truth. If the hint and the agent disagree, show both and let the user choose.
- `harness scan eval_onboarding --metrics` reads the full metric catalog from the Harness Evals SDK in the pinned runner. It prints every catalog entry, including descriptions, constructor options and required options, default thresholds, LLM/embedding requirements, execution scope, and onboarding support. It does not scan the repository, install packages, call an agent or judge, or write files. Do not combine it with `--route`.
- Write the .harness/evals tree only with the Harness CLI validate-and-write command `harness stage eval_onboarding`. Pass `--confirm --file <path>` pointing at a temporary JSON file, or pass legacy flags that map unambiguously onto `onboarding/v1`. `--confirm` is required after the user says yes. Without it, the command writes nothing. The file uses `schema_version: "onboarding/v1"`. Value sources are `literal`, `input`, `env`, and `harness_secret` with D2 field names (`name`, `ref`, `env`). Do not use `env_name` or `secret_ref`. Never put a secret value in the file. Flags override the same legacy fields in a legacy file. They cannot be combined with an `onboarding/v1` file. Staging requires an explicit non-empty `cases` list with unique case identities; if scan has no cases, ask the user to confirm or add a sample case before staging.
- `harness run eval_onboarding --agent <identifier> [--environment local|<live-name>] [--report-json <absolute-path>]` runs a staged agent. `local` is the default. The command checks the pinned 0.7.9 runner and does not upgrade it. An older or stale runner handshake fails with exact remediation and leaves the files unchanged. Do not remove query parameters or auth headers to make a runner accept the tree. A PAT or SAT is passed to the runner. An SSO access token is not. `--report-json` is local-only: it passes the exact path to D2's `ai-evals onboard-run --report-json`, requires an existing parent outside the repository source tree, and prints the report path only after a successful runner exit. It does not upload or persist anything.
- `harness upload eval_onboarding` validates locally, uploads `.harness/evals`, and prints Agent and Eval links. Git sync is not applied by the upload. Without `--persist-report`, this is import-only.
- `harness upload eval_onboarding --persist-report <path>` uploads the archive, consumes the D3 manifest, shows the source-to-imported metric mapping, and persists an already-completed D2 `--report-json` result. Use this flag only after asking the user `Persist this completed local result in Harness?`. For a multi-eval archive, pass `--eval-identifier <identifier>`.

`stage` accepts `--confirm` plus `--agent`, `--endpoint`, `--method`, `--request-field`, `--request-fields`, `--response-path`, `--auth-header`, `--auth-env`, `--input-text`, and `--expected` for a legacy file. `--auth-header` is a header name. `--auth-env` is an environment-variable name. Never pass a secret value. A header name without an environment-variable name does not map to `onboarding/v1`, and the command writes nothing.

Metric selections go in the candidate file's `metrics` list. Each selection has an SDK `kind`, optional numeric `threshold`, optional unique `score_name`, constructor `options`, and optional `config`. Use `config.metadata` for non-secret provider/model settings and `config.criteria` for judge criteria. Never put keys or credentials in metric options or config. The stager derives metric type and dimension from the SDK catalog and checks construction without calling providers. This does not verify credentials, provider connectivity, or dataset suitability.

Cases preserve D2 JSON shapes for `input`, `expected`, `expected_output`, and `precomputed_output`: each may be a scalar, list, object, or null. They can also carry `context`, `expected_tools`, `expected_tool_calls`, `tags`, and `metadata`; these are preserved in JSONL. Use `cases` instead of `input_text`/`expected` when a metric needs these fields or there is more than one case. A dataset field is useful only if the runner maps it to the SDK field the metric reads; check that mapping before recommending trace, conversation, or telemetry metrics.

Example candidate file, illustrating the `onboarding/v1` shape only (not a default agent, endpoint, case, or metric recommendation):

```json
{
  "schema_version": "onboarding/v1",
  "identifier": "support",
  "name": "Support",
  "candidate": {
    "id": "support",
    "confidence": "high",
    "evidence": [{"source": "src/chat.py", "summary": "POST /chat", "locator": "src/chat.py"}]
  },
  "target": {
    "method": "POST",
    "response_path": "output.text",
    "request_template": {"text": {"source": "input", "selector": "text"}},
    "query_params": {"region": {"source": "env", "name": "REGION"}},
    "auth_headers": {"Authorization": {"source": "env", "name": "SUPPORT_TOKEN"}}
  },
  "environments": {"local": {"endpoint": "http://127.0.0.1:8080/api/chat", "secrets": {}}},
  "evals": [{"identifier": "support-eval", "name": "Support", "dataset": "datasets/support.jsonl", "metric_set": "metric-sets/support.yaml"}],
  "suite": {"identifier": "support-suite", "name": "Support", "pass_strategy": "all_must_pass", "evals": ["support-eval"]},
  "cases": [{"id": "case-1", "input": {"text": "hello"}, "expected": "ok"}],
  "metrics": [
    {"kind": "contains", "threshold": 1.0, "options": {"case_sensitive": false}}
  ]
}
```

The tree the command writes is `.harness/evals/agents/<identifier>/agent.yaml`, `.harness/evals/datasets/<identifier>.jsonl`, and `.harness/evals/metric-sets/<identifier>.yaml`. The agent file references that dataset and metric set and declares the suite. Include every user-selected metric, its options, threshold, and score name in the candidate. Never omit `metrics` in this workflow: omission retains a legacy exact-match fallback, not an intelligent recommendation. An empty list is rejected. If `stage` reports that a destination differs and was left unchanged, show that message and ask how to proceed. Do not overwrite the file by hand, delete existing files, or promise that re-staging will replace a non-identical tree.

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

Metric discovery requires the pinned runner but not Harness authentication or provider credentials. If it is missing or mismatched, show the CLI's exact remediation and ask before installing the pinned runner; do not silently upgrade or install a second SDK. Repeat catalog discovery after installation. If discovery fails, stop metric selection and explain the error instead of inventing a catalog or falling back to exact match.

Show provider setup only for the metrics the user chooses. LLM metrics require a configured judge; embedding metrics require an embedding backend; some metrics require both. Explain that these may make paid external calls and send case data to the selected provider. Agree on non-secret provider/model settings and credentials supplied through the provider's supported local environment variables. Heuristic metrics do not need a provider key. Never put a key in chat, command arguments, candidate files, or the repository, and never print environment-variable values.

## Stage 1. Discover

Scan the repository yourself. Any framework counts. A route, a chat handler, an agent class, a prompt call, or a server that returns model output is enough to present a candidate. You may run `harness scan eval_onboarding` as a hint. Do not treat the hint as the only place an agent can be found, and do not write files in this stage.

Present what you found: agent purpose, endpoint, method, port, request field, response path, and auth header name. Cite source paths for the contract, prompts, tools, tests, and sample cases. Identify output shape, whether wording varies, available reference answers, retrieval context, tool traces, conversation history, and telemetry. Mark missing information explicitly. If several agents or routes are plausible, list them and ask the user to pick. Do not pick one by file order, use a fixed agent identifier, or fabricate reference answers.

If nothing is found, say so, then ask one question at a time:

1. What local URL should the eval call?
2. Which HTTP method should the eval use?
3. Where does the request text go in the body?
4. Where is the reply text in the response?
5. Is an auth header name required? Reply with the header name, or none. Do not send a secret value.

Restate the values you will use, including any the user just gave.

Checkpoint: Use this endpoint, pick another candidate, or set one up?

## Generic local-agent prerequisite check

After the candidate is confirmed and before proposing a local run, check local prerequisites one at a time. Do not start a service, authenticate, or change a profile while checking them.

1. Confirm that the selected local service is already reachable at the confirmed endpoint. Use a documented, non-mutating readiness check when one exists; otherwise ask the user to confirm reachability. Never infer reachability from source code.
2. For every required query or path parameter, present the parameter name and its source mapping. Ask the user to confirm the environment-variable names that will supply dynamic values. Never ask for or print the values.
3. If authentication is required, present the evidenced header name. If the source only indicates an authentication annotation, ask whether a named header is required and which environment-variable name supplies it, or whether no header should be sent. A source annotation is not proof of a header mapping.
4. If the target or a selected metric needs an external LLM, confirm the provider/model or gateway setting and the required environment-variable names. Explain that credentials stay in the user's environment and are never entered into chat, command arguments, candidate files, or the repository.

Ask exactly one prerequisite question at a time and wait for the answer before asking the next. A successful readiness check does not authorize a write, run, upload, or report persistence. After all prerequisite answers, present the complete candidate and request confirmation again before any of those actions.

Checkpoint: Are the local service, environment-variable names, optional auth header mapping, and external LLM prerequisite ready?

### Choose metrics for the confirmed agent

Before proposing files, run `harness scan eval_onboarding --metrics`. Inspect the complete returned catalog, not just the first entries or a remembered shortlist. Summarize available categories and how many entries are supported by onboarding. Entries marked unsupported remain visible, but explain their limitation rather than recommending them as runnable.

Match catalog descriptions and options against the confirmed agent's purpose and observed data. Where a description is insufficient, read the selected metric's SDK implementation identified by `implementation` using the pinned runner's installed SDK; do not import repository code or install another SDK. Check required case fields and whether this runner actually supplies them. Check `execution_scope`: dataset-level and repeated-run metrics need the appropriate dataset or run grouping, not a single ordinary sample. Do not confuse constructor options with dataset fields or assume `metadata` populates arbitrary runtime fields.

Recommend a small, complementary set, with a table containing the exact SDK kind, why it fits this agent (with repository evidence), required case fields, required options, proposed threshold and rationale, and LLM/embedding cost/setup needs. Use SDK thresholds as starting points, not calibrated promises; preserve an explicit user threshold, including zero. Offer other relevant alternatives and let the user choose metrics not on the shortlist.

Use these as reasoning examples, not a fixed mapping or metric list; recommend a named kind only if it exists in the returned catalog:

- Stable labels or exact protocol responses: equality checks may fit. Open-ended reasoning or incident analysis: consider task-specific judge criteria, answer relevance, or reference-based similarity; exact wording usually does not measure correctness.
- Structured output: consider schema validation or structural comparison. Supply the actual schema or a user-confirmed structured reference, not a placeholder.
- Retrieval-grounded answers: consider faithfulness and context quality only when real retrieved context and any required reference answer can be supplied. Do not invent context or grade empty context.
- Tool-using or conversational agents: consider tool correctness, argument matching, or conversation quality only after verifying the available trace/history format reaches the fields the metric reads. Otherwise explain the instrumentation gap and offer output-level alternatives.
- Security, safety, latency, cost, repeated-run, or calibration checks: recommend only when appropriate to the use case and supported by real evidence/data. A catalog entry being constructible does not mean the HTTP target supplies its measurements or that this runner supports its execution scope.

Ask focused questions for missing prerequisites, such as reference answers, judge criteria, schema, or willingness to use a paid judge. If a metric cannot be meaningfully run yet, label it as blocked with the missing prerequisite and offer another metric. Do not silently drop a chosen metric, substitute exact match, fabricate instrumentation, or claim that syntactic validation proves meaningful scores.

Checkpoint: Which metrics and thresholds should we use, and are you comfortable with any listed provider requirements?

After the user chooses, resolve every required constructor option from the catalog's `options_schema`. Put constructor parameters under `options` (for example a schema or matching mode); use `config` only for supported judge fields and non-secret provider metadata. Use unique `score_name` values when choosing the same kind with different configurations. Confirm judge criteria and provider/model settings before staging or running. Re-run discovery if the installed SDK changes.

## Stage 2. Stage files

After the user confirms the agent and metrics, show the proposed `.harness/evals` files and sample cases before writing anything. Show the chosen agent identifier, method, full endpoint, request fields, response path, auth header name or none, dataset rows with the fields the metrics need, and every selected metric's kind, options, threshold, score name, and provider/model settings. Explain what each score measures. Reuse real cases from the repository where suitable, with source paths. Otherwise propose realistic cases for user review and identify missing references; do not present an invented expected answer as ground truth. Only metrics that do not require a reference can run meaningfully without one.

Checkpoint: Write these files?

Only after yes, create the temporary `onboarding/v1` candidate JSON with the confirmed `metrics` and cases and call `harness stage eval_onboarding --confirm --file <path> --repo <repository>`. Do not write the tree with a standalone script or by hand. If catalog lookup, options validation, or schema validation fails, show the failure and correct the candidate with user approval; never silently retry without the selected metrics. After success, read back the three generated files and verify the chosen agent, case fields, metric kinds, options, thresholds, and references were preserved. Report the directory paths.

## Stage 3. Local run

Checkpoint: Run this eval locally?

Only after yes, run `harness run eval_onboarding --agent <identifier> --environment local`. If the user wants to retain the completed local result for the optional persistence stage, agree on an absolute report path outside the repository source tree and add `--report-json <path>`. Explain the result in plain language: what was called, what passed or failed, and any diagnostic the command printed. Do not hide a non-zero exit. Do not rewrite files unless the user asks.

Checkpoint: What should we do next: run against a live agent, upload to Harness, run on the platform, view results, or add cases or metrics?

## Later stages

Do only the stage the user picks. Ask before each action. After it finishes, ask the next-step checkpoint again.

### Run against a live agent

This stage stays on the Harness CLI. The hosted MCP server cannot call localhost or an endpoint that is not public.

Ask for the live environment name. Show the command you will run.

Checkpoint: Run this eval against that live environment?

Then run `harness run eval_onboarding --agent <identifier> --environment <live-name>`. Explain the result the same way as the local run.

### Upload to Harness

This stage stays on the Harness CLI. The hosted MCP server cannot build the zip or call import-archive.

Say that this validates the tree and uploads it, and that the CLI will print Agent and Eval links.

Checkpoint: Upload this eval to Harness?

Then run `harness upload eval_onboarding`. Show the links it prints.

### Run on the platform

Check once whether the Harness MCP server is connected, and reuse that choice for the platform stages. Do not invent MCP tool names.

This stage applies only after the eval exists in the project. If MCP is connected, find the eval with `harness_list` or `harness_get` and `resource_type` `evaluation` (GET `/gateway/ai-evals/api/v1/orgs/{org}/projects/{project}/evals` and `.../evals/{id}`). Run it with `harness_execute`, `resource_type` `evaluation`, and action `run` (POST `.../evals/{id}/run`).

If MCP is not connected, use `harness list evaluation`, `harness get evaluation <id>`, and `harness execute evaluation:run <eval_id>`. MCP and the CLI call the same `/gateway/ai-evals/api/v1/orgs/{org}/projects/{project}/...` API, so the results are identical.

Checkpoint: Run it on the platform?

### View results

Reuse the MCP choice from the platform run. Scores are on the eval's runs. The Agent and Eval links from the upload open the same records.

If MCP is connected, use `harness_list` or `harness_get` with these resource types:

- `eval_run` for project runs and one run (`.../runs`, `.../runs/{id}`)
- `eval_run_by_eval` for runs of one eval (`.../evals/{id}/runs`)
- `eval_run_item` for items in a run (`.../runs/{id}/items`)

If MCP is not connected, use `harness list eval_run:all`, `harness get eval_run <run_id>`, `harness list eval_run <eval_id>`, and `harness list eval_run:items <run_id>`.

Checkpoint: Open the result links?

### Add cases or metrics

Read the existing agent, dataset, and metric-set files first and preserve the user's existing choices. Refresh the SDK catalog, repeat the recommendation/prerequisite check for the requested change, and show the updated cases or metrics before writing.

Checkpoint: Write these updated files?

Then call `harness stage eval_onboarding --confirm` with the full confirmed candidate, including existing and new metrics. The stager does not overwrite non-identical files: if it reports a collision, stop and ask the user how to handle it; do not bypass that protection. Preserve chosen metrics rather than resetting to exact match. Show LLM or embedding provider setup only when those metrics were chosen, and ask again before any subsequent run or upload.

### Optional Stage 5. Persist Results

This stage is optional. It uses a report emitted by the preceding D4 local run
and does not persist anything automatically. If no report was retained, ask
before rerunning locally with an absolute path outside the repository source
tree:

```bash
harness run eval_onboarding \
  --agent <identifier> \
  --environment local \
  --report-json <absolute-report-path>
```

The D4 command forwards `<absolute-report-path>` unchanged to D2's
`ai-evals onboard-run --report-json <absolute-report-path>` and prints the path
only after a successful runner exit. It does not call the API. Then run the
archive-only upload:

```bash
harness upload eval_onboarding
```

Show the imported eval and the D3 source metric/score-name to persisted
metric UUID/score-name mapping returned by that upload. Do not guess a missing
or ambiguous mapping. Then ask exactly:

`Persist this completed local result in Harness?`

Checkpoint: Persist this completed local result in Harness?

Only after the user says yes, invoke:

```bash
harness upload eval_onboarding --persist-report <path> [--eval-identifier <identifier>]
```

`<path>` must be the same completed report path printed by the successful D4
run. The CLI validates report shape and secret safety before network access,
maps every score using the full
`(source_metric_id, source_score_name)` pair, preserves D2 item external keys
and allowed dataset IDs, and retries uncertain report transport with the same
idempotency key. A 409 requires operator review; a 404 requires re-upload or
the correct eval; a 422 requires fixing the report or mapping. The server's
returned run URL is printed on 201/replay success. Hosted API profiles use
`/gateway`; a raw local API uses an explicit `--api-url http://localhost:<port>
--api-base-path /`.
