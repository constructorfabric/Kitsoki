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
INITIALIZATION_DIR=".initializing"
INITIALIZATION_METADATA="metadata"
DEFAULT_TARGET="staging/local"
DEFAULT_BASE="$DEFAULT_TARGET"
LOCAL_CONFIG=".kitsoki.local.yaml"
RECOVERED_QUARANTINE_MANIFEST=".kitsoki-recovered-quarantine.json"

usage() {
  cat >&2 <<'EOF'
usage:
  scripts/dev-workspace.sh create --id ID [--branch BRANCH] [--base BASE] [--target TARGET] [--repo REPO] [--root ROOT] [--session-id SID] [--bootstrap] [--json]
  scripts/dev-workspace.sh bootstrap <workspace>
  scripts/dev-workspace.sh status <workspace|id> [--repo REPO] [--root ROOT] [--json]
  scripts/dev-workspace.sh commit <workspace|id> --message MESSAGE [--repo REPO] [--root ROOT] [--json]
  scripts/dev-workspace.sh merge <workspace|id> [--repo REPO] [--root ROOT] [--branch BRANCH] [--target TARGET] [--gate CMD] [--replace-landing] [--teardown]
  scripts/dev-workspace.sh park <workspace|id> [--repo REPO] [--root ROOT] [--session-id SID] [--message MESSAGE] [--json]
  scripts/dev-workspace.sh seal-recovered-quarantine <workspace> --snapshot-ref REF --recovery-ref REF [--repo REPO] [--root ROOT]
  scripts/dev-workspace.sh recover <workspace|id> [--repo REPO] [--root ROOT] [--discard-incomplete]
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

# Creation cannot put a sentinel in the target until git clone has made the
# directory. Keep the lock beside targets instead: mkdir gives us an atomic
# claim without making an incomplete target look like a managed workspace.
CREATE_LOCK=""
CREATE_PATH=""
CREATE_IN_PROGRESS=0

cleanup_interrupted_create() {
  local status=$?
  if [ "$CREATE_IN_PROGRESS" = "1" ]; then
    # This process owns both paths. Removing them together means ordinary
    # failures and interruptible signals never leave a half-clone behind.
    rm -rf "$CREATE_PATH" "$CREATE_LOCK" 2>/dev/null || true
  fi
  return "$status"
}
trap cleanup_interrupted_create EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

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

same_device() {
  python3 - "$1" "$2" <<'PY'
import os
import sys

sys.exit(0 if os.stat(sys.argv[1]).st_dev == os.stat(sys.argv[2]).st_dev else 1)
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

is_object_id() {
  local oid="$1"
  case "$oid" in
    ""|*[!0-9a-f]*) return 1 ;;
  esac
  [ "${#oid}" = "40" ] || [ "${#oid}" = "64" ]
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

json_file_value() {
  local file="$1"
  local key="$2"
  local fallback="${3:-}"
  python3 - "$file" "$key" "$fallback" <<'PY'
import json
import sys

path, key, fallback = sys.argv[1:]
try:
    with open(path, encoding="utf-8") as f:
        value = json.load(f).get(key, "")
except (FileNotFoundError, json.JSONDecodeError, OSError):
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

initialization_marker() {
  local root="$1"
  local id="$2"
  printf '%s/%s/%s\n' "$root" "$INITIALIZATION_DIR" "$id"
}

initialization_metadata_value() {
  local marker="$1"
  local key="$2"
  [ -f "$marker/$INITIALIZATION_METADATA" ] || return 0
  sed -n "s/^${key}=//p" "$marker/$INITIALIZATION_METADATA" | head -n 1
}

initialization_state() {
  local root="$1"
  local id="$2"
  local marker pid
  marker="$(initialization_marker "$root" "$id")"
  [ -d "$marker" ] || return 1
  pid="$(initialization_metadata_value "$marker" pid)"
  if [[ "$pid" =~ ^[0-9]+$ ]] && kill -0 "$pid" 2>/dev/null; then
    printf 'active\n'
  else
    printf 'stale\n'
  fi
}

initialization_refusal() {
  local command="$1"
  local root="$2"
  local id="$3"
  local state marker pid path
  state="$(initialization_state "$root" "$id")" || return 0
  marker="$(initialization_marker "$root" "$id")"
  pid="$(initialization_metadata_value "$marker" pid)"
  path="$(initialization_metadata_value "$marker" path)"
  if [ "$state" = "active" ]; then
    die "$command: workspace is initializing${pid:+ (pid $pid)}: ${path:-$root/$id}; retry status after creation completes"
  fi
  die "$command: workspace has a stale initialization marker: ${path:-$root/$id}; inspect it, then run recover $id --discard-incomplete if it is an interrupted clone"
}

acquire_initialization_lock() {
  local root="$1"
  local id="$2"
  local path="$3"
  local branch="$4"
  local session_id="$5"
  local marker
  marker="$(initialization_marker "$root" "$id")"
  mkdir -p "$root/$INITIALIZATION_DIR"
  if ! mkdir "$marker" 2>/dev/null; then
    initialization_refusal create "$root" "$id"
    die "create: could not acquire initialization marker for $path"
  fi
  {
    printf 'pid=%s\n' "$$"
    printf 'path=%s\n' "$path"
    printf 'id=%s\n' "$id"
    printf 'branch=%s\n' "$branch"
    printf 'session_id=%s\n' "$session_id"
    printf 'started_at=%s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  } >"$marker/$INITIALIZATION_METADATA"
  CREATE_LOCK="$marker"
  CREATE_PATH="$path"
  CREATE_IN_PROGRESS=1
}

release_initialization_lock() {
  rm -rf "$CREATE_LOCK"
  CREATE_LOCK=""
  CREATE_PATH=""
  CREATE_IN_PROGRESS=0
}

ensure_not_initializing() {
  local command="$1"
  local root="$2"
  local path="$3"
  local id
  id="$(basename "$path")"
  if initialization_state "$root" "$id" >/dev/null; then
    initialization_refusal "$command" "$root" "$id"
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
    echo "/$RECOVERED_QUARANTINE_MANIFEST"
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
  local bootstrap_tmp=""
  local bootstrap_status=0
  [ -d "$path" ] || die "workspace does not exist: $path"
  # tsx leaves its IPC socket beneath TMPDIR when an interrupted bootstrap
  # exits. A retry must not inherit that dead socket and fail with EADDRINUSE.
  rm -rf "$path/.temp"/tsx-* 2>/dev/null || true
  copy_local_config "$path" "$source_repo"
  # A Studio server can itself run from the managed staging capsule, putting a
  # child workspace far beyond macOS's Unix-domain socket-path limit. Keep the
  # bootstrap-only tsx socket root short and unique; Make propagates TEMP_DIR
  # to both TMPDIR and KITSOKI_TEMP_ROOT for the runstatus commands.
  bootstrap_tmp="$(mktemp -d /tmp/kitsoki-bootstrap.XXXXXX)" || die "could not create short bootstrap temp root"
  if make -C "$path" -n bootstrap-workspace >/dev/null 2>&1; then
    make -C "$path" TEMP_DIR="$bootstrap_tmp" bootstrap-workspace || bootstrap_status=$?
  else
    make -C "$path" TEMP_DIR="$bootstrap_tmp" bootstrap-worktree || bootstrap_status=$?
  fi
  rm -rf "$bootstrap_tmp"
  return "$bootstrap_status"
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
  # A previous bootstrap can have preserved a read-only local config. This is
  # transient workspace state; replace it atomically on retry.
  chmod u+w "$dst" 2>/dev/null || true
  rm -f "$dst"
  cp -p "$src" "$dst"
}

workspace_dirty() {
  local path="$1" status
  if ! status="$(git -C "$path" status --porcelain --untracked-files=all)"; then
    die "could not inspect workspace status: $path"
  fi
  [ -n "$status" ]
}

import_workspace_issue_file() {
  local src="$1"
  local dst="$2"
  mkdir -p "$(dirname "$dst")"
  if [ -e "$dst" ]; then
    cmp -s "$src" "$dst" || {
      echo "error: close: refusing to overwrite a different source-checkout ticket: $dst" >&2
      return 1
    }
    return 0
  fi
  cp -p "$src" "$dst"
  echo "preserved workspace ticket: $dst"
}

workspace_artifact_matches() {
  local src="$1"
  local dst="$2"
  if [ -f "$src" ] && [ -f "$dst" ]; then
    cmp -s "$src" "$dst"
    return
  fi
  if [ -d "$src" ] && [ -d "$dst" ]; then
    diff -qr "$src" "$dst" >/dev/null
    return
  fi
  return 1
}

# Older filing paths could leave durable tickets inside an ignored workspace
# artifact tree. Import the known canonical and retired shapes for immediate
# visibility, but leave the complete source tree in the quarantined checkout.
# Unknown providers, future kinds, hidden entries, and concurrent late writes
# must never be deleted merely because this compatibility importer cannot yet
# interpret them.
import_workspace_issues() {
  local path="$1"
  local repo="$2"
  local src_root="$path/.artifacts/issues"
  local dst_root="$repo/.artifacts/issues"
  local kind entry id child sidecar
  [ -d "$src_root" ] || return 0

  for kind in bugs features epics; do
    [ -d "$src_root/$kind" ] || continue
    mkdir -p "$dst_root/$kind"
    for entry in "$src_root/$kind"/*; do
      [ -e "$entry" ] || continue
      if [ -f "$entry" ] && [[ "$entry" == *.md ]]; then
        import_workspace_issue_file "$entry" "$dst_root/$kind/$(basename "$entry")" || return 1
        continue
      fi
      if [ -d "$entry" ] && [ -f "$entry/issue.md" ]; then
        id="$(basename "$entry")"
        import_workspace_issue_file "$entry/issue.md" "$dst_root/$kind/$id.md" || return 1
        sidecar="$dst_root/$kind/$id.artifacts"
        for child in "$entry"/*; do
          [ -e "$child" ] || continue
          [ "$(basename "$child")" = "issue.md" ] && continue
          mkdir -p "$sidecar"
          if [ -e "$sidecar/$(basename "$child")" ]; then
            if ! workspace_artifact_matches "$child" "$sidecar/$(basename "$child")"; then
              echo "error: close: refusing to overwrite source-checkout ticket evidence: $sidecar/$(basename "$child")" >&2
              return 1
            fi
            continue
          fi
          cp -Rp "$child" "$sidecar/"
        done
        continue
      fi
      if [ -d "$entry" ] && [[ "$entry" == *.artifacts ]]; then
        if [ -e "$dst_root/$kind/$(basename "$entry")" ]; then
          if ! workspace_artifact_matches "$entry" "$dst_root/$kind/$(basename "$entry")"; then
            echo "error: close: refusing to merge ambiguous source-checkout ticket evidence: $dst_root/$kind/$(basename "$entry")" >&2
            return 1
          fi
          continue
        fi
        cp -Rp "$entry" "$dst_root/$kind/"
      fi
    done
  done
}

preserve_workspace_review_artifacts() {
  local path="$1" repo="$2" id="$3" stamp destination preserved=0
  local visible_file ignored_file relative source destination_path
  stamp="$(date -u +%Y%m%dT%H%M%SZ)"
  destination="$repo/.artifacts/workspace-close/${id}-${stamp}-$$"
  visible_file="$(mktemp "${TMPDIR:-/tmp}/kitsoki-close-visible.XXXXXX")"
  ignored_file="$(mktemp "${TMPDIR:-/tmp}/kitsoki-close-ignored.XXXXXX")"
  if ! git -C "$path" ls-files --others --exclude-standard -z -- \
    .context .artifacts .kitsoki.local.yaml >"$visible_file"; then
    rm -f "$visible_file" "$ignored_file"
    echo "error: close: could not inventory untracked review artifacts; keeping workspace" >&2
    return 1
  fi
  if ! git -C "$path" ls-files --others --ignored --exclude-standard -z -- \
    .context .artifacts .kitsoki.local.yaml >"$ignored_file"; then
    rm -f "$visible_file" "$ignored_file"
    echo "error: close: could not inventory ignored review artifacts; keeping workspace" >&2
    return 1
  fi
  while IFS= read -r -d '' relative; do
    source="$path/$relative"
    destination_path="$destination/$relative"
    # A concurrent deletion is not data loss caused by close. A concurrent
    # replacement remains in the source checkout and is caught by the later
    # quarantine/status boundary.
    [ -e "$source" ] || [ -L "$source" ] || continue
    mkdir -p "$(dirname "$destination_path")"
    if ! cp -Rp "$source" "$destination_path"; then
      rm -f "$visible_file" "$ignored_file"
      echo "error: close: could not preserve $source; keeping workspace" >&2
      return 1
    fi
    preserved=1
  done < <(cat "$visible_file" "$ignored_file")
  rm -f "$visible_file" "$ignored_file"
  if [ "$preserved" = "1" ]; then
    echo "close: preserved ignored review artifacts at $destination" >&2
  else
    rmdir "$destination" >/dev/null 2>&1 || true
  fi
}

preserve_all_ignored_artifacts() {
  local path="$1" repo="$2" id="$3" stamp destination
  local before_list after_list before_tar after_tar
  stamp="$(date -u +%Y%m%dT%H%M%SZ)"
  destination="$repo/.artifacts/workspace-close/${id}-${stamp}-$$-ignored"
  before_list="$(mktemp "${TMPDIR:-/tmp}/kitsoki-close-ignored-before.XXXXXX")"
  after_list="$(mktemp "${TMPDIR:-/tmp}/kitsoki-close-ignored-after.XXXXXX")"
  before_tar="$(mktemp "${TMPDIR:-/tmp}/kitsoki-close-ignored-before-tar.XXXXXX")"
  after_tar="$(mktemp "${TMPDIR:-/tmp}/kitsoki-close-ignored-after-tar.XXXXXX")"
  if ! git -C "$path" ls-files --others --ignored --exclude-standard -z >"$before_list"; then
    rm -f "$before_list" "$after_list" "$before_tar" "$after_tar"
    echo "error: close: could not inventory the complete ignored tree; keeping quarantine" >&2
    return 1
  fi
  if [ ! -s "$before_list" ]; then
    rm -f "$before_list" "$after_list" "$before_tar" "$after_tar"
    return 0
  fi
  if ! tar -C "$path" --null --files-from="$before_list" -cf "$before_tar"; then
    rm -f "$before_list" "$after_list" "$before_tar" "$after_tar"
    echo "error: close: could not archive the complete ignored tree; keeping quarantine" >&2
    return 1
  fi
  if ! git -C "$path" ls-files --others --ignored --exclude-standard -z >"$after_list" ||
    ! tar -C "$path" --null --files-from="$after_list" -cf "$after_tar"; then
    rm -f "$before_list" "$after_list" "$before_tar" "$after_tar"
    echo "error: close: ignored tree changed while archiving; keeping quarantine" >&2
    return 1
  fi
  if ! cmp -s "$before_list" "$after_list" || ! cmp -s "$before_tar" "$after_tar"; then
    mkdir -p "$destination"
    cp -p "$before_list" "$destination/ignored-paths-before.nul" || true
    cp -p "$before_tar" "$destination/ignored-root-before.tar" || true
    rm -f "$before_list" "$after_list" "$before_tar" "$after_tar"
    echo "error: close: ignored tree changed while archiving; first snapshot retained at $destination and quarantine kept" >&2
    return 1
  fi
  if ! mkdir -p "$destination" ||
    ! mv "$before_list" "$destination/ignored-paths.nul" ||
    ! mv "$before_tar" "$destination/ignored-root.tar"; then
    rm -f "$before_list" "$after_list" "$before_tar" "$after_tar"
    echo "error: close: could not publish the complete ignored-tree archive; keeping quarantine" >&2
    return 1
  fi
  rm -f "$after_list" "$after_tar"
  echo "close: preserved complete ignored tree at $destination/ignored-root.tar" >&2
}

preserve_git_repository_state() {
  local path="$1" repo="$2" id="$3" stamp destination
  local all_before all_after primary_objects missing_objects
  local metadata_before metadata_after unique_dir unique_pack unique_index pack_hash
  [ -d "$path/.git" ] || {
    echo "error: close: managed quarantine has no standalone Git directory; keeping quarantine" >&2
    return 1
  }
  stamp="$(date -u +%Y%m%dT%H%M%SZ)"
  destination="$repo/.artifacts/workspace-close/${id}-${stamp}-$$-git"
  all_before="$(mktemp "${TMPDIR:-/tmp}/kitsoki-close-git-objects-before.XXXXXX")"
  all_after="$(mktemp "${TMPDIR:-/tmp}/kitsoki-close-git-objects-after.XXXXXX")"
  primary_objects="$(mktemp "${TMPDIR:-/tmp}/kitsoki-close-git-primary.XXXXXX")"
  missing_objects="$(mktemp "${TMPDIR:-/tmp}/kitsoki-close-git-missing.XXXXXX")"
  metadata_before="$(mktemp "${TMPDIR:-/tmp}/kitsoki-close-git-metadata-before.XXXXXX")"
  metadata_after="$(mktemp "${TMPDIR:-/tmp}/kitsoki-close-git-metadata-after.XXXXXX")"
  unique_dir="$(mktemp -d "${TMPDIR:-/tmp}/kitsoki-close-git-unique.XXXXXX")"
  unique_pack="$unique_dir/unique-objects.pack"
  unique_index="$unique_dir/unique-objects.idx"
  if ! git -C "$path" cat-file --batch-all-objects --batch-check='%(objectname)' | LC_ALL=C sort -u >"$all_before" ||
    ! tar -C "$path" \
      --exclude='.git/objects/[0-9a-f][0-9a-f]' \
      --exclude='.git/objects/pack/*.pack' \
      --exclude='.git/objects/pack/*.idx' \
      --exclude='.git/objects/pack/*.rev' \
      -cf "$metadata_before" .git; then
    rm -f "$all_before" "$all_after" "$primary_objects" "$missing_objects" \
      "$metadata_before" "$metadata_after" "$unique_pack" "$unique_index"
    rmdir "$unique_dir" >/dev/null 2>&1 || true
    echo "error: close: could not inventory Git repository state; keeping quarantine" >&2
    return 1
  fi
  if ! git -C "$repo" rev-list --objects --all | awk '{ print $1 }' | LC_ALL=C sort -u >"$primary_objects"; then
    rm -f "$all_before" "$all_after" "$primary_objects" "$missing_objects" \
      "$metadata_before" "$metadata_after" "$unique_pack" "$unique_index"
    rmdir "$unique_dir" >/dev/null 2>&1 || true
    echo "error: close: could not inventory objects reachable from primary refs; keeping quarantine" >&2
    return 1
  fi
  if ! comm -23 "$all_before" "$primary_objects" >"$missing_objects"; then
    rm -f "$all_before" "$all_after" "$primary_objects" "$missing_objects" \
      "$metadata_before" "$metadata_after" "$unique_pack" "$unique_index"
    rmdir "$unique_dir" >/dev/null 2>&1 || true
    echo "error: close: could not compute the unique Git object set; keeping quarantine" >&2
    return 1
  fi
  pack_hash=""
  if [ -s "$missing_objects" ]; then
    rm -f "$unique_index"
    if ! git -C "$path" pack-objects --stdout <"$missing_objects" >"$unique_pack" ||
      ! pack_hash="$(git index-pack -o "$unique_index" "$unique_pack")" ||
      [ -z "$pack_hash" ] ||
      ! git verify-pack "$unique_index" >/dev/null; then
      rm -f "$all_before" "$all_after" "$primary_objects" "$missing_objects" \
        "$metadata_before" "$metadata_after" "$unique_pack" "$unique_index"
      rmdir "$unique_dir" >/dev/null 2>&1 || true
      echo "error: close: could not create a verified pack of unique Git objects; keeping quarantine" >&2
      return 1
    fi
  else
    rm -f "$unique_pack" "$unique_index"
  fi
  if ! git -C "$path" cat-file --batch-all-objects --batch-check='%(objectname)' | LC_ALL=C sort -u >"$all_after" ||
    ! tar -C "$path" \
      --exclude='.git/objects/[0-9a-f][0-9a-f]' \
      --exclude='.git/objects/pack/*.pack' \
      --exclude='.git/objects/pack/*.idx' \
      --exclude='.git/objects/pack/*.rev' \
      -cf "$metadata_after" .git; then
    rm -f "$all_before" "$all_after" "$primary_objects" "$missing_objects" \
      "$metadata_before" "$metadata_after" "$unique_pack" "$unique_index"
    rmdir "$unique_dir" >/dev/null 2>&1 || true
    echo "error: close: Git repository changed while archiving; keeping quarantine" >&2
    return 1
  fi
  if ! cmp -s "$all_before" "$all_after" || ! cmp -s "$metadata_before" "$metadata_after"; then
    mkdir -p "$destination"
    cp -p "$all_before" "$destination/all-objects-before.txt" || true
    cp -p "$metadata_before" "$destination/git-metadata-before.tar" || true
    [ ! -f "$unique_pack" ] || cp -p "$unique_pack" "$destination/unique-objects-before.pack" || true
    [ ! -f "$unique_index" ] || cp -p "$unique_index" "$destination/unique-objects-before.idx" || true
    rm -f "$all_before" "$all_after" "$primary_objects" "$missing_objects" \
      "$metadata_before" "$metadata_after" "$unique_pack" "$unique_index"
    rmdir "$unique_dir" >/dev/null 2>&1 || true
    echo "error: close: Git repository changed while archiving; first snapshot retained at $destination and quarantine kept" >&2
    return 1
  fi
  if ! mkdir -p "$destination" ||
    ! mv "$all_before" "$destination/all-objects.txt" ||
    ! mv "$primary_objects" "$destination/primary-reachable-objects.txt" ||
    ! mv "$missing_objects" "$destination/unique-object-ids.txt" ||
    ! mv "$metadata_before" "$destination/git-metadata.tar"; then
    rm -f "$all_before" "$all_after" "$primary_objects" "$missing_objects" \
      "$metadata_before" "$metadata_after" "$unique_pack" "$unique_index"
    rmdir "$unique_dir" >/dev/null 2>&1 || true
    echo "error: close: could not publish Git repository recovery metadata; keeping quarantine" >&2
    return 1
  fi
  if [ -f "$unique_pack" ]; then
    if ! mv "$unique_pack" "$destination/pack-$pack_hash.pack" ||
      ! mv "$unique_index" "$destination/pack-$pack_hash.idx" ||
      ! printf '%s\n' "$pack_hash" >"$destination/unique-objects.pack-hash"; then
      rm -f "$all_after" "$metadata_after" "$unique_pack" "$unique_index"
      rmdir "$unique_dir" >/dev/null 2>&1 || true
      echo "error: close: could not publish the verified unique-object pack; keeping quarantine" >&2
      return 1
    fi
  fi
  rm -f "$all_after" "$metadata_after"
  rmdir "$unique_dir" >/dev/null 2>&1 || true
  if ! git -C "$path" for-each-ref --format='%(refname)%09%(objectname)' >"$destination/refs.tsv" ||
    ! git -C "$path" reflog show --all --date=iso-strict --format='%H%x09%gD%x09%gs' >"$destination/reflog.tsv" ||
    ! git -C "$path" rev-parse HEAD >"$destination/HEAD"; then
    echo "error: close: could not publish Git ref/reflog ownership metadata; keeping quarantine" >&2
    return 1
  fi
  echo "close: preserved complete Git metadata and unique objects at $destination" >&2
}

quarantine_activity() {
  local path="$1" lsof_bin output status stderr_file stderr_output
  lsof_bin="$(command -v lsof 2>/dev/null || true)"
  if [ -z "$lsof_bin" ]; then
    return 2
  fi
  stderr_file="$(mktemp "${TMPDIR:-/tmp}/kitsoki-close-lsof.XXXXXX")"
  set +e
  output="$("$lsof_bin" -n -P -t +D "$path" 2>"$stderr_file")"
  status=$?
  set -e
  stderr_output="$(cat "$stderr_file")"
  rm -f "$stderr_file"
  if [ -n "$output" ]; then
    printf '%s\n' "$output"
    return 1
  fi
  if [ -n "$stderr_output" ]; then
    return 2
  fi
  # lsof exits 1 when no files match. Any larger status is inconclusive and
  # must fail closed rather than authorize deletion.
  case "$status" in
    0|1) return 0 ;;
    *) return 2 ;;
  esac
}

rewrite_workspace_identity() {
  local path="$1" id="$2" root="$3"
  python3 - "$path" "$id" "$root" <<'PY'
import json
import os
import sys

path, workspace_id, root = sys.argv[1:]
for name in (".kitsoki-clone", ".kitsoki-dev-workspace.json", "capsule-manifest.json"):
    manifest_path = os.path.join(path, name)
    with open(manifest_path, encoding="utf-8") as f:
        data = json.load(f)
    if name == "capsule-manifest.json":
        data["workspace"] = path
        data["environment"]["id"] = workspace_id
        data["environment"]["root"] = root
    else:
        data["id"] = workspace_id
        data["root"] = root
        if name == ".kitsoki-dev-workspace.json":
            data["workspace"] = path
            data["capsule_manifest"] = os.path.join(path, "capsule-manifest.json")
    temp = manifest_path + ".tmp"
    with open(temp, "w", encoding="utf-8") as f:
        json.dump(data, f, indent=2, sort_keys=True)
        f.write("\n")
    os.replace(temp, manifest_path)
PY
}

# A local clone may share objects with its source repository. Before using a
# ref fetched after workspace creation, make the failure mode explicit here
# rather than letting rebase (or a later primary-checkout fetch) discover a
# missing object halfway through a landing operation.
verify_commit_objects() {
  local path="$1"
  local ref="$2"
  local context="$3"
  local commit
  commit="$(git -C "$path" rev-parse --verify "${ref}^{commit}" 2>/dev/null)" || die "$context: cannot resolve commit $ref in $path"
  git -C "$path" cat-file -e "${commit}^{tree}" 2>/dev/null || die "$context: cannot read tree for $ref in $path"
  if ! git -C "$path" rev-list --objects "$commit" | git -C "$path" cat-file --batch-check='%(objectname) %(objecttype)' >/dev/null; then
    die "$context: missing or unreadable objects for $ref in $path"
  fi
}

verify_clean_readable_checkout() {
  local path="$1"
  local ref="$2"
  local context="$3"
  local status
  verify_commit_objects "$path" "$ref" "$context"
  git -C "$path" diff --quiet || die "$context: checkout has unstaged changes"
  git -C "$path" diff --cached --quiet || die "$context: checkout has staged changes"
  if ! status="$(git -C "$path" status --porcelain --untracked-files=all)"; then
    die "$context: could not inspect checkout status"
  fi
  [ -z "$status" ] || die "$context: checkout is not clean"
}

expected_tree_after_delta() {
  local path="$1" old_base="$2" snapshot="$3" new_base="$4" tree
  tree="$(git -C "$path" merge-tree --write-tree --no-messages \
    --merge-base "$old_base" "$new_base" "$snapshot")" || return 1
  printf '%s\n' "$tree"
}

refuse_untracked_tree_collisions() {
  local path="$1" from_ref="$2" to_ref="$3" context="$4" output
  if ! output="$(python3 - "$path" "$from_ref" "$to_ref" <<'PY'
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
changed = {
    p.decode("utf-8", "surrogateescape")
    for p in changed_run.stdout.split(b"\0") if p
}
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
    echo "error: $context would overwrite untracked or ignored workspace paths:" >&2
    [ -z "$output" ] || printf '%s\n' "$output" >&2
    return 1
  fi
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

  if initialization_state "$root" "$id" >/dev/null; then
    initialization_refusal create "$root" "$id"
  fi
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
      if [ "$json" = "1" ]; then
        bootstrap_workspace "$path" "$repo" >&2
      else
        bootstrap_workspace "$path" "$repo"
      fi
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
  acquire_initialization_lock "$root" "$id" "$path" "$branch" "$session_id"
  local source_commit source_ref
  source_ref="${base:-HEAD}"
  # An MCP-managed workspace can itself be a clone-backed agent capsule. Such
  # a capsule carries staging/local as source/staging/local rather than a local
  # branch, so resolve that tracked base before refusing creation. This keeps
  # nested managed workspaces on the declared staging base instead of silently
  # falling back to main.
  if ! git -C "$repo" rev-parse --verify --quiet "${source_ref}^{commit}" >/dev/null; then
    if [ -n "$base" ] && git -C "$repo" rev-parse --verify --quiet "source/${base}^{commit}" >/dev/null; then
	  # A local clone advertises local heads, not another clone's remote-tracking
	  # refs. Materialize the declared staging base in this managed parent before
	  # cloning so its child receives the same immutable base and can later merge
	  # back to that parent staging ref.
	  git -C "$repo" branch --quiet "$base" "source/$base" 2>/dev/null ||
	    git -C "$repo" rev-parse --verify --quiet "${base}^{commit}" >/dev/null ||
	    die "create: could not materialize tracked base $base"
      source_ref="source/$base"
    else
      die "create: base ref is not a commit: ${base:-HEAD}"
    fi
  fi
  source_commit="$(git -C "$repo" rev-parse "$source_ref")"
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
  release_initialization_lock

  if [ "$bootstrap" = "1" ]; then
    # ManagedWorkspaceService consumes --json as a machine protocol. Bootstrap
    # remains observable, but its progress must not corrupt that JSON response.
    if [ "$json" = "1" ]; then
      bootstrap_workspace "$path" "$repo" >&2
    else
      bootstrap_workspace "$path" "$repo"
    fi
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
  local id state marker pid initialized_path
  id="$(basename "$path")"
  if state="$(initialization_state "$root" "$id")"; then
    marker="$(initialization_marker "$root" "$id")"
    pid="$(initialization_metadata_value "$marker" pid)"
    initialized_path="$(initialization_metadata_value "$marker" path)"
    if [ "$json" = "1" ]; then
      python3 - "$id" "${initialized_path:-$path}" "$state" "$pid" <<'PY'
import json
import sys
ws_id, path, state, pid = sys.argv[1:]
print(json.dumps({
    "ok": False,
    "id": ws_id,
    "path": path,
    "state": "initializing",
    "initialization": state,
    "pid": int(pid) if pid.isdigit() else None,
}, indent=2, sort_keys=True))
PY
    else
      echo "workspace: ${initialized_path:-$path}"
      echo "state: initializing ($state)"
      [ -n "$pid" ] && echo "pid: $pid"
    fi
    return 2
  fi
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
  ensure_not_initializing commit "$root" "$path"
  ensure_managed_workspace "$path"
  if ! workspace_dirty "$path"; then
    die "commit: workspace has no changes"
  fi
  git -C "$path" add -A
  git -C "$path" commit --signoff -m "$message"
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
  local replace_landing=0
  while [ "$#" -gt 0 ]; do
    case "$1" in
      --repo) repo="${2:?--repo requires a value}"; shift 2 ;;
      --root) root="${2:?--root requires a value}"; shift 2 ;;
      --branch) branch="${2:?--branch requires a value}"; shift 2 ;;
      --target|--onto) target="${2:?--target requires a value}"; shift 2 ;;
      --gate) gate="${2:?--gate requires a value}"; shift 2 ;;
      --teardown) teardown=1; shift ;;
      --replace-landing) replace_landing=1; shift ;;
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
  ensure_not_initializing merge "$root" "$path"
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

  # Kitsoki's staging target has a fast deterministic repository gate. Keep it
  # on the normal landing path while leaving generic disposable-repo tests and
  # explicitly supplied gates unchanged.
  if [ -z "$gate" ] && [ "$target" = "$DEFAULT_TARGET" ] && [ -f "$repo/Makefile" ] &&
    make -C "$repo" -n capsule-ci-quick >/dev/null 2>&1; then
    gate="make capsule-ci-quick"
    echo "merge: using default staging gate: $gate" >&2
  fi

  local target_base target_existed=0
  if git -C "$repo" rev-parse --verify --quiet "refs/heads/$target" >/dev/null; then
    target_base="$target"
    target_existed=1
  elif git -C "$path" rev-parse --verify --quiet "source/$target^{commit}" >/dev/null; then
    # Agent capsules track their target under source/<target> until the first
    # local merge. Preserve that target rather than rebasing a nested workspace
    # onto main merely because refs/heads/<target> is intentionally absent.
    target_base="$target"
  else
    target_base="main"
  fi
  # Always refresh from the source before rebasing: the target may have moved
  # after this clone-backed capsule was created. Verify the complete fetched
  # graph in the capsule before rebase so shared-object problems fail before
  # any worktree mutation.
  local workspace_original workspace_delta_base expected_workspace_tree workspace_recovery_ref
  workspace_original="$(git -C "$path" rev-parse --verify HEAD)"
  workspace_recovery_ref="refs/kitsoki/workspace-merge-recovery/$workspace_original"
  if git -C "$repo" rev-parse --verify --quiet "$workspace_recovery_ref" >/dev/null; then
    [ "$(git -C "$repo" rev-parse "$workspace_recovery_ref")" = "$workspace_original" ] ||
      die "merge: content-addressed workspace recovery ref points at the wrong object"
  else
    git -C "$repo" fetch --no-tags "$path" "$workspace_original:$workspace_recovery_ref"
  fi
  git -C "$path" fetch --no-tags source "+refs/heads/$target_base:refs/remotes/source/$target_base"
  verify_commit_objects "$path" "source/$target_base" "merge: fetched target"
  verify_commit_objects "$path" HEAD "merge: workspace branch before rebase"
  local target_snapshot
  target_snapshot="$(git -C "$path" rev-parse --verify "source/$target_base")"
  if [ "$target_existed" = "1" ]; then
    [ "$(git -C "$repo" rev-parse --verify "refs/heads/$target")" = "$target_snapshot" ] ||
      die "merge: target $target advanced while fetching; rerun merge"
  elif git -C "$repo" rev-parse --verify --quiet "refs/heads/$target" >/dev/null; then
    die "merge: target $target was created while fetching; rerun merge"
  fi
  workspace_delta_base="$(git -C "$path" merge-base "$workspace_original" "$target_snapshot")" ||
    die "merge: could not determine workspace/target merge base"
  expected_workspace_tree="$(expected_tree_after_delta \
    "$path" "$workspace_delta_base" "$workspace_original" "$target_snapshot")" ||
    die "merge: could not construct expected rebased workspace tree"
  refuse_untracked_tree_collisions \
    "$path" "$workspace_original" "$expected_workspace_tree" "workspace rebase" ||
    die "merge: move or preserve the reported local paths before retrying"
  git -C "$path" rebase "source/$target_base"
  verify_clean_readable_checkout "$path" HEAD "merge: rebased workspace"
  local workspace_result
  workspace_result="$(git -C "$path" rev-parse --verify HEAD)"
  if [ "$(git -C "$path" rev-parse "$workspace_result^{tree}")" != "$expected_workspace_tree" ]; then
    git -C "$path" update-ref "refs/kitsoki/workspace-merge-failed/$workspace_result" "$workspace_result" || true
    if ! workspace_dirty "$path"; then
      git -C "$path" update-ref "refs/heads/$branch" "$workspace_original" "$workspace_result" || true
      git -c submodule.recurse=false -C "$path" reset --hard "$workspace_original" >/dev/null 2>&1 || true
    fi
    die "merge: rebased workspace tree differs from the expected preserved tree; original retained at $workspace_recovery_ref"
  fi
  if [ -n "$gate" ]; then
    (cd "$path" && sh -c "$gate")
  fi
  verify_clean_readable_checkout "$path" HEAD "merge: workspace after gate"
  [ "$(git -C "$path" rev-parse --verify HEAD)" = "$workspace_result" ] ||
    die "merge: validation gate moved workspace HEAD; committed state must remain unchanged"
  [ "$(git -C "$path" rev-parse --verify "refs/heads/$branch")" = "$workspace_result" ] ||
    die "merge: workspace branch advanced during validation; refusing unproven work"

  if [ "$target" = "main" ]; then
    local changed_files_file
    changed_files_file="$(mktemp "${TMPDIR:-/tmp}/kitsoki-dev-workspace-files.XXXXXX")"
    git -C "$path" diff --name-only "source/$target_base" "$workspace_result" >"$changed_files_file"
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
    if git -C "$repo" merge-base --is-ancestor "$landing_branch" "$target_snapshot"; then
      git -C "$repo" branch -D "$landing_branch" >/dev/null
    elif [ "$replace_landing" = "1" ]; then
      # A previous attempt may have created the transport ref and then lost a
      # race while rebasing the managed workspace onto an advanced target. The
      # workspace branch, freshly rebased and gated above, is authoritative;
      # explicitly replacing this disposable transport ref is safe and avoids
      # stranding an otherwise clean workspace.
      git -C "$repo" branch -D "$landing_branch" >/dev/null
    else
      die "merge: landing branch already exists and is not merged: $landing_branch (rerun with --replace-landing after reviewing the workspace)"
    fi
  fi
  git -C "$repo" fetch --no-tags "$path" "$workspace_result:refs/heads/$landing_branch"
  [ "$(git -C "$path" rev-parse --verify "refs/heads/$branch")" = "$workspace_result" ] ||
    die "merge: workspace branch advanced while importing the proven result"
  verify_commit_objects "$path" "$workspace_result" "merge: landing source"
  verify_commit_objects "$repo" "$landing_branch" "merge: landing branch"
  local landed=0
  if [ "$target" = "main" ]; then
    local -a merge_args
    merge_args=("$branch" "--source-dir" "$path")
    if [ -n "$gate" ]; then
      merge_args+=("--gate" "$gate")
    fi
    set +e
    (cd "$repo" && scripts/merge-to-main.sh "${merge_args[@]}")
    local main_merge_status=$?
    set -e
    if [ "$main_merge_status" -ne 0 ]; then
      if git -C "$repo" merge-base --is-ancestor "$landing_branch" "refs/heads/main"; then
        echo "warning: merge-to-main exited $main_merge_status after main advanced; treating landing as successful" >&2
      else
        return "$main_merge_status"
      fi
    fi
    landed=1
  else
    if ! git -C "$repo" merge-base --is-ancestor "$target_snapshot" "$landing_branch"; then
      die "merge: $landing_branch is not a fast-forward of captured $target_base"
    fi
    local old_target new_target
    new_target="$(git -C "$repo" rev-parse --verify "$landing_branch")"
    if [ "$target_existed" = "1" ]; then
      old_target="$target_snapshot"
      git -C "$repo" update-ref -m "dev-workspace merge $branch into $target" \
        "refs/heads/$target" "$new_target" "$old_target" ||
        die "merge: target $target advanced before the atomic landing; rerun merge"
    else
      git -C "$repo" update-ref -m "dev-workspace create $target from $branch" \
        "refs/heads/$target" "$new_target" "" ||
        die "merge: target $target was created before the atomic landing; rerun merge"
    fi
    echo "$target -> $(git -C "$repo" rev-parse --short "$target")"
    landed=1
  fi

  # From this point the target already contains the work. Cleanup is useful,
  # but must not turn a successful landing into a reported failure.
  git -C "$repo" update-ref -d "$workspace_recovery_ref" >/dev/null 2>&1 || true
  if ! git -C "$repo" branch -D "$landing_branch" >/dev/null; then
    echo "warning: merge landed but could not remove temporary branch $landing_branch" >&2
  fi

  # Keep Kitsoki's long-lived staging capsule at the landed ref. Consumer
  # targets still refresh defensively, but a successful branch landing should
  # not leave the staging checkout knowingly stale. Generic repositories and
  # projects without the managed staging capsule are unaffected.
  local staging_refresh_script="$repo/scripts/refresh-staging-local.sh"
  if [ -x "$path/scripts/refresh-staging-local.sh" ]; then
    # The landed staging workspace can contain the refresh fix itself while
    # protected main intentionally remains older. Use the immutable landed
    # source, not the stale primary working tree, for post-landing convergence.
    staging_refresh_script="$path/scripts/refresh-staging-local.sh"
  fi
  if [ "$target" = "$DEFAULT_TARGET" ] &&
    [ -d "$repo/.capsules/staging/local/.git" ] &&
    [ -x "$staging_refresh_script" ]; then
    echo "merge: refreshing managed staging capsule after landing" >&2
    (cd "$repo" && "$staging_refresh_script" \
      --skip-remote \
      --staging-branch "$target" \
      --staging-capsule "$repo/.capsules/staging/local" \
      --gate "git diff --check") ||
      die "merge: staging/local landed but its managed capsule could not be refreshed"
  fi

  if [ "$teardown" = "1" ]; then
    local current_workspace_tip
    current_workspace_tip="$(git -C "$path" rev-parse --verify "refs/heads/$branch")"
    if ! git -C "$repo" merge-base --is-ancestor "$current_workspace_tip" "refs/heads/$target"; then
      echo "warning: merge landed, but workspace branch advanced to unlanded $current_workspace_tip; keeping $path" >&2
    elif ! cmd_close --repo "$repo" --root "$root" \
      --expected-branch "$branch" --expected-tip "$current_workspace_tip" "$path"; then
      echo "warning: merge landed but teardown failed for $path" >&2
    fi
  fi
  [ "$landed" = "1" ] || die "merge: target did not advance"
  echo "merged: $branch -> $target"
}

cmd_park() {
  local repo="."
  local root=""
  local ref=""
  local session_id=""
  local message=""
  local json=0
  while [ "$#" -gt 0 ]; do
    case "$1" in
      --repo) repo="${2:?--repo requires a value}"; shift 2 ;;
      --root) root="${2:?--root requires a value}"; shift 2 ;;
      --session-id|--session_id) session_id="${2:?--session-id requires a value}"; shift 2 ;;
      --message|-m) message="${2:?--message requires a value}"; shift 2 ;;
      --json) json=1; shift ;;
      -h|--help) usage; exit 0 ;;
      *)
        [ -z "$ref" ] || die "park: unexpected argument: $1"
        ref="$1"
        shift
        ;;
    esac
  done
  [ -n "$ref" ] || die "park: workspace path or id is required"
  repo="$(repo_root "$repo")"
  root="$(resolve_root "$repo" "$root")"
  local path
  path="$(workspace_path "$repo" "$root" "$ref")"
  local abs_path current_branch target base source_id stamp recovery_id recovery_branch recovery_workspace script_path
  abs_path="$(abs_path "$path")"
  if ! git -C "$abs_path" rev-parse --is-inside-work-tree >/dev/null 2>&1; then
    if [ "$json" = "1" ]; then
      python3 - "$abs_path" <<'PY'
import json
import sys
print(json.dumps({
    "ok": True,
    "parked": False,
    "source_workspace": sys.argv[1],
    "reason": "not a git checkout",
}, indent=2, sort_keys=True))
PY
    else
      echo "parked: false"
      echo "reason: not a git checkout"
    fi
    return 0
  fi
  ensure_not_initializing park "$root" "$abs_path"
  ensure_managed_workspace "$abs_path"
  ensure_under_root_or_forced "$root" "$abs_path" 0
  if ! workspace_dirty "$abs_path"; then
    if [ "$json" = "1" ]; then
      python3 - "$abs_path" <<'PY'
import json
import sys
print(json.dumps({
    "ok": True,
    "parked": False,
    "source_workspace": sys.argv[1],
    "reason": "workspace is clean",
}, indent=2, sort_keys=True))
PY
    else
      echo "parked: false"
      echo "reason: workspace is clean"
    fi
    return 0
  fi

  root="$(manifest_value "$abs_path" root "$root")"
  current_branch="$(git -C "$abs_path" branch --show-current 2>/dev/null || true)"
  [ -n "$current_branch" ] || die "park: source workspace must be on a branch"
  base="$(manifest_value "$abs_path" base "${current_branch:-HEAD}")"
  target="$(manifest_value "$abs_path" target "$DEFAULT_TARGET")"
  source_id="$(workspace_id_from_path "$abs_path")"
  [ -n "$source_id" ] || source_id="$(basename "$abs_path")"
  stamp="$(date +%Y%m%dT%H%M%S)"
  recovery_id="$(safe_ref_fragment "${source_id}-park-${stamp}-$$")"
  recovery_branch="agent/$recovery_id"
  recovery_workspace="$root/$recovery_id"
  script_path="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/$(basename "${BASH_SOURCE[0]}")"
  local untracked_file untracked_tar current_untracked_file current_untracked_tar
  local create_json recovery_commit park_message snapshot_oid snapshot_tree snapshot_index_tree
  local current_oid current_tree current_index_tree recovery_parent recovery_snapshot_ref expected_tree
  local primary_snapshot_ref primary_recovery_ref quarantine_root quarantine_path replacement_bootstrap
  untracked_file="$(mktemp "${TMPDIR:-/tmp}/kitsoki-park-untracked.XXXXXX")"
  untracked_tar="$(mktemp "${TMPDIR:-/tmp}/kitsoki-park-untracked-tar.XXXXXX")"
  current_untracked_file="$(mktemp "${TMPDIR:-/tmp}/kitsoki-park-current-untracked.XXXXXX")"
  current_untracked_tar="$(mktemp "${TMPDIR:-/tmp}/kitsoki-park-current-untracked-tar.XXXXXX")"
  park_message="${message:-chore: park rerouted work from $source_id}"

  snapshot_oid="$(git -C "$abs_path" stash create "kitsoki park snapshot $stamp")"
  [ -n "$snapshot_oid" ] || snapshot_oid="$(git -C "$abs_path" rev-parse HEAD)"
  snapshot_tree="$(git -C "$abs_path" rev-parse "$snapshot_oid^{tree}")"
  snapshot_index_tree="$(git -C "$abs_path" rev-parse "$snapshot_oid^2^{tree}" 2>/dev/null || git -C "$abs_path" rev-parse HEAD^{tree})"
  git -C "$abs_path" ls-files --others --exclude-standard -z >"$untracked_file"
  if [ -s "$untracked_file" ]; then
    tar -C "$abs_path" --null --files-from="$untracked_file" -cf "$untracked_tar"
  fi

  if ! create_json="$("$script_path" create --repo "$repo" --root "$root" --id "$recovery_id" --branch "$recovery_branch" --base "$base" --target "$target" --session-id "$session_id" --json --no-bootstrap 2>/dev/null)"; then
    rm -f "$untracked_file" "$untracked_tar" "$current_untracked_file" "$current_untracked_tar"
    die "park: could not create recovery capsule"
  fi
  recovery_workspace="$(python3 -c 'import json,sys; print(json.load(sys.stdin)["path"])' <<<"$create_json")"
  recovery_parent="$(git -C "$recovery_workspace" rev-parse HEAD)"
  recovery_snapshot_ref="refs/kitsoki/dirty-snapshot/$snapshot_oid"
  git -C "$recovery_workspace" fetch --no-tags "$abs_path" "$snapshot_oid:$recovery_snapshot_ref"
  git -C "$recovery_workspace" reset --hard "$recovery_snapshot_ref" >/dev/null
  if [ -s "$untracked_file" ]; then
    tar -C "$recovery_workspace" -xf "$untracked_tar"
  fi
  git -C "$recovery_workspace" add -A
  expected_tree="$(git -C "$recovery_workspace" write-tree)"
  git -C "$recovery_workspace" reset --soft "$recovery_parent"
  if ! git -C "$recovery_workspace" commit --allow-empty --signoff -m "$park_message" >/dev/null; then
    rm -f "$untracked_file" "$untracked_tar" "$current_untracked_file" "$current_untracked_tar"
    die "park: could not commit recovery capsule"
  fi
  recovery_commit="$(git -C "$recovery_workspace" rev-parse HEAD)"
  [ "$(git -C "$recovery_workspace" rev-parse HEAD^{tree})" = "$expected_tree" ] ||
    die "park: committed recovery tree does not match the captured dirty snapshot"
  primary_snapshot_ref="refs/kitsoki/dirty-snapshot/$snapshot_oid"
  primary_recovery_ref="refs/kitsoki/dirty-recovery/$recovery_commit"
  git -C "$repo" fetch --no-tags "$abs_path" "$snapshot_oid:$primary_snapshot_ref"
  git -C "$repo" fetch --no-tags "$recovery_workspace" "$recovery_commit:$primary_recovery_ref"
  [ "$(git -C "$repo" rev-parse "$primary_snapshot_ref")" = "$snapshot_oid" ] ||
    die "park: original staged/worktree snapshot was not anchored in the primary repository"
  [ "$(git -C "$repo" rev-parse "$primary_recovery_ref^{tree}")" = "$expected_tree" ] ||
    die "park: signed recovery tree was not anchored in the primary repository"
  git -C "$recovery_workspace" update-ref -d "$recovery_snapshot_ref" >/dev/null 2>&1 || true

  current_oid="$(git -C "$abs_path" stash create "kitsoki park snapshot recheck $stamp")"
  [ -n "$current_oid" ] || current_oid="$(git -C "$abs_path" rev-parse HEAD)"
  current_tree="$(git -C "$abs_path" rev-parse "$current_oid^{tree}")"
  current_index_tree="$(git -C "$abs_path" rev-parse "$current_oid^2^{tree}" 2>/dev/null || git -C "$abs_path" rev-parse HEAD^{tree})"
  git -C "$abs_path" ls-files --others --exclude-standard -z >"$current_untracked_file"
  if [ -s "$current_untracked_file" ]; then
    tar -C "$abs_path" --null --files-from="$current_untracked_file" -cf "$current_untracked_tar"
  fi
  if [ "$current_tree" != "$snapshot_tree" ] ||
    [ "$current_index_tree" != "$snapshot_index_tree" ] ||
    ! cmp -s "$untracked_file" "$current_untracked_file" ||
    ! cmp -s "$untracked_tar" "$current_untracked_tar"; then
    die "park: source changed while its recovery snapshot was being committed; original left unchanged"
  fi
  quarantine_root="$repo/.capsules/workspaces"
  mkdir -p "$quarantine_root"
  if ! same_device "$(dirname "$abs_path")" "$quarantine_root"; then
    die "park: source root is on a different filesystem from the managed quarantine root; recovery is anchored but the source was left unchanged"
  fi
  quarantine_path="$quarantine_root/closed-recovered-$recovery_id-source"
  [ ! -e "$quarantine_path" ] || die "park: source quarantine already exists: $quarantine_path"
  mv "$abs_path" "$quarantine_path"
  if ! rewrite_workspace_identity "$quarantine_path" "$(basename "$quarantine_path")" "$quarantine_root"; then
    [ ! -e "$abs_path" ] && mv "$quarantine_path" "$abs_path" >/dev/null 2>&1 || true
    die "park: could not record managed recovery-quarantine identity"
  fi
  replacement_bootstrap="--no-bootstrap"
  if make -C "$repo" -n bootstrap-workspace >/dev/null 2>&1 ||
    make -C "$repo" -n bootstrap-worktree >/dev/null 2>&1; then
    replacement_bootstrap="--bootstrap"
  fi
  if ! "$script_path" create \
    --repo "$repo" \
    --root "$root" \
    --id "$source_id" \
    --branch "$current_branch" \
    --base "$base" \
    --target "$target" \
    --session-id "$session_id" \
    "$replacement_bootstrap" >/dev/null; then
    if [ ! -e "$abs_path" ]; then
      rewrite_workspace_identity "$quarantine_path" "$source_id" "$root" >/dev/null 2>&1 || true
      mv "$quarantine_path" "$abs_path" >/dev/null 2>&1 || true
    fi
    die "park: recovery is anchored, but a clean source workspace could not be recreated"
  fi
  if ! "$script_path" seal-recovered-quarantine \
    --repo "$repo" \
    --root "$quarantine_root" \
    --snapshot-ref "$primary_snapshot_ref" \
    --recovery-ref "$primary_recovery_ref" \
    "$quarantine_path" >/dev/null; then
    die "park: clean source was recreated and recovery refs are anchored, but the source quarantine could not be sealed"
  fi

  rm -f "$untracked_file" "$untracked_tar" "$current_untracked_file" "$current_untracked_tar"
  if [ "$json" = "1" ]; then
    python3 - "$abs_path" "$recovery_workspace" "$recovery_branch" "$recovery_commit" "$park_message" "$quarantine_path" "$primary_snapshot_ref" "$primary_recovery_ref" <<'PY'
import json
import sys
source_workspace, recovery_workspace, recovery_branch, recovery_commit, reason, quarantine, snapshot_ref, recovery_ref = sys.argv[1:]
print(json.dumps({
    "ok": True,
    "parked": True,
    "source_workspace": source_workspace,
    "recovery_workspace": recovery_workspace,
    "recovery_branch": recovery_branch,
    "recovery_commit": recovery_commit,
    "source_quarantine": quarantine,
    "snapshot_ref": snapshot_ref,
    "recovery_ref": recovery_ref,
    "cleaned": True,
    "reason": reason,
}, indent=2, sort_keys=True))
PY
  else
    echo "parked: true"
    echo "source: $abs_path"
    echo "recovery: $recovery_workspace"
    echo "branch: $recovery_branch"
    echo "commit: $recovery_commit"
    echo "source quarantine: $quarantine_path"
    echo "snapshot ref: $primary_snapshot_ref"
    echo "recovery ref: $primary_recovery_ref"
  fi
}

cmd_seal_recovered_quarantine() {
  local repo="."
  local root=""
  local ref=""
  local snapshot_ref=""
  local recovery_ref=""
  while [ "$#" -gt 0 ]; do
    case "$1" in
      --repo) repo="${2:?--repo requires a value}"; shift 2 ;;
      --root) root="${2:?--root requires a value}"; shift 2 ;;
      --snapshot-ref) snapshot_ref="${2:?--snapshot-ref requires a value}"; shift 2 ;;
      --recovery-ref) recovery_ref="${2:?--recovery-ref requires a value}"; shift 2 ;;
      -h|--help) usage; exit 0 ;;
      *)
        [ -z "$ref" ] || die "seal-recovered-quarantine: unexpected argument: $1"
        ref="$1"
        shift
        ;;
    esac
  done
  [ -n "$ref" ] || die "seal-recovered-quarantine: workspace path is required"
  [ -n "$snapshot_ref" ] || die "seal-recovered-quarantine: --snapshot-ref is required"
  [ -n "$recovery_ref" ] || die "seal-recovered-quarantine: --recovery-ref is required"
  case "$snapshot_ref" in refs/kitsoki/dirty-snapshot/*) ;; *) die "seal-recovered-quarantine: invalid snapshot ref" ;; esac
  case "$recovery_ref" in refs/kitsoki/dirty-recovery/*) ;; *) die "seal-recovered-quarantine: invalid recovery ref" ;; esac
  repo="$(repo_root "$repo")"
  root="$(resolve_root "$repo" "$root")"
  local path id branch head tree tip_ref recovery_tree marker status
  local snapshot_commit recovery_commit
  path="$(workspace_path "$repo" "$root" "$ref")"
  id="$(basename "$path")"
  case "$id" in closed-recovered-*) ;; *) die "seal-recovered-quarantine: path must use a closed-recovered-* id" ;; esac
  ensure_managed_workspace "$path"
  ensure_under_root_or_forced "$root" "$path" 0
  rewrite_workspace_identity "$path" "$id" "$root" ||
    die "seal-recovered-quarantine: could not record managed quarantine identity"
  snapshot_commit="$(git -C "$repo" rev-parse --verify "$snapshot_ref^{commit}" 2>/dev/null || true)"
  recovery_commit="$(git -C "$repo" rev-parse --verify "$recovery_ref^{commit}" 2>/dev/null || true)"
  is_object_id "$snapshot_commit" && [ "${snapshot_ref##*/}" = "$snapshot_commit" ] ||
    die "seal-recovered-quarantine: primary snapshot ref is missing or unreadable"
  is_object_id "$recovery_commit" && [ "${recovery_ref##*/}" = "$recovery_commit" ] ||
    die "seal-recovered-quarantine: primary recovery ref is missing or unreadable"
  recovery_tree="$(git -C "$repo" rev-parse "$recovery_ref^{tree}")"
  write_git_excludes "$path"
  branch="$(git -C "$path" branch --show-current)"
  [ -n "$branch" ] || die "seal-recovered-quarantine: workspace must be on a branch"
  git -C "$path" add -A
  git -C "$path" commit --allow-empty --no-verify --signoff \
    -m "chore: seal recovered workspace quarantine" >/dev/null
  head="$(git -C "$path" rev-parse HEAD)"
  tree="$(git -C "$path" rev-parse HEAD^{tree})"
  tip_ref="refs/kitsoki/recovered-quarantine/$head"
  if git -C "$repo" rev-parse --verify --quiet "$tip_ref" >/dev/null; then
    [ "$(git -C "$repo" rev-parse "$tip_ref")" = "$head" ] ||
      die "seal-recovered-quarantine: content-addressed tip ref points at the wrong object"
  else
    git -C "$repo" fetch --no-tags "$path" "$head:$tip_ref"
  fi
  marker="$path/$RECOVERED_QUARANTINE_MANIFEST"
  python3 - "$marker" "$snapshot_ref" "$recovery_ref" "$recovery_tree" "$tip_ref" "$head" "$tree" <<'PY'
import json
import os
import sys
from datetime import datetime, timezone

path, snapshot_ref, recovery_ref, recovery_tree, tip_ref, head, tree = sys.argv[1:]
payload = {
    "schema": "kitsoki.recovered-quarantine/v1",
    "snapshot_ref": snapshot_ref,
    "recovery_ref": recovery_ref,
    "recovery_tree": recovery_tree,
    "tip_ref": tip_ref,
    "head": head,
    "tree": tree,
    "sealed_at": datetime.now(timezone.utc).replace(microsecond=0).isoformat().replace("+00:00", "Z"),
}
temp = path + ".tmp"
with open(temp, "w", encoding="utf-8") as f:
    json.dump(payload, f, indent=2, sort_keys=True)
    f.write("\n")
os.replace(temp, path)
PY
  if ! status="$(git -C "$path" status --porcelain --untracked-files=all)"; then
    die "seal-recovered-quarantine: could not verify sealed status"
  fi
  if [ -n "$status" ]; then
    echo "warning: recovered quarantine changed while sealing and remains cleanup-ineligible: $path" >&2
  fi
  if [ "$tree" != "$recovery_tree" ]; then
    echo "error: recovered quarantine contains late work beyond the signed recovery tree; exact tip retained and quarantine left cleanup-ineligible: $path" >&2
    return 2
  fi
  echo "sealed recovered quarantine: $path"
  echo "quarantine tip ref: $tip_ref"
}

cmd_close() {
  local repo="."
  local root=""
  local force=0
  local ref=""
  local expected_branch=""
  local expected_tip=""
  local purge_quarantine=0
  while [ "$#" -gt 0 ]; do
    case "$1" in
      --repo) repo="${2:?--repo requires a value}"; shift 2 ;;
      --root) root="${2:?--root requires a value}"; shift 2 ;;
      --force) force=1; shift ;;
      --expected-branch) expected_branch="${2:?--expected-branch requires a value}"; shift 2 ;;
      --expected-tip) expected_tip="${2:?--expected-tip requires a value}"; shift 2 ;;
      --purge-quarantine) purge_quarantine=1; shift ;;
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
  ensure_not_initializing close "$root" "$path"
  ensure_managed_workspace "$path"
  ensure_under_root_or_forced "$root" "$path" "$force"
  if [ -z "$expected_branch" ] && [ -z "$expected_tip" ]; then
    expected_branch="$(git -C "$path" branch --show-current)"
    [ -n "$expected_branch" ] || {
      echo "error: close: workspace must be on a branch" >&2
      return 1
    }
    expected_tip="$(git -C "$path" rev-parse --verify "refs/heads/$expected_branch")"
  fi
  if [ -n "$expected_branch" ] || [ -n "$expected_tip" ]; then
    if [ -z "$expected_branch" ] || [ -z "$expected_tip" ]; then
      echo "error: close: --expected-branch and --expected-tip must be supplied together" >&2
      return 1
    fi
    if [ "$(git -C "$path" branch --show-current)" != "$expected_branch" ]; then
      echo "error: close: workspace branch changed before teardown; keeping workspace: $path" >&2
      return 1
    fi
    if [ "$(git -C "$path" rev-parse --verify "refs/heads/$expected_branch")" != "$expected_tip" ]; then
      echo "error: close: workspace tip changed before teardown; keeping workspace: $path" >&2
      return 1
    fi
  fi
  if ! import_workspace_issues "$path" "$repo"; then
    echo "error: close: workspace tickets were not safely preserved; keeping workspace: $path" >&2
    return 1
  fi
  if [ "$force" != "1" ] && workspace_dirty "$path"; then
    die "close: workspace has uncommitted changes: $path"
  fi
  local workspace_close_id
  workspace_close_id="$(workspace_id_from_path "$path")"
  preserve_workspace_review_artifacts "$path" "$repo" "$workspace_close_id" || return 1
  if [ "$purge_quarantine" = "1" ]; then
    case "$(basename "$path")" in
      closed-*) ;;
      *) echo "error: close: --purge-quarantine only accepts a closed-* managed workspace" >&2; return 1 ;;
    esac
    local purge_target recovered_marker snapshot_ref recovery_ref recovery_tree tip_ref marker_head marker_tree
    local snapshot_commit recovery_commit tip_commit
    local purge_path purge_id purge_status purge_branch purge_head original_quarantine_id
    local activity_pids activity_status
    purge_target="$(manifest_value "$path" target "$DEFAULT_TARGET")"
    recovered_marker="$path/$RECOVERED_QUARANTINE_MANIFEST"
    tip_ref=""
    if [ -f "$recovered_marker" ]; then
      case "$(basename "$path")" in
        closed-recovered-*) ;;
        *) echo "error: close: recovered-quarantine marker on unexpected path" >&2; return 1 ;;
      esac
      snapshot_ref="$(json_file_value "$recovered_marker" snapshot_ref "")"
      recovery_ref="$(json_file_value "$recovered_marker" recovery_ref "")"
      recovery_tree="$(json_file_value "$recovered_marker" recovery_tree "")"
      tip_ref="$(json_file_value "$recovered_marker" tip_ref "")"
      marker_head="$(json_file_value "$recovered_marker" head "")"
      marker_tree="$(json_file_value "$recovered_marker" tree "")"
      case "$snapshot_ref" in refs/kitsoki/dirty-snapshot/*) ;; *) echo "error: close: invalid recovered snapshot ref" >&2; return 1 ;; esac
      case "$recovery_ref" in refs/kitsoki/dirty-recovery/*) ;; *) echo "error: close: invalid recovered recovery ref" >&2; return 1 ;; esac
      case "$tip_ref" in refs/kitsoki/recovered-quarantine/*) ;; *) echo "error: close: invalid recovered tip ref" >&2; return 1 ;; esac
      [ "$marker_head" = "$expected_tip" ] || { echo "error: close: recovered quarantine HEAD changed" >&2; return 1; }
      [ "$(git -C "$path" rev-parse HEAD^{tree})" = "$marker_tree" ] || { echo "error: close: recovered quarantine tree changed" >&2; return 1; }
      [ "$marker_tree" = "$recovery_tree" ] || { echo "error: close: recovered quarantine contains work beyond the signed recovery tree; refusing purge" >&2; return 1; }
      snapshot_commit="$(git -C "$repo" rev-parse --verify "$snapshot_ref^{commit}" 2>/dev/null || true)"
      recovery_commit="$(git -C "$repo" rev-parse --verify "$recovery_ref^{commit}" 2>/dev/null || true)"
      tip_commit="$(git -C "$repo" rev-parse --verify "$tip_ref^{commit}" 2>/dev/null || true)"
      is_object_id "$snapshot_commit" && [ "${snapshot_ref##*/}" = "$snapshot_commit" ] || { echo "error: close: recovered snapshot ref is not exact and content-addressed" >&2; return 1; }
      is_object_id "$recovery_commit" && [ "${recovery_ref##*/}" = "$recovery_commit" ] || { echo "error: close: recovered recovery ref is not exact and content-addressed" >&2; return 1; }
      is_object_id "$tip_commit" && [ "${tip_ref##*/}" = "$tip_commit" ] || { echo "error: close: recovered quarantine tip ref is not exact and content-addressed" >&2; return 1; }
      [ "$(git -C "$repo" rev-parse "$recovery_ref^{tree}")" = "$recovery_tree" ] || { echo "error: close: recovered recovery tree changed" >&2; return 1; }
      [ "$tip_commit" = "$expected_tip" ] || { echo "error: close: recovered quarantine tip ref changed" >&2; return 1; }
    elif ! git -C "$repo" merge-base --is-ancestor "$expected_tip" "refs/heads/$purge_target"; then
      echo "error: close: quarantine tip is not contained in $purge_target; refusing purge" >&2
      return 1
    fi

    # Isolate the quarantine with a same-root atomic rename before the final
    # checks. A new path-based writer can no longer enter it; a writer that
    # changed it before the rename is caught below, while the hygiene provider
    # has already proven there was no process holding it open. Keep the isolated
    # path visible and managed so interruption can never strand hidden data.
    original_quarantine_id="$(basename "$path")"
    case "$original_quarantine_id" in
      closed-recovered-*) purge_id="closed-recovered-purging-$(safe_ref_fragment "$original_quarantine_id")-$$" ;;
      *) purge_id="closed-purging-$(safe_ref_fragment "$original_quarantine_id")-$$" ;;
    esac
    purge_path="$root/$purge_id"
    [ ! -e "$purge_path" ] || { echo "error: close: purge path already exists: $purge_path" >&2; return 1; }
    if ! mv "$path" "$purge_path"; then
      echo "error: close: could not atomically isolate quarantine for purge" >&2
      return 1
    fi
    if ! rewrite_workspace_identity "$purge_path" "$purge_id" "$root"; then
      rewrite_workspace_identity "$purge_path" "$original_quarantine_id" "$root" >/dev/null 2>&1 || true
      [ ! -e "$path" ] && mv "$purge_path" "$path" >/dev/null 2>&1 || true
      echo "error: close: could not keep isolated quarantine managed; restored when possible" >&2
      return 1
    fi
    purge_branch="$(git -C "$purge_path" branch --show-current 2>/dev/null || true)"
    purge_head="$(git -C "$purge_path" rev-parse --verify HEAD 2>/dev/null || true)"
    if ! purge_status="$(git -C "$purge_path" status --porcelain --untracked-files=all 2>/dev/null)" ||
      [ "$purge_branch" != "$expected_branch" ] || [ "$purge_head" != "$expected_tip" ] ||
      [ -n "$purge_status" ]; then
      rewrite_workspace_identity "$purge_path" "$original_quarantine_id" "$root" >/dev/null 2>&1 || true
      [ ! -e "$path" ] && mv "$purge_path" "$path" >/dev/null 2>&1 || true
      echo "error: close: quarantine changed at the atomic purge boundary; restored when possible" >&2
      return 1
    fi
    # A process that retained a cwd or descriptor through the rename must be
    # rejected before preservation starts. The randomized old-path removal
    # prevents a new path-based writer from entering after this proof; the
    # second probe below closes the archive-to-delete boundary.
    set +e
    activity_pids="$(quarantine_activity "$purge_path")"
    activity_status=$?
    set -e
    if [ "$activity_status" != "0" ]; then
      rewrite_workspace_identity "$purge_path" "$original_quarantine_id" "$root" >/dev/null 2>&1 || true
      [ ! -e "$path" ] && mv "$purge_path" "$path" >/dev/null 2>&1 || true
      if [ "$activity_status" = "1" ]; then
        echo "error: close: quarantine has active process IDs after isolation (${activity_pids//$'\n'/,}); restored when possible" >&2
      else
        echo "error: close: quarantine activity probe is unavailable or inconclusive after isolation; restored when possible" >&2
      fi
      return 1
    fi
    preserve_workspace_review_artifacts "$purge_path" "$repo" "$workspace_close_id" || {
      rewrite_workspace_identity "$purge_path" "$original_quarantine_id" "$root" >/dev/null 2>&1 || true
      [ ! -e "$path" ] && mv "$purge_path" "$path" >/dev/null 2>&1 || true
      return 1
    }
    preserve_all_ignored_artifacts "$purge_path" "$repo" "$workspace_close_id" || {
      rewrite_workspace_identity "$purge_path" "$original_quarantine_id" "$root" >/dev/null 2>&1 || true
      [ ! -e "$path" ] && mv "$purge_path" "$path" >/dev/null 2>&1 || true
      return 1
    }
    preserve_git_repository_state "$purge_path" "$repo" "$workspace_close_id" || {
      rewrite_workspace_identity "$purge_path" "$original_quarantine_id" "$root" >/dev/null 2>&1 || true
      [ ! -e "$path" ] && mv "$purge_path" "$path" >/dev/null 2>&1 || true
      return 1
    }
    if ! purge_status="$(git -C "$purge_path" status --porcelain --untracked-files=all 2>/dev/null)" ||
      [ "$(git -C "$purge_path" rev-parse --verify HEAD 2>/dev/null || true)" != "$expected_tip" ] ||
      [ -n "$purge_status" ]; then
      rewrite_workspace_identity "$purge_path" "$original_quarantine_id" "$root" >/dev/null 2>&1 || true
      [ ! -e "$path" ] && mv "$purge_path" "$path" >/dev/null 2>&1 || true
      echo "error: close: quarantine changed while final evidence was copied; restored when possible" >&2
      return 1
    fi
    set +e
    activity_pids="$(quarantine_activity "$purge_path")"
    activity_status=$?
    set -e
    if [ "$activity_status" != "0" ]; then
      rewrite_workspace_identity "$purge_path" "$original_quarantine_id" "$root" >/dev/null 2>&1 || true
      [ ! -e "$path" ] && mv "$purge_path" "$path" >/dev/null 2>&1 || true
      if [ "$activity_status" = "1" ]; then
        echo "error: close: quarantine has active process IDs after isolation (${activity_pids//$'\n'/,}); restored when possible" >&2
      else
        echo "error: close: quarantine activity probe is unavailable or inconclusive after isolation; restored when possible" >&2
      fi
      return 1
    fi
    find "$purge_path" -type d -exec chmod u+rwx {} + 2>/dev/null || true
    find "$purge_path" -type f -exec chmod u+rw {} + 2>/dev/null || true
    if ! rm -rf "$purge_path"; then
      rewrite_workspace_identity "$purge_path" "$original_quarantine_id" "$root" >/dev/null 2>&1 || true
      [ ! -e "$path" ] && mv "$purge_path" "$path" >/dev/null 2>&1 || true
      echo "error: close: quarantine purge failed; restored when possible: $path" >&2
      return 1
    fi
    if [ -e "$purge_path" ]; then
      rewrite_workspace_identity "$purge_path" "$original_quarantine_id" "$root" >/dev/null 2>&1 || true
      [ ! -e "$path" ] && mv "$purge_path" "$path" >/dev/null 2>&1 || true
      echo "error: close: quarantine remains after purge; restored when possible: $path" >&2
      return 1
    fi
    git -C "$repo" update-ref -d "refs/kitsoki/workspace-teardown-recovery/$expected_tip" >/dev/null 2>&1 || true
    echo "purged quarantine: $path"
    return 0
  fi
  local teardown_recovery_ref=""
  if [ -n "$expected_tip" ]; then
    teardown_recovery_ref="refs/kitsoki/workspace-teardown-recovery/$expected_tip"
    if git -C "$repo" rev-parse --verify --quiet "$teardown_recovery_ref" >/dev/null; then
      if [ "$(git -C "$repo" rev-parse "$teardown_recovery_ref")" != "$expected_tip" ]; then
        echo "error: close: content-addressed teardown recovery ref points at the wrong object" >&2
        return 1
      fi
    else
      git -C "$repo" fetch --no-tags "$path" "$expected_tip:$teardown_recovery_ref"
    fi
  fi
  local quarantine_path quarantine_id attempt=0 actual_tip actual_recovery_ref
  while :; do
    quarantine_id="closed-$(safe_ref_fragment "$workspace_close_id")-$(date -u +%Y%m%dT%H%M%SZ)-$$-$attempt"
    quarantine_path="$root/$quarantine_id"
    [ ! -e "$quarantine_path" ] && break
    attempt=$((attempt + 1))
    [ "$attempt" -lt 1000 ] || { echo "error: close: could not allocate quarantine path" >&2; return 1; }
  done
  if ! mv "$path" "$quarantine_path"; then
    echo "error: close: could not atomically quarantine workspace before removal: $path" >&2
    return 1
  fi
  if ! rewrite_workspace_identity "$quarantine_path" "$quarantine_id" "$root"; then
    [ ! -e "$path" ] && mv "$quarantine_path" "$path" >/dev/null 2>&1 || true
    echo "error: close: could not record managed quarantine identity; workspace restored when possible" >&2
    return 1
  fi
  actual_tip="$(git -C "$quarantine_path" rev-parse --verify "refs/heads/$expected_branch")"
  actual_recovery_ref="refs/kitsoki/workspace-teardown-recovery/$actual_tip"
  if ! git -C "$repo" rev-parse --verify --quiet "$actual_recovery_ref" >/dev/null; then
    git -C "$repo" fetch --no-tags "$quarantine_path" "$actual_tip:$actual_recovery_ref"
  fi
  if [ "$actual_tip" != "$expected_tip" ]; then
    echo "warning: close: workspace advanced during quarantine; retained unlanded tip $actual_tip" >&2
  fi
  echo "removed from managed workspaces: $path"
  echo "quarantined: $quarantine_path"
}

