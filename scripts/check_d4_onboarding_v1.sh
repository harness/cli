#!/bin/sh
# Cross-repo onboarding/v1 check. This is not a normal D4 CI job.
#
# Normal CI compares modules/aievals/testdata/onboarding_v1_candidate.json
# with the Go serializer and does not need a D2 checkout.
#
# The dedicated workflow checks out the pinned D2 revision and supplies
# D2_ONBOARDING_ROOT. For a local run, provide a D2 checkout explicitly:
#   D2_ONBOARDING_ROOT=/path/to/onboarding-d2-local-runner ./scripts/check_d4_onboarding_v1.sh
#
# Keep the D2 fixture mirror and the D2_REF in
# .github/workflows/onboarding-v1-cross-repo.yml in lockstep.
set -eu
if [ -z "${D2_ONBOARDING_ROOT:-}" ]; then
  echo "D2_ONBOARDING_ROOT is absent. Refusing to skip the onboarding/v1 compatibility check." >&2
  exit 1
fi
ROOT=$(CDPATH= cd -- "$(dirname "$0")/.." && pwd)
FIXTURE="$ROOT/modules/aievals/testdata/onboarding_v1_candidate.json"
if [ ! -f "$FIXTURE" ]; then
  echo "missing $FIXTURE" >&2
  exit 1
fi
cd "$D2_ONBOARDING_ROOT"
poetry run python "$ROOT/scripts/check_d4_onboarding_v1.py" "$FIXTURE"
