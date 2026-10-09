#!/usr/bin/env bash
# bad_git_repo_harness.sh — smoke test for the reusable bad-git fixture factory.
#
# Run from repo root:
#   bash stories/git-ops/tests/bad_git_repo_harness.sh
#
# This does not touch the real checkout. Every scenario is created under one
# mktemp root using nonsense files, then asserted with real git commands.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/../../.." && pwd)"
HARNESS="$REPO_ROOT/stories/git-ops/tests/lib/bad_git_repo.sh"

source "$HARNESS"

TMP="$(mktemp -d "${TMPDIR:-/tmp}/kitsoki-bad-git-smoke.XXXXXX")"
trap 'rm -rf "$TMP"' EXIT

echo "=== bad git repo harness smoke ==="
fail=0
while IFS= read -r scenario; do
  [ -n "$scenario" ] || continue
  root="$TMP/$scenario"
  printf -- '--- %s ---\n' "$scenario"
  if repo="$(bad_git_create "$scenario" "$root")" && bad_git_assert "$scenario" "$repo"; then
    printf '  ok: %s -> %s\n' "$scenario" "$repo"
  else
    printf '  FAIL: %s\n' "$scenario"
    fail=1
  fi
done < <(bad_git_cases)

echo "=================================="
if [ "$fail" -ne 0 ]; then
  echo "FAIL: one or more bad-git scenarios did not materialize correctly."
  exit 1
fi
echo "PASS: all bad-git scenarios materialized and asserted."
