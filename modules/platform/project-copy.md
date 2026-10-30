# Copy a project (CD core)

`harness migrate project:project` copies project-scoped CD configuration from one project to another in the same org. The CLI reads the source and writes the destination. Delegates, executions, code repositories, GitOps, and feature flags are not copied.

```sh
harness migrate project:project \
  --from source_project \
  --to destination_project \
  --org example_org \
  --resources cd-core

harness migrate project:project \
  --from source_project \
  --to destination_project \
  --org example_org \
  --resources cd-core \
  --apply
```

The first command is a plan. Nothing is written. `--apply` creates the destination project when it does not exist, then copies resources. `--from` and `--to` are required and must differ. The profile's default project is not used. Pass `--org` for the shared org.

## What is copied

Order: project, secrets, connectors, services, environments, infrastructures, service overrides, templates, pipelines, input sets, triggers, policies, policy sets.

- Inline `SecretText` is created with the placeholder value `123456`. The source value is not read. Update each listed secret before running the destination project.
- Reference secrets copy the secret manager and reference path. A destination secret with a different path is a conflict.
- Other secret types are reported as blocked and skipped.
- Templates are copied in dependency order using project-scoped `templateRef` values.
- Triggers are created disabled.
- Git-backed entities are skipped and the command exits nonzero so the skip is not treated as a complete copy.
- Org and account references, including `account.` and `org.` identifiers, are reported and left in place.
- Delegate selectors are checked against connected delegates (`CONNECTED`). Delegates themselves are not installed.
- Identical destination resources are skipped. A same identifier with different content aborts the copy before any write.
- A create failure stops the copy. Re-run `--apply` to continue; matching resources already written are skipped.

## Test against a source project

Use a project in an org your token can read and write. Pick a destination identifier that does not already exist.

```sh
task build
./bin/harness migrate project:project \
  --from YOUR_SOURCE_PROJECT \
  --to YOUR_DESTINATION_PROJECT \
  --org YOUR_ORG \
  --resources cd-core
```

Read the plan. Then apply:

```sh
./bin/harness migrate project:project \
  --from YOUR_SOURCE_PROJECT \
  --to YOUR_DESTINATION_PROJECT \
  --org YOUR_ORG \
  --resources cd-core \
  --apply
```

After apply, replace each inline secret:

```sh
./bin/harness update secret SECRET_ID --org YOUR_ORG --project YOUR_DESTINATION_PROJECT --set value='the-real-value'
```

Check the destination in the UI, or:

```sh
./bin/harness list pipeline --org YOUR_ORG --project YOUR_DESTINATION_PROJECT
./bin/harness list connector --org YOUR_ORG --project YOUR_DESTINATION_PROJECT
./bin/harness list secret --org YOUR_ORG --project YOUR_DESTINATION_PROJECT
```

Run the same `--apply` command again. Resources that match should be reported as `skip`, and inline secrets you already updated should not be reset to `123456`.

`--out report.json` writes the report to a new file and refuses to overwrite an existing one. `--format json` prints the same report to stdout.
