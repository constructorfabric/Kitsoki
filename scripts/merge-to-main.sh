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
staging_snapshot=""
source_snapshot=""
source_result=""
source_recovery_ref=""

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

source_dir_dirty() {
  local dir="$1" status line
  if ! status="$(git -C "$dir" status --porcelain --untracked-files=all)"; then
    echo "error: could not inspect source dir status: $dir" >&2
    return 1
  fi
  while IFS= read -r line; do
    case "$line" in
      ""|"?? $CAPSULE_SENTINEL"|"?? $CLONE_SENTINEL"|"?? capsule-manifest.json"|"?? .kitsoki-dev-workspace.json"|"?? .kitsoki-owner") ;;
      *) printf '%s\n' "$line" ;;
    esac
  done <<<"$status"
}

expected_tree_after_delta() {
  local dir="$1" old_base="$2" snapshot="$3" new_base="$4" tree
  tree="$(git -C "$dir" merge-tree --write-tree --no-messages \
    --merge-base "$old_base" "$new_base" "$snapshot")" || return 1
  printf '%s\n' "$tree"
}

refuse_untracked_tree_collisions() {
  local dir="$1" from_ref="$2" to_ref="$3" context="$4" output
  if ! output="$(python3 - "$dir" "$from_ref" "$to_ref" <<'PY'
import subprocess
import sys

repo, from_ref, to_ref = sys.argv[1:]
changed_run = subprocess.run(
    ["git", "-C", repo, "diff", "--name-only", "-z", "--no-renames", from_ref, to_ref],
    stdout=subprocess.PIPE,
    stderr=subprocess.PIPE,
    check=False,
)
if changed_run.returncode:
    sys.stderr.buffer.write(changed_run.stderr)
    sys.exit(changed_run.returncode)
changed = {p.decode("utf-8", "surrogateescape") for p in changed_run.stdout.split(b"\0") if p}
status_run = subprocess.run(
    [
        "git", "-C", repo, "status", "--porcelain=v1", "-z",
        "--untracked-files=all", "--ignored=traditional",
    ],
    stdout=subprocess.PIPE,
    stderr=subprocess.PIPE,
    check=False,
)
if status_run.returncode:
    sys.stderr.buffer.write(status_run.stderr)
    sys.exit(status_run.returncode)
local = set()
for entry in status_run.stdout.split(b"\0"):
    if not entry or entry[:2] not in (b"??", b"!!"):
        continue
    path = entry[3:].decode("utf-8", "surrogateescape").rstrip("/")
    if path:
        local.add(path)

def overlaps(a, b):
    return a == b or a.startswith(b + "/") or b.startswith(a + "/")

conflicts = sorted({p for p in local for c in changed if overlaps(p, c)})
for path in conflicts:
    print(path)
sys.exit(1 if conflicts else 0)
PY
)"; then
    echo "error: $context would overwrite untracked or ignored source paths:" >&2
    [ -z "$output" ] || printf '%s\n' "$output" >&2
    return 1
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
  # A staging capsule is a snapshot of the primary staging ref, not an
  # independent promotion source. Capture both ends before any potentially
  # long-running rebase or gate so work merged into staging while the gate is
  # running cannot be overwritten or accidentally promoted.
  if [ "$branch" = "$DEFAULT_BRANCH" ]; then
    staging_snapshot="$(git rev-parse --verify "$DEFAULT_BRANCH")" || {
      echo "error: primary checkout has no $DEFAULT_BRANCH ref" >&2
      exit 1
    }
    source_snapshot="$(git -C "$source_dir" rev-parse --verify HEAD)"
    if [ "$source_snapshot" != "$staging_snapshot" ]; then
      echo "error: staging capsule is stale: $source_dir is at $source_snapshot, primary $DEFAULT_BRANCH is at $staging_snapshot; refresh staging before promotion" >&2
      exit 1
    fi
  fi
  if ! source_dirty="$(source_dir_dirty "$source_dir")"; then
    exit 1
  fi
  if [ -n "$source_dirty" ]; then
    echo "error: source dir has uncommitted changes: $source_dir" >&2
    printf '%s\n' "$source_dirty" >&2
    exit 1
  fi
  source_original="$(git -C "$source_dir" rev-parse --verify HEAD)"
  source_original_recovery_ref="refs/kitsoki/promotion-source-recovery/$source_original"
  if git rev-parse --verify --quiet "$source_original_recovery_ref" >/dev/null; then
    [ "$(git rev-parse "$source_original_recovery_ref")" = "$source_original" ] || {
      echo "error: content-addressed promotion recovery ref points at the wrong object" >&2
      exit 1
    }
  else
    git fetch --no-tags "$source_dir" "$source_original:$source_original_recovery_ref"
  fi
  if git -C "$source_dir" remote get-url source >/dev/null 2>&1; then
    git -C "$source_dir" fetch source main
    source_main="$(git -C "$source_dir" rev-parse --verify source/main)"
    source_delta_base="$(git -C "$source_dir" merge-base "$source_original" "$source_main")" || {
      echo "error: could not determine source/main merge base" >&2
      exit 1
    }
    expected_source_tree="$(expected_tree_after_delta \
      "$source_dir" "$source_delta_base" "$source_original" "$source_main")" || {
      echo "error: could not construct expected rebased source tree" >&2
      exit 1
    }
    refuse_untracked_tree_collisions \
      "$source_dir" "$source_original" "$expected_source_tree" "source rebase" || exit 1
    git -C "$source_dir" rebase source/main
    source_rebased="$(git -C "$source_dir" rev-parse --verify HEAD)"
    if [ "$(git -C "$source_dir" rev-parse "$source_rebased^{tree}")" != "$expected_source_tree" ]; then
      git -C "$source_dir" update-ref "refs/kitsoki/promotion-rebase-failed/$source_rebased" "$source_rebased" || true
      if failed_source_dirty="$(source_dir_dirty "$source_dir")" && [ -z "$failed_source_dirty" ]; then
        git -C "$source_dir" update-ref "refs/heads/$source_branch" "$source_original" "$source_rebased" || true
        git -c submodule.recurse=false -C "$source_dir" reset --hard "$source_original" >/dev/null 2>&1 || true
      fi
      echo "error: rebased source tree differs from the expected preserved tree; original retained at $source_original_recovery_ref" >&2
      exit 1
    fi
  else
    echo "warning: source dir has no 'source' remote; skipping rebase onto local main before gate" >&2
  fi
  source_pre_gate="$(git -C "$source_dir" rev-parse --verify HEAD)"
  source_recovery_ref="refs/kitsoki/promotion-source-recovery/$source_pre_gate"
  if git rev-parse --verify --quiet "$source_recovery_ref" >/dev/null; then
    [ "$(git rev-parse "$source_recovery_ref")" = "$source_pre_gate" ] || {
      echo "error: content-addressed promotion recovery ref points at the wrong object" >&2
      exit 1
    }
  else
    git fetch --no-tags "$source_dir" "$source_pre_gate:$source_recovery_ref"
  fi
  if [ "$force" = "1" ]; then
    echo "warning: --force supplied; skipping gate: $DEFAULT_GATE" >&2
  else
    [ -n "$gate" ] || { echo "error: gate is empty; pass --force to skip the gate" >&2; exit 1; }
    echo "merge-to-main: running gate in $source_dir: $gate" >&2
    (cd "$source_dir" && sh -c "$gate")
  fi

  if ! source_dirty="$(source_dir_dirty "$source_dir")"; then
    exit 1
  fi
  if [ -n "$source_dirty" ]; then
    echo "error: source dir has uncommitted changes after rebase/gate: $source_dir" >&2
    printf '%s\n' "$source_dirty" >&2
    exit 1
  fi

  source_result="$(git -C "$source_dir" rev-parse --verify HEAD)"
  if [ "$source_result" != "$source_pre_gate" ]; then
    echo "error: source-dir gate moved HEAD ($source_pre_gate -> $source_result); validation gates must not mutate committed state" >&2
    echo "error: pre-gate source remains at $source_recovery_ref" >&2
    exit 1
  fi
  if [ "$(git -C "$source_dir" rev-parse --verify "refs/heads/$source_branch")" != "$source_result" ]; then
    echo "error: source branch advanced during validation; refusing to promote an unproven result" >&2
    exit 1
  fi
  if [ "$branch" = "$DEFAULT_BRANCH" ] && ! git -C "$source_dir" merge-base --is-ancestor "$source_snapshot" "$source_result"; then
    echo "error: staging capsule changed incompatibly during rebase or gate: result $source_result does not contain source snapshot $source_snapshot" >&2
    exit 1
  fi

  import_branch="capsule/promote-$(safe_ref_fragment "$branch")"
  if git rev-parse --verify --quiet "$import_branch" >/dev/null; then
    git branch -D "$import_branch" >/dev/null
  fi
  # Fetch the exact post-gate object, rather than the moving branch name.
  git fetch "$source_dir" "$source_result:refs/heads/$import_branch"
  if [ "$(git -C "$source_dir" rev-parse --verify "refs/heads/$source_branch")" != "$source_result" ]; then
    echo "error: source branch advanced while importing the proven result; main was not changed" >&2
    git branch -D "$import_branch" >/dev/null 2>&1 || true
    exit 1
  fi
  if [ "$branch" = "$DEFAULT_BRANCH" ]; then
    # This is the compare-and-swap. Do not use a force fetch here: it would
    # overwrite staging work that arrived while the gate was running.
    if ! git update-ref "refs/heads/$DEFAULT_BRANCH" "$source_result" "$staging_snapshot"; then
      current_staging="$(git rev-parse --verify "$DEFAULT_BRANCH" 2>/dev/null || true)"
      echo "error: $DEFAULT_BRANCH advanced during promotion gate (expected $staging_snapshot, found ${current_staging:-missing}); main was not changed" >&2
      git branch -D "$import_branch" >/dev/null 2>&1 || true
      exit 1
    fi
    if [ "$(git rev-parse --verify "$DEFAULT_BRANCH")" != "$source_result" ]; then
      echo "error: $DEFAULT_BRANCH changed before main advancement; main was not changed" >&2
      git branch -D "$import_branch" >/dev/null 2>&1 || true
      exit 1
    fi
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

