#!/usr/bin/env bash
# dev-workspace.sh - deterministic clone-backed development workspace lifecycle.
#
# Agents should use this script instead of running git worktree/clone/merge
# commands directly. The script owns the local git plumbing and leaves a
# capsule-compatible sentinel/manifest so launch policy, cleanup, and forensics
# can identify the workspace as Kitsoki-managed.
set -euo pipefail

CAPSULE_SENTINEL=".kitsoki-capsule"
CAPSULE_MANIFEST="capsule-manifest.json"
CLONE_SENTINEL=".kitsoki-clone"
DEV_MANIFEST=".kitsoki-dev-workspace.json"
DEFAULT_TARGET="staging/local"
DEFAULT_BASE="$DEFAULT_TARGET"
LOCAL_CONFIG=".kitsoki.local.yaml"

usage() {
  cat >&2 <<'EOF'
usage:
  scripts/dev-workspace.sh create --id ID [--branch BRANCH] [--base BASE] [--target TARGET] [--repo REPO] [--root ROOT] [--session-id SID] [--bootstrap] [--json]
  scripts/dev-workspace.sh bootstrap <workspace>
  scripts/dev-workspace.sh status <workspace|id> [--repo REPO] [--root ROOT] [--json]
  scripts/dev-workspace.sh commit <workspace|id> --message MESSAGE [--repo REPO] [--root ROOT] [--json]
  scripts/dev-workspace.sh merge <workspace|id> [--repo REPO] [--root ROOT] [--branch BRANCH] [--target TARGET] [--gate CMD] [--teardown]
  scripts/dev-workspace.sh close <workspace|id> [--repo REPO] [--root ROOT] [--force]
  scripts/dev-workspace.sh teardown <workspace|id> [--repo REPO] [--root ROOT] [--force]

Defaults:
  REPO   current git repository root
  ROOT   <repo>/.capsules/workspaces
  BASE   staging/local
  BRANCH agent/<id>
  TARGET staging/local
EOF
}

die() {
  echo "error: $*" >&2
  exit 1
}

repo_root() {
  local repo="${1:-.}"
  git -C "$repo" rev-parse --show-toplevel
}

abs_path() {
  python3 - "$1" <<'PY'
import os
import sys
print(os.path.abspath(sys.argv[1]))
PY
}

resolve_root() {
  local repo="$1"
  local root="${2:-}"
  if [ -z "$root" ]; then
    root="$repo/.capsules/workspaces"
  elif [ "${root#/}" = "$root" ]; then
    root="$repo/$root"
  fi
  abs_path "$root"
}

validate_id() {
  local id="$1"
  case "$id" in
    ""|"."|".."|*/*|*\\*)
      die "invalid workspace id: $id"
      ;;
  esac
}

safe_ref_fragment() {
  printf '%s' "$1" | tr -c 'A-Za-z0-9._-' '-'
}

workspace_path() {
  local repo="$1"
  local root="$2"
  local ref="$3"
  if [ "${ref#/}" != "$ref" ] || [[ "$ref" == ./* || "$ref" == ../* || "$ref" == */* ]]; then
    abs_path "$ref"
  else
    validate_id "$ref"
    abs_path "$root/$ref"
  fi
}

workspace_id_from_path() {
  local path="$1"
  if [ -f "$path/$DEV_MANIFEST" ]; then
    python3 - "$path/$DEV_MANIFEST" <<'PY'
import json
import sys
with open(sys.argv[1], encoding="utf-8") as f:
    print(json.load(f).get("id", ""))
PY
    return
  fi
  basename "$path"
}

manifest_value() {
  local path="$1"
  local key="$2"
  local fallback="$3"
  python3 - "$path/$DEV_MANIFEST" "$key" "$fallback" <<'PY'
import json
import sys

manifest, key, fallback = sys.argv[1:]
try:
    with open(manifest, encoding="utf-8") as f:
        value = json.load(f).get(key, "")
except FileNotFoundError:
    value = ""
if value is None:
    value = ""
print(str(value) if str(value).strip() else fallback)
PY
}

