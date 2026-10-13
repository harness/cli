# CI Module

Commands under `harness ... local_annotation` / `... error` / `... coverage` / `... ci_artifact` are meant to run inside a Harness pipeline step (invoked by lite-engine or a Run step script), though most also work from a developer workstation. They split into two groups:

- **Local annotation, error categorization, and coverage analysis** are local-only — they read/write local files and make no Harness API call, so they need no auth.
- **Coverage upload/wait-until and `ci_artifact` push/pull** talk to a Harness service over HTTP and do need auth — resolved automatically from pipeline env vars inside a step, or from the active CLI profile outside one.

---

## Annotations

`harness execute local_annotation` writes or merges one annotation into a local JSON file that lite-engine reads after the step finishes and posts back to the pipeline execution.

### Feature flag gate

The command is a **silent no-op** (prints a notice to stderr, exits 0) unless one of these is set:

- `CI_ENABLE_HARNESS_ANNOTATIONS=true`
- `PIPE_HARNESS_ANNOTATIONS=true`

This means it is always safe to call from a pipeline step script regardless of whether the account has the feature enabled.

### Flags

| Flag | Description |
|---|---|
| `--context` | Context ID of the annotation (required, max 256 chars, trimmed, truncated if longer) |
| `--style` | `info` \| `success` \| `warning` \| `error` (default `info`) |
| `--summary` | Inline markdown summary. Ignored if `--summary-file` is also given |
| `--summary-file` | Path to a summary file, max 64KB. Takes precedence over `--summary` |
| `--mode` | `append` \| `replace` \| `delete` (default `replace`) |
| `--priority` | 1–10 (default `3`) |

```
harness execute local_annotation --context build-status --style info --summary "Build completed"
harness execute local_annotation --context deployment --style success --summary-file ./deploy-notes.md
harness execute local_annotation --context test-results --style warning --summary "Some tests failed" --priority 5
harness execute local_annotation --context build-status --mode delete
```

### Merge semantics

Entries are keyed by `--context`. The first call for a given context creates it; a later call with the same context merges according to `--mode`:

- **`replace`** — overwrites style/summary/priority with the new values.
- **`append`** — appends the new summary onto the existing one (joined with `\n`); style/priority update only if a non-empty/non-zero value is given.
- **`delete`** — clears style/summary/priority on the entry (does not remove it from the file).

Root-level identifiers (`accountId`, `orgId`, `projectId`, `pipelineId`, `planExecutionId`, `stageExecutionId`) are read from `HARNESS_ACCOUNT_ID`/`HARNESS_ORG_ID`/`HARNESS_PROJECT_ID`/`HARNESS_PIPELINE_ID`/`HARNESS_EXECUTION_ID`/`HARNESS_STAGE_ID` and filled in only once — the first write across a step/stage wins, later writes don't overwrite them. `HARNESS_STEP_ID` is written per-entry on every call.

### Output file

Written to `$HARNESS_ANNOTATIONS_FILE`, or `./annotations.json` if unset. Writes are atomic (tmp file + rename), so a concurrent reader never observes a partial file.

On success, prints a JSON result to stdout:

```json
{
  "context": "build-status",
  "stepid": "step1",
  "message": "Annotation stored for context 'build-status' with step ID 'step1'"
}
```

---

## Error Categorization

An `errors.yaml` rule file matches a failed step's stdout/stderr/exit code against known failure signatures, so a pipeline can report *why* a step failed instead of just that it did.

### `errors.yaml` schema

```yaml
version: "1"
settings:
  evaluationMode: firstMatch
ruleGroups:
  - name: OOM Kill
    enabled: true
    conditionExpression:
      operator: OR
      conditions:
        - key: standardErrorOutput
          operand: contains
          value: "Killed"
        - key: errorCode
          operand: is
          value: 137
    actions:
      - type: setErrorCategory
        value: INFRASTRUCTURE
      - type: setErrorMessage
        value: "Step was killed, likely OOM"
```

- `conditionExpression` is a recursive AND/OR tree: a node has `operator`+`conditions`, a leaf has `key`+`operand`+`value` — never both on the same node.
- Valid `key`s: `standardOutput`, `standardErrorOutput`, `errorCode`, `stepId`, `stageId`, `pipelineId`.
- Valid `operand`s: `contains` (case-insensitive substring), `is` (exact, case-sensitive), `regex`, `isNot`, `doesNotMatch`.
- Valid action `type`s: `setErrorCategory`, `setErrorSubcategory`, `setErrorMessage`.
- Rule groups are evaluated in order; the first matching group wins (`firstMatch`).
- A disabled group (`enabled: false`) is skipped.

