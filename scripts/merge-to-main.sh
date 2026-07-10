#!/usr/bin/env bash
# merge-to-main.sh [branch] - gate and fast-forward a staged branch into
# protected local main.
#
# The primary checkout keeps tracked source read-only so agents do implementation
# work in managed clone-backed capsule workspaces and only write the primary
# checkout during intentional landings. This helper is that landing path: it
# lifts write permissions on the exact files and directories changed by the
# fast-forward, runs the merge, then restores the read-only guard.
#
# By default it promotes the managed staging capsule at .capsules/staging/local
# on branch staging/local. It rebases that capsule onto local main, runs
# `make test`, imports the staged branch into the primary checkout, and then
# fast-forwards main. Passing --force is required to skip the gate.
#
# Run from the primary checkout, while on main:
#   scripts/merge-to-main.sh
#   scripts/merge-to-main.sh --gate "make test-full"
#   scripts/merge-to-main.sh --force
#   scripts/merge-to-main.sh <branch> --source-dir <managed-capsule>
#
# Optional /goal (goal-seeker) ledger integration: when a change being landed is
# part of a tracked goal, pass --goal-dir and --change-id (both required
# together) to record the landing in the goal's append-only log immediately,
# instead of reconstructing ledger history from `git log` after the fact:
#
#   scripts/merge-to-main.sh <branch> --goal-dir <dir> --change-id <id> \
#     [--goal-work <dir>] [--goal-script <path>]
#
#   --goal-dir <dir>     the goal's docs/goals/<slug> directory (decomposition.yaml)
#   --change-id <id>     the decomposition change id this branch/merge completes
#   --goal-work <dir>    the goal's --work dir (log.jsonl/ledger.json). Defaults
#                        to .artifacts/goal/<basename(goal-dir)>, mirroring
#                        goal.py's own `_work_for` default derivation.
#   --goal-script <path> explicit path to goal.py. If omitted, this script looks
#                        for it at .agents/skills/goal/goal.py, then
#                        .claude/skills/goal/goal.py (its dev symlink) — the
#                        canonical committed location once the goal-seeker
#                        story (WM.0) lands it there. Until then, pass
#                        --goal-script explicitly (e.g. a branch worktree's copy).
#
# These flags are pure additions: omit them and this script behaves exactly as
# it always has. If both are given, after a successful merge this script
# appends one combined `kind: gate` log entry (state=integrated,
# gate.status=green, actor=integrator, branch=main, sha=<new HEAD sha>) via
# `goal.py append`. The ledger update is observability, not a correctness gate
# for the merge: if it fails for any reason (bad path, malformed json, missing
# goal.py, ...) this script prints a warning to stderr and still exits 0, since
# the merge itself already succeeded and is the important, hard-to-reverse part.
set -euo pipefail

CAPSULE_SENTINEL=".kitsoki-capsule"
CLONE_SENTINEL=".kitsoki-clone"
DEFAULT_BRANCH="staging/local"
DEFAULT_SOURCE_DIR=".capsules/staging/local"
DEFAULT_GATE="make test"

branch=""
source_dir=""
gate="$DEFAULT_GATE"
force=0
import_branch=""
goal_dir=""
change_id=""
goal_work=""
goal_script=""

usage() {
  cat >&2 <<EOF
usage:
  scripts/merge-to-main.sh [branch] [--source-dir DIR] [--gate CMD] [--force]
  scripts/merge-to-main.sh [branch] --goal-dir DIR --change-id ID [--goal-work DIR] [--goal-script PATH]

Defaults:
  branch      $DEFAULT_BRANCH
  source-dir  $DEFAULT_SOURCE_DIR for $DEFAULT_BRANCH
  gate        $DEFAULT_GATE

--force skips the gate. Use it only when an equivalent gate already ran.
EOF
}

abs_path() {
  python3 - "$1" <<'PY'
import os
import sys
print(os.path.abspath(sys.argv[1]))
PY
}

safe_ref_fragment() {
  printf '%s' "$1" | tr -c 'A-Za-z0-9._-' '-'
}

