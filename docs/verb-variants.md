# Harness CLI — When to Add a Verb, a Noun, a Variant, or a Field

This document defines how to decide where a new operation or resource lives in the
`harness <verb> <noun>` grammar. It is the reference for the recurring tension: an
operation doesn't fit cleanly under an existing `<verb> <noun>`, and you have to choose
between minting a new verb, minting a new noun, introducing a `noun:variant`, or adding a
field. It supplements [verb-arch.md](verb-arch.md).

---

## The principle that decides everything

**The grammar shape must tell the agent the truth before dispatch.**

Every other rule here is a consequence of this one. The CLI's primary consumers are AI
agents and the scripts they write; the property that makes commands reliable to generate
is that the _shape_ of a command carries its _semantics_. An agent should be able to look
at a command — or at the discovery surface (`list noun`, `get noun`, `--help`) — and know
what it does, where it applies, and which flags are valid, without trial runs and without
reading prose.

This reframes the closed-verb-set goal. The objective is **not** "minimize the number of
verbs." It is "every verb is unambiguously placeable." A coherent set of legible verbs
beats a smaller set with one fuzzy member, because a fuzzy verb taxes every decision an
agent makes while a clear extra verb taxes none. Count was never the metric;
legibility-per-token is.

---

## The four mechanisms

There are four places an operation or resource can live, plus one non-grammar escape hatch.
They are ordered here by cost — prefer the earliest one that honestly fits.

| Mechanism                  | Shape                        | Means                                                                                        |
| -------------------------- | ---------------------------- | -------------------------------------------------------------------------------------------- |
| **Reuse verb + noun**      | `execute pipeline`           | This is just an instance of an existing operation on an existing resource.                   |
| **Colon variant**          | `execute pipeline:input_set` | Same resource identity; a named alternative invocation mode, access path, or representation. |
| **Noun** (incl. join noun) | `create role_assignment`     | A genuinely separate identity — its own address, lifecycle, and API.                         |
| **Verb**                   | `migrate pipeline`           | A distinct action-class that recurs across nouns and reduces to no existing verb.            |
| _(Handler-level input)_    | `get user <id-or-email>`     | _Not grammar._ One command accepting multiple id forms; resolved in the handler.             |

The preference order is: **reuse > colon variant > noun > verb.** A colon sits _below_
adding a noun in cost because it doesn't even claim resource-hood — it's the cheapest
honest way to say "same thing, different mode." A verb is the most expensive because the
set is closed and every addition is permanent and global.

---

## Verbs are action-classes, not narrow meanings

The most important correction to make up front: a verb names a **class** of action, not a
single literal operation. `execute` does not mean "run a pipeline." It means "initiate work
that produces a run or result, synchronously or asynchronously." A firewall scan, a chaos
experiment, a terraform apply, a `bash -n` syntax check, and a pipeline run are all members
of that class.

The rough classes:

- **`create` / `get` / `list` / `update` / `delete`** — the state of a resource.
- **`execute`** — initiate work that yields a run or result.
- **Module-approved workflow verbs** — `push` / `pull` / `migrate` / `audit` / `configure`
  / `validate` / `install` — operations with their own established mental
  model that recur across nouns.

The discipline that keeps `execute` from absorbing the universe is **pick the verb by the
action-class, then use the colon for subtypes within that class.** A state change is
`update`, not a new verb (toggling a feature flag is `update feature_flag`, never
`push_settings_flag`). A read is `get`/`list`. Work-that-runs is `execute`. Once
the class is right, a subtype within it is a colon, not a verb.

---

## The verb gate — three conditions, all required

Do not mint a top-level verb unless **all three** hold:

1. **Recurs across nouns.** The operation applies to many resource types, not one.
2. **Reduces to no existing verb-class.** It genuinely isn't a member of an existing class.
3. **Self-evidently placeable.** The verb predicts its own applicability — an agent reading
   it knows immediately where it applies and would never misplace it.

The third condition is the one that's easy to miss and does the most work. A verb that
makes an agent pause and disambiguate ("scan _what_?") costs more than a verb it can place
instantly, regardless of how many verbs already exist.

**Worked contrast:**

- `migrate` clears all three: it recurs across resource types, reduces to no existing
  verb-class, and is self-evidently placeable. → **Verb.**