# Disable rename/copy detection so both preimage and destination paths are in
# the protection set. A dirty rename source must block before Git can touch it.
changed_files_file="$(mktemp "${TMPDIR:-/tmp}/kitsoki-merge-files.XXXXXX")"
git diff --name-only -z --no-renames HEAD "$branch" >"$changed_files_file"
if [ ! -s "$changed_files_file" ]; then
  rm -f "$changed_files_file"
  echo "nothing to merge; main already contains $branch"
  if [ -n "$import_branch" ] && git rev-parse --verify --quiet "$import_branch" >/dev/null; then
    git branch -D "$import_branch" >/dev/null
  fi
  if [ -n "$source_dir" ]; then
    git update-ref -d "$source_original_recovery_ref" >/dev/null 2>&1 || true
    git update-ref -d "$source_recovery_ref" >/dev/null 2>&1 || true
  fi
  exit 0
fi

set +e
dirty_overlap="$(python3 - "$PWD" "$changed_files_file" <<'PY'
import subprocess
import sys

repo, changed_files_file = sys.argv[1:]
with open(changed_files_file, "rb") as f:
    changed = [p.decode("utf-8", "surrogateescape") for p in f.read().split(b"\0") if p]
if not changed:
    sys.exit(0)

status = subprocess.run(
    [
        "git", "-C", repo, "status", "--porcelain=v1", "-z",
        "--untracked-files=all", "--ignored=traditional",
    ],
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
    path = entry[3:].decode("utf-8", "surrogateescape").rstrip("/")
    if path:
        dirty.add(path)
    if ("R" in code or "C" in code) and i < len(parts) and parts[i]:
        dirty.add(parts[i].decode("utf-8", "surrogateescape").rstrip("/"))
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
if [ "$dirty_status" -ne 0 ]; then
  rm -f "$changed_files_file"
  if [ -z "$dirty_overlap" ]; then
    echo "error: failed to inspect primary checkout dirty paths" >&2
    exit "$dirty_status"
  fi
  echo "error: primary checkout has uncommitted changes in files this branch would update; refusing to merge" >&2
  printf '%s\n' "$dirty_overlap" >&2
  exit 1
fi

dirs_file="$(mktemp "${TMPDIR:-/tmp}/kitsoki-merge-dirs.XXXXXX")"
guard_paths_file="$(mktemp "${TMPDIR:-/tmp}/kitsoki-merge-guards.XXXXXX")"
python3 - "$changed_files_file" "$dirs_file" "$guard_paths_file" <<'PY'
import os
import sys

changed_file, dirs_file, guards_file = sys.argv[1:]
with open(changed_file, "rb") as f:
    paths = [p.decode("utf-8", "surrogateescape") for p in f.read().split(b"\0") if p]

dirs = set()
guards = set()
for path in paths:
    if os.path.lexists(path):
        guards.add(path)
    directory = os.path.dirname(path) or "."
    dirs.add(directory)
    if directory == ".":
        guards.add(".")
    while directory not in (".", "/"):
        if os.path.lexists(directory):
            guards.add(directory)
        parent = os.path.dirname(directory) or "."
        if parent == ".":
            guards.add(".")
        if parent == directory:
            break
        directory = parent

def write_nul(path, values):
    with open(path, "wb") as f:
        for value in sorted(values):
            f.write(value.encode("utf-8", "surrogateescape") + b"\0")

write_nul(dirs_file, dirs)
write_nul(guards_file, guards)
PY

restore_guard_fallback() {
  while IFS= read -r -d '' f; do
    [ -e "$f" ] && [ ! -L "$f" ] && chmod a-w "$f" || true
  done <"$changed_files_file"
  while IFS= read -r -d '' d; do
    [ -n "$d" ] && [ -e "$d" ] && [ ! -L "$d" ] && chmod a-w "$d" 2>/dev/null || true
  done <"$dirs_file"
  while IFS= read -r -d '' p; do
    [ -n "$p" ] && [ -e "$p" ] && [ ! -L "$p" ] && chmod a-w "$p" 2>/dev/null || true
  done <"$guard_paths_file"
}

restore_guard() {
  restore_guard_fallback
  if [ -x scripts/protected-main-mode.sh ]; then
    if ! scripts/protected-main-mode.sh repair-writable-roots --quiet; then
      echo "warning: protected-main-mode.sh repair-writable-roots failed; scratch roots may need manual chmod repair" >&2
    fi
  fi
  rm -f "$changed_files_file" "$dirs_file" "$guard_paths_file"
}

trap restore_guard EXIT

while IFS= read -r -d '' p; do
  [ -n "$p" ] && [ ! -L "$p" ] && chmod u+w "$p" 2>/dev/null || true
done <"$guard_paths_file"
while IFS= read -r -d '' f; do
  [ -e "$f" ] && [ ! -L "$f" ] && chmod u+w "$f" || true
done <"$changed_files_file"

# The update-ref above prevents an in-gate staging overwrite. Check again at
# the last possible point so a later concurrent staging change cannot be
# promoted under the wrong receipt.
if [ -n "$staging_snapshot" ]; then
  current_staging="$(git rev-parse --verify "$DEFAULT_BRANCH" 2>/dev/null || true)"
  if [ "$current_staging" != "$source_result" ]; then
    echo "error: $DEFAULT_BRANCH changed before main advancement (expected $source_result, found ${current_staging:-missing}); main was not changed" >&2
    exit 1
  fi
fi

merge_out=""
pre_merge_head="$(git rev-parse --verify HEAD)"
set +e
merge_out="$(git merge --ff-only "$branch" 2>&1)"
merge_status=$?
set -e
if [ -n "$merge_out" ]; then
  printf '%s\n' "$merge_out"
fi
if [ "$merge_status" -ne 0 ]; then
  current_head="$(git rev-parse --verify HEAD 2>/dev/null || true)"
  if [ "$current_head" = "$pre_merge_head" ]; then
    if git restore --source="$pre_merge_head" --staged --worktree \
      --pathspec-from-file="$changed_files_file" --pathspec-file-nul >/dev/null 2>&1; then
      echo "error: merge failed; restored only the proven-clean branch paths from $pre_merge_head" >&2
    else
      echo "error: merge failed; automatic path-scoped cleanup failed, leaving state for inspection" >&2
    fi
  else
    echo "error: merge failed after HEAD changed ($pre_merge_head -> ${current_head:-missing}); refusing destructive cleanup" >&2
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

if [ -n "$source_dir" ]; then
  git update-ref -d "$source_original_recovery_ref" >/dev/null 2>&1 || true
  git update-ref -d "$source_recovery_ref" >/dev/null 2>&1 || true
fi

echo "main -> $(git rev-parse --short HEAD); read-only guard restored for changed paths"
