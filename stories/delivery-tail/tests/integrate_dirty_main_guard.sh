#!/usr/bin/env bash
# integrate_dirty_main_guard.sh — REAL-git proof of the integrate room's
# clean-main guard (#clean-main): integrating into a DIRTY integration tree
# (uncommitted TRACKED changes in MAIN_WT) must be REFUSED, not merged on top
# of work the merge / build-fail `reset --hard` would clobber.
#
# This protects the self-hosting direct-ship loop, which sets MAIN_WT="." (the
# live repo): a routine land on a dirty checkout would silently destroy the
# operator's WIP — the "reset --hard nukes main" hazard. The guard runs
# `git status --porcelain --untracked-files=no` in MAIN_WT BEFORE any
# checkout/merge/reset and emits outcome:"dirty_main" on any dirty tracked file.
# It is tracked-only: reset --hard never deletes untracked files, and an
# untracked artifact in main shouldn't veto a land.
#
# Run from repo root: bash stories/delivery-tail/tests/integrate_dirty_main_guard.sh
# Exit 0 → guard present and correct. Exit 1 → the guard is missing/broken.
set -uo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/../../.." && pwd)"
ROOM="$REPO_ROOT/stories/delivery-tail/rooms/integrate.yaml"
fail=0
note() { echo "  $1"; }

echo "=== integrate clean-main guard: $ROOM ==="

# ── STRUCTURAL: the room runs the MAIN_WT porcelain check + emits dirty_main ──
if grep -q 'git -C "$MAIN_WT" status --porcelain --untracked-files=no' "$ROOM" && \
   grep -q 'outcome:"dirty_main"' "$ROOM"; then
  note 'ok: room runs git status --porcelain --untracked-files=no on MAIN_WT and emits outcome:"dirty_main".'
else
  note "FAIL: room is missing the clean-main (dirty integration tree) precondition."
  fail=1
fi

# ── FUNCTIONAL: an uncommitted TRACKED change is detected as dirty by the exact
#    signal the guard uses, while (a) an untracked-only file is NOT (tracked-only
#    by design), and (b) a fully-committed tree is clean.
export NO_COLOR=1 GIT_TERMINAL_PROMPT=0 GIT_PAGER=cat \
  GIT_AUTHOR_NAME=t GIT_AUTHOR_EMAIL=t@t GIT_COMMITTER_NAME=t GIT_COMMITTER_EMAIL=t@t
TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT
(
  cd "$TMP"
  git init -q -b main repo
  cd repo
  echo base > tracked.txt && git add . && git commit -qm base

  # (a) An UNTRACKED-only file must NOT trip the tracked-only guard signal.
  echo scratch > untracked.txt
  DIRTY_UNTRACKED_ONLY=$(git status --porcelain --untracked-files=no)
  if [ -n "$DIRTY_UNTRACKED_ONLY" ]; then
    echo "  FAIL: untracked-only file wrongly flagged by --untracked-files=no → '$DIRTY_UNTRACKED_ONLY'"; exit 1
  fi
  echo "  ok: untracked-only file is ignored (guard would pass — reset --hard can't harm it)"

  # (b) A modified TRACKED file IS detected → the guard fires and blocks the land.
  echo changed >> tracked.txt
  DIRTY_TRACKED=$(git status --porcelain --untracked-files=no)
  if [ -z "$DIRTY_TRACKED" ]; then
    echo "  FAIL: uncommitted tracked change not detected by --untracked-files=no"; exit 1
  fi
  echo "  ok: uncommitted tracked change detected → '$DIRTY_TRACKED'"

  # (c) After committing, the tree is clean → guard would NOT fire.
  git add -A && git commit -qm "land tracked change"
  rm -f untracked.txt
  DIRTY_AFTER=$(git status --porcelain --untracked-files=no)
  if [ -n "$DIRTY_AFTER" ]; then
    echo "  FAIL: tree still dirty after commit → '$DIRTY_AFTER'"; exit 1
  fi
  echo "  ok: committed tree is clean → guard would pass"
) || fail=1

echo "==================================================================="
if [ "$fail" -ne 0 ]; then
  echo "FAIL: integrate clean-main guard is missing or the porcelain signal is wrong."
  exit 1
fi
echo "PASS: integrate refuses a dirty integration tree (tracked); untracked + clean pass."