cmd_recover() {
  local repo="."
  local root=""
  local ref=""
  local discard_incomplete=0
  while [ "$#" -gt 0 ]; do
    case "$1" in
      --repo) repo="${2:?--repo requires a value}"; shift 2 ;;
      --root) root="${2:?--root requires a value}"; shift 2 ;;
      --discard-incomplete) discard_incomplete=1; shift ;;
      -h|--help) usage; exit 0 ;;
      *)
        [ -z "$ref" ] || die "recover: unexpected argument: $1"
        ref="$1"
        shift
        ;;
    esac
  done
  [ -n "$ref" ] || die "recover: workspace path or id is required"
  repo="$(repo_root "$repo")"
  root="$(resolve_root "$repo" "$root")"
  local path id marker state recorded_path
  path="$(workspace_path "$repo" "$root" "$ref")"
  id="$(basename "$path")"
  marker="$(initialization_marker "$root" "$id")"
  state="$(initialization_state "$root" "$id")" || die "recover: no initialization marker for $path"
  [ "$state" = "stale" ] || die "recover: workspace is still initializing; do not recover a live creation"
  recorded_path="$(initialization_metadata_value "$marker" path)"
  if [ -n "$recorded_path" ]; then
    recorded_path="$(abs_path "$recorded_path")"
  fi
  [ -z "$recorded_path" ] || [ "$recorded_path" = "$path" ] || die "recover: marker path does not match requested workspace: $recorded_path"

  if [ -e "$path" ]; then
    if [ -f "$path/$CAPSULE_SENTINEL" ] || [ -f "$path/$CLONE_SENTINEL" ]; then
      : # Creation completed before interruption; preserve the valid workspace.
    elif [ -d "$path/.git" ]; then
      [ "$discard_incomplete" = "1" ] || die "recover: interrupted clone remains at $path; rerun with --discard-incomplete to remove only this un-managed clone"
      rm -rf "$path"
    else
      die "recover: refusing to remove unexpected non-workspace path: $path"
    fi
  fi
  rm -rf "$marker"
  echo "recovered initialization: $path"
}

main() {
  local cmd="${1:-}"
  case "$cmd" in
    create|open) shift; cmd_create "$@" ;;
    bootstrap) shift; cmd_bootstrap "$@" ;;
    status) shift; cmd_status "$@" ;;
    commit) shift; cmd_commit "$@" ;;
    merge|land) shift; cmd_merge "$@" ;;
    park) shift; cmd_park "$@" ;;
    seal-recovered-quarantine) shift; cmd_seal_recovered_quarantine "$@" ;;
    recover) shift; cmd_recover "$@" ;;
    close|teardown) shift; cmd_close "$@" ;;
    -h|--help|"") usage; [ -n "$cmd" ] || exit 1 ;;
    *) usage; die "unknown command: $cmd" ;;
  esac
}

main "$@"
