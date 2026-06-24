#!/usr/bin/env bash
# mutation_failure_surfacing.sh — proves the cleanup failure-surfacing is
# LOAD-BEARING, not decorative (closes delivery-loop epic open question #2).
#
# The smoking gun (git-ops-smoothing.md §Why): a failing `git worktree remove`
# masked by `|| true` plus an unconditional `set: last_op_outcome: "cleaned"`
# reported a confident false success for a no-op. The smoothed cleanup room
# drops the swallow and gates "cleaned" on the real exit, and
# flows/cleanup_noop_is_not_cleaned.yaml pins that a failed remove is NEVER
# reported cleaned.
#
# This mutation test re-introduces the bug in a throwaway copy of the story and
# asserts that cleanup_noop_is_not_cleaned then FAILS. If the regression flow
# still passed against the mutated (swallowing) room, the flow would be
# decorative — this script makes the load-bearing claim falsifiable.
#
# Run from the repo root:  bash stories/git-ops/tests/mutation_failure_surfacing.sh
set -euo pipefail

REPO_ROOT=$(cd "$(dirname "$0")/../../.." && pwd)
cd "$REPO_ROOT"

TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT

# Copy the whole story into the scratch dir so the relative app path resolves.
cp -R stories/git-ops "$TMP/git-ops"
MUT_CLEANUP="$TMP/git-ops/rooms/cleanup.yaml"

# MUTATION: re-introduce the pre-smoothing bug — an UNCONDITIONAL
# `set: last_op_outcome: "cleaned"` appended to the remove_all effects, so the
# outcome is reported "cleaned" regardless of the real exit the bind saw. This
# is exactly the false-success the smoothing removed (the smoothed room derives
# last_op_outcome from stdout_json.outcome and never hard-sets "cleaned"). The
# mutation is on the room's bind/route logic, which the stubbed flow exercises
# directly (the bash script body is replaced by the host.run stub).
python3 - "$MUT_CLEANUP" <<'PY'
import sys
p = sys.argv[1]
s = open(p).read()
anchor = '''                last_op_outcome: "stdout_json.outcome"
                last_op_output:  "stdout_json.last_op_output"
                last_op_ok:      ok
              on_error: cleanup'''
inject = anchor + '''
            - set:
                last_op_outcome: "cleaned"'''
assert anchor in s, "mutation anchor not found in cleanup.yaml"
# Mutate only the FIRST occurrence (remove_all).
s = s.replace(anchor, inject, 1)
open(p, "w").write(s)
PY

echo "[mutation] re-added unconditional 'set: last_op_outcome: cleaned' to remove_all"

# The mutated room must make the regression flow FAIL (a failed remove now
# falsely reports cleaned). We expect a NON-ZERO exit from the flow runner.
set +e
go run ./cmd/kitsoki test flows "$TMP/git-ops/app.yaml" >"$TMP/out.txt" 2>&1
RC=$?
set -e

# The canonical (un-mutated) suite passes; the mutated one must FAIL, and
# specifically on the regression flow with the false-success assertion.
if [ "$RC" -eq 0 ]; then
  echo "MUTATION TEST FAILED: the mutant still passed — the surfacing is decorative."
  exit 1
fi

if ! grep -q "cleanup_noop_is_not_cleaned" "$TMP/out.txt"; then
  echo "MUTATION TEST INCONCLUSIVE: cleanup_noop_is_not_cleaned did not run."
  cat "$TMP/out.txt"
  exit 1
fi

# Pin the specific false-success: the mutant reports outcome "cleaned" on a
# remove the bind saw as failed. That exact assertion must appear.
if ! grep -A4 "cleanup_noop_is_not_cleaned" "$TMP/out.txt" | grep -q "FAIL"; then
  echo "MUTATION TEST FAILED: cleanup_noop_is_not_cleaned did NOT fail under the mutant."
  grep -A4 "cleanup_noop_is_not_cleaned" "$TMP/out.txt"
  exit 1
fi

echo "[mutation] OK — the false-success mutant FAILS cleanup_noop_is_not_cleaned:"
grep -A4 "cleanup_noop_is_not_cleaned" "$TMP/out.txt" | grep -E "FAIL|✗|cleaned" | head -4
echo "PASS: failure-surfacing is load-bearing (epic OQ #2 closed — story-only)."
