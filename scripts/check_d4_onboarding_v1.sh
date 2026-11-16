#!/bin/sh
# Cross-repo onboarding/v1 check. This is not a normal D4 CI job.
#
# Normal CI compares modules/aievals/testdata/onboarding_v1_candidate.json
# with the Go serializer and does not need a D2 checkout.
#
# This command fails when D2_ONBOARDING_ROOT is absent:
#   D2_ONBOARDING_ROOT=/path/to/onboarding-d2-local-runner ./scripts/check_d4_onboarding_v1.sh
#
# D2 still needs its own test that loads this exact fixture. That test is not
# in this repository.
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
