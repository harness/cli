#!/bin/sh
# Cross-language onboarding/v1 check.
#
# D4 generates a candidate document. D2's loader and translator parse it.
# GitHub CI skips when the D2 worktree is not available:
#
#   D2_ONBOARDING_ROOT=/path/to/onboarding-d2-local-runner ./scripts/check_d4_onboarding_v1.sh
#
# The Go test TestD2LoadsGeneratedOnboardingV1Candidate performs the same
# Poetry invocation when D2_ONBOARDING_ROOT is set.
set -eu
if [ -z "${D2_ONBOARDING_ROOT:-}" ]; then
  echo "skip: D2_ONBOARDING_ROOT is unset"
  exit 0
fi
ROOT=$(CDPATH= cd -- "$(dirname "$0")/.." && pwd)
cd "$ROOT"
export D2_ONBOARDING_ROOT
go test ./modules/aievals -run TestD2LoadsGeneratedOnboardingV1Candidate -count=1