**Guardrails** (reject or bound a hostile/malformed file rather than hang or OOM): max 100 rule groups, max condition-tree depth 20, max regex pattern length 1000, max log line 64KB, max log file 25MB, and a 4-second evaluation timeout.

### `harness execute error:validate`

Human-readable syntax/structure check — run manually or in CI before trusting a rule file in a real pipeline.

```
harness execute error:validate --yaml-path=./errors.yaml
```

Prints a `✓`/`✗` report (version, rule group count, condition/action counts, enabled/disabled per group) and exits non-zero on any validation failure.

### `harness execute error:evaluate`

What lite-engine calls internally on every failed step. **Never hard-fails** — any internal error (parse failure, timeout) still prints a JSON result and returns exit 0, since categorization failing should never fail the step a second time.

```
harness execute error:evaluate --yaml-path=./errors.yaml \
  --stdout-path=./step-stdout.log --stderr-path=./step-stderr.log \
  --exit-code=1 --step-id=step1 --stage-id=stage1 --pipeline-id=pipeline1
```

| Flag | Description |
|---|---|
| `--yaml-path` | Path to the errors.yaml file (required) |
| `--cache-dir` | Cache directory for parsed rules (default: `$HARNESS_ERRORS_CACHE_DIR` or `/tmp/.harness-errors-cache`) |
| `--stdout-path` / `--stderr-path` | Paths to the step's log files |
| `--exit-code` | Exit code from the step (default `1`) |
| `--step-id` / `--stage-id` / `--pipeline-id` | Identifiers available to `conditionExpression` matching |

Parsed rules are cached (gob-encoded) keyed by the YAML file's content, so repeated `evaluate` calls against the same `errors.yaml` across many failed steps in one pipeline don't re-parse it each time.

Output shape:

```json
{"matched":true,"category":"INFRASTRUCTURE","subcategory":"","message":"Step was killed, likely OOM","matched_rule":"OOM Kill","source":"custom"}
```

`matched:false` (with no other fields) means no rule group matched. `timed_out:true` means evaluation hit the 4-second guardrail. `error` carries a parse/internal error message when one occurred — it is still printed with exit 0.

---

## Coverage

Coverage reports from Go/LCOV/JaCoCo/Clover/Cobertura/Coveralls files can be analyzed locally, uploaded to the Harness coverage service, and used as a pipeline quality gate.

### `harness execute coverage:analyze`

Local-only — parses coverage report file(s) and prints a summary (overall percentage, per-file coverage, hit/partial/miss counts, and patch coverage when `--base` is given or auto-detected). Format is auto-detected per file, and formats can be mixed across multiple `--file` flags. With no `--file`, coverage files are auto-discovered in common directories.

```
harness execute coverage:analyze --file coverage.out
harness execute coverage:analyze --file coverage.out --file lcov.info
harness execute coverage:analyze --file coverage.out --base origin/main
harness execute coverage:analyze --patch --base origin/main
```

| Flag | Description |
|---|---|
| `--file` | Path to coverage report file(s), repeatable |
| `--exclude` | Glob pattern to exclude (repeatable); prefix with `regex:` for a regex, e.g. `--exclude 'regex:.*_gen\.go$'` |
| `--patch` | Show patch coverage for changed lines only (requires `--base`) |
| `--base` / `--head` | Revisions to diff for patch coverage |

### `harness execute coverage:upload`

Parses local coverage report file(s) and uploads them to the Harness coverage service: registers the repository if needed, resolves exclude settings (coverage repository settings override `--exclude`), encodes each file, and uploads via a presigned URL. Most identifying fields (account, SCM provider/owner/identifier/commit/branch, pipeline, execution, pull request) are auto-detected from the CI environment and can be overridden.

```
harness execute coverage:upload --file coverage.out
harness execute coverage:upload --file coverage.out --base origin/main
harness execute coverage:upload --file coverage.out --target-scope <account>/<org>/<project>
```

Auth: `pipeline_auth` resolves `HARNESS_COVERAGE_SERVICE_TOKEN` / `HARNESS_COVERAGE_SERVICE_ENDPOINT` inside a pipeline step.

### `harness execute coverage:wait-until`

Polls the coverage service until all reports for the execution are processed, then evaluates coverage against the given thresholds — at least one of `--gt`/`--gte`/`--patch-gt`/`--patch-gte` is required, and the command exits non-zero if any threshold isn't met.

