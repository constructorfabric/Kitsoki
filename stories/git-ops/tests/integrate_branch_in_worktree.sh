#!/usr/bin/env bash
# integrate_branch_in_worktree.sh — REAL-git proof of the worktree-lock fix (#5).
#
# THE BUG: the maker's feature branch is checked out in a LINKED worktree (the
# common case — the loop seeds a distinct maker worktree per branch). A naive
# integrate that rebases the branch IN THE MAIN WORKTREE fails:
#     fatal: 'feature' is already used by worktree at '.../wt'
# and (worse) a blind `git checkout feature` in MAIN_WT would also be refused.
#
# THE FIX: DISCOVER the worktree that owns the branch (worktree list --porcelain)
# and rebase THERE; do the --no-ff merge in MAIN_WT (merge never checks out the
# source branch, so no lock conflict). This script runs that EXACT mechanism and
# asserts:
#   1. integrate succeeds (no "already used by worktree" error),
#   2. the feature commit AND a concurrent moved-main commit are BOTH on main —
#      i.e. no lost work.
#
# Run from repo root: bash stories/git-ops/tests/integrate_branch_in_worktree.sh
set -uo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/../../.." && pwd)"
ROOM="$REPO_ROOT/stories/git-ops/rooms/integrate.yaml"
fail=0
note() { echo "  $1"; }

echo "=== integrate worktree-lock guard (#5): $ROOM ==="

# ── STRUCTURAL: the room discovers the owning worktree + rebases there ───────
if grep -q 'worktree list --porcelain' "$ROOM" && \
   grep -q 'git -C "$WORKTREE" rebase --onto' "$ROOM"; then
  note 'ok: room discovers the owning worktree and rebases in it.'
else
  note "FAIL: room does not discover/rebase in the branch-owning worktree."
  fail=1
fi
# The merge must run in MAIN_WT (merge does not check out the source).
if grep -q 'git -C "$MAIN_WT" merge --no-ff "$BRANCH"' "$ROOM"; then
  note 'ok: merge --no-ff runs in MAIN_WT (no source checkout).'
else
  note "FAIL: merge does not run in MAIN_WT."
  fail=1
fi

# ── FUNCTIONAL: run the actual mechanism against a real repo ─────────────────
export NO_COLOR=1 GIT_TERMINAL_PROMPT=0 GIT_PAGER=cat \
  GIT_AUTHOR_NAME=t GIT_AUTHOR_EMAIL=t@t GIT_COMMITTER_NAME=t GIT_COMMITTER_EMAIL=t@t
TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT
(
  set -e
  cd "$TMP"
  git init -q -b main repo
  cd repo
  echo base > base.txt && git add . && git commit -qm base

  # Feature branch forks from base AND is CHECKED OUT IN A LINKED WORKTREE.
  git worktree add -q -b feature ../wt >/dev/null 2>&1
  echo feat > ../wt/feature.txt
  git -C ../wt add . && git -C ../wt commit -qm feat
  # Commit it so the lost-work guard passes (clean worktree).

  # MEANWHILE main moves: a concurrent session lands concurrent.txt on main.
  echo concurrent > concurrent.txt && git add . && git commit -qm concurrent
  MOVED_MAIN_SHA=$(git rev-parse HEAD)

  # ── The integrate mechanism (mirrors integrate.yaml core logic) ──
  MAIN_WT="$PWD"
  INTEGRATION=main
  BRANCH=feature
  BASE_SHA=$(git -C "$MAIN_WT" rev-parse "$INTEGRATION")

  # DISCOVER the worktree that owns BRANCH.
  WORKTREE=$(git -C "$MAIN_WT" worktree list --porcelain | awk -v b="refs/heads/$BRANCH" '$1=="worktree"{wt=$2} $1=="branch" && $2==b {print wt; exit}')
  [ -z "$WORKTREE" ] && WORKTREE="$MAIN_WT"
  if [ "$WORKTREE" = "$MAIN_WT" ]; then
    echo "  FAIL: did not discover the owning linked worktree (got MAIN_WT)"; exit 1
  fi
  echo "  ok: discovered owning worktree → $WORKTREE"

  # Rebase IN THE OWNING WORKTREE (this is what avoids the lock error).
  MB=$(git -C "$WORKTREE" merge-base "$BRANCH" "$INTEGRATION")
  REBASE_OUT=$(git -C "$WORKTREE" rebase --onto "$BASE_SHA" "$MB" "$BRANCH" 2>&1) || {
    echo "  FAIL: rebase in owning worktree failed: $REBASE_OUT"; exit 1
  }
  if echo "$REBASE_OUT" | grep -qi "already used by worktree"; then
    echo "  FAIL: worktree-lock error surfaced: $REBASE_OUT"; exit 1
  fi
  echo "  ok: rebased in owning worktree without a lock error"

  # Merge in MAIN_WT (no source checkout → no lock).
  git -C "$MAIN_WT" checkout -q "$INTEGRATION"
  MERGE_OUT=$(git -C "$MAIN_WT" merge --no-ff "$BRANCH" -m "integrate: $BRANCH" 2>&1) || {
    echo "  FAIL: merge --no-ff in MAIN_WT failed: $MERGE_OUT"; exit 1
  }

  # ASSERT no lost work.
  [ -f feature.txt ]    || { echo "  LOST WORK: feature.txt missing on main"; exit 1; }
  [ -f concurrent.txt ] || { echo "  LOST WORK: concurrent.txt missing on main"; exit 1; }
  if ! git merge-base --is-ancestor "$MOVED_MAIN_SHA" HEAD; then
    echo "  LOST WORK: concurrent main commit not an ancestor of integrated HEAD"; exit 1
  fi
  echo "  ok: feature + concurrent commits BOTH on main — no lost work"
) || fail=1

echo "==================================================================="
if [ "$fail" -ne 0 ]; then
  echo "FAIL: integrate worktree-lock guard regressed (#5)."
  exit 1
fi
echo "PASS: integrate rebases in the branch-owning worktree — no lock error, no lost work."
