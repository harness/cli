# Verb Reference — How to Classify an Operation

One section per verb. Each says what the verb *means* as an action-class and, more
importantly, where its boundary with the neighboring verbs falls. Use this to answer "is this
`import` or `execute`?" — not to look up flags.

For the grammar itself see [verb-arch.md](verb-arch.md). For the decision procedure that
picks between a verb, a noun, and a colon variant see [verb-variants.md](verb-variants.md).

---

## Before the verb sections: three tests

Apply these in order. Most classification arguments end at the first one.

1. **What happens to Harness?** The verb is fixed by the effect on Harness state, not by
   where the input came from. Reading a GitLab group and creating repos in Harness is the
   same operation as reading a Drone bundle and creating repos in Harness. The source is a
   parameter.
2. **What is the id?** **The id identifies the noun.** That is the entire rule — if the
   identifier you would pass is not an identifier *of the noun*, the noun is wrong.
   `execute repository:pr <pr-id>` is wrong because a PR id does not name a repository; the
   command is `execute pr:<variant> <pr-id>`. Applied to migration it settles source nouns
   with no extra argument: `my-group/my-repo` is a GitLab identifier, so the noun is
   `gitlab_repository`. `repository` is Harness Code's repo and takes a *Harness* repo id.
   If there is no identifier at all, there may be no noun (`no_id: true`).
3. **Does the shape tell the truth before dispatch?** A flag that changes what the positional
   argument *means*, or that reverses the verb's direction, is a different verb hiding in a
   boolean.

**Verbs are action-classes, not literal operations.** `execute` does not mean "run a
pipeline"; it means "initiate work that yields a run or result." A firewall scan, a terraform
apply, and a connector test are all members of that class. Once the class is right, a subtype
*within* it is a colon variant, not a new verb.

**Blast radius decides how visible a distinction must be.** Where two operations have
opposite consequences — one mutates Harness, one writes a local file — the distinction goes
in the verb, the first token, visible in any audit of a script. Where the consequences are
identical either way, it can live in the noun.

---

## Summary

**Status is load-bearing:** only `implemented` verbs can back a new command today. A
`proposed` verb needs framework approval and a `verb.go` entry before any spec may use it —
don't add `import` to a spec file on the strength of this doc.

| Verb | One-line meaning | Status |
| ---- | ---------------- | ------ |
| `list` | Return zero or more resources of one type, optionally scoped to a parent. | implemented |
| `get` | Retrieve one resource by its identifier. | implemented |
| `create` | Bring a new Harness resource into existence. | implemented |
| `update` | Write a resource's fields — reversible by writing the old value back. | implemented |
| `delete` | Destroy a server-side Harness resource. | implemented |
| `execute` | Initiate work that yields a run or result. | implemented |
| `push` | Upload a local artifact to a Harness registry. | implemented (artifact only) |
| `pull` | Download an artifact from a Harness registry to disk. | implemented (artifact only) |
| `install` | Place a binary or component into local state. | implemented |
| `configure` | Write durable local config pointing a third-party client at Harness. | implemented (registry only) |
| `convert` | Translate a representation into another; output is a local file, never Harness state. | proposed |
| `import` | Read a foreign system and create the corresponding resources in Harness. | proposed |
| `uninstall` | Remove a locally installed component. Reverse of `install`. | proposed |
| `export` | Move a large Harness resource out to another system, mediated by the CLI. | reserved — no occupant |
| `wrap` | Run a local tool with Harness config injected. **Takes a tool name, not a noun.** | proposed |

`auth`, `version`, and `debug` are not resource verbs — they operate on the tool itself and
sit outside the verb/noun grammar. That set is closed.

---

## `list`

Return zero or more resources of one type, with optional filtering and an optional parent
scope. Never mutates. The identifier slot holds a *parent* id, not the resource's own id.

The useful boundary is against `get`: `list` is chosen by cardinality, not by whether
filtering happens. `list pipeline my-project` is still a list even when it returns one row.
If a command's natural answer is "here is the one thing you named," it is `get`.

`list` is also independent evidence for a separate noun. `list gitlab_repository --group X`
answers "what would import, before I commit to it" — an operation `import` cannot express
without a `--dry-run` flag. When two things support different valid verbs, that is evidence
they are different nouns.

## `get`

Retrieve a single resource by identifier. Never mutates.

Alternative *representations* of the same resource are colon variants, not new verbs:
`get pipeline:summary`. Alternative *formats* are flags or `field_extract` (`get pipeline`
already exposes `pipeline_yaml`). Both of these are why `export` is not needed for
single-resource extraction in a portable format — that is already `get`.

