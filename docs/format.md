# Output & Formatting

Everything on this page shapes what a command prints: encoding (`--format`), which fields
appear (`--columns`/`--fields`), and how many rows come back (`--offset`/`--limit`/`--all`/
`--count`). Pick the format based on what you're about to *do* with the output — the table
below — before touching anything else on this page.

**Don't narrow columns/fields speculatively.** Reaching for `--columns`/`--fields` before you
know you need to costs a `--list-columns`/`--list-fields` round trip (or a guess that errors)
for a savings that usually doesn't matter. Only bother once you've hit a real cost — e.g.
`--format json` on a `list` with hundreds of rows blowing up your context — or you already know
the exact field name without looking it up.

Everything here governs **stdout** only, and stdout for `get`/`list` is always clean —
just the formatted data, nothing else. Errors, spinners, and progress bars go to stderr instead,
so parsing stdout never means filtering out incidental noise. `--out <file>` redirects this same
clean stdout content to a file.

---

## Which format for which job

| Format | Use it when... |
|---|---|
| `table` | Default inspection. Human-scannable, includes the fields people look up most (ids, names, status) without asking. Start here when you don't yet know what you're looking for. |
| `csv` | Handing data to a *person*, or a spreadsheet tool (Excel, Sheets, Numbers). RFC 4180 quoting makes it correct for humans but annoying to parse by hand — don't reach for this to consume the data yourself. |
| `tsv` | Feeding a CLI pipeline: `awk`, `cut`, `head`/`tail`, `column -t`. No quoting to fight — a cell is either plain text or (if it contains a tab/newline) JSON-encoded, so `jq -r` or a plain split still works. This is the one to parse programmatically when column shape is all you need. |
| `json` | You need a field that isn't in the default table columns, or the data is nested/array-shaped and won't flatten into a row (e.g. a list of role assignments). This is the full, unshaped API response for `get`; for `list` it's still reshaped unless you add `--raw`. Once you're here, shape further with `jq`, not `--columns` — `--columns` has no effect on `json`/`jsonl` output, and `jq` is the more natural, more expressive tool for filtering/mapping JSON anyway. |
| `jsonl` | Same case as `json`, but the list is huge or you're streaming: one JSON object per line, so you can pipe into `jq` (or a while-read loop) per-row without materializing everything. |
| `yaml` | **Round-tripping.** Only on `get` commands that declare a YAML subtree (e.g. `get pipeline`, `get project`). Fetch with `--format yaml`, edit the file, feed it back with `create`/`update -f` (you may need to drop or adjust server-set fields like ids). None of the other formats round-trip — table/csv/tsv/markdown are lossy projections, and plain `json` often carries extra fields the write endpoint won't accept as-is. |
| `markdown` | You're producing content for a human-facing doc: a PR description, an issue comment, a wiki page. GFM table syntax, pastes directly. |
| `text` | `get`/`execute` commands with a bespoke human layout. Not selectable by name — it's just the default when `--format` is omitted and the command declares one. |

Two independent flag families exist because `list` commands and single-item commands
(`get`, `execute`) shape their output differently:

| | `list <noun>` | `get <noun>` / single-item |
|---|---|---|
| Choose which fields | `--columns` | `--fields` |
| Discover available fields | `--list-columns` | `--list-fields` |
| Default format | `table` (TTY) | `text` if declared, else `json` |
| Formats supported | `table`, `csv`, `tsv`, `json`, `jsonl`, `markdown` | `json`, `yaml` (if declared), `text` (command-specific) |

Run `harness <verb> <noun> --help` to see exactly which of these flags a given command declared —
not every command supports every flag below (e.g. `--all`/`--count` only appear on countable list
endpoints; `--yaml` only on commands that declare a yaml pick expression).

---

## `--format`

Controls the output encoding. See the use-case table above for *which* one to reach for.

```
harness list pipeline --format json
harness get project myproj --format yaml
```

