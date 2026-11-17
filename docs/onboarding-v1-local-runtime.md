# Local onboarding/v1 runtime demo

This is a developer-only source bootstrap. It never publishes the runner and
does not change the default package-index bootstrap.

```bash
export HARNESS_CLI_HOME="$(mktemp -d)"
export HARNESS_CLI_ENABLED_MODULES=aievals

# Use the D4 binary from this worktree and the checked-out D2 runner source.
./bin/harness setup eval_onboarding \
  --runner-source <D2_WORKTREE>
```

`--runner-source` requires `HARNESS_CLI_HOME` so the bootstrap cannot write
`~/.harness`. It validates the source manifest, package version `0.7.9`, and
`onboarding/v1`, copies the source into
`$HARNESS_CLI_HOME/aievals-runner/source`, installs that snapshot into
`$HARNESS_CLI_HOME/aievals-runner`, and records the handshake. The copied
source is persistent; it is not a symlink or a temporary checkout.

Retrying the same command against the same isolated home verifies the
recorded source, version, and schema, reports that the runner is already
compatible, and does not reinstall it. A different source or a corrupt or
partial home is rejected with remediation instead of being overwritten.

For a staged tree, run the normal local workflow:

```bash
./bin/harness run eval_onboarding \
  --root <REPOSITORY>/.harness/evals \
  --agent <AGENT_IDENTIFIER>
```

The run remains report-free unless `--report-json` is supplied. To retain a
completed local result for the optional persistence flow, choose an absolute
report path whose parent already exists and which is outside
`<REPOSITORY>`, then run:

```bash
./bin/harness run eval_onboarding \
  --root <REPOSITORY>/.harness/evals \
  --agent <AGENT_IDENTIFIER> \
  --environment local \
  --report-json <ABSOLUTE_REPORT_PATH>
```

D4 forwards `<ABSOLUTE_REPORT_PATH>` unchanged to the verified D2
`ai-evals onboard-run --report-json` command and prints the path only after a
successful runner exit. This creates only a local report; it does not upload
or persist it. Upload the archive first, show its manifest mapping, and ask
the user separately before invoking
`./bin/harness upload eval_onboarding --persist-report <ABSOLUTE_REPORT_PATH>`.

The current D2 checkout has a path dependency on the checked-out Harness
Evals SDK. Its `runner.py` still requires SDK `0.28.2`, while the supplied D1
worktree is `0.29.3`; therefore bootstrap succeeds, but `run` stops before an
HTTP call with the exact SDK-version diagnostic until those branches are
aligned. Do not bypass that diagnostic by relabeling the installed package.
This source demo does not publish or substitute the dependency. All other
runner dependencies are resolved by the selected local Python installer.
