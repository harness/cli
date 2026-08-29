<div align="center">

# Harness CLI 3.0

**A unified, spec-driven CLI for the entire Harness platform. Built for Humans and Agents, supercharging the Developer Experience across Harness ecosystem.**

Manage pipelines, artifacts, code, GitOps, load testing, infrastructure, feature flags, governance, and platform resources
with a single consistent grammar.

[![License: Apache 2.0](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](LICENSE)
[![Made with Go](https://img.shields.io/badge/Made_with-Go-00ADD8.svg?logo=go)](https://go.dev)
[![Platform: macOS · Linux · Windows](https://img.shields.io/badge/Platform-macOS_·_Linux_·_Windows-lightgrey.svg)](#install)
[![Releases](https://img.shields.io/badge/Downloads-GitHub_Releases-brightgreen.svg)](https://github.com/harness/cli/releases)

[Install](#-install) ·
[Quickstart](#-quickstart) ·
[Grammar](#-grammar) ·
[Discovery](#-discovery) ·
[Commands](#-commands) ·
[Output](#-output-formats) ·
[Configuration](#-configuration) ·
[Contributing](#-contributing)

</div>

---

## Table of Contents

- [Why Harness CLI?](#-why-harness-cli)
- [Install](#-install)
- [Upgrade](#-upgrade)
- [Shell Completions](#-shell-completions)
- [Quickstart](#-quickstart)
- [Grammar](#-grammar)
- [Authentication](#-authentication)
- [Discovery](#-discovery)
- [Modules & Commands](#-commands)
- [Output Formats](#-output-formats)
- [Profiles & Scope](#-profiles--scope)
- [Configuration](#-configuration)
- [Global Flags](#-global-flags)
- [Exit Codes](#-exit-codes)
- [Build from Source](#-build-from-source)
- [Contributing](#-contributing)
- [License](#-license)

---

## ✨ Why Harness CLI?

- **One grammar, every resource** — `harness <verb> <noun>` works identically across pipelines, code, artifacts, GitOps, load testing, IaC, feature flags, governance, and platform.
- **Spec-driven** — commands are declared in YAML specs and wired at startup, so new resources arrive without waiting on custom code paths.
- **Self-describing** — every module, noun, field, and verb is queryable at runtime with `list module`, `get module`, `list noun --matrix`, and `get noun`.
- **Human and machine friendly** — the same command outputs a colored table for you, or JSON / JSONL / YAML / CSV / TSV / Markdown for scripts and agents.
- **Interactive when you want, headless when you don't** — TUI wizards for onboarding and picking, non-interactive flags for CI, and `HARNESS_API_KEY` for zero-config env-var auth.
- **Live log streaming** — follow pipeline executions with real-time SSE-based log tailing; `get execution_log --ui` opens an interactive log viewer with step navigation.
- **Interactive TUI (`--ui`)** — browse paginated lists, drill into PRs (details → AI review → conversation → checks → logs), and pick resources without memorizing IDs.
- **Harness Code, end to end** — clone repos (`pull repository`), review PRs (`list pr:review_pending`, `execute pr:review`), merge/label/comment, and read AI review insights (`get pr:insight`, `get pr:conversation`).
- **GitOps & load testing** — manage Argo CD agents, applications, clusters, and ApplicationSets; run JMeter/Locust/k6 load tests with `--follow` streaming.
- **SSO login** — `harness auth login --sso` (or the in-wizard "Login with SSO" option) drives a browser-based OAuth2 PKCE flow, no PAT/SAT required.
- **Tab completion that talks to the API** — completions for IDs return live `id<tab>Name` suggestions.
- **Multi-account, multi-profile** — named profiles let you jump between accounts, orgs, and projects on the same shell.
- **Agent-friendly** — detects and reports the coding agent (Claude Code, Cursor, Gemini CLI, Codex, Cline, and more) so operators know how the CLI is being driven.

---

## 📦 Install

### Recommended: one-line installer

**macOS / Linux**

```sh
curl -fsSL https://raw.githubusercontent.com/harness/cli/main/install.sh | sh
```

**Windows (PowerShell)**

```powershell
irm https://raw.githubusercontent.com/harness/cli/main/install.ps1 | iex
```

The installer will:

- Download the latest `harness-bundle` for your platform (macOS, Linux, and Windows — `amd64` / `arm64`).
- Install `harness` to `~/.local/bin` on Unix or `%LOCALAPPDATA%\Programs\harness` on Windows (override with `--install-dir` / `-InstallDir`).
- Install the `har` plugin from the same bundle into `~/.harness/bin`.
- Optionally add the install directory to your `PATH` and enable shell completions.

### Installer flags

| Flag                   | Description                                                    |
| ---------------------- | -------------------------------------------------------------- |
| `--install-dir <path>` | Override the install directory (default: `~/.local/bin` on Unix, `%LOCALAPPDATA%\Programs\harness` on Windows) |
| `--core`               | Install only the `harness` binary (skip the `har` plugin)      |
| `--non-interactive`    | Skip all prompts (useful for CI, Docker, provisioning scripts) |
| `--no-verify`          | Skip checksum verification                                     |

Windows PowerShell flags: `-InstallDir`, `-Version`, `-Core`, `-NonInteractive`, `-NoVerify`.

> [!NOTE]
> `install.ps1` reads assets from GitHub Releases by default. Set `HARNESS_INSTALL_BASE_URL` to a local directory or internal mirror holding the release zip and checksums file to install from there instead — useful for air-gapped environments and for testing unreleased builds.

> [!TIP]
> When passing flags through a pipe, use `sh -s --` — `-s` tells `sh` to read from stdin, and `--` separates `sh`'s own options from the installer flags.

```sh
# install core + har bundle (default)
curl -fsSL https://raw.githubusercontent.com/harness/cli/main/install.sh | sh

# install harness core only (skip the har plugin)
curl -fsSL https://raw.githubusercontent.com/harness/cli/main/install.sh | sh -s -- --core

# non-interactive install to a custom directory
curl -fsSL https://raw.githubusercontent.com/harness/cli/main/install.sh | sh -s -- --non-interactive --install-dir /usr/local/bin
```

```powershell
# Windows — install core + har bundle (default)
irm https://raw.githubusercontent.com/harness/cli/main/install.ps1 | iex

# Windows — core only, non-interactive
$env:HARNESS_NONINTERACTIVE=1; $env:HARNESS_CORE_ONLY=1; irm https://raw.githubusercontent.com/harness/cli/main/install.ps1 | iex
```

### Homebrew (macOS)

```sh
brew install --cask harness/tap/harness-cli
```

Installs the core `harness` binary only. Modules/Plugins are managed by the CLI, not by Homebrew.

### Manual install

Prefer to install by hand? Download an archive from [GitHub Releases](https://github.com/harness/cli/releases), place `harness` on your `PATH`, and register the bundled `har` plugin with `harness install plugin`. Unix bundles are `tar.gz`; Windows bundles are `zip`. Published for `linux_amd64`, `linux_arm64`, `darwin_amd64`, `darwin_arm64`, `windows_amd64`, and `windows_arm64`.

This is also the path to take if `curl | sh` doesn't work in your environment — e.g. WSL behind a corporate SSL-inspecting proxy, air-gapped/vetted-binary environments, or scripted installs — see [`docs/manual-install.md`](docs/manual-install.md) for step-by-step instructions.

---

## 🔄 Upgrade

The CLI can upgrade itself in place:

```sh
harness install cli                  # upgrade to latest
harness install cli --version v1.2.3 # install a specific version
harness install cli --check          # print the resolved version without installing (exits 1 if not found)
harness install cli --force          # reinstall even if already up to date
harness install cli --core-only      # skip module updates
```

| Flag                   | Description                                                          |
| ---------------------- | -------------------------------------------------------------------- |
| `--version <v>`        | Version to install (default: `latest`)                               |
| `--install-dir <path>` | Directory to install the core binary into (default: `~/.local/bin`)  |
| `--force`              | Install even if the current version is already up to date            |
| `--check`              | Print the resolved version without installing; exits 1 if not found  |
| `--core-only`          | Only install the core binary, skip module updates                    |

> [!NOTE]
> If you installed with Homebrew, upgrade with Homebrew: `brew upgrade --cask harness/tap/harness-cli`.

External modules are managed the same way:

```sh
harness install module har           # install the Artifact Registry plugin
```

Modules that aren't compiled into the CLI ship as plugins — separate binaries the
CLI installs into `~/.harness/bin` and dispatches to. `install module` is a
convenience form of `install plugin`, which also takes a tarball URL or a local
path for plugins that aren't Harness modules:

```sh
harness install plugin har                             # by name (same as install module har)
harness install plugin harness/cli                     # from a GitHub release (owner/repo)
harness install plugin https://example.com/foo.tar.gz  # from a tarball URL
harness install plugin ./foo.tar.gz                    # from a local tarball
harness install plugin all                             # upgrade every installed plugin
```

---

## ⌨️ Shell Completions

Tab-completion is fully wired and hits the live API where useful — completions for IDs return `id<tab>Name` pairs.

**Zsh**

```sh
source <(harness completion zsh)
```

**Bash**

```sh
source <(harness completion bash)
```

Add the appropriate line to your `.zshrc` or `.bashrc` to make it permanent. The installer can do this for you.

---

## 🚀 Quickstart

```sh
# 1. Log in (interactive TUI)
harness auth login

# 2. See the shape of the world
harness list module
harness list noun --matrix

# 3. Do something
harness list pipeline
harness execute pipeline my-pipeline --follow
harness get execution my-pipeline/<execution-id>
```

---

## 🧭 Grammar

Every command is:

```sh
harness <verb> <noun> [identifier] [flags]
```

### Verbs

| Verb           | Meaning                                                              |
| -------------- | -------------------------------------------------------------------- |
| `list`         | List resources of a noun (paginated).                                |
| `get`          | Fetch a single resource by ID.                                       |
| `create`       | Create a resource — supports `--file/-f` (YAML) or `--set key=value`.|
| `update`       | Update a resource — `--set key=value`, `--del key`.                  |
| `delete`       | Delete a resource by ID.                                             |
| `execute`      | Run, trigger, or invoke a resource (pipelines, scans, HQL, etc.).    |
| `push` / `pull`| Push/pull artifacts in HAR; clone a Code repo (`pull repository`).   |
| `configure`    | Configure a local client to use a Harness resource (e.g. a registry).|
| `install`      | Install or upgrade the CLI and its modules.                          |
| `auth`         | Manage authentication profiles.                                      |
| `version`      | Print the CLI version.                                               |

### Qualified nouns (`noun:variant`)

Some resources expose multiple variants of the same verb. The CLI uses a colon to qualify them:

```sh
harness get pipeline:summary <id>
harness execute pipeline:input_set <id>
harness execute pipeline:dynamic <id>
harness list pr:mine
harness list pr:review_pending
harness get pr:conversation <repo>/<pr>
harness execute pr:review <repo>/<pr> --decision approve
harness execute pr:merge <repo>/<pr>
harness execute pr:close <repo>/<pr>
harness pull repository <repo_id> [<dest-dir>]
harness execute loadtest my-test --follow
harness execute gitops_application:sync <agent>/<app>
harness get execution_log <pipeline>/<exec-id> --ui
harness push artifact:docker my-image:1.0
harness push artifact:npm ./my-package.tgz
harness execute artifact_version:firewall_scan <ver>
harness execute registry:migrate <registry>
harness execute execution:abort <id>
harness execute execution:retry <id>
harness execute approval_instance:approve <id>
harness execute evaluation:run <id>
harness execute feature_flag:kill <id>
harness execute hql:run
```

Run `harness list noun --matrix` at any time to see every qualified verb the CLI supports.

---

## 🔐 Authentication

Credentials resolve in this order:

1. `--profile <name>` flag
2. `HARNESS_API_KEY` env var
3. `HARNESS_PROFILE` env var
4. CI runner env vars (auto-detected)
5. Default profile from `~/.harness/config.yaml`

### Interactive login

```sh
harness auth login
```

Launches a TUI wizard that walks through the API URL, PAT/SAT token, and default org/project. Requires a TTY. "Login with SSO" is offered right in the URL picker, so SSO is reachable from plain `harness auth login` — no extra flag needed.

### SSO login

```sh
harness auth login --sso
```

Routes straight to the browser-based OAuth2 PKCE flow instead of prompting for a PAT/SAT token. `--org` / `--project` passed on the command line are respected and won't be overwritten by the picker.

### Non-interactive login (CI, scripting)

Prefer `HARNESS_API_KEY` for CI. If you need a saved profile without a TTY, pass credentials as flags:

```sh
harness auth login \
  --api-url  app.harness.io \
  --api-token <token> \
  --account   <id> \
  --org       <id> \
  --project   <id>
```

### Named profiles

```sh
harness auth login --profile staging
harness list pipeline --profile staging
```

Set the active profile for a whole shell session:

```sh
export HARNESS_PROFILE=staging
```

### Manage profiles

```sh
harness auth profiles              # list all saved profiles
harness auth status                # show resolved profile and validate credentials
harness auth setscope --org my-org --project my-project
harness auth env                   # print env vars for the current auth context
harness auth env --export          # prefixed with "export "
harness auth token                 # print the active API token
harness auth sso_status            # show SSO token expiry (SSO profiles)
harness auth sso_refresh           # refresh an SSO access token
harness auth logout                # remove a profile
```

Profile config is saved to `~/.harness/config.yaml`; the token is stored in `~/.harness/credentials`.

---

## 🔎 Discovery

The CLI is self-describing — you rarely need to leave the terminal to find a command.

```sh
harness list module                # every loaded module
harness get module <name>          # domain model, nouns, and guides for a module
harness list noun                  # every registered noun
harness list noun --matrix         # all nouns × verbs at a glance
harness get noun <noun>            # fields and commands for a specific noun
harness <verb> <noun> --help       # flags specific to a command
```

`get module <name> --matrix` prints the verb matrix scoped to a single module.

---

## 🧩 Commands

Legend used in the tables below:

| Symbol | Meaning                                                     |
| ------ | ----------------------------------------------------------- |
| `✓`    | Supported                                                   |
| `L`    | Supports `--level` (project / org / account scope)          |
| `S`    | Set-fields — create/update with `--set key=value`           |
| `GTP`  | Get-then-put — `--set` / `--del` semantics for updates      |
| `Y`    | YAML file — outputs or accepts a YAML body with `-f`        |

> [!NOTE]
> All `list` commands support paging (`--limit`, `--offset`, `--all`, `--count`).

<details open>
<summary><b>Core & Discovery</b></summary>

| Command              | Purpose                                                     |
| -------------------- | ----------------------------------------------------------- |
| `auth login`         | Interactive or non-interactive login to a profile           |
| `auth sso_status`    | Show SSO token expiry for the active profile                |
| `auth sso_refresh`   | Refresh an SSO access token                                 |
| `auth logout`        | Remove a profile                                            |
| `auth status`        | Show resolved profile and validate credentials              |
| `auth setscope`      | Set default org / project on a profile                      |
| `auth profiles`      | List all authentication profiles                            |
| `auth env`           | Print env vars for the current auth context                 |
| `auth token`         | Print the active API token                                  |
| `version`            | Print the CLI version                                       |
| `install cli`        | Install or upgrade the Harness CLI and installed modules    |
| `install module`     | Install a Harness CLI module (e.g. `har`)                   |
| `install plugin`     | Install a plugin by name, GitHub ref, tarball URL, or path  |
| `list plugin`        | List installed external plugin binaries                     |
| `get plugin <name>`  | Show metadata for an installed plugin                       |
| `list module`        | Show all available modules                                  |
| `get module <name>`  | Domain model, nouns, and guides for a module                |
| `list noun`          | Show all registered nouns (supports `--matrix`)             |
| `get noun <noun>`    | Fields and commands for a specific noun                     |

</details>

<details open>
<summary><b>Platform / Access Control</b></summary>

| Noun              | list | get | create | update | delete | execute |
| ----------------- | :--: | :-: | :----: | :----: | :----: | :-----: |
| `account`         |      |  ✓  |        |        |        |         |
| `organization`    |  ✓   |  ✓  |   S    |  GTP   |   ✓    |         |
| `project`         |  L   |  ✓  |   S    |  GTP   |   ✓    |         |
| `user`            |  L   |  ✓  |        |        |        |         |
| `user_group`      |  L   |  ✓  |        |        |        |         |
| `service_account` |  L   |  ✓  |        |        |        |         |
| `role`            |  L   |  ✓  |        |        |        |         |
| `role_assignment` |  L   |  ✓  |        |        |        |         |
| `resource_group`  |  L   |  ✓  |        |        |        |         |
| `permission`      |  ✓   |  ✓  |        |        |        |         |
| `setting`         |  L   |  ✓  |        |        |        |         |
| `connector`       |  L   |  ✓  |   S    |  GTP   |   ✓    |    ✓    |
| `delegate`        |  L   |  ✓  |        |        |        |         |
| `delegate_token`  |  ✓   |     |   ✓    |        |   ✓    |         |
| `agent`           |  ✓   |  ✓  |   S    |  GTP   |        |         |
| `secret`          |  L   |  ✓  |   S    |  GTP   |   ✓    |         |
| `entity_usage`    |  ✓   |     |        |        |        |         |

`execute connector:test` runs a connectivity test against a configured connector.

</details>

<details open>
<summary><b>Pipelines / CI · CD</b></summary>

| Noun                      | list | get | create | update | delete | execute |
| ------------------------- | :--: | :-: | :----: | :----: | :----: | :-----: |
| `pipeline`                |  ✓   |  Y  |   Y    |   Y    |   ✓    |    ✓    |
| `pipeline:dynamic`        |      |     |        |        |        |    ✓    |
| `pipeline:input_set`      |      |     |        |        |        |    ✓    |
| `pipeline:summary`        |      |  ✓  |        |        |        |         |
| `pipeline_v1`             |  ✓   |  ✓  |        |        |        |         |
| `execution`               |  ✓   |  ✓  |        |        |        |    ✓    |
| `execution:abort`         |      |     |        |        |        |    ✓    |
| `execution:retry`         |      |     |        |        |        |    ✓    |
| `execution:retry_history` |      |  ✓  |        |        |        |         |
| `execution_step`          |  ✓   |     |        |        |        |         |
| `execution_log`           |  ✓   |  ✓  |        |        |        |         |
| `trigger`                 |  ✓   |  ✓  |   S    |  GTP   |   ✓    |         |
| `input_set`               |  ✓   |  ✓  |   S    |  GTP   |   ✓    |         |
| `runtime_input_template`  |      |  ✓  |        |        |        |         |
| `template`                |  ✓   |  ✓  |   S    |        |        |         |
| `template_version`        |  ✓   |  ✓  |        |  GTP   |   ✓    |         |
| `approval_instance`       |  ✓   |  ✓  |        |        |        |    ✓    |
| `freeze_window`           |  L   |  ✓  |        |        |        |         |
| `global_freeze`           |      |  ✓  |        |        |        |         |

`execute pipeline` supports `--branch`, `--input-set`, `--input key=value` (repeatable), `--input-file`, and `--follow` (live log streaming that exits when the execution reaches a terminal state and inherits the execution's exit status).

`get execution_log <pipeline>/<exec-id> --ui` opens a live log viewer with step navigation, graph, and save-to-file. `create template` / `update template_version` round-trip YAML bodies with `-f`.

`execute approval_instance:approve` / `:reject` action a manual approval; `execute execution:abort` / `:retry` control a running execution; `update template_version:set-stable` promotes a template version.

</details>

<details open>
<summary><b>CD (Deployment)</b></summary>

| Noun               | list | get | create | update | delete |
| ------------------ | :--: | :-: | :----: | :----: | :----: |
| `service`          |  ✓   |  ✓  |   S    |  GTP   |   ✓    |
| `environment`      |  ✓   |  ✓  |   S    |  GTP   |   ✓    |
| `infrastructure`   |  ✓   |  ✓  |   S    |  GTP   |   ✓    |
| `service_override` |  ✓   |  ✓  |   S    |  GTP   |   ✓    |

</details>

<details open>
<summary><b>Code (Repositories & Pull Requests)</b></summary>

| Noun                    | list | get | create | update | delete | execute | pull |
| ----------------------- | :--: | :-: | :----: | :----: | :----: | :-----: | :--: |
| `repository`            |  ✓   |  ✓  |   S    |  GTP   |   ✓    |         |  ✓   |
| `pr`                    |  ✓   |  ✓  |   S    |  GTP   |        |         |  ✓   |
| `pr:mine`               |  ✓   |     |        |        |        |         |      |
| `pr:review_pending`     |  ✓   |     |        |        |        |         |      |
| `pr:merge`              |      |     |        |        |        |    ✓    |      |
| `pr:close`              |      |     |        |        |        |    ✓    |      |
| `pr:reopen`             |      |     |        |        |        |    ✓    |      |
| `pr:ready`              |      |     |        |        |        |    ✓    |      |
| `pr:edit`               |      |     |        |        |        |    ✓    |      |
| `pr:review`             |      |     |        |        |        |    ✓    |      |
| `pr:label`              |      |     |        |        |        |    ✓    |      |
| `pr:unlabel`            |      |     |        |        |        |    ✓    |      |
| `pr:insight`            |      |  ✓  |        |        |        |         |      |
| `pr:review_group`       |      |  ✓  |        |        |        |         |      |
| `pr:conversation`       |      |  ✓  |        |        |        |         |      |
| `branch`                |  ✓   |  ✓  |   S    |        |   ✓    |         |      |
| `commit`                |  ✓   |  ✓  |        |        |        |         |      |
| `tag`                   |  ✓   |     |   S    |        |   ✓    |         |      |
| `pr_activity`           |  ✓   |     |        |        |        |         |      |
| `pr_commit`             |  ✓   |  ✓  |        |        |        |         |      |
| `pr_check`              |  ✓   |  ✓  |        |        |        |         |      |
| `pr_check:log`          |      |  ✓  |        |        |        |         |      |
| `pr_comment`            |  ✓   |     |   S    |  GTP   |   ✓    |         |      |
| `commit_check`          |  ✓   |     |        |        |        |         |      |
| `pr_reviewer`           |  ✓   |     |   S    |        |   ✓    |         |      |
| `pr_codeowner`          |  ✓   |     |        |        |        |         |      |
| `code_principal`        |  ✓   |     |        |        |        |         |      |
| `repo_label`            |  ✓   |     |   S    |  GTP   |   ✓    |         |      |
| `pr_label`              |  ✓   |     |        |        |        |         |      |
| `pr_suggested_reviewer` |  ✓   |     |        |        |        |         |      |
| `pr_suggested_label`    |  ✓   |     |        |        |        |         |      |
| `pr_success_criterion`  |  ✓   |     |        |        |        |         |      |

`get pr` renders a rich PR summary; add `--ui` to browse details, AI review, conversation, checks, and logs interactively.

**Review workflow** — list PRs awaiting your review, submit a decision, and inspect CI checks:

```sh
harness list pr:review_pending
harness execute pr:review <repo>/<pr> --decision approve
harness list pr_check <repo>/<pr>
harness get pr_check:log <repo>/<pr>/<check-id>
harness pull repository <repo_id> [<dest-dir>]   # clone to a local directory
```

**AI review insights** — surface Harness Code's AI code-review output for a PR without leaving the terminal:

```sh
harness get pr:insight <repo>/<pr>              # risk summary for the PR
harness get pr:review_group <repo>/<pr>         # findings bucketed by risk group
harness get pr:conversation <repo>/<pr>         # full conversation thread view
harness list pr_suggested_reviewer <repo>/<pr>  # AI-suggested reviewers
harness list pr_suggested_label <repo>/<pr>     # AI-suggested labels
harness list pr_success_criterion <repo>/<pr>   # AI review success-criteria checks
```

</details>

<details open>
<summary><b>Artifact Registry (<code>har</code>) — external module</b></summary>

The `har` binary ships alongside `harness` in the default bundle. It manages registries, artifacts, and versions across every major package format.

| Noun                             | list | get | create | update | delete | execute | push | pull |
| -------------------------------- | :--: | :-: | :----: | :----: | :----: | :-----: | :--: | :--: |
| `registry`                       |  ✓   |  ✓  |   S    |        |   ✓    |         |      |      |
| `registry:firewall_scan`         |      |     |        |        |        |    ✓    |      |      |
| `registry:migrate`               |      |     |        |        |        |    ✓    |      |      |
| `registry_metadata`              |      |  ✓  |        |  GTP   |        |         |      |      |
| `artifact`                       |  ✓   |  ✓  |        |        |   ✓    |         |  ✓†  |  ✓   |
| `artifact_metadata`              |      |  ✓  |        |  GTP   |        |         |      |      |
| `artifact_version`               |  ✓   |  ✓  |        |        |   ✓    |    ✓    |      |      |
| `artifact_version:copy`          |      |     |        |        |        |    ✓    |      |      |
| `artifact_version:firewall_scan` |      |     |        |        |        |    ✓    |      |      |
| `artifact_version_metadata`      |      |  ✓  |        |  GTP   |        |         |      |      |
| `artifact_file`                  |  ✓   |     |        |        |        |         |      |      |

`configure registry <id> --client npm` wires a local package manager client to a Harness registry.

**Push variants (†)** — pick the variant that matches your package type; each validates the file format and sets the correct registry type:

```
push artifact:maven       push artifact:cargo       push artifact:swift
push artifact:npm         push artifact:go          push artifact:puppet
push artifact:python      push artifact:conda       push artifact:helm
push artifact:nuget       push artifact:dart        push artifact:docker
push artifact:rpm         push artifact:composer    push artifact:ruby
```

</details>

<details open>
<summary><b>Infrastructure as Code Management (<code>iacm</code>)</b></summary>

| Noun              | list | get | execute |
| ----------------- | :--: | :-: | :-----: |
| `workspace`       |  ✓   |  ✓  |    ✓    |
| `host`            |  ✓   |  ✓  |         |
| `inventory`       |  ✓   |  ✓  |         |
| `playbook`        |  ✓   |  ✓  |         |
| `registry_module` |  ✓   |  ✓  |         |
| `provider`        |  ✓   |  ✓  |         |

`execute workspace` runs plans/applies/destroys against a Terraform/OpenTofu workspace.

</details>

<details open>
<summary><b>GitOps (<code>gitops</code>)</b></summary>

Argo CD–backed GitOps: agents, applications, destination clusters, source repositories, and ApplicationSets. Compound IDs use `<agent>/<name>` or `<agent>/<uuid>` for ApplicationSets.

| Noun                      | list | get | create | update | delete | execute |
| ------------------------- | :--: | :-: | :----: | :----: | :----: | :-----: |
| `gitops_agent`            |  ✓   |  ✓  |   S    |        |   ✓    |         |
| `gitops_agent:install`    |      |     |        |        |        |    ✓    |
| `gitops_application`      |  ✓   |  ✓  |   Y    |   Y    |   ✓    |         |
| `gitops_application:sync` |      |     |        |        |        |    ✓    |
| `gitops_application:refresh` |   |     |        |        |        |    ✓    |
| `gitops_cluster`          |  ✓   |  ✓  |   Y    |   Y    |   ✓    |         |
| `gitops_repository`       |  ✓   |  ✓  |   Y    |   Y    |   ✓    |         |
| `gitops_application_set`  |  ✓   |  ✓  |   Y    |   Y    |   ✓    |         |

`create gitops_agent` registers the agent in Harness only — run `execute gitops_agent:install <id>` afterward to fetch Helm values or a kubectl manifest (the CLI does not install into your cluster). `execute gitops_application:sync` / `:refresh` reconcile application state from Git.

</details>

<details open>
<summary><b>Resilience Testing (<code>rt</code>)</b></summary>

Run JMeter, Locust, and k6 load tests against your services. A **loadtest** is a reusable definition; `execute loadtest <id> --follow` starts a run and streams until it finishes.

| Noun                         | list | get | create | update | delete | execute |
| ---------------------------- | :--: | :-: | :----: | :----: | :----: | :-----: |
| `loadtest`                   |  ✓   |  ✓  |   Y    |   Y    |   ✓    |    ✓    |
| `loadtest:from_json`         |      |     |   Y    |        |        |         |
| `loadtest:from_template`     |      |     |   Y    |        |        |         |
| `loadtest:variables`         |  ✓   |     |        |        |        |         |
| `loadtest:sync`              |      |     |        |        |        |    ✓    |
| `loadtest_run`               |  ✓   |  ✓  |        |  GTP   |        |         |
| `loadtest_run:stop`          |      |     |        |        |        |    ✓    |
| `loadtest_run:rerun`         |      |     |        |        |        |    ✓    |
| `loadtest_run:summary`       |      |  ✓  |        |        |        |         |
| `loadtest_run:metrics`       |  ✓   |     |        |        |        |         |
| `loadtest_run:graph`         |  ✓   |     |        |        |        |         |
| `loadtest_run:requests`      |  ✓   |     |        |        |        |         |
| `loadtest_run:endpoints`     |  ✓   |     |        |        |        |         |
| `loadtest_script`            |      |  ✓  |        |  GTP   |        |         |
| `loadtest_script:revisions`  |  ✓   |     |        |        |        |         |
| `loadtest_script:revision`   |      |  ✓  |        |        |        |         |
| `loadtest_template`          |  ✓   |  ✓  |   Y    |   Y    |   ✓    |         |
| `loadtest_template:variables`|  ✓   |     |        |        |        |         |
| `loadtest_template:yaml`     |      |  ✓  |        |        |        |         |
| `loadtest_template_revision` |  ✓   |  ✓  |   Y    |        |   ✓    |         |
| `composite_loadtest`         |  ✓   |     |   S    |        |        |         |
| `loadtest_usage`             |  ✓   |  ✓  |        |        |        |         |
| `loadtest_usage:report`      |      |  ✓  |        |        |        |         |

`create loadtest <id> -f test.yaml` is the usual path — every tool type requires a `toolConfig` block. Override runtime variables per run with `--set targetUsers=50` on `execute loadtest`.

</details>

<details open>
<summary><b>Governance (OPA policies)</b></summary>

| Noun                | list | get | create | update | delete |
| ------------------- | :--: | :-: | :----: | :----: | :----: |
| `policy`            |  ✓   |  ✓  |   S    |  GTP   |   ✓    |
| `policy_set`        |  ✓   |  ✓  |   S    |  GTP   |   ✓    |
| `policy_evaluation` |  ✓   |     |        |        |        |

</details>

<details open>
<summary><b>Audit Trail</b></summary>

| Noun          | list | get |
| ------------- | :--: | :-: |
| `audit_event` |  ✓   |  ✓  |

Filter with `--from` and `--to` for a specific time window.

</details>

<details>
<summary><b>Knowledge Graph & HQL</b></summary>

| Noun                | list | get | execute |
| ------------------- | :--: | :-: | :-----: |
| `kg:type`           |  ✓   |  ✓  |         |
| `kg:queryable_type` |  ✓   |     |         |
| `kg:related_type`   |  ✓   |     |         |
| `kg:connection`     |  ✓   |     |         |
| `hql:run`           |      |     |    ✓    |
| `hql:validate`      |      |     |    ✓    |
| `hql:explain`       |      |     |    ✓    |
| `hql:grammar`       |      |     |    ✓    |

HQL is Harness's graph query language over the unified schema. Run `execute hql:grammar` to fetch the full grammar; `execute hql:validate` and `execute hql:explain` help iterate on queries before you run them.

```sh
harness execute hql:run --query 'find entity "platform:project" | select { * } | limit 10'
```

</details>

---

## 📤 Output Formats

Every command supports `--format`. `list` commands default to `table`; single-resource commands default to `text`.

```sh
harness list pipeline --format table      # default for lists
harness list pipeline --format json
harness list pipeline --format jsonl      # one JSON object per line — stream friendly
harness list pipeline --format yaml
harness list pipeline --format csv
harness list pipeline --format tsv
harness list pipeline --format markdown

harness get  pipeline my-pipeline --format text   # default for get
harness get  pipeline my-pipeline --format json
harness get  pipeline my-pipeline --format yaml   # object only — ready to edit and pass back with `update -f`
```

Shorthands: `--json` == `--format json`, `--yaml` == `--format yaml`.

### Custom columns and fields

```sh
harness list pipeline --list-columns                       # show available columns
harness list pipeline --columns name,tags
harness list pipeline --columns "+lastRun"                 # add to defaults
harness list pipeline --columns "Owner:it.metadata.owner"  # rename with an expression

# Per-noun default columns (semicolon-separated noun=cols pairs)
export HARNESS_CLI_COLUMNS="pipeline=id,name,status;execution=id,status,started"

harness get  pipeline my-pipeline --list-fields
harness get  pipeline my-pipeline --fields name,git_url    # tab-separated for `read` / `$( ... )`
```

Other output flags:

| Flag              | Description                                                  |
| ----------------- | ------------------------------------------------------------ |
| `--no-headers`    | Suppress table/CSV headers and paging footer                 |
| `-o`, `--out`     | Write output to a file instead of stdout                     |
| `--raw`           | Emit the full raw API response (only with `--format json`)   |

---

## 🗂 Profiles & Scope

### Multiple profiles

```sh
harness auth login  --profile prod --api-token <token> --account <id>
harness list pipeline --profile prod
export HARNESS_PROFILE=prod   # session-wide switch
```

### Scope flags

Every command accepts scope overrides:

```sh
harness list pipeline --org my-org --project my-project
harness list secret   --level account          # target account scope
harness list secret   --level org --org my-org # target an org
```

### Paging

```sh
harness list pipeline               # default page (limit 20)
harness list pipeline --limit 100
harness list pipeline --offset 40 --limit 20
harness list pipeline --all         # fetch every page
harness list pipeline --count       # just the total count
```

---

## ⚙️ Configuration

### Environment variables

| Variable                  | Description                                                                     |
| ------------------------- | ------------------------------------------------------------------------------- |
| `HARNESS_API_KEY`         | API token. Takes precedence over saved profile credentials.                     |
| `HARNESS_ACCOUNT_ID`      | Account ID. Used together with `HARNESS_API_KEY` for env-var auth.              |
| `HARNESS_PROFILE`         | Name of the saved profile to use. Same effect as `--profile <name>`.            |
| `HARNESS_DEFAULT_ORG`     | Default org for commands that need one. Overridden by `--org`.                  |
| `HARNESS_DEFAULT_PROJECT` | Default project for commands that need one. Overridden by `--project`.         |
| `HARNESS_API_URL`         | Override the API URL (advanced; typically only needed for self-hosted Harness). |
| `HARNESS_DEBUG`           | Set to `1` to enable debug logging without passing `--debug`.                   |
| `HARNESS_NO_COLOR`        | Set to `1` to disable ANSI colors. `NO_COLOR` is also respected.                |
| `HARNESS_CLI_COLUMNS`     | Per-noun default `--columns` overrides (`pipeline=id,name;execution=id,status`). |
| `HARNESS_CONFIG_HOME`     | Override the location of `~/.harness/`.                                         |

Common CI runner env vars are auto-detected. `HARNESS_API_KEY` always wins.

### Config files

- `~/.harness/config.yaml` — named profiles (API URL, account ID, default org/project)
- `~/.harness/credentials` — tokens (kept out of the config so it can be safely shared/committed)

---

## 🚩 Global Flags

Flags below work on every command.

**Scope**

| Flag                          | Description                                    |
| ----------------------------- | ---------------------------------------------- |
| `--profile <name>`            | Use a named auth profile                       |
| `--org <id>`                  | Override the resolved org                      |
| `--project <id>`              | Override the resolved project                  |
| `--level account\|org\|project` | Set scope for multi-level nouns              |

**Behavior**

| Flag                    | Description                                                                                    |
| ----------------------- | ---------------------------------------------------------------------------------------------- |
| `--debug`               | Enable debug logging                                                                            |
| `--timeout <seconds>`   | Abort after N seconds; accepts decimals (`1.5`); `0` = no timeout; exits `124` on timeout      |
| `--ui`                  | Launch an interactive TUI — browse lists, pick resources, or open the live execution log viewer (requires a TTY) |
| `-h`, `--help`          | Help for the current command                                                                    |

---

## 🚦 Exit Codes

| Code  | Meaning                                                     |
| ----- | ----------------------------------------------------------- |
| `0`   | Success                                                     |
| `1`   | Generic failure (validation error, API error, not found …)  |
| `2`   | Usage error — invalid flags or unknown command              |
| `124` | Command timed out (`--timeout` exceeded)                    |
| `130` | Interrupted by the user (`Ctrl+C`)                          |

`execute pipeline --follow` exits with the pipeline's terminal status — a successful execution is `0`, a failed one is `1`.

Use exit codes in scripts:

```sh
if ! harness list pipeline --search deploy-prod --json | jq -e 'length > 0'; then
  echo "no matching pipelines"
  exit 1
fi
```

---

## 🛠 Build from Source

Requires [Task](https://taskfile.dev) and Go 1.26+.

```sh
brew install go-task
task build            # builds bin/harness and bin/harness-har
task build:main       # builds only bin/harness (faster; skips har)
```

Add the built binaries to your `PATH` for the duration of the session:

```sh
source local-setup.zsh
```

For details on the release process, see [`BUILD.md`](BUILD.md).

---

## 🤝 Contributing

Contributions are welcome. Most command additions are pure YAML edits under `pkg/spec/` — no Go required. See [`AGENTS.md`](AGENTS.md) for a deep dive on the spec-driven design, and open an issue or pull request on GitHub to get started.

- **Report a bug or request a feature** → [open an issue](https://github.com/harness/cli/issues)
- **Send a change** → open a pull request against `main`

---

## 📜 License

Licensed under the [Apache License 2.0](LICENSE). Copyright © 2026 Harness Inc.