- `scan` (one-off firewall scan on artifacts) clears (1) at best and fails (3) — scan an
  artifact? a repo? a pipeline? a project? The ambiguity is the cost. → **Not a verb.**
  It's an `execute` subtype: `execute artifact:firewall_scan` (kicks off fresh analysis).

The colon doesn't just save a verb slot — it converts an _unplaceable_ verb into a
_placeable_ variant, putting the operation where its applicability is unambiguous.

---

## The colon variant — charter and rules

**Separator: `:`** — and the alternatives are worse. `/` is already the id-path separator
(`pipeline/exec-id`); `@` should be reserved for version/ref pinning (`artifact@1.2.3`);
`.` reads as attribute access and collides with expr-lang paths. So `:` against a noun
in the command grammar means exactly one thing: same resource, named alternative mode.

**Unified meaning across all verb classes:** _same resource identity, a named alternative
{invocation mode | access path | representation}._ Operation variants and view variants are
the same feature; the **verb tells you which flavor**, so you never need a second separator:

- `create pipeline:remote` — alternative invocation/payload
- `execute pipeline:input_set` — alternative endpoint
- `get pipeline:summary` — alternative representation

**Each variant is its own fully-declarative spec entry** — own path, own body builder, own
flag list, own validation. This is the key cost insight: the colon does **not** introduce
switch/case dispatch or behind-the-scenes endpoint-picking. Those are what the _merge_
(unioning variants into one command) would require, and they are the thing to avoid. The
colon keeps each entry clean and separate exactly as it is today; the only framework work
is (a) allow `:` in the noun token at parse time, (b) group by base-noun in
`get noun`/completion. Dispatch is unchanged.

**One variant may be the bare default; some pairs have no default.** If a `(verb, noun)`
pair has an obvious default mode, the bare form works and the colon refines it
(`execute pipeline` = raw inputs, `execute pipeline:input_set` refines). If there's no
sensible default, register only the explicit colon form and no bare form — the discovery
surface then advertises only the real command, so an agent never sees a bare form that
dead-ends.

**Variants are rare by charter.** If a noun accretes many variants, that's a smell that a
separate resource is hiding inside it — go back to the identity test.

---

## The three-way fork

For "same operation/resource feels off," walk this in order:

1. **Pure field/format projection on a single call** — same endpoint, difference is only
   which fields or format render, all verbs identical → **flag** (`--format yaml`,
   `--summary`) or internal `fields_noun`. Not a colon.

2. **Different endpoint, representation, or verb surface on the same resource** →
   **colon variant.** The sharpest instance: when the views support _different valid verbs_
   (you can `get` and `update` the yaml view but only `get` the summary), a flag cannot
   express it — the verb is chosen before the flag is read, so `update pipeline --summary`
   could only fail at runtime. A colon expresses it cleanly: `get pipeline:summary` is
   registered, `update pipeline:summary` simply isn't. _This asymmetry is the example to
   lead with when documenting why views are colons and not flags._

3. **Genuinely separate identity** — its own address, lifecycle, and API → **noun.**