| Value | Applies to | Notes |
|---|---|---|
| `table` | list | Human-readable, default when attached to a TTY and columns are known |
| `csv` | list | RFC 4180 quoting |
| `tsv` | list | Tab-separated; a cell containing a tab/newline is JSON-encoded so the column stays parseable with `jq -r` |
| `markdown` | list | GFM table |
| `json` | list, get | Always for `get` unless a `text`/`yaml` formatter is declared |
| `jsonl` | list | One JSON object per line — best for streaming/piping into `jq` per-row |
| `yaml` | get | Only on commands that declare a YAML subtree (e.g. `get pipeline`, `get project`) |
| `text` | get | Command-specific human layout; not selectable by name, it's just what you get when `--format` is omitted and the command has one |

`--json` and `--yaml` are shorthand for `--format json` / `--format yaml` — they're mutually
exclusive with `--format` and with each other and with `--fields`.

---

## `--columns` (list commands)

Chooses which fields appear, and in what order, in `table`/`csv`/`tsv`/`markdown` output. Has no
effect on `json`/`jsonl` (those always emit full objects).

```
harness list pipeline --columns name,identifier
harness list pipeline --columns "Name:it.name"        # ad-hoc column, id:expr
harness list pipeline --columns +description          # add to the default set
harness list pipeline --columns -description           # remove from the default set
```

Comma-separated tokens (or a JSON array string). Two modes, chosen by whether **any** token is
unsigiled:

- **Replace mode** — if any token has no `+`/`-` prefix, the result is *exactly* the listed columns.
  Each token is either a known field ID, or an ad-hoc `id:expr` pair (label auto-titled from `id`,
  value from evaluating `expr` against `it`).
- **Modify mode** — if *every* token is sigiled, `+id` appends a column to the command's default
  set and `-id` removes one from it, matched by header.

Mixing sigiled and unsigiled tokens is an error.

Discover valid IDs with `--list-columns` (prints ID/Label/Expr and exits, no API call).

### Ad-hoc column expressions

