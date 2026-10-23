---
name: aievals-onboarding
version: 1
schema: provisional-onboarding-1
---

# AI Evals onboarding

TODO(onboarding-schema): this skill and the `setup`, `run`, and `upload` verbs on `eval_onboarding` are provisional until the CLI owner approves them.

Enable the hidden module once with `harness install module aievals`. Then run:

- `harness setup eval_onboarding`
- `harness run eval_onboarding --agent <identifier> [--environment local]`
- `harness upload eval_onboarding`

`local` is the default environment. The CLI invokes `ai-evals onboard-run --root .harness/evals --agent <identifier> --environment local`.

Do not write a standalone Python runner.
Do not create eval.yaml.

## What to scan

Scan typed Java, TypeScript, and typed Python routes, DTOs, and tests before untyped route literals and tests.

Read typed Java (`@RestController`, `@PostMapping` / `@GetMapping`, request and response classes, `src/test` assertions), typed TypeScript (interfaces plus a typed route handler and tests), and typed Python (Pydantic models, a typed route, and tests) first. Untyped route literals and tests are best-effort.

Infer only these facts, each with a file and line:

- entrypoint
- endpoint path and HTTP method
- local port
- request field paths and response path
- auth header name and environment variable name
- real test cases (input and expected value)

Infer secret names only, never values. If a test contains a literal token, ignore that value and leave the environment variable name as a gap.

## Confidence

Auto-stage high-confidence values. A value is high when a typed route, DTO, or config literal is corroborated by a test.

Batch medium-confidence confirmations into one confirmation. Untyped route literals, untyped tests, and a response path that appears only in a test are medium.

Ask one batched question for low-confidence gaps. Do not invent a port, response path, or environment variable name. Ask for the variable name, never the secret value.

`harness setup eval_onboarding` performs this scan. Follow its evidence list. Do not hand-write a second tree.

## Staging

Stage candidate files in a temporary tree, invoke the D2 Python validator, compare destinations, and atomically write only missing or identical files.

The validator is `ai-evals` importing `ai_evals.onboarding.load.load_onboarding_tree` on a `.harness/evals` directory. A destination that already exists with different bytes is left unchanged. An identical file is left in place. A missing file is written by renaming a temporary file in the same directory.

The tree is `agents/<identifier>/agent.yaml`, `datasets/<identifier>.jsonl`, and `metric-sets/<identifier>.yaml` under `.harness/evals`. Headers that need a secret use `source: harness_secret` with `ref` and `env` set to names. The local environment maps that ref to the same environment variable name.

## Run and upload

`harness run eval_onboarding` checks the pinned `ai-evals-runner` 0.7.7 handshake (`provisional-onboarding-1`) and then runs `onboard-run`. It does not upgrade the pin. A PAT or SAT is exported to the runner as `HARNESS_API_KEY`. An SSO access token is not sent to the Python runner.

`harness upload eval_onboarding` validates locally, zips `.harness/evals` with deterministic bytes, and uploads that zip through the active profile. The server returns Agent and Eval paths. Git sync is not applied by the upload.
