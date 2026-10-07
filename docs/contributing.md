# Contributing: PR scope and review

Every PR is either a **core PR** or a **module PR**. Core PRs get a thorough review and require approval from the core CLI team (see [`.github/CODEOWNERS`](../.github/CODEOWNERS)). Module PRs get a lighter review and generally merge faster.

## Core vs module files

- **Module files:**
  - `modules/<module>/**`, including help text and tests — except `modules/core/`
  - `pkg/spec/<module>.spec.yaml` — except `core.spec.yaml`
- **Core files:** everything else, notably:
  - `modules/core/**`
  - `pkg/spec/core.spec.yaml`, `pkg/spec/spec.go`, and other Go in `pkg/spec/`
  - `cmd/harness/main-harness.go`
  - all other `pkg/**` and `cmd/**`, build/CI config, and repo-level docs (including this file and `AGENTS.md`)

`.github/CODEOWNERS` encodes the same split: paths owned by the core team are core; unowned paths are module.

## Rules

- **Module PRs touch only module files.** If a module change needs something new from the core, open a core PR first, then adopt it in a follow-up module PR.
- **Don't bundle a new core feature with its module adoption.** "Add `foo` support to the spec framework" and "use `foo` in the FME spec" are two PRs.
- **Core PRs may edit module files only for compatibility.** Example: renaming a spec YAML key, or a cross-cutting change that requires every spec to be updated mechanically. The module edits must be required by the core change, not new module behavior.
- **Adding a new module is a core change**, because it requires wiring in `cmd/harness/main-harness.go`. The module's own spec, help text, and Go can follow in module PRs once the wiring lands, or ride along in the same core PR.

## Module tests: test the module, not the framework

Module tests cover logic the module owns: custom Go handlers, field types, request transformations written in `modules/<module>/`. Keep them next to that code (e.g. `modules/fme/segment_keys_test.go`).

Do not write module tests that verify the spec framework works — e.g. a mock HTTP server asserting that a declared `body_params` entry reaches the request, that a `path` template is expanded, or that a declared flag is accepted. A spec does what it declares; that contract is verified once by core tests in `pkg/`. If a declared spec behaves incorrectly, that is a core bug: fix it with a core test in a core PR, not a module-specific workaround test.

See also the general [Test selection](../AGENTS.md#test-selection) guidance.
