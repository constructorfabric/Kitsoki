#!/usr/bin/env bash
# no_shared_stash_stack.sh — guard against reintroducing shared stash-stack
# operations in git-ops rooms.
#
# `git stash create` / `git stash apply <commit>` are allowed for checkpoint
# refs because they do not push/pop the repository-global stash stack. `stash
# push` and `stash pop` are not allowed in story rooms: linked worktrees share
# the stash namespace, so auto-stashing can interleave unrelated operator work.
set -euo pipefail

ROOT=$(cd "$(dirname "$0")/../../.." && pwd)
cd "$ROOT"

matches=$(rg -n "git stash (push|pop)|stash (push|pop)" stories/git-ops/rooms || true)
if [ -n "$matches" ]; then
  echo "git-ops must not use the shared stash stack from story rooms:" >&2
  printf '%s\n' "$matches" >&2
  exit 1
fi

echo "PASS: git-ops rooms do not push/pop the shared stash stack."