ensure_managed_source_dir() {
  local dir="$1"
  [ -d "$dir/.git" ] || { echo "error: source dir is not a git checkout: $dir" >&2; exit 1; }
  if [ ! -f "$dir/$CAPSULE_SENTINEL" ] && [ ! -f "$dir/$CLONE_SENTINEL" ]; then
    echo "error: source dir is not a managed capsule: $dir (missing $CAPSULE_SENTINEL or $CLONE_SENTINEL)" >&2
    exit 1
  fi
}

while [ $# -gt 0 ]; do
  case "$1" in
    --goal-dir)
      goal_dir="${2:?--goal-dir requires a value}"
      shift 2
      ;;
    --change-id)
      change_id="${2:?--change-id requires a value}"
      shift 2
      ;;
    --goal-work)
      goal_work="${2:?--goal-work requires a value}"
      shift 2
      ;;
    --goal-script)
      goal_script="${2:?--goal-script requires a value}"
      shift 2
      ;;
    --source-dir|--staging|--staging-capsule)
      source_dir="${2:?$1 requires a value}"
      shift 2
      ;;
    --gate)
      gate="${2:?--gate requires a value}"
      shift 2
      ;;
    --force)
      force=1
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    -*)
      echo "error: unknown flag: $1" >&2
      usage
      exit 1
      ;;
    *)
      if [ -n "$branch" ]; then
        echo "error: unexpected extra argument: $1" >&2
        exit 1
      fi
      branch="$1"
      shift
      ;;
  esac
done

if [ -z "$branch" ]; then
  branch="$DEFAULT_BRANCH"
fi
if [ -z "$source_dir" ] && [ "$branch" = "$DEFAULT_BRANCH" ]; then
  source_dir="$DEFAULT_SOURCE_DIR"
fi
if [ "$force" = "1" ] && [ "$gate" != "$DEFAULT_GATE" ]; then
  echo "error: --force skips the gate; do not also pass --gate" >&2
  exit 1
fi

if [ -n "$goal_dir" ] && [ -z "$change_id" ]; then
  echo "error: --goal-dir requires --change-id" >&2
  exit 1
fi
if [ -z "$goal_dir" ] && [ -n "$change_id" ]; then
  echo "error: --change-id requires --goal-dir" >&2
  exit 1
fi

if [ "$(git rev-parse --git-dir)" != "$(git rev-parse --git-common-dir)" ]; then
  echo "error: run this in the primary checkout, not a linked worktree or secondary checkout" >&2
  exit 1
fi
if [ "$(git symbolic-ref --quiet --short HEAD 2>/dev/null || true)" != "main" ]; then
  echo "error: the primary checkout is not on main" >&2
  exit 1
fi

repo_root="$(git rev-parse --show-toplevel)"
cd "$repo_root"

if [ -n "$source_dir" ]; then
  if [ "${source_dir#/}" = "$source_dir" ]; then
    source_dir="$repo_root/$source_dir"
  fi
  source_dir="$(abs_path "$source_dir")"
  ensure_managed_source_dir "$source_dir"
  source_branch="$(git -C "$source_dir" branch --show-current)"
  if [ "$source_branch" != "$branch" ]; then
    echo "error: source dir $source_dir is on $source_branch, expected $branch" >&2
    exit 1
  fi
  source_dirty="$(git -C "$source_dir" status --porcelain --untracked-files=all | grep -Ev '^[?][?] (\.kitsoki-capsule|\.kitsoki-clone|capsule-manifest.json|\.kitsoki-dev-workspace.json|\.kitsoki-owner)$' || true)"
  if [ -n "$source_dirty" ]; then
    echo "error: source dir has uncommitted changes: $source_dir" >&2
    printf '%s\n' "$source_dirty" >&2
    exit 1
  fi
  if git -C "$source_dir" remote get-url source >/dev/null 2>&1; then
    git -C "$source_dir" fetch source main
    git -C "$source_dir" rebase source/main
  else
    echo "warning: source dir has no 'source' remote; skipping rebase onto local main before gate" >&2
  fi
  if [ "$force" = "1" ]; then
    echo "warning: --force supplied; skipping gate: $DEFAULT_GATE" >&2
  else
    [ -n "$gate" ] || { echo "error: gate is empty; pass --force to skip the gate" >&2; exit 1; }
    echo "merge-to-main: running gate in $source_dir: $gate" >&2
    (cd "$source_dir" && sh -c "$gate")
  fi

  import_branch="capsule/promote-$(safe_ref_fragment "$branch")"
  if git rev-parse --verify --quiet "$import_branch" >/dev/null; then
    git branch -D "$import_branch" >/dev/null
  fi
  git fetch "$source_dir" "$branch:refs/heads/$import_branch"
  if [ "$branch" = "$DEFAULT_BRANCH" ]; then
    git fetch "$source_dir" "+$branch:refs/heads/$branch"
  fi
  branch="$import_branch"