`get` accepting several id forms (name, uuid, email) is a handler concern, not a grammar
concern. `get user <id-or-email>` is one command.

## `create`

Bring a new Harness resource into existence. The id is either supplied or server-assigned.

`create` covers a wide range of input shapes — a flag set, a `-f` YAML file, a remote git
reference — and that variance is expected rather than a smell. Alternative payload or
invocation modes are colon variants (`create pipeline:remote`).

The boundary against `import`: `create` takes input the user authored or already holds in
Harness's own vocabulary. `import` reads a *foreign system* and derives resources from it.
`create pipeline -f harness-pipeline.yaml` after a `convert` is `create`, not `import` — by
that point the YAML is a Harness artifact.

## `update`

Mutate an existing resource's state. This absorbs every state change, including ones that
sound like actions: toggling a feature flag is `update fme_flag`, not a new verb. Enable,
disable, activate, archive, pause, and resume are all `update` unless they kick off work.

**The test is reversibility of the resource's state, not idempotence.** An `update` writes a
field; writing the old value back returns the resource to where it was. `update` may still
have side effects out in the world — notifications fire, webhooks trigger, audit rows are
written — and those are not undone. But nothing *happened to the resource* that a second
`update` cannot put back. That is what makes `update` the cheap, safe verb.

`execute` is the opposite: it has an **effect**, and there is no inverse call. This is the
distinction that decides the cases that feel genuinely ambiguous — both verbs write to the
same resource, so "does it change state?" cannot separate them:

- `update pr --title "..."` — a field. Set it back and the PR is as it was. → **`update`**
- `execute pr:merge` — commits land on the target branch, the PR closes, builds fire. There is
  no un-merge; recovering means new work (a revert commit), not a reverse call. → **`execute`**

So the question is not "did a field change?" but **"is there a call that puts this back?"** —
and specifically, *the same call with the old value*. A revert is not an un-commit; it is
another commit. Two `execute`s that cancel each other out are still both `execute`, because
each one **yields something new** — a commit, a run, a merge record — where `update` only
overwrites a field and yields nothing. If recovery means adding to the history rather than
rewriting a value, it is `execute`.

The pull toward inventing verbs here is strong and should be resisted. A verb that describes
a *value* a field can hold is not an action-class. `merge`, though, is not a value a field
holds — it is an effect, which is why it is `execute pr:merge` and not a new verb *or* an
`update`.

## `delete`

Destroy a server-side Harness resource. Terminal, and the only negation verb needed for
anything that lives in an account.

