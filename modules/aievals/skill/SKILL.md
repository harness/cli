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

Do not change profiles automatically. To use an existing profile such as `qa`, pass it explicitly to commands that need Harness credentials:

- `harness run eval_onboarding --agent <identifier> --profile qa`
- `harness upload eval_onboarding --profile qa`

For automation, use the existing profile resolution with `HARNESS_PROFILE=qa` instead. An explicit `--profile <name>` takes precedence over `HARNESS_PROFILE`.

Do not write a standalone Python runner.
Do not create eval.yaml.

## What to scan

Discover routes only through supported, service-scoped Spring/JAX-RS/Micronaut, FastAPI, and confirmed Express adapters.

The scan parses every Java, Python, JavaScript, and TypeScript source into a syntax tree. Comments, string contents, and files that fail to parse never produce routes, DTOs, ports, or test evidence; skipped files and routes are reported.

Java supports Spring, JAX-RS, and Micronaut route annotations whose names resolve through an import or a fully qualified reference. Python supports only decorators bound to an imported `FastAPI` instance, or to an `APIRouter` mounted with `include_router` on a `FastAPI` app in the same module. JavaScript and TypeScript support only methods on an `express()` app, or on an Express router mounted with `use()` on that app in the same module. Route paths, prefixes, and parameter names must be string literals or literal concatenations. Do not treat generic `app.get`, route literals, client calls, DTOs, ports, or configuration strings as routes.

JAX-RS routes combine `@Path` with `@POST`, `@GET`, `@PUT`, `@DELETE`, or `@PATCH`; Spring supports `@RequestMapping` and verb mapping annotations; Micronaut supports `@Controller` plus verb annotations.

Ignore documentation, tests, generated output, dependencies, caches, and operational endpoints whose final normalized segment is `health`, `metrics`, `ready`, or `docs`. Tests are evidence only, bound to the selected route and service: each request expression keeps only the headers, body, and assertions attached to it.

Any one supported adapter route is a signal:

- a supported Java route annotation
- a FastAPI/APIRouter verb decorator
- a confirmed Express app/router verb method

Unsupported syntax cannot be selected with --route; use guided/manual configuration. Ambiguous supported routes require --route; --yes never authorizes either. `--yes` accepts only medium-confidence values for an already selected supported route.

Fill method, path, port, request field, and response path from whatever was seen. Use plain defaults for the rest: POST if a body is evident, otherwise GET; localhost on port 8080 when no port was seen; response path `output.text` or the first response field; agent id from the directory or service name. Use a real test case when one exists. When the expected value was not evidenced, write a minimal dataset row and report that gap without invalidating JSONL. Do not write nothing.

## Confidence

Auto-stage high-confidence values. Batch medium-confidence confirmations into one confirmation. Require `--yes` before staging that batch. Ask one batched question for low-confidence gaps.

Stage an unambiguous route as soon as a signal exists. When multiple plausible routes are found in any supported language or framework, show the service/source-qualified candidates and require an explicit normalized `--route '<METHOD path>'` selection before staging. A method-qualified selection keeps `GET /path` distinct from `POST /path`; if it still matches more than one service, append the service root shown in the candidate list, for example `--route 'POST /reports [services/billing]'`. Do not pick a route by source-file ordering or by the longest path.

Auto-stage high-confidence values. For medium-confidence route evidence, show the batch and require `--yes` before staging; this is the noninteractive acknowledgement. Do not silently accept that batch.

Infer secret names only, never values. Auth is optional. Never render `source: harness_secret` without a non-empty `ref`. If an explicit header lacks an evidenced secret reference, omit the header. `@HarnessAuth` alone does not identify a header or secret mapping, so omit the header and tell the user to configure authentication safely. Never invent a HARNESS_API_KEY header mapping. Never block on a feature flag such as TR_RCA_EVALUATION_API.

For Java records, infer the request and response DTO fields from the selected route only. Report required query parameters and path parameters, but configure their dynamic values manually before running: the pinned onboarding schema has an endpoint URL, headers, and request body template, not a separate parameter map.

`harness setup eval_onboarding` performs this scan. Follow its evidence list. Do not hand-write a second tree when the command staged one.

## No signal

When there is no route, no port, and no client call, switch to a short step-by-step guide. Do not claim an agent was staged. Ask one question at a time, in this order, and stage a file as soon as the answer arrives:

1. What local URL should the eval call?
2. Which HTTP method should the eval use?
3. Where does the request text go in the body?
4. Where is the reply text in the response?
5. Is an auth header name required? Reply with the header name, or none. Do not send a secret value.

After those answers, write the same `.harness/evals` tree the happy path writes and tell the user the exact local run command: `harness run eval_onboarding --agent <identifier> --environment local`.

## Staging

Stage candidate files in a temporary tree, invoke the D2 Python validator, compare destinations, and atomically write only missing or identical files.

The validator is `ai-evals` importing `ai_evals.onboarding.load.load_onboarding_tree` on a `.harness/evals` directory. A destination that already exists with different bytes is left unchanged, except when the separate `.onboard-guide-draft` sidecar marks a guided draft that a later guide answer may replace. An identical file is left in place. A missing file is written by renaming a temporary file in the same directory.

The tree is `agents/<identifier>/agent.yaml`, `datasets/<identifier>.jsonl`, and `metric-sets/<identifier>.yaml` under `.harness/evals`. Headers that need a secret use `source: harness_secret` with `ref` and `env` set to names when that name is already evidenced. The local environment maps that ref to the same environment variable name. A header with no evidenced secret reference is omitted.

## Run and upload

`harness run eval_onboarding` checks the pinned `ai-evals-runner` 0.7.7 handshake (`provisional-onboarding-1`) and then runs `onboard-run`. It does not upgrade the pin. A PAT or SAT is exported to the runner as `HARNESS_API_KEY`. An SSO access token is not sent to the Python runner.

`harness upload eval_onboarding` validates locally, zips `.harness/evals` with deterministic bytes, and uploads that zip through the active profile. The server returns Agent and Eval paths. Git sync is not applied by the upload.