elif [ "$force" != "1" ]; then
  echo "error: --source-dir is required to run the default gate for $branch; pass --force only if an equivalent gate already ran" >&2
  exit 1
else
  echo "warning: --force supplied; promoting $branch without a source-dir gate" >&2
fi

if ! git rev-parse --verify --quiet "$branch" >/dev/null; then
  echo "error: no such branch: $branch" >&2
  exit 1
fi
if ! git merge-base --is-ancestor HEAD "$branch"; then
  echo "error: $branch is not a fast-forward of main; rebase it onto main in its managed capsule first" >&2
  exit 1
fi

files="$(git diff --name-only HEAD "$branch")"
if [ -z "$files" ]; then
  echo "nothing to merge; main already contains $branch"
  if [ -n "$import_branch" ] && git rev-parse --verify --quiet "$import_branch" >/dev/null; then
    git branch -D "$import_branch" >/dev/null
  fi
  exit 0
fi

changed_files_file="$(mktemp "${TMPDIR:-/tmp}/kitsoki-merge-files.XXXXXX")"
printf '%s\n' "$files" >"$changed_files_file"
set +e
dirty_overlap="$(python3 - "$PWD" "$changed_files_file" <<'PY'
import subprocess
import sys

repo, changed_files_file = sys.argv[1:]
with open(changed_files_file, encoding="utf-8") as f:
    changed = [line.rstrip("\n") for line in f if line.rstrip("\n")]
if not changed:
    sys.exit(0)

status = subprocess.run(
    ["git", "-C", repo, "status", "--porcelain=v1", "-z", "--untracked-files=all"],
    stdout=subprocess.PIPE,
    check=False,
)
if status.returncode != 0:
    sys.exit(status.returncode)

dirty = set()
parts = status.stdout.split(b"\0")
i = 0
while i < len(parts):
    entry = parts[i]
    i += 1
    if not entry:
        continue
    code = entry[:2].decode("ascii", "replace")
    path = entry[3:].decode("utf-8", "surrogateescape")
    if path:
        dirty.add(path)
    if ("R" in code or "C" in code) and i < len(parts) and parts[i]:
        dirty.add(parts[i].decode("utf-8", "surrogateescape"))
        i += 1

def overlaps(a, b):
    return a == b or a.startswith(b + "/") or b.startswith(a + "/")

conflicts = sorted({d for d in dirty for c in changed if overlaps(d, c)})
for path in conflicts:
    print(path)
sys.exit(1 if conflicts else 0)
PY
)"
dirty_status=$?
set -e
rm -f "$changed_files_file"
if [ "$dirty_status" -ne 0 ]; then
  if [ -z "$dirty_overlap" ]; then
    echo "error: failed to inspect primary checkout dirty paths" >&2
    exit "$dirty_status"
  fi
  echo "error: primary checkout has uncommitted changes in files this branch would update; refusing to merge" >&2
  printf '%s\n' "$dirty_overlap" >&2
  exit 1
fi

dirs="$(printf '%s\n' "$files" | xargs -n1 dirname | sort -u)"
guard_paths="$(
  printf '%s\n' "$files" | while IFS= read -r f; do
    [ -e "$f" ] && printf '%s\n' "$f"
    d="$(dirname "$f")"
    [ "$d" = "." ] && printf '%s\n' "."
    while [ "$d" != "." ] && [ "$d" != "/" ]; do
      [ -e "$d" ] && printf '%s\n' "$d"
      parent="$(dirname "$d")"
      [ "$parent" = "." ] && printf '%s\n' "."
      [ "$parent" = "$d" ] && break
      d="$parent"
    done
  done | sort -u
)"

