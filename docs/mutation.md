# Field mutations and update bodies

Use this guide when adding mutable fields or custom collection handlers, especially for FME. The CLI owns flag parsing, GET-then-update flow, field selection, and request assembly; a field-type handler owns the API-specific representation of one field. Commands use `harness update <noun> <id> [flags]` (verb first).

## Wire a field to the picked object

Declare a noun field with a display `expr`, a `mutable_path` relative to the picked object, and optionally a `field_type`:

```yaml
- id: owners
  expr: it.owners
  mutable_path: owners
  field_type: fme:owners
```

`expr` is **only for display**; it may join or format values and is not the mutation source. `mutable_path` is the read/write location within `update_body_pick`, including for nested paths such as `config.owners`. A field without `mutable_path` is not accessible through mutation flags. For noun variants that use another noun's fields, set `fields_noun` on the command (as `feature_flag:definition` does).

An endpoint with `update_strategy: get-then-patch` or `get-then-put` GETs the resource, evaluates `update_body_pick` against the **root GET response**, and copies the resulting object before changing it. With no `update_body_pick`, it falls back to `item_expr`; whole-picked-field normalization described below does not run in that fallback. The pick is a read source, not the sparse PATCH body. Include a collection's **current members** in the pick when a mutation must preserve them. For PATCH, a whole resource subtree can be picked even if its read-only GET fields are rejected on write: the flag-built PATCH sends only fields marked for output (which must themselves have an accepted write shape). For PUT, pick the complete writable resource subtree rather than a positive list of today's fields, or new API fields can be lost.

For example, once the FME API returns a complete read representation for the fields being edited, an endpoint can pick the full object while sending only changed fields:

```yaml
flags_builtin:
  set: true
  del: true
endpoint:
  method: PATCH
  update_strategy: get-then-patch
  update_body_pick: it
```

To use a custom type, register it in Go through the module's `ModuleInit`:

```go
reg.RegisterFieldType("owners", cmdctx.FieldTypeHandler{
    Normalize: normalizeOwners,
    Mutate:    mutateOwners,
    Encode:    encodeOwners,
})
```

`ModuleRegistrar.RegisterFieldType("owners", ...)` registers the type as `fme:owners` for the `fme` module. There is currently no `modules/fme` package: create its module initialization and wire it in `cmd/harness/main-harness.go` before referring to `fme:owners` in a spec. Writable fields must resolve to a handler with a `Mutate` function; `Normalize` and `Encode` are optional. Built-in types include scalar (the default), `tags` (key/value map), and `set` (string members).

There is no generic object-set field type or bracketed literal-member syntax. The built-in `tags` handler is a string-keyed map, **not** a handler for FME's named tag objects. If FME needs those semantics, register an API-specific type such as `fme:tags` and define its member identity, accepted operands, duplicate handling, clear behavior, and wire encoding there. Core dispatches the operations; it does not decide the shape of FME owners or tags.

## Handler lifecycle

The callback signatures and mutation kinds live in `pkg/cmdctx/cmdctx.go`. For a GET-then-PATCH or GET-then-PUT **with `update_body_pick`**:

1. Core copies the picked object. For each **present** noun field with `mutable_path` and a registered `Normalize`, it calls `Normalize(field, current)` once and stores the result in the working object. Missing fields are not invented. A present `null` is passed to the normalizer. Normalization errors abort before the write, even for an untouched field.
2. Core passes operations to `Mutate(field, current, op)` in captured CLI order. A handler sees the normalized current field value, and each subsequent operation on that field sees the previous written result. For a missing picked field that is targeted, normalization happens on first touch with a `nil` current value. `op.Kind` is `set`, `add`, or `del`; `op.Raw` is the original operand, `op.Key` is the part before `=`, `op.Value` is the part after it, and `op.HasValue` distinguishes a bare operand from one containing `=`. For `--del`, check `Raw`/`HasValue` as appropriate; don't silently treat `--del owner=` as bare `--del owner`.
3. `Mutate` returns `(next, write, err)`. `write=true` keeps `next` and marks that field for output even if `next == nil`; `write=false` leaves its current value alone and does not newly mark it for output. If an earlier operation already marked the field, a later no-op does not unmark it. Any error aborts before the write.
4. Core calls `Encode(field, finalValue)` **once per field marked for output**, after its last operation, and places the result at `mutable_path`. It does not encode untouched fields. Make `Normalize`/`Mutate`/`Encode` handle read shapes, intermediate values, and the API's write shape respectively; do not count on `Mutate` running just to convert an untouched field.

For `create_strategy: set-fields`, mutations apply to the `create_body_init` seed; there is no `update_body_pick` and no up-front normalization of every field. A targeted field can still run `Normalize` on first touch.