**Applied to pipeline views:** bare `pipeline` is the yaml view (default; `get` + `update`
both operate on it — don't register a redundant `pipeline:yaml` alias). `pipeline:summary`
is the named, get-only, non-default view (a different endpoint with data the yaml view
doesn't carry). The "list returns more than get" oddity is _not_ a user-chosen view — it's
the list endpoint's shape differing from get's, handled internally via `fields_noun`; it
never reaches the grammar.

---

## Relationships: join noun vs. field

Relationships (role↔user, permission↔role, user-as-admin-of-project) decide on the
**write boundary**, not on whether they're conceptually a relationship (they always are):

- **Mutated through its own API** (separate create/delete, without touching either
  endpoint object) → **join noun.** `role_assignment` already exists in the registry as
  exactly this. Use it as a real resource: `create role_assignment --user alice --role admin`,
  `list role_assignment --user alice`, `delete role_assignment <id>`. CRUD-on-the-edge falls
  out naturally (create = grant, delete = revoke, list = show grants, filtered by either
  side — one noun answers both directions). The feeling that "there's no API on `user` to
  add a role" is the signal pointing you _to_ the join noun and _away_ from `update user`.

- **Mutated as a field on the resource** (the resource's own PUT accepts it) → **field**,
  tags-style. If roles were writable through the user object, it collapses to
  `update user alice --set roles.admin=true` / `--del roles.admin` — the existing
  `get-then-put` + `--set`/`--del` + `field_type: set` machinery, no join noun at all.

**Read/write asymmetry (the real, awkward case):** role assignments come back _in_ the user
GET but cannot be written _through_ the user PUT — roles are a denormalized projection on
read, a separate resource on write. The CLI cannot erase this split, but it must not hide
it, because hiding it produces false runs (an agent sees `roles: [...]` in `get user` and
infers a `--del roles.admin` that doesn't exist). Resolve it by letting the grammar tell the
write-truth while staying informative on read: render roles as a **read-only field** on
`user` (read-only fields don't participate in update and aren't advertised as writable),
and route all writes through the `role_assignment` join noun. The grammar then refuses to
imply a write the API rejects.

---

## Discovery surface placement

Variants are **not rows**. A row in `list noun` is a resource-identity unit; a colon
variant is an operation mode, so it appears in the **verbs bracket** alongside the base
verb it refines:

```
artifact   A versioned package stored in a Harness artifact registry. [list, get, delete, push artifact:cargo, push artifact:npm, push artifact:docker, ...]
```

The full `verb noun:variant` form is used (not just the colon suffix), so each entry is
unambiguous in isolation — an agent reading it knows the exact command without reconstructing
context from the row. The get-only-ness of a summary view is visible by the _absence_ of
`update noun:summary` in the bracket — honesty a flag couldn't provide.

The disclosure stack, coarse → fine:

| Layer        | Surface                                  | Variants shown as                                           |
| ------------ | ---------------------------------------- | ----------------------------------------------------------- |
| Flat index   | `list noun`                              | full `verb noun:variant` form inside the verbs bracket      |
| Domain model | `get module <name>`                      | full `verb noun:variant` form inside the verbs bracket      |
| Per-resource | `get noun <noun>`                        | every command incl. variants, one line each                 |
| Per-command  | `harness <verb> <noun>:<variant> --help` | flags for that exact command                                |

Because discovery is generated from the registry, the index can only ever advertise real
registered commands — closing the dead-end worry at the discovery layer rather than via
runtime errors. And because each variant is its own entry, each `--help` is clean: exactly
the flags that apply, every required flag truly required, zero `(remote only)` conditional
noise. That conditional-flag noise is the single biggest source of malformed agent
invocations; splitting removes it by construction.

---

## Quick-reference decision table

| Situation                                       | Mechanism                          | Example                                           |
| ----------------------------------------------- | ---------------------------------- | ------------------------------------------------- |
| Instance of an existing operation               | reuse verb + noun                  | `execute pipeline`                                |
| Same resource, alternative invocation/payload   | colon variant                      | `create pipeline:remote`                          |
| Same resource, alternative endpoint             | colon variant                      | `execute pipeline:input_set`                      |
| Same resource, alternative representation       | colon variant                      | `get pipeline:summary`                            |
| Operation subtype that doesn't fit a default    | explicit-only colon                | `execute artifact:firewall_scan`                  |
| Same data, just fewer fields / different format | flag or `fields_noun`              | `get pipeline --format yaml`                      |
| Same resource, two id forms                     | handler-level resolution           | `get user <id-or-email>`                          |
| Relationship mutated via its own API            | join noun                          | `create role_assignment`                          |
| Relationship writable on the resource's PUT     | field (tags-style)                 | `update user --set roles.admin=true`              |
| Read shape differs from write boundary          | read-only field + writes elsewhere | roles on `get user`, writes via `role_assignment` |
| State change on a resource                      | `update` (never a new verb)        | `update feature_flag --enabled=true`              |
| Distinct action-class, recurring, placeable     | verb                               | `migrate pipeline`                                |

---

## Anti-patterns

- **A one-off verb for a single operation on a single noun.** Fails the recurrence and
  placeability gates. → colon variant.
- **A "noun" with no fields** (e.g. `pipeline_with_input_set`). A fieldless noun is not a
  resource; it's an operation mode masquerading as one. → colon variant.
- **A fake join noun invented under pressure** when the relationship has no independent
  lifecycle, _or_ forcing `update <resource>` when the write API lives on the edge. Decide
  on the write boundary.
- **A second separator** to distinguish operation variants from view variants. The verb
  already disambiguates. One separator, meaning inferred from the verb.
- **A read-only projection that masquerades as editable** — implies a write the API
  rejects, the worst kind of inconsistency.
- **Optimizing for verb count over verb legibility.** A tight set of placeable verbs beats
  a smaller set with one fuzzy member.