restore_guard_fallback() {
  printf '%s\n' "$files" | while IFS= read -r f; do
    [ -e "$f" ] && chmod a-w "$f" || true
  done
  printf '%s\n' "$dirs" | while IFS= read -r d; do
    [ -n "$d" ] && [ -e "$d" ] && chmod a-w "$d" 2>/dev/null || true
  done
  printf '%s\n' "$guard_paths" | while IFS= read -r p; do
    [ -n "$p" ] && [ -e "$p" ] && chmod a-w "$p" 2>/dev/null || true
  done
}

restore_guard() {
  restore_guard_fallback
  if [ -x scripts/protected-main-mode.sh ]; then
    if ! scripts/protected-main-mode.sh repair-writable-roots --quiet; then
      echo "warning: protected-main-mode.sh repair-writable-roots failed; scratch roots may need manual chmod repair" >&2
    fi
  fi
}

trap restore_guard EXIT

printf '%s\n' "$guard_paths" | while IFS= read -r p; do
  [ -n "$p" ] && chmod u+w "$p" 2>/dev/null || true
done
printf '%s\n' "$files" | while IFS= read -r f; do
  [ -e "$f" ] && chmod u+w "$f" || true
done

merge_out=""
set +e
merge_out="$(git merge --ff-only "$branch" 2>&1)"
merge_status=$?
set -e
if [ -n "$merge_out" ]; then
  printf '%s\n' "$merge_out"
fi
if [ "$merge_status" -ne 0 ]; then
  current_head="$(git rev-parse --verify HEAD 2>/dev/null || true)"
  if [ -n "$current_head" ]; then
    git reset --hard "$current_head" >/dev/null 2>&1 || true
    echo "error: merge failed; reset primary checkout index/worktree to HEAD $current_head" >&2
  else
    git reset --hard >/dev/null 2>&1 || true
    echo "error: merge failed; attempted to reset primary checkout index/worktree" >&2
  fi
  exit "$merge_status"
fi

restore_guard
trap - EXIT

if [ -n "$import_branch" ] && git rev-parse --verify --quiet "$import_branch" >/dev/null; then
  git branch -D "$import_branch" >/dev/null
fi

if [ -n "$goal_dir" ] && [ -n "$change_id" ]; then
  resolved_goal_script="$goal_script"
  if [ -z "$resolved_goal_script" ]; then
    for candidate in ".agents/skills/goal/goal.py" ".claude/skills/goal/goal.py"; do
      if [ -f "$candidate" ]; then
        resolved_goal_script="$candidate"
        break
      fi
    done
  fi

  if [ -z "$resolved_goal_script" ]; then
    echo "warning: --goal-dir/--change-id given but no goal.py found (looked for .agents/skills/goal/goal.py, .claude/skills/goal/goal.py; pass --goal-script explicitly) — skipping ledger update" >&2
  elif [ ! -f "$resolved_goal_script" ]; then
    echo "warning: --goal-script $resolved_goal_script does not exist — skipping ledger update" >&2
  else
    resolved_goal_work="$goal_work"
    if [ -z "$resolved_goal_work" ]; then
      resolved_goal_work=".artifacts/goal/$(basename "$goal_dir")"
    fi
    new_sha="$(git rev-parse --short HEAD)"
    if entry_json="$(python3 - "$change_id" "$new_sha" <<'PY'
import json
import sys

change_id, sha = sys.argv[1], sys.argv[2]
print(json.dumps({
    "kind": "gate",
    "change_id": change_id,
    "actor": "integrator",
    "state": "integrated",
    "gate": {"status": "green"},
    "branch": "main",
    "sha": sha,
}))
PY
    )"; then
      if ! append_out="$(python3 "$resolved_goal_script" append --work "$resolved_goal_work" --entry "$entry_json" 2>&1)"; then
        echo "warning: goal ledger update failed (goal.py append exited non-zero): $append_out" >&2
      else
        printf '%s\n' "$append_out"
      fi
    else
      echo "warning: failed to build goal ledger entry JSON for change $change_id — skipping ledger update" >&2
    fi
  fi
fi

echo "main -> $(git rev-parse --short HEAD); read-only guard restored for changed paths"
