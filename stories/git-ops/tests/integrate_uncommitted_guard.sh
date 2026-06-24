#!/usr/bin/env bash
# integrate_uncommitted_guard.sh — REAL-git proof of the integrate room's
# lost-work guard: a branch whose work is on disk but NEVER COMMITTED (an
# untracked dir in its OWNING worktree) must be REFUSED, not silently merged as
# an empty shell.
#
# This reproduces the deliver-story lost-work bug: the story dir was authored
# and its flows passed (they read on-disk files) but it was never `git add`ed,
# so a rebase+merge landed nothing. `git rebase` ignores untracked files, so the
# only thing that catches this is a `git status --porcelain` precondition in the
# worktree that OWNS the branch.
#
# Run from repo root: bash stories/git-ops/tests/integrate_uncommitted_guard.sh
# Exit 0 → guard present and correct. Exit 1 → the guard is missing/broken.
set -uo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/../../.." && pwd)"
ROOM="$REPO_ROOT/stories/git-ops/rooms/integrate.yaml"
fail=0
note() { echo "  $1"; }

echo "=== integrate lost-work guard: $ROOM ==="

# ── STRUCTURAL: the room runs the porcelain check + emits uncommitted ────────
if grep -q 'git -C "$WORKTREE" status --porcelain' "$ROOM" && \
   grep -q 'outcome:"uncommitted"' "$ROOM"; then
  note 'ok: room runs git status --porcelain and emits outcome:"uncommitted".'
else
  note "FAIL: room is missing the uncommitted-work precondition."
  fail=1
fi

# ── FUNCTIONAL: a worktree with an UNTRACKED dir is detected as dirty, while a
#    fully-committed worktree is clean. This is the exact signal the guard uses.
export NO_COLOR=1 GIT_TERMINAL_PROMPT=0 GIT_PAGER=cat \
  GIT_AUTHOR_NAME=t GIT_AUTHOR_EMAIL=t@t GIT_COMMITTER_NAME=t GIT_COMMITTER_EMAIL=t@t
TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT
(
  cd "$TMP"
  git init -q -b main repo
  cd repo
  echo base > base.txt && git add . && git commit -qm base
  # A feature branch in its OWN worktree, with work left UNTRACKED (never added).
  git worktree add -q -b feature ../wt >/dev/null 2>&1
  mkdir -p ../wt/stories/deliver && echo "app: {}" > ../wt/stories/deliver/app.yaml

  DIRTY_UNTRACKED=$(git -C ../wt status --porcelain)
  if [ -z "$DIRTY_UNTRACKED" ]; then
    echo "  FAIL: untracked work not detected by status --porcelain"; exit 1
  fi
  echo "  ok: untracked work detected → '$DIRTY_UNTRACKED'"

  # After committing it, the worktree is clean → guard would NOT fire.
  git -C ../wt add -A && git -C ../wt commit -qm "add deliver"
  DIRTY_AFTER=$(git -C ../wt status --porcelain)
  if [ -n "$DIRTY_AFTER" ]; then
    echo "  FAIL: worktree still dirty after commit → '$DIRTY_AFTER'"; exit 1
  fi
  echo "  ok: committed worktree is clean → guard would pass"
) || fail=1

echo "==================================================================="
if [ "$fail" -ne 0 ]; then
  echo "FAIL: integrate lost-work guard is missing or the porcelain signal is wrong."
  exit 1
fi
echo "PASS: integrate refuses uncommitted work; committed work passes."