ensure_managed_workspace() {
  local path="$1"
  if [ ! -f "$path/$CAPSULE_SENTINEL" ] && [ ! -f "$path/$CLONE_SENTINEL" ]; then
    die "refusing unmanaged workspace: $path (missing $CAPSULE_SENTINEL or $CLONE_SENTINEL)"
  fi
}

ensure_under_root_or_forced() {
  local root="$1"
  local path="$2"
  local force="$3"
  case "$path" in
    "$root"/*) return 0 ;;
  esac
  [ "$force" = "1" ] || die "refusing path outside workspace root $root: $path (pass --force to override)"
}

write_git_excludes() {
  local path="$1"
  local exclude="$path/.git/info/exclude"
  mkdir -p "$(dirname "$exclude")"
  {
    echo "/$CAPSULE_SENTINEL"
    echo "/$CAPSULE_MANIFEST"
    echo "/$CLONE_SENTINEL"
    echo "/$DEV_MANIFEST"
    echo "/.kitsoki-owner"
  } >>"$exclude"
}

write_manifests() {
  local path="$1"
  local id="$2"
  local repo="$3"
  local root="$4"
  local branch="$5"
  local base="$6"
  local target="$7"
  local session_id="$8"
  local source_commit="$9"
  local head="${10}"
  local script_path="${11}"

  python3 - "$path" "$id" "$repo" "$root" "$branch" "$base" "$target" "$session_id" "$source_commit" "$head" "$script_path" <<'PY'
import json
import os
import sys
from datetime import datetime, timezone

path, ws_id, repo, root, branch, base, target, session_id, source_commit, head, script_path = sys.argv[1:]
opened_at = datetime.now(timezone.utc).replace(microsecond=0).isoformat().replace("+00:00", "Z")

capsule_manifest = {
    "capsule_name": "dev-workspace",
    "spec_path": script_path,
    "workspace": path,
    "opened_at": opened_at,
    "source": {
        "synthetic": False,
        "repo": repo,
        "commit": source_commit,
        "head": head,
        "branch": branch,
    },
    "network": "none",
    "tree_digest": "",
    "environment": {
        "kind": "dev-clone",
        "id": ws_id,
        "root": root,
        "base": base,
        "target": target,
        "session_id": session_id,
    },
}
clone_manifest = {
    "id": ws_id,
    "source": repo,
    "root": root,
    "branch": branch,
    "base": base,
    "target": target,
    "session_id": session_id,
    "created_at": opened_at,
    "managed_by": "scripts/dev-workspace.sh",
}
dev_manifest = dict(clone_manifest)
dev_manifest.update({
    "workspace": path,
    "source_commit": source_commit,
    "head": head,
    "capsule_manifest": os.path.join(path, "capsule-manifest.json"),
})

with open(os.path.join(path, ".kitsoki-capsule"), "w", encoding="utf-8") as f:
    f.write("dev-workspace\n")
if session_id:
    with open(os.path.join(path, ".kitsoki-owner"), "w", encoding="utf-8") as f:
        f.write(session_id + "\n")
for name, data in [
    ("capsule-manifest.json", capsule_manifest),
    (".kitsoki-clone", clone_manifest),
    (".kitsoki-dev-workspace.json", dev_manifest),
]:
    with open(os.path.join(path, name), "w", encoding="utf-8") as f:
        json.dump(data, f, indent=2, sort_keys=True)
        f.write("\n")
PY
}

emit_workspace_json() {
  local path="$1"
  local reused="${2:-false}"
  python3 - "$path" "$reused" <<'PY'
import json
import os
import sys

path, reused = sys.argv[1:]
data = {}
for name in (".kitsoki-dev-workspace.json", ".kitsoki-clone"):
    try:
        with open(os.path.join(path, name), encoding="utf-8") as f:
            data = json.load(f)
            break
    except FileNotFoundError:
        pass
out = {
    "ok": True,
    "id": data.get("id", os.path.basename(path)),
    "path": path,
    "branch": data.get("branch", ""),
    "base": data.get("base", ""),
    "target": data.get("target", "staging/local"),
    "root": data.get("root", os.path.dirname(path)),
    "reused": reused == "true",
}
print(json.dumps(out, indent=2, sort_keys=True))
PY
}

bootstrap_workspace() {
  local path="$1"
  local source_repo="${2:-}"
  [ -d "$path" ] || die "workspace does not exist: $path"
  copy_local_config "$path" "$source_repo"
  if make -C "$path" -n bootstrap-workspace >/dev/null 2>&1; then
    make -C "$path" bootstrap-workspace
  else
    make -C "$path" bootstrap-worktree
  fi
}

copy_local_config() {
  local path="$1"
  local source_repo="${2:-}"
  if [ -z "$source_repo" ]; then
    source_repo="$(manifest_value "$path" source "")"
  fi
  [ -n "$source_repo" ] || return 0
  local src="$source_repo/$LOCAL_CONFIG"
  local dst="$path/$LOCAL_CONFIG"
  [ -f "$src" ] || return 0
  [ "$src" != "$dst" ] || return 0
  cp -p "$src" "$dst"
}

workspace_dirty() {
  local path="$1"
  [ -n "$(git -C "$path" status --porcelain)" ]
}

primary_dirty_overlaps_file_list() {
  local repo="$1"
  local files_path="$2"
  python3 - "$repo" "$files_path" <<'PY'
import subprocess
import sys

repo, files_path = sys.argv[1:]
with open(files_path, encoding="utf-8") as f:
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
if conflicts:
    for path in conflicts:
        print(path)
    sys.exit(1)
PY
}

cmd_create() {
  local repo="."
  local root=""
  local id=""
  local branch=""
  local base="$DEFAULT_BASE"
  local target="$DEFAULT_TARGET"
  local session_id=""
  local bootstrap=0
  local json=0

  while [ "$#" -gt 0 ]; do
    case "$1" in
      --repo) repo="${2:?--repo requires a value}"; shift 2 ;;
      --root) root="${2:?--root requires a value}"; shift 2 ;;
      --id) id="${2:?--id requires a value}"; shift 2 ;;
      --branch|--name) branch="${2:?--branch requires a value}"; shift 2 ;;
      --base) base="${2:?--base requires a value}"; shift 2 ;;
      --target) target="${2:?--target requires a value}"; shift 2 ;;
      --session-id|--session_id) session_id="${2:?--session-id requires a value}"; shift 2 ;;
      --bootstrap) bootstrap=1; shift ;;
      --no-bootstrap) bootstrap=0; shift ;;
      --json) json=1; shift ;;
      -h|--help) usage; exit 0 ;;
      *) die "create: unexpected argument: $1" ;;
    esac
  done

  [ -n "$id" ] || die "create: --id is required"
  validate_id "$id"
  [ -n "$branch" ] || branch="agent/$id"
  repo="$(repo_root "$repo")"
  root="$(resolve_root "$repo" "$root")"
  local path="$root/$id"
  local script_path="$repo/scripts/dev-workspace.sh"

  if [ -e "$path" ]; then
    ensure_managed_workspace "$path"
    local existing_branch
    existing_branch="$(git -C "$path" branch --show-current 2>/dev/null || true)"
    if [ -n "$branch" ] && [ "$existing_branch" != "$branch" ]; then
      die "create: $path already holds branch $existing_branch (wanted $branch)"
    fi
    local existing_session
    if [ -f "$path/.kitsoki-owner" ]; then
      existing_session="$(tr -d '\r\n' <"$path/.kitsoki-owner")"
    else
      existing_session=""
    fi
    if [ -n "$session_id" ] && [ -n "$existing_session" ] && [ "$session_id" != "$existing_session" ]; then
      die "create: \"$id\" is already checked out by session \"$existing_session\"; refusing to share"
    fi
    if [ "$bootstrap" = "1" ]; then
      bootstrap_workspace "$path" "$repo"
    fi
    if [ "$json" = "1" ]; then
      emit_workspace_json "$path" true
    else
      echo "workspace: $path"
      echo "branch: $existing_branch"
      echo "reused: true"
    fi
    return 0
  fi

  mkdir -p "$root"
  local source_commit
  source_commit="$(git -C "$repo" rev-parse "${base:-HEAD}")"
  # The source is always a local repo path. Use local clone mode so existing
  # objects are hardlinked instead of copied while refs/worktree state stay
  # isolated inside the managed capsule clone.
  git -C "$repo" clone --local --origin source "$repo" "$path"
  write_git_excludes "$path"
  local base_ref="$base"
  if [ -n "$base" ] && ! git -C "$path" rev-parse --verify --quiet "$base^{commit}" >/dev/null; then
    if git -C "$path" rev-parse --verify --quiet "source/$base^{commit}" >/dev/null; then
      base_ref="source/$base"
    fi
  fi
  if [ -n "$branch" ]; then
    git -C "$path" switch -q -c "$branch" "$base_ref"
  elif [ -n "$base" ]; then
    git -C "$path" switch -q --detach "$base_ref"
  fi
  git -C "$path" config user.name "Kitsoki Agent"
  git -C "$path" config user.email "agent@kitsoki.dev"
  local head
  head="$(git -C "$path" rev-parse HEAD)"
  write_manifests "$path" "$id" "$repo" "$root" "$branch" "$base" "$target" "$session_id" "$source_commit" "$head" "$script_path"

  if [ "$bootstrap" = "1" ]; then
    bootstrap_workspace "$path" "$repo"
  fi

  if [ "$json" = "1" ]; then
    emit_workspace_json "$path" false
  else
    echo "workspace: $path"
    echo "branch: $branch"
    echo "base: $base"
    echo "manifest: $path/$CAPSULE_MANIFEST"
  fi
}

cmd_bootstrap() {
  [ "$#" -eq 1 ] || die "bootstrap: expected exactly one workspace path"
  bootstrap_workspace "$(abs_path "$1")"
}

cmd_status() {
  local repo="."
  local root=""
  local json=0
  local ref=""
  while [ "$#" -gt 0 ]; do
    case "$1" in
      --repo) repo="${2:?--repo requires a value}"; shift 2 ;;
      --root) root="${2:?--root requires a value}"; shift 2 ;;
      --json) json=1; shift ;;
      -h|--help) usage; exit 0 ;;
      *)
        [ -z "$ref" ] || die "status: unexpected argument: $1"
        ref="$1"
        shift
        ;;
    esac
  done
  [ -n "$ref" ] || die "status: workspace path or id is required"
  repo="$(repo_root "$repo")"
  root="$(resolve_root "$repo" "$root")"
  local path
  path="$(workspace_path "$repo" "$root" "$ref")"
  ensure_managed_workspace "$path"
  local branch dirty head
  branch="$(git -C "$path" branch --show-current 2>/dev/null || true)"
  head="$(git -C "$path" rev-parse --short HEAD)"
  dirty=false
  if workspace_dirty "$path"; then
    dirty=true
  fi
  if [ "$json" = "1" ]; then
    python3 - "$path" "$branch" "$head" "$dirty" <<'PY'
import json
import os
import sys
path, branch, head, dirty = sys.argv[1:]
print(json.dumps({
    "ok": True,
    "id": os.path.basename(path),
    "path": path,
    "branch": branch,
    "head": head,
    "dirty": dirty == "true",
}, indent=2, sort_keys=True))
PY
  else
    echo "workspace: $path"
    echo "branch: $branch"
    echo "head: $head"
    echo "dirty: $dirty"
  fi
}

cmd_commit() {
  local repo="."
  local root=""
  local ref=""
  local message=""
  local json=0
  while [ "$#" -gt 0 ]; do
    case "$1" in
      --repo) repo="${2:?--repo requires a value}"; shift 2 ;;
      --root) root="${2:?--root requires a value}"; shift 2 ;;
      --message|-m) message="${2:?--message requires a value}"; shift 2 ;;
      --json) json=1; shift ;;
      -h|--help) usage; exit 0 ;;
      *)
        [ -z "$ref" ] || die "commit: unexpected argument: $1"
        ref="$1"
        shift
        ;;
    esac
  done
  [ -n "$ref" ] || die "commit: workspace path or id is required"
  [ -n "$message" ] || die "commit: --message is required"
  repo="$(repo_root "$repo")"
  root="$(resolve_root "$repo" "$root")"
  local path
  path="$(workspace_path "$repo" "$root" "$ref")"
  ensure_managed_workspace "$path"
  if ! workspace_dirty "$path"; then
    die "commit: workspace has no changes"
  fi
  git -C "$path" add -A
  git -C "$path" commit -m "$message"
  local sha
  sha="$(git -C "$path" rev-parse HEAD)"
  if [ "$json" = "1" ]; then
    python3 - "$path" "$sha" <<'PY'
import json
import sys
path, sha = sys.argv[1:]
print(json.dumps({"ok": True, "path": path, "sha": sha}, indent=2, sort_keys=True))
PY
  else
    echo "committed: $sha"
  fi
}

cmd_merge() {
  local repo="."
  local root=""
  local ref=""
  local branch=""
  local target=""
  local gate=""
  local teardown=0
  while [ "$#" -gt 0 ]; do
    case "$1" in
      --repo) repo="${2:?--repo requires a value}"; shift 2 ;;
      --root) root="${2:?--root requires a value}"; shift 2 ;;
      --branch) branch="${2:?--branch requires a value}"; shift 2 ;;
      --target|--onto) target="${2:?--target requires a value}"; shift 2 ;;
      --gate) gate="${2:?--gate requires a value}"; shift 2 ;;
      --teardown) teardown=1; shift ;;
      -h|--help) usage; exit 0 ;;
      *)
        [ -z "$ref" ] || die "merge: unexpected argument: $1"
        ref="$1"
        shift
        ;;
    esac
  done
  [ -n "$ref" ] || die "merge: workspace path or id is required"
  repo="$(repo_root "$repo")"
  root="$(resolve_root "$repo" "$root")"
  local path
  path="$(workspace_path "$repo" "$root" "$ref")"
  ensure_managed_workspace "$path"
  if [ -z "$target" ]; then
    target="$(manifest_value "$path" target "$DEFAULT_TARGET")"
  fi
  [ -n "$target" ] || die "merge: target branch is empty"
  git -C "$repo" check-ref-format --branch "$target" >/dev/null || die "merge: invalid target branch: $target"
  local current_branch
  current_branch="$(git -C "$repo" branch --show-current)"
  if [ "$target" = "main" ]; then
    [ "$current_branch" = "main" ] || die "merge: repo must be on main for --target main"
  elif [ "$current_branch" = "$target" ]; then
    die "merge: target $target is checked out in the primary checkout; switch the primary checkout back to main before updating the local stabilization branch"
  fi
  if workspace_dirty "$path"; then
    die "merge: workspace has uncommitted changes"
  fi
  [ -n "$branch" ] || branch="$(git -C "$path" branch --show-current)"
  [ -n "$branch" ] && [ "$branch" != "HEAD" ] || die "merge: workspace must be on a branch or --branch must be provided"

  local target_base
  if git -C "$repo" rev-parse --verify --quiet "refs/heads/$target" >/dev/null; then
    target_base="$target"
  else
    target_base="main"
  fi
  git -C "$path" fetch source "+refs/heads/$target_base:refs/remotes/source/$target_base"
  git -C "$path" rebase "source/$target_base"
  if [ -n "$gate" ]; then
    (cd "$path" && sh -c "$gate")
  fi

  if [ "$target" = "main" ]; then
    local changed_files_file
    changed_files_file="$(mktemp "${TMPDIR:-/tmp}/kitsoki-dev-workspace-files.XXXXXX")"
    git -C "$path" diff --name-only "source/$target_base" HEAD >"$changed_files_file"
    local dirty_overlap
    set +e
    dirty_overlap="$(primary_dirty_overlaps_file_list "$repo" "$changed_files_file")"
    local dirty_status=$?
    set -e
    rm -f "$changed_files_file"
    if [ "$dirty_status" -ne 0 ]; then
      if [ -z "$dirty_overlap" ]; then
        die "merge: failed to inspect primary checkout dirty paths"
      fi
      echo "error: primary checkout has uncommitted changes in files this workspace would update:" >&2
      printf '%s\n' "$dirty_overlap" >&2
      exit 1
    fi
  fi

  local id safe landing_branch
  id="$(workspace_id_from_path "$path")"
  safe="$(safe_ref_fragment "$id")"
  landing_branch="capsule/${safe}-land"
  if git -C "$repo" rev-parse --verify --quiet "$landing_branch" >/dev/null; then
    if git -C "$repo" merge-base --is-ancestor "$landing_branch" "$target_base"; then
      git -C "$repo" branch -D "$landing_branch" >/dev/null
    else
      die "merge: landing branch already exists and is not merged: $landing_branch"
    fi
  fi
  git -C "$repo" fetch "$path" "$branch:refs/heads/$landing_branch"
  if [ "$target" = "main" ]; then
    local -a merge_args
    merge_args=("$branch" "--source-dir" "$path")
    if [ -n "$gate" ]; then
      merge_args+=("--gate" "$gate")
    fi
    (cd "$repo" && scripts/merge-to-main.sh "${merge_args[@]}")
  else
    if ! git -C "$repo" merge-base --is-ancestor "$target_base" "$landing_branch"; then
      die "merge: $landing_branch is not a fast-forward of $target_base"
    fi
    local old_target new_target
    old_target="$(git -C "$repo" rev-parse --verify "refs/heads/$target" 2>/dev/null || true)"
    new_target="$(git -C "$repo" rev-parse --verify "$landing_branch")"
    if [ -n "$old_target" ]; then
      git -C "$repo" update-ref -m "dev-workspace merge $branch into $target" "refs/heads/$target" "$new_target" "$old_target"
    else
      git -C "$repo" update-ref -m "dev-workspace create $target from $branch" "refs/heads/$target" "$new_target"
    fi
    echo "$target -> $(git -C "$repo" rev-parse --short "$target")"
  fi
  git -C "$repo" branch -D "$landing_branch" >/dev/null

  if [ "$teardown" = "1" ]; then
    cmd_close --repo "$repo" --root "$root" "$path"
  fi
  echo "merged: $branch -> $target"
}

cmd_close() {
  local repo="."
  local root=""
  local force=0
  local ref=""
  while [ "$#" -gt 0 ]; do
    case "$1" in
      --repo) repo="${2:?--repo requires a value}"; shift 2 ;;
      --root) root="${2:?--root requires a value}"; shift 2 ;;
      --force) force=1; shift ;;
      -h|--help) usage; exit 0 ;;
      *)
        [ -z "$ref" ] || die "close: unexpected argument: $1"
        ref="$1"
        shift
        ;;
    esac
  done
  [ -n "$ref" ] || die "close: workspace path or id is required"
  repo="$(repo_root "$repo")"
  root="$(resolve_root "$repo" "$root")"
  local path
  path="$(workspace_path "$repo" "$root" "$ref")"
  ensure_managed_workspace "$path"
  ensure_under_root_or_forced "$root" "$path" "$force"
  if [ "$force" != "1" ] && workspace_dirty "$path"; then
    die "close: workspace has uncommitted changes: $path"
  fi
  chmod -R u+rwX "$path" 2>/dev/null || true
  rm -rf "$path"
  echo "removed: $path"
}

main() {
  local cmd="${1:-}"
  case "$cmd" in
    create|open) shift; cmd_create "$@" ;;
    bootstrap) shift; cmd_bootstrap "$@" ;;
    status) shift; cmd_status "$@" ;;
    commit) shift; cmd_commit "$@" ;;
    merge|land) shift; cmd_merge "$@" ;;
    close|teardown) shift; cmd_close "$@" ;;
    -h|--help|"") usage; [ -n "$cmd" ] || exit 1 ;;
    *) usage; die "unknown command: $cmd" ;;
  esac
}

main "$@"