## What goes into the request

| Path | Body built from mutation flags |
|---|---|
| GET-then-PATCH | Starts as `{}`; adds only fields with `write=true`, at their `mutable_path`. Untouched fields, unknown GET fields, and no-op operations are omitted. |
| GET-then-PUT | Starts as a full copy of the picked object, including fields not targeted by flags; mutations replace their paths. Untouched fields with a normalizer are normalized, but **not encoded** in this implementation. Verify their write shape before relying on full PUT. |
| `set-fields` create | Starts from `create_body_init` and applies targeted mutations; it does not GET a resource. |

The endpoint's `update_body_wrap` (when nonempty) wraps the resulting update object. `body_params` are then added to the outer request at their specified dot-path; an expression yielding `nil` adds no key. Use them for write-only metadata such as FME definition `comment` and `title`, which GET does not return. They are separate from mutation tracking.

The difference between **omitted** and **explicitly written** matters for merge-PATCH: `--del owner` can have a custom handler return `nil, true` to emit `"owner": null`; an array handler can return an empty, non-nil slice with `write=true` to emit `"owners": []`. An untouched owner emits neither. For an array, preserve all surviving members in `next`, since a merge-PATCH array replaces the whole array. Choose the clear value according to the API's semantics, not a core convention.

`flags_builtin.set` registers `--set`, `--add`, and `--del` together (a del-only command is also possible). Explicit flags, including repeated operands, are applied in CLI order; positional sets (where supported) follow flagged operations. Core does not collapse earlier `--set` values when it has captured order. For a programmatically constructed context without captured order, the legacy fallback uses the last `--set` per key and applies sets before deletes. `--list-fields` shows fields exposed to mutation.

- Scalars use `--set name=value` (including `name=` for an empty string) or bare `--del name`; `--add` is an error. Only the built-in `set` type accepts bare `--set modules.CD` (also `--set modules.CD=`); a scalar or tag-map `--set` still requires `=`.
- Built-in `tags` uses `--set tags.key=value`, `--add tags.key=value` (adds an absent key, no-ops for the same value, errors on a conflicting value), and `--del tags.key`. Built-in `set` uses member operands such as `--add modules.CD` and `--del modules.CD`; an empty member (`modules.`) is invalid. Existing tag/set delete operands containing `=` continue to treat it as part of the literal member key, so use `op.Raw` for delete selectors where appropriate.
- A custom FME handler can define its own operand syntax (for example, `user:<email>`), validate it, and preserve unrelated members. Flag registration alone does not make every custom workflow or endpoint body function consume mutations: those implementations must read `ctx.MutationFlags` / `ctx.MutationOrderCaptured` or their legacy `SetArgs` / `DelArgs` inputs themselves. The automatic body builder described here is for `set-fields` create and GET-then-PUT/PATCH.

## File input, GET YAML, and current limits

- If an endpoint accepts `-f`, supplying a file takes precedence over GET-then-update. The supplied body is sent as a write document (with the endpoint's file-body wrapping behavior); it is **not** run through the field handlers or the flag-built sparse PATCH path. `body_params` from the automatic mutation path are not merged into it. A full write-ready PATCH file is allowed, but inspect its shape before sending it.
- `get --format yaml` still uses the **get command's separate `yaml_pick_expr` / `yaml_exclude`**, not `update_body_pick` or custom field handlers. FME's current YAML output is not automatically write-ready for tags/owners; do not promise a GET-YAML-to-`-f` round trip until its specs and conversion path are addressed.
- There is no `patch_always_fields` facility yet. PATCH does not copy unchanged picked IDs or other required fields automatically. If an API requires one, establish that requirement before changing the spec/core; an explicit `body_params` value may suffice when it is available independently of GET.
- Core does not look up owner emails or reconstruct missing API identifiers. FME currently returns a group name without the identifier needed to write that group back; that requires a server-side round-trip fix before mixed-owner edits can be reliable.

## Testing a custom type

Test with synthetic GET values and handlers at the mutation-builder layer: normalization of untouched present fields, first-touch behavior for absent fields, ordered repeated operations, preservation of other collection members, and `Encode` once for a changed field. Check that a no-op PATCH omits the field while a deliberate clear sends `null` or `[]`, that a nested `mutable_path` lands correctly, and that callback errors return no write body. Avoid a mock HTTP test that only reasserts a body already covered by a direct mutation test.

Run `go test ./pkg/registry ./pkg/cmdctx` for core changes. For a spec-backed command, inspect `harness update <noun> <id> --list-fields` and use the hidden `--preview-request` with example mutations: it assembles the request without sending the write, although GET-then-update still performs its preparatory GET. Do not use live writes as the first test of a handler.