`delete` carries "the thing itself is gone," which is exactly why it is wrong for local
install state — see [`uninstall`](#uninstall). If a resource is only being detached,
deactivated, or unlinked, that is `update`.

## `execute`

Initiate work that yields a run or a result — synchronously or asynchronously. Pipeline runs,
terraform applies, connector tests, chaos experiments, syntax checks.

This is the verb most at risk of absorbing the universe, and the discipline that prevents it
is picking the action-class first. If the command *reads* existing state, it is
`get`/`list`. If it *changes a field you could write back*, it is `update`. Only if it has an
effect with no reverse call is it `execute`. Once you are inside the class, subtypes are
colons: `execute pipeline:input_set`, `execute artifact:firewall_scan`.

**`execute` has no inverse** — that is its defining property, not a limitation. You do not
un-execute; at most you `delete` or cancel the run resource the execution produced, which is
a different operation on a different noun. When a command looks like it edits a resource but
cannot be walked back by writing the old value — merging a PR, applying a plan, promoting a
release — it is `execute`, not `update`. See [`update`](#update) for the worked contrast.

## `push`

Upload a local artifact to a Harness registry. A module-approved verb because it is a
client-side workflow — local archive parsing, streaming, progress — not one HTTP call.

`push` is not the general verb for "send data to Harness." It is specifically the
local-file-to-registry transfer half of the `push`/`pull` couplet. Sending a config
*definition* is `create` or `update`; deriving resources from a foreign system is `import`.

## `pull`

Download an artifact from a Harness registry to the local filesystem, with streaming and
destination resolution. Mirror of `push`.

The boundary against `get` is what lands where: `get` returns a resource's metadata or
representation on stdout; `pull` writes bytes to a file on disk. The boundary against
`export`: `pull` retrieves one stored blob to the local filesystem, unchanged and with no
destination system involved.

## `install`

Download and place a binary or component in local state — the CLI itself, a plugin, a module.
Touches the local machine, not a Harness account.

The id slot is deliberately loose: a name, a tarball URL, a local tarball, a local binary
path. That looseness is the reason `--uninstall` cannot be a flag on it — uninstall accepts
only one of those forms, so the flag would silently redefine the positional.

## `configure`

Write local configuration so a third-party client points at Harness — `.npmrc` and friends.
No API call at all; the entire effect is on the local filesystem.

The boundary against [`wrap`](#wrap) is persistence. `configure` mutates durable local
config, which then affects every later invocation of that tool by anyone. `wrap` injects
config into one child process and leaves nothing behind. Both are legitimate; they answer
different questions ("set up my machine" vs. "run this one command correctly").

The boundary against `update`: `configure` never touches server state.

---

## `convert`

*Proposed.* Translate a representation into another. Reads a file or a Harness resource,
writes a local file. **Never mutates Harness, requires no auth, contacts no network.**

```sh
harness convert gitlab_pipeline .gitlab-ci.yml --out harness-pipeline.yaml
harness convert jenkins_job config.xml          # --out defaults to stdout
```

The `no_auth` property is not an incidental detail — it is the classification test. If the
command must reach an account to do its job, it is not `convert`. This also makes `convert` a
real product surface: a prospect who has not signed up can run it.

`convert` is not a mode of `import`, because for pipeline configs there is no import at all.
The output is a file you commit, review, edit, and eventually `create pipeline -f`. Because
the target is always the filesystem, the operation is symmetric — which is the one place
direction may live in the *noun* rather than the verb: `convert gitlab_pipeline <file>`
inward, `convert pipeline:gitlab <id>` outward. Getting the direction backwards produces a
wrong file and you notice immediately, so the blast radius is identical either way.

## `import`

*Proposed.* Read an external system or bundle and create the corresponding resources in
Harness. **Mutates Harness. Requires auth.**

```sh
harness import gitlab_repository --group my-group --token $T
harness import registry --config migration.yaml
```

**Direction is in the verb, permanently.** There is no `--direction` and no outward
`import x:y` variant — the verb already said inward. This is the asymmetry with `convert`,
and it follows from blast radius: the difference between creating resources in an account and
writing a local file must sit in the first token.

**The source goes in the noun, never in a `--from` flag.** The id rule already forces this:
the thing being named is a GitLab repo, so the noun is `gitlab_repository`. Beyond that, each
source system has a genuinely
different scope flag set (`--org` vs. `--group --include-subgroups` vs.
`--host --project --username`), and a `--from` flag would make flag validity conditional on a
flag value — the largest single source of malformed agent invocations. Input shapes may still
vary widely under one verb (`--config` for structurally complex sources), the same way
`create pipeline -f` and `create connector` differ.

**"Foreign" means foreign to the target, not non-Harness.** Another Harness account, org, or
project is a foreign source; account-to-account transfer is a legitimate `import`.

The boundary against `create`: foreign vocabulary in, Harness resources out. The boundary
against `execute` is **who runs the engine.** `import` means the CLI is intermediating — it
opens the connection to the source, reads it, and writes the result into Harness, so the bytes
pass through the client. If one API call tells the server to do the whole thing, the CLI is
just triggering work and it is `execute`:

```sh
harness execute registry:copy    # server-side copy; CLI only kicks it off
harness import jfrog_registry    # CLI connects to JFrog and writes into Harness
```

That test is what makes Harness-to-Harness unambiguous rather than a special case: the same
account-to-account move is `import` when the CLI brokers it and `execute <noun>:copy` when the
server does. Run outward, the same test gives [`export`](#export).

## `uninstall`

*Proposed.* Reverse of `install`: remove a locally installed binary or component and its
bookkeeping.

Not `install --uninstall`, because a negation is never a flag — it would redefine the
positional, fork the `--help` page, and hide the reverse operation from discovery. Not
`delete plugin`, because every other `delete` in this CLI destroys a server-side resource;
once install-by-URL and third-party publishing exist, `delete plugin` reads as *unpublish*.
`uninstall` can only mean local.

The "negative verb explosion" worry does not apply. Most verbs are not acquisitions and have
no inverse — you do not un-execute, un-import, or un-convert. `delete` already absorbs the
negation slot for everything server-side, leaving exactly one uncovered space: local install
state.

## `export`

*Reserved, no occupant, not in scope.* Read Harness state and write it into another system,
mediated by the CLI. **The outward mirror of `import`** — same shape, same engine, opposite
direction: moving a Harness Code repo to GitHub is `export`, not `convert` and not `execute`.

The test is the same one `import` uses, applied outward: **the CLI runs the engine.** `export`
is for something big enough to need multiple API calls, pagination, and a client-side driver.
That is what separates it from its neighbors:

- `get pipeline --format terraform` — one call, one representation, stdout. → **`get`**
- `convert pipeline:gitlab <id>` — translate to a local file, no destination system. → **`convert`**
- `execute registry:copy` — one call, the server moves it. → **`execute`**
- `export repository --to github --org myorg` — CLI drives a multi-call transfer. → **`export`**

So it is not distinguished by cardinality or output format; those belong to `get` and
`convert`. It is distinguished by being a *mediated transfer with a destination system*, which
none of the others are.

It is **not** the harness-migrate `terraform` command, which reads a source-system export and
writes a local `.tf` file without ever contacting Harness — that is `convert`.

Reserved rather than built because nothing occupies it yet. Real candidates: account-to-account
moves, DR/backup, compliance archival, GitOps-style export-to-git.

---

# The no-noun exception

Every verb above takes a noun. `wrap` does not, which is why it sits apart from them rather
than in the list.

## `wrap`

*Proposed.* Invoke an external binary with Harness configuration injected through the child's
environment, passing the tool's arguments through untouched.

```sh
harness wrap npm ci --save-dev
harness --profile prod wrap git push --force
```

**`wrap` is an env translator**, and that is the whole classification. It reads
Harness-currency config (profile state, env vars, detected local config) and emits
tool-currency config into the child environment — `NPM_CONFIG_REGISTRY`, `PIP_INDEX_URL`,
`GIT_CONFIG_*`. It does not build a modified argument list. A tool that cannot be configured
through its environment does not get a `wrap` entry.

`wrap` is the **one closed exception to `<verb> <noun>`**: it takes a *tool name*, there is no
id, Harness flag parsing stops at the tool name, and `wrap` declares no flags of its own.
That last point is a consequence, not a separate rule — it trades its flag space to the tool
because a wrap-owned `--registry` would collide with npm's real one. It is not a general
extension mechanism; no other verb may copy the shape.

Because a tool name is not a noun, the id rule does not apply and `list noun` / `get noun`
cannot surface these commands — discovery comes from top-level help, with one spec entry per
tool so the supported set stays honestly closed.

The boundary against `execute`: `execute` starts work *in Harness*. `wrap` runs a local
process that happens to be pointed at Harness. The boundary against `configure`: `wrap`
leaves no durable state behind.

---

## Rejected: `migrate`

Recorded because it was the leading candidate for most of the design discussion, and because
[verb-variants.md](verb-variants.md) still cites it as a valid example.

It fails on scale honesty — `migrate gitlab_pipeline --file .gitlab-ci.yml` calls a
single-file YAML transform a migration — and it conflates two operations with opposite blast
radius under one token, forcing `--file` and `--org`/`--token` to be mutually exclusive on the
same command. `convert` and `import` are both scale-neutral and each takes an unconditional
flag set. Notably, harness-migrate itself named its own commands `convert`, `export`, and
`import`.

**Migration is the activity these verbs serve, not a verb itself.** Customers say "we're
migrating off GitLab"; that project consists of importing repos, converting pipeline configs,
and remapping users.

---

## Removed: `describe`, `diagnose`, `search`, `status`, `ask`, `plugin`

These were declared in `pkg/registry/verb.go` as leftovers from the MCP migration, never had a
spec occupant, and have been **removed**. `describe` duplicated `get noun`; `search` was `list`
with a filter; `status` could not be separated from `get` by any test in this document;
`plugin` as a group verb contradicted the `install plugin` that actually ships. Of the set only
`diagnose` had a defensible distinct meaning — report already-computed posture without kicking
off fresh work — and it can be re-argued when something needs it.

The lesson worth keeping: **mint a verb when its first occupant arrives, not in anticipation.**
An unoccupied verb is worse than an absent one, because it appears in help output and
`VerbOrder` and invites agents to generate commands that do not exist.

---

## Boundary quick reference

| If you are choosing between | Ask |
| --- | --- |
| `get` vs `list` | Is the answer "the one you named" or "zero or more"? |
| `update` vs a new verb | Does it change a field's value? Then `update`. |
| `execute` vs `update` | Does the same call with the old value put it back, or does recovery add to the history? |
| `create` vs `import` | Is the input in Harness vocabulary or a foreign system's? |
| which noun at all | Does the id you'd pass actually identify that noun? |
| `convert` vs `import` | Does it end in a local file or in mutated Harness state? |
| `import` / `export` vs `execute :copy` | Does the CLI run the engine, or does one call tell the server to? |
| `get` vs `pull` vs `export` | stdout representation / one stored blob to disk / mediated transfer to another system |
| `delete` vs `uninstall` | Server-side resource or local install state? |
| `configure` vs `wrap` | Durable local config, or one process invocation? |
| `wrap` vs `execute` | Local process pointed at Harness, or work started in Harness? |
| any verb vs `--flag` | Would the flag reverse direction or redefine the positional? Then verb. |