```
harness execute coverage:wait-until --gt 70
harness execute coverage:wait-until --gte 80 --wait-timeout 10m
harness execute coverage:wait-until --patch-gt 80
harness execute coverage:wait-until --gt 70 --patch-gte 80
```

`--wait-timeout` (default `5m`) is this command's own polling timeout, distinct from the global `--timeout` flag (which hard-kills the whole process). `--interval` (default `10s`) controls poll frequency. Account/execution auto-detect from the CI environment the same way as `coverage:upload`.

---

## Artifacts (`ci_artifact`)

`push ci_artifact` / `pull ci_artifact` move a generic build artifact in and out of Harness Artifact Registry (HAR) for the current pipeline's artifact version.

### Auth

Both commands declare a `pipeline_auth` block, so auth resolves the same way as any other pipeline-aware command (see [auth.md](auth.md)):

- **Inside a pipeline step**: resolved from `HARNESS_HAR_TOKEN` (token) and `HARNESS_HAR_URL` (registry base URL), sent as `Authorization: CIManager <token>`.
- **Outside a pipeline step**: falls back to the active CLI profile's registry URL and token.

### Artifact identity

Every push/pull targets `{name}/{version}` in a registry:

| Flag | Pipeline default |
|---|---|
| `--name` | `$HARNESS_PIPELINE_UNIQUE_ID` |
| `--version` | `"v" + $HARNESS_BUILD_ID` |
| `--registry` | `$CI_NATIVE_HAR_REGISTRY` |

The env var defaults only apply inside a pipeline step; pass the flags explicitly to push/pull from a workstation.

### `harness push ci_artifact <source>...`

```
harness push ci_artifact ./build/app.zip
harness push ci_artifact ./cache-dir --destination cache
harness push ci_artifact './dist/*.tar.gz,./reports/*.xml'
```

Each `<source>` may be:

- **A file path** — uploaded directly under the artifact version.
- **A directory** — walked recursively; the directory's own basename is preserved in the registry path (`./cache` → `{name}/{version}/[destination/]cache/...`).
- **A glob pattern** (e.g. `*.zip`, `dir/*`) — matched files are uploaded; patterns with no glob match also try as a regex against file basenames in the pattern's directory.
- **Comma-separated** — `"./dist/*.zip,./reports/*.xml"`, or pass multiple `<source>` args.

All files are included, including hidden ones (names starting with `.`). Symlinks and other non-regular files are silently skipped. Each file is capped at 5 GiB.

Files upload concurrently (5 workers). Each file retries up to 3 times with exponential backoff + jitter on transient failures; 4xx responses (except 429) and local filesystem errors (missing file, permission denied) fail immediately without retrying. `--destination` nests everything under an extra subdirectory within the version.

On full success, prints a summary and writes a merged JSON record to `$HARNESS_ARTIFACT_FILE_PATH` (if set):

```json
{
  "artifactName": "my-pipeline-unique-id",
  "version": "v42",
  "registry": "my-registry",
  "harPackageUuid": "...",
  "harVersionUuid": "...",
  "fileCount": 2,
  "totalBytes": 10240,
  "files": [
    {"name": "app.zip", "sourcePath": "/abs/path/app.zip", "destination": "/my-pipeline-unique-id/v42/app.zip", "size": 10240, "uploadedAt": "2026-01-01T00:00:00Z", "sha256": "..."}
  ]
}
```

If `$HARNESS_ARTIFACT_FILE_PATH` already exists (a prior `push ci_artifact` call in the same step wrote it), entries are merged by `destination` — the newest upload for a given destination wins — so parallel matrix jobs or multiple pushes in one step accumulate into one unified record. An exclusive advisory lock on a companion `.lock` file serializes the merge across concurrent processes. If any file fails to upload, the whole command fails and the output file is **not** written for that invocation.

### `harness pull ci_artifact <path-in-version>`

```
harness pull ci_artifact files/Dockerfile
harness pull ci_artifact files/Dockerfile --destination ./downloads/
harness pull ci_artifact reports/coverage.html --destination /tmp/coverage.html
```

`<path-in-version>` is the path relative to the version root — the same value supplied as `--destination` (joined with the filename) during the corresponding push. `--destination` controls where the file lands locally:

- Unset — saves the filename in the current directory.
- An existing directory, or a path ending in `/` — saves the filename inside it.
- Anything else — used as the exact output path.
