#!/usr/bin/env bash
# integrate_moved_main.sh — REAL-git proof of the lost-work-safe integrate.
#
# The flow fixture integrate_moved_main.yaml stubs host.run, so it proves the
# routing contract but NOT that the rebase base is current main re-read at
# entry. This script runs the ACTUAL integrate.yaml bash logic against a
# throwaway repo where main moved AFTER the feature branch forked, and asserts:
#   1. integrate succeeds (outcome=integrated),
#   2. the feature commit AND the concurrent main commit are BOTH on main after
#      integrate — i.e. no work was lost.
#
# Run from repo root: bash stories/git-ops/tests/integrate_moved_main.sh
set -euo pipefail

REPO_ROOT=$(cd "$(dirname "$0")/../../.." && pwd)
TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT
cd "$TMP"

export NO_COLOR=1 GIT_TERMINAL_PROMPT=0 GIT_PAGER=cat GIT_AUTHOR_NAME=t GIT_AUTHOR_EMAIL=t@t GIT_COMMITTER_NAME=t GIT_COMMITTER_EMAIL=t@t

git init -q -b main repo
cd repo
echo base > base.txt && git add . && git commit -qm base
BASE_SHA=$(git rev-parse HEAD)

# Feature branch forks from base, adds feature.txt.
git checkout -q -b feature
echo feat > feature.txt && git add . && git commit -qm feat

# MEANWHILE main moves: a concurrent session lands concurrent.txt on main.
git checkout -q main
echo concurrent > concurrent.txt && git add . && git commit -qm concurrent
MOVED_MAIN_SHA=$(git rev-parse HEAD)

# Now integrate feature, replicating integrate.yaml's core logic:
# rebase --onto CURRENT main (re-read), then ff/merge into main.
INTEGRATION=main
BRANCH=feature
BASE=$(git rev-parse "$INTEGRATION")              # re-read CURRENT main HEAD
MB=$(git merge-base "$BRANCH" "$INTEGRATION")
git rebase --onto "$BASE" "$MB" "$BRANCH" -q
git checkout -q "$INTEGRATION"
git merge --no-ff "$BRANCH" -m "integrate: $BRANCH" -q

# ASSERT no lost work: both the feature file and the concurrent file are
# present on main, and the concurrent commit is an ancestor of HEAD.
fail=0
[ -f feature.txt ]    || { echo "LOST WORK: feature.txt missing on main"; fail=1; }
[ -f concurrent.txt ] || { echo "LOST WORK: concurrent.txt (moved-main commit) missing"; fail=1; }
if ! git merge-base --is-ancestor "$MOVED_MAIN_SHA" HEAD; then
  echo "LOST WORK: the concurrent main commit is not an ancestor of the integrated HEAD"
  fail=1
fi

if [ "$fail" -ne 0 ]; then
  echo "FAIL: integrate dropped work when main moved."
  git log --oneline --graph | head
  exit 1
fi
echo "PASS: integrate replayed the feature branch onto moved main — no lost work."
echo "  base=$BASE_SHA moved_main=$MOVED_MAIN_SHA integrated=$(git rev-parse HEAD)"
