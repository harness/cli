#!/usr/bin/env bash
# Copyright © 2026 Harness Inc.
# SPDX-License-Identifier: Apache-2.0

set -euo pipefail

readonly DIST_DIR="${1:-dist}"
readonly GRAMMAR_TAGS='-tags=grammar_subset,grammar_subset_java,grammar_subset_python,grammar_subset_javascript,grammar_subset_typescript,grammar_subset_tsx'

count=0
while IFS= read -r -d '' artifact; do
  metadata="$(go version -m "$artifact")"
  if ! grep -Fqx $'\tbuild\t'"$GRAMMAR_TAGS" <<<"$metadata"; then
    printf 'release artifact lacks the supported grammar subset: %s\n%s\n' "$artifact" "$metadata" >&2
    exit 1
  fi
  ((count += 1))
done < <(find "$DIST_DIR" -type f \( -name harness -o -name harness.exe \) -print0)

if [[ "$count" -ne 6 ]]; then
  printf 'expected six cross-built harness release artifacts, found %d in %s\n' "$count" "$DIST_DIR" >&2
  exit 1
fi
