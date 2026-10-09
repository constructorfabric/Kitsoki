#!/usr/bin/env bash
# land-branch.sh <branch> [--gate "<shell command>"] - land <branch> onto
# protected local main, automatically handling the non-fast-forward case.
#
# scripts/merge-to-main.sh only accepts fast-forward merges. When main has
# advanced past a branch's base, the prescribed fix is to rebase the branch
# onto main in its own worktree and re-run the helper. This wrapper automates
# that recipe as one command:
#
#   1. If <branch> is already a fast-forward of main, just call
#      scripts/merge-to-main.sh <branch> directly.
#   2. Otherwise, create a scratch worktree at .worktrees/<branch>-land on a
#      fresh branch <branch>-land cut from the current main tip, and
#      cherry-pick <branch>'s own commits (main..<branch>, oldest first) onto
#      it. This avoids rebasing/checking out the original branch in place.
#   3. If --gate "<cmd>" is given, run it (via sh -c) inside the scratch
#      worktree; a non-zero exit aborts the landing.
#   4. On success, land <branch>-land with scripts/merge-to-main.sh, which is
#      now guaranteed to be a clean fast-forward.
#   5. Always remove the scratch worktree and branch, whether landing
#      succeeded, a cherry-pick conflicted, or the gate failed.
#
# A cherry-pick conflict or a failing --gate aborts cleanly (no leftover
# worktree/branch, non-zero exit) rather than attempting automatic conflict
# resolution or landing an unverified branch.
#
# Run from the primary checkout, while on main:
#   scripts/land-branch.sh <branch> [--gate "<shell command>"]
set -euo pipefail

usage() {
  echo "usage: scripts/land-branch.sh <branch> [--gate \"<shell command>\"]" >&2
}

branch=""
gate=""

while [ "$#" -gt 0 ]; do
  case "$1" in
    --gate)
      gate="${2:?--gate requires a value}"
      shift 2
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      if [ -n "$branch" ]; then
        echo "error: unexpected argument: $1" >&2
        usage
        exit 1
      fi
      branch="$1"
      shift
      ;;
  esac
done

if [ -z "$branch" ]; then
  usage
  exit 1
fi

if [ "$(git rev-parse --git-dir)" != "$(git rev-parse --git-common-dir)" ]; then
  echo "error: run this in the primary checkout, not a linked worktree" >&2
  exit 1
fi
if [ "$(git symbolic-ref --quiet --short HEAD 2>/dev/null || true)" != "main" ]; then
  echo "error: the primary checkout is not on main" >&2
  exit 1
fi
if ! git rev-parse --verify --quiet "$branch" >/dev/null; then
  echo "error: no such branch: $branch" >&2
  exit 1
fi

repo_root="$(git rev-parse --show-toplevel)"
cd "$repo_root"

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# Fast path: branch is already a fast-forward of main.
if git merge-base --is-ancestor HEAD "$branch"; then
  "$script_dir/merge-to-main.sh" "$branch" --force
  echo "path: direct fast-forward"
  echo "main -> $(git rev-parse --short HEAD)"
  exit 0
fi

# Slow path: replay the branch's own commits onto a fresh worktree cut from
# the current main tip, then hand that off to merge-to-main.sh.
safe_branch="$(basename "$branch")"
land_branch="${branch}-land"
worktree=".worktrees/${safe_branch}-land"

if git rev-parse --verify --quiet "$land_branch" >/dev/null; then
  echo "error: branch already exists: $land_branch" >&2
  exit 1
fi
if [ -e "$worktree" ]; then
  echo "error: worktree path already exists: $worktree" >&2
  exit 1
fi

commits="$(git log main.."$branch" --format=%H --reverse)"
if [ -z "$commits" ]; then
  echo "nothing to land; main already contains $branch"
  exit 0
fi

redundant_commits="$(git cherry main "$branch" | awk '/^- / { print $2 }')"
if [ -n "$redundant_commits" ]; then
  echo "error: $branch contains commit patches already represented on current main; inspect before replaying stale work" >&2
  git log --no-walk --oneline $redundant_commits >&2
  echo "hint: main may already contain another fix for the same ticket; rebase/trim the branch and rerun validation before landing" >&2
  exit 1
fi

cleanup() {
  if [ -e "$worktree" ]; then
    git worktree remove "$worktree" --force >/dev/null 2>&1 || true
  fi
  if git rev-parse --verify --quiet "$land_branch" >/dev/null; then
    git branch -D "$land_branch" >/dev/null 2>&1 || true
  fi
}

abort() {
  echo "error: $*" >&2
  cleanup
  exit 1
}

git worktree add -b "$land_branch" "$worktree" main >/dev/null

set +e
git -C "$worktree" cherry-pick $commits
cherry_status=$?
set -e
if [ "$cherry_status" -ne 0 ]; then
  git -C "$worktree" cherry-pick --abort >/dev/null 2>&1 || true
  abort "cherry-pick of $branch onto $land_branch conflicted; resolve manually (worktree removed)"
fi

if [ -n "$gate" ]; then
  set +e
  (cd "$worktree" && sh -c "$gate")
  gate_status=$?
  set -e
  if [ "$gate_status" -ne 0 ]; then
    abort "gate command failed for $land_branch: $gate"
  fi
fi

cd "$repo_root"
"$script_dir/merge-to-main.sh" "$land_branch" --force

cleanup

echo "path: cherry-pick-and-land ($branch -> $land_branch)"
echo "main -> $(git rev-parse --short HEAD)"
