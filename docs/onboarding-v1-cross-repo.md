# Onboarding v1 cross-repo compatibility

Normal D4 Go tests are the first compatibility gate. They compare the
canonical serializer output byte-for-byte with
`modules/aievals/testdata/onboarding_v1_candidate.json`; they do not require a
D2 checkout or a network connection.

The dedicated
`.github/workflows/onboarding-v1-cross-repo.yml` workflow performs the second
gate. It checks out D2 from `harness/ai-evals` at reviewed revision
`8617179d2a072fd019ad9729dfd2c8ab02096f77` on
`onboarding-d2-local-runner`, installs the D2 runner manifest, and runs
`scripts/check_d4_onboarding_v1.sh` with `D2_ONBOARDING_ROOT` set to that
checkout. The checker loads the generated candidate and verifies scalar, list,
object, and null `precomputed_output` values in the managed single-eval YAML,
including its JSON materialization round trip. The cross-repo loader test is
not part of ordinary D4 unit-test CI.

## Updating the contract

The mirrored D2 fixture, its expected digest, and the workflow's `D2_REF`
are one compatibility unit. Update the fixture and reference together; never
advance only the checkout pin or only the fixture.

1. Update D4's canonical document and regenerate the committed golden fixture.
2. Run the normal D4 Go tests so serializer/fixture parity remains mandatory.
3. Update D2's mirrored fixture and its expected digest in the D2 contract
   test.
4. Land the D2 change on `onboarding-d2-local-runner`.
5. Change `D2_REF` in the dedicated workflow to the full reviewed D2 commit
   SHA (`8617179d2a072fd019ad9729dfd2c8ab02096f77` here), and keep the
   `D2_BRANCH` comment/context and checksum verification accurate.
6. Run the dedicated workflow before treating the fixture change as complete.

The workflow requires `AI_EVALS_READ_TOKEN` for the pinned D2 checkout and
`HARNESS_PYPI_USERNAME` / `HARNESS_PYPI_PASSWORD` for the supported D2 runner
environment. These credentials are used only by CI; no secret values are
placed in the fixture or passed to the loader.
