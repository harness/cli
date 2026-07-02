# Harness CLI — Command Grammar and Registry

This document covers the command grammar (`harness <verb> <noun>`), the registry that backs it, and how handler dispatch works.

---

## Verb-first grammar

**The grammar is: `harness <verb> <noun> [identifier] [flags]`**

```
harness <verb> <noun> [id] [flags]              get, update, delete, execute
harness list <noun> [scope] [flags]             scope narrows results to a parent resource
harness <verb> <noun>:<qualifier> [id] [flags]  qualified variant (e.g. get pipeline:summary, execute pipeline:input_set)
```

Running `harness` with no arguments prints the full quick reference including all flags and examples.

### Rationale

The three consumers of the CLI, in order of priority, are:

1. **AI agents** — constructing and invoking commands programmatically
2. **Scripts and script authors** — increasingly written by agents, long-lived, parameterizable
3. **Power users** (SREs, field engineering, technical sales) — small group, will internalize the model

Agents and scripts share the same requirements: **predictability over discoverability**. A closed verb vocabulary is easier to reason about and generate correctly than a growing tree of module-owned subcommands.

Verb-first also ensures **intent leads** in scripts. `harness delete connector old-github` communicates what is happening before what it is happening to. This matters when auditing infrastructure scripts.

### Management exceptions

A small number of commands sit outside the verb/noun grammar because they operate on the tool itself, not on Harness resources:

```bash
harness auth      # manage authentication and credentials
harness version   # show version info
```

This set is closed. It does not grow as Harness adds resource types.

### Verb set

The framework enforces a closed verb set. Module teams cannot unilaterally introduce new verbs. New verbs require explicit framework-level approval.

#### Core verbs

These cover the standard CRUD, execution, and self-management operations.

| CLI verb    | Purpose                                                              |
| ----------- | -------------------------------------------------------------------- |
| `list`      | List resources with optional filtering                               |
| `get`       | Retrieve a single resource by identifier                             |
| `create`    | Create a new resource                                                |
| `update`    | Update an existing resource                                          |
| `delete`    | Delete a resource                                                    |
| `execute`   | Execute or trigger something (pipeline, workspace, connector test)   |
| `install`   | Download and install a binary locally (`cli`, `module`)              |

#### Module-approved verbs

Operations that require client-side workflows and don't map to a single API call. Proposed by module teams, approved at the framework level.

| Verb          | Noun       | Rationale                                                            |
| ------------- | ---------- | -------------------------------------------------------------------- |
| `push`        | `artifact` | Upload requires local archive parsing and streaming                  |
| `pull`        | `artifact` | Download with streaming progress and destination resolution          |
| `configure`   | `registry` | Local file system writes (`.npmrc` etc.) — no API call               |

---

## Registry as the source of truth

**The registry knows everything about the entire CLI surface — including commands backed by external binaries.**

Every noun, every supported verb, every flag, and every handler type is registered in the spec files before any command executes. Discoverability (`harness get noun`, `harness list noun`, shell completions, `--help`) works uniformly across all commands regardless of what executes behind them.

Spec files live at `pkg/spec/[module].spec.yaml`. Every module — builtin or plugin — has one. `harness` loads all specs at startup, so it always knows the full command surface including for plugins that aren't installed yet.

### Three handler types

```
registry entry
  └── Endpoint      →  single HTTP dispatch (API-backed resources)
  └── Workflow      →  multi-step Go handler (auth flows, install, push/pull)
  └── ExternalExec  →  exec to external binary (har and other plugins)
```

The user-facing grammar is identical for all three. A plugin binary does not need to self-describe — the spec file already declares what it accepts.

### Registry ownership and plugins

Each module owns its own spec file. See [plugins.md](plugins.md) for how builtin vs. plugin modules work, when to use each, and how the dispatch model is structured.

---

## Auth

See [auth.md](auth.md) for the full auth model, resolution order, and profile management.
