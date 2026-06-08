# Harness CLI 3.0 [Beta]

CLI for Humans and Agents. A unified CLI for Harness. Manage pipelines, artifacts, platform resources, and more from the command line.

---

## Install

```sh
curl -fsSL https://raw.githubusercontent.com/harness/harness-unified-cli/main/install.sh | sh
```

The installer will:

- Download the latest `harness-bundle` for your platform (macOS and Linux, amd64/arm64)
- Install both the `harness` and `harness-har` binaries to `~/.local/bin` (override with `--install-dir`)
- Optionally add `~/.local/bin` to your `PATH` and enable shell completions

Prefer to install manually? Download a release archive directly from [GitHub Releases](https://github.com/harness/harness-unified-cli/releases) and place the binaries on your `PATH`.

### Installer flags

| Flag                   | Description                                                    |
| ---------------------- | -------------------------------------------------------------- |
| `--install-dir <path>` | Override the install directory (default: `~/.local/bin`)       |
| `--core`               | Install only the `harness` binary (skips `harness-har`)        |
| `--non-interactive`    | Skip all prompts (useful for CI, Docker, provisioning scripts) |
| `--no-verify`          | Skip checksum verification                                     |

When passing flags via a pipe, use `sh -s --` — `-s` tells sh to read from stdin, and `--` separates sh's own options from the installer flags passed as `$@`.

```sh
# install bundle (default)
curl -fsSL https://raw.githubusercontent.com/harness/harness-unified-cli/main/install.sh | sh

# install harness only (no harness-har)
curl -fsSL https://raw.githubusercontent.com/harness/harness-unified-cli/main/install.sh | sh -s -- --core

# non-interactive install to a custom directory
curl -fsSL https://raw.githubusercontent.com/harness/harness-unified-cli/main/install.sh | sh -s -- --non-interactive --install-dir /usr/local/bin
```

### Upgrading

Once installed, upgrade to the latest version with:

```sh
harness install cli
```

Or install a specific version:

```sh
harness install cli --version v1.2.3
```

Check if a newer version is available without installing:

```sh
harness install cli --check
```

| Flag                   | Description                                                         |
| ---------------------- | ------------------------------------------------------------------- |
| `--install-dir <path>` | Override the install directory (default: `~/.local/bin`)            |
| `--force`              | Reinstall even if the current version is already up to date         |
| `--check`              | Print the resolved version without installing; exits 1 if not found |

---

## Shell Completions

Tab-completion is fully wired — completions for identifiers hit the live API and return `id<tab>Name` suggestions.

**Zsh:**

```sh
source <(harness completion zsh)
```

**Bash:**

```sh
source <(harness completion bash)
```

Add the appropriate line to your `.zshrc` or `.bashrc` to make it permanent. The installer can do this automatically.

---

## Auth

All commands resolve auth from (in order): `--profile` flag → `HARNESS_API_KEY` env var → `HARNESS_PROFILE` env var → CI runner env vars → default profile.

### Login

```sh
harness auth login
```

Launches an interactive TUI to set up a profile (requires a TTY). Use `--profile <name>` to log into multiple accounts:

```sh
harness auth login --profile staging
```

Profile config is saved to `~/.harness/config.yaml`; the token is stored in `~/.harness/credentials`.

For non-interactive use (CI, scripting), prefer the `HARNESS_API_KEY` env var instead of logging in. If you do need to create a profile non-interactively, see `harness auth login --help`.

### Change default org/project

Without arguments, launches an interactive TUI to select org/project. Pass flags to set directly:

```sh
harness auth setscope --org my-org --project my-project
```

### Check status

```sh
harness auth status
harness auth status --profile staging
```

### Logout

Clears the profile's credentials and removes it from the config:

```sh
harness auth logout
harness auth logout --profile staging
```

---

## Commands

The grammar is `harness <verb> <noun> [identifier] [flags]`. Use `--help` at any level.

### Supported commands

| Symbol | Meaning                                                     |
| ------ | ----------------------------------------------------------- |
| `✓`    | Supported                                                   |
| `P`    | Supports paging (`--limit`, `--offset`, `--all`, `--count`) |
| `L`    | Supports `--level` flag (account / org / project scope)     |
| `S`    | Set-fields — create via `--set` / positional args           |
| `GTP`  | Get-then-put — update via `--set` / `--del`                 |
| `Y`    | YAML file — outputs or accepts a YAML file via `-f`         |

Symbols combine — `PL` means paged and level-aware.

#### Discovery

| Command              | Description                                              |
| -------------------- | -------------------------------------------------------- |
| `list module`        | Show all available modules                               |
| `get module <name>`  | Domain model, nouns, and guides for a module             |
| `list noun`          | Show all available nouns                                 |
| `get noun <noun>`    | Fields and commands for a specific noun                  |
| `list noun --matrix` | **All nouns × verbs at a glance** — great starting point |

#### Platform / Access Control

| Noun              | list | get | create | update | delete | execute |
| ----------------- | ---- | --- | ------ | ------ | ------ | ------- |
| `account`         |      | ✓   |        |        |        |         |
| `organization`    | P    | ✓   | S      | GTP    | ✓      |         |
| `project`         | PL   | ✓   | S      | GTP    | ✓      |         |
| `user`            | PL   | L   |        |        |        |         |
| `user_group`      | PL   | L   |        |        |        |         |
| `service_account` | PL   | L   |        |        |        |         |
| `role`            | PL   | L   |        |        |        |         |
| `role_assignment` | PL   | L   |        |        |        |         |
| `resource_group`  | PL   | L   |        |        |        |         |
| `permission`      | P    | ✓   |        |        |        |         |
| `connector`       | PL   | L   |        |        |        |         |
| `secret`          | PL   | L   |        |        |        |         |
| `delegate`        | PL   | L   |        |        |        |         |
| `setting`         | PL   | L   |        |        |        |         |

#### Pipelines / CI/CD

| Noun                      | list | get | create | update | delete | execute |
| ------------------------- | ---- | --- | ------ | ------ | ------ | ------- |
| `pipeline`                | P    | Y   | Y      | Y      | ✓      | ✓       |
| `pipeline:summary`        |      | ✓   |        |        |        |         |
| `pipeline:input_set`      |      |     |        |        |        | ✓       |
| `pipeline_v1`             | ✓    | ✓   |        |        |        |         |
| `execution`               | P    | ✓   |        |        |        |         |
| `execution_step`          | ✓    |     |        |        |        |         |
| `execution_log`           | ✓    | ✓   |        |        |        |         |
| `trigger`                 | ✓    | ✓   |        |        |        |         |
| `input_set`               | ✓    | ✓   |        |        |        |         |
| `runtime_input_template`  |      | ✓   |        |        |        |         |
| `approval_instance`       | ✓    |     |        |        |        |         |
| `template`                | ✓    | ✓   |        |        |        |         |
| `freeze_window`           | ✓    | ✓   |        |        |        |         |
| `global_freeze`           |      | ✓   |        |        |        |         |

#### IaCM

| Noun        | list | execute |
| ----------- | ---- | ------- |
| `workspace` | P    | ✓       |

#### Artifact Registry

| Noun                        | list | get | create | update | delete | push | pull |
| --------------------------- | ---- | --- | ------ | ------ | ------ | ---- | ---- |
| `registry`                  | ✓    | ✓   | ✓      |        |        |      |      |
| `registry_metadata`         |      | ✓   |        | ✓      | ✓      |      |      |
| `artifact`                  | ✓    | ✓   |        |        | ✓      | ✓    | ✓    |
| `artifact_metadata`         |      | ✓   |        | ✓      | ✓      |      |      |
| `artifact_version`          | ✓    | ✓   |        |        | ✓      |      |      |
| `artifact_version_metadata` |      | ✓   |        | ✓      | ✓      |      |      |
| `artifact_file`             | ✓    |     |        |        |        |      |      |

---

## Output Formats

All commands support `--format`. The default is `text` for most commands; `list` commands default to `table`.

```sh
# list commands
harness list pipeline --format table     # default
harness list pipeline --format text
harness list pipeline --format json
harness list pipeline --format jsonl     # one JSON object per line
harness list pipeline --format csv
harness list pipeline --format tsv
harness list pipeline --format markdown

# get/other commands
harness get pipeline my-pipeline --format json
harness get pipeline my-pipeline --format text   # default
```

---

## Multiple Profiles

Use `--profile` to target a specific account/org/project config:

```sh
harness auth login --profile prod --api-token <token> --account <id>
harness list pipeline --profile prod
```