The `expr` half of an `id:expr` token is an [expr-lang](https://github.com/expr-lang/expr)
expression, evaluated once per row with `it` bound to that row's data. This works in *every*
column-based format — `table`, `csv`, `tsv`, `markdown` — since it's the same evaluation
regardless of encoding.

```
harness list pipeline --columns "id,name,Slow:it.executionTime > 300000"
harness list execution --columns "id,Elapsed:duration(it.startTs, it.endTs)"
harness list execution --columns "id,Short:truncate(it.name, 20)"
```

Basic expr-lang syntax: `it.field` (or `it['odd-key']` for keys that aren't valid identifiers),
comparisons (`==`, `!=`, `>`, `<`), boolean logic (`&&`, `||`, `!`), ternary (`cond ? a : b`),
nil-coalescing (`a ?? b`), string concat (`+`). Check real field names for a noun with
`--list-columns`/`--list-fields` first — the expr references the raw API field name, which
isn't always the same string as the column ID.

A handful of helper functions are available inside these expressions, mainly for shaping
values that don't already render well as a flat cell:

| Function | Purpose |
|---|---|
| `coalesce(a, b, ...)` | First non-nil/non-empty/non-zero argument |
| `isBlank(v)` | True if `v` is nil or `""` |
| `truncate(s, n)` | Collapse whitespace, cut to `n` runes, append `…` if shortened |
| `substr(s, start, n)` | Substring by rune index; negative `start` counts from the end |
| `lastPart(s)` | Text after the last `/` in `s` |
| `duration(startMs, endMs)` | Elapsed time between two epoch-ms fields, e.g. `"5m20s"` |
| `epochMs(v)` | Epoch-ms → `"2006-01-02 15:04:05"` UTC |
| `parseDateMs(v)` | Date string or relative span (`"30d"`, `"2w"`) → epoch-ms string |
| `formatTags(csv)` / `formatTagDisplay(map)` | Convert between tag representations |
| `jsonArray(v)` / `jsonArrayPretty(v)` | Render a nested array/object field as a JSON string, for when you need a nested value inside a flat format |
| `formatOrder(order, subOrder)` | `"N"` or `"N.M"` |
| `url(it)` / `url_link(it[, label])` | The item's UI URL (empty string if the noun has none) |

`--fields` (below) does **not** support this `id:expr` syntax — only known field IDs.

This machinery is for the row-based formats (`table`/`csv`/`tsv`/`markdown`). If you've already
reached for `--format json` (or `jsonl`), don't try to replicate `--columns` logic there — pipe
into `jq` instead. It's a more natural fit for shaping JSON than fighting this expression syntax.

### `HARNESS_CLI_COLUMNS`

A per-noun default for `--columns`, read from the environment, for humans who don't want to
retype a custom layout every time (`export HARNESS_CLI_COLUMNS="pipeline=name,identifier"`).
Also handy for demos — set a clean, narrow layout once in the shell profile so every `list`
command in the session prints a tidy table without a `--columns` flag cluttering each invocation.
An explicit `--columns` flag always overrides it. Not something to set or rely on when scripting
or driving the CLI — pass `--columns` directly on the command instead.

---

## `--fields` (single-item commands)

Extracts specific field values from a `get` response and prints them **tab-separated on one line**
— built for `$(...)` capture in shell scripts, not for humans.

```
name=$(harness get pipeline my-pipeline --fields name)
read -r name identifier <<< "$(harness get pipeline my-pipeline --fields name,identifier)"
```

Comma-separated field IDs only — no `id:expr` ad-hoc expressions (that's a `--columns`-only
feature). Unknown IDs print as an empty string rather than erroring. Mutually exclusive with
`--format`, `--json`, and `--yaml` (fields output has its own fixed shape).

Discover valid IDs with `--list-fields`.

---

## `--list-columns` / `--list-fields`

Print the command's available field table (`ID`, `Label`, `Expr`) and exit — no API call is made.
Use this before writing a `--columns`/`--fields` value instead of guessing IDs from `--format json`
output field names (the column ID and the raw JSON key are not always the same string).

```
harness list pipeline --list-columns
harness get pipeline --list-fields
```

---

## `--no-headers`

Suppresses the header row in `table`/`csv`/`tsv` output, and the paging footer in `table` output.
Useful when piping into tools that don't expect a header line:

```
harness list pipeline --format csv --no-headers --columns identifier | xargs -I{} ...
```

---

## `--out <file>`

Writes output to a file instead of stdout (nothing is printed to the terminal). Applies to every
format, including the `table` paging footer.

```
harness list pipeline --format json --out pipelines.json
```

---

## Paging: `--offset`, `--limit`, `--all`, `--count`

Control how many rows a `list` command returns. Only shown on commands whose endpoint supports
paging; `--all`/`--count` specifically require a *countable* paging strategy (hidden otherwise).

| Flag | Effect |
|---|---|
| `--offset N` | Skip the first N items (item-level, not page-level — the CLI slices transparently across API page boundaries) |
| `--limit N` | Return at most N items |
| `--all` | Fetch every page. Incompatible with `--offset`/`--limit` |
| `--count` | Print the total matching count and exit, instead of items. Incompatible with `--offset`/`--limit`/`--all`. Reflects other filters (e.g. `--search`) but not the offset/limit window |

```
harness list pipeline --limit 25
harness list pipeline --offset 25 --limit 25
harness list pipeline --all
harness list pipeline --count
```

**Agent heuristic for walking pages without `--count`:** normal list output is a flat array with no
paging envelope baked into the item shape (see the `table` footer for the human-readable total).
Got back fewer items than `--limit`? You're at the end. Got back exactly `--limit`? There may be
more — retry with `--offset <old offset + limit>`.

A `table`-format response with paging also prints a footer after the rows:

```
Showing 1-25 of 142
```

(`Showing 1-25` with no total, if the API didn't report one.) Omitted for every non-table format,
and suppressed by `--no-headers`.

---

## Interactive output (not a `--format`)

`--ui` launches a full interactive TUI browser for a list/get command (requires stdout+stdin to
both be a TTY; errors otherwise). It's a different UX from everything above, not a rendering
option — nothing selected by `--format` applies while `--ui` is active. Spinners/progress bars
during long-running operations similarly bypass `--format`: they go to stderr and are suppressed
entirely outside a TTY, so scripted/piped usage of stdout is never polluted by them.

---

## `--raw`

Debugging-only. Emits the response before the CLI strips the server envelope (request ids,
pagination metadata, etc. — varies by endpoint; many nouns have none). Only valid with
`--format json`/`--json`. Not a fix for a field missing from regular `json` output — that's a
spec gap, not an envelope issue.

---

## Related

- [docs/paging.md](paging.md) — paging flags in more depth
