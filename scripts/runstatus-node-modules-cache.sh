#!/usr/bin/env bash
# Cache tools/runstatus/node_modules across managed development workspaces.
set -euo pipefail

usage() {
  cat >&2 <<'EOF'
usage: scripts/runstatus-node-modules-cache.sh <key|valid|restore|prepare-install|save>
EOF
}

die() {
  echo "error: $*" >&2
  exit 1
}

repo_root() {
  git rev-parse --show-toplevel
}

abs_path() {
  python3 - "$1" <<'PY'
import os
import sys
print(os.path.realpath(sys.argv[1]))
PY
}

user_write_bit() {
  python3 - "$1" <<'PY'
import os
import stat
import sys
print("1" if os.stat(sys.argv[1]).st_mode & stat.S_IWUSR else "0")
PY
}

node_id() {
  node <<'NODE' 2>/dev/null || true
const parts = [
  process.platform,
  process.arch,
  `modules-${process.versions.modules || "unknown"}`,
];
process.stdout.write(parts.join("-"));
NODE
}

pnpm_id() {
  pnpm --version 2>/dev/null || true
}

cache_key() {
  local lockfile="$1"
  local package_json="$2"
  local node_version="${3:-node-unavailable}"
  local pnpm_version="${4:-pnpm-unavailable}"

  python3 - "$lockfile" "$package_json" "$node_version" "$pnpm_version" <<'PY'
import hashlib
import pathlib
import sys

lockfile, package_json, node_version, pnpm_version = sys.argv[1:]
h = hashlib.sha256()
h.update(b"kitsoki-runstatus-node-modules-cache-v1\0")
for label, value in [
    ("node", node_version or "node-unavailable"),
    ("pnpm", pnpm_version or "pnpm-unavailable"),
]:
    h.update(label.encode())
    h.update(b"\0")
    h.update(value.encode())
    h.update(b"\0")
for label, path in [("package", package_json), ("lockfile", lockfile)]:
    p = pathlib.Path(path)
    h.update(label.encode())
    h.update(b"\0")
    h.update(p.read_bytes())
    h.update(b"\0")
print("v1-" + h.hexdigest())
PY
}

cache_root() {
  local repo="$1"
  local manifest="$repo/.kitsoki-dev-workspace.json"
  if [ -f "$manifest" ]; then
    python3 - "$manifest" <<'PY'
import json
import os
import sys

with open(sys.argv[1], encoding="utf-8") as f:
    root = json.load(f).get("root", "")
if root:
    print(os.path.join(os.path.dirname(root), "cache", "runstatus-node-modules"))
PY
    return
  fi
  printf '%s\n' "$repo/.capsules/cache/runstatus-node-modules"
}

remove_path() {
  local path="$1"
  if [ -L "$path" ] || [ -f "$path" ]; then
    rm -f "$path"
  elif [ -d "$path" ]; then
    chmod -R u+w "$path" 2>/dev/null || true
    rm -rf "$path"
  fi
}

valid_modules() {
  local modules="$1"
  local key="$2"
  [ -d "$modules" ] || return 1
  [ -f "$modules/.modules.yaml" ] || return 1
  [ -e "$modules/.bin/tsx" ] || return 1
  [ -f "$modules/.kitsoki-cache-key" ] || return 1
  [ "$(cat "$modules/.kitsoki-cache-key")" = "$key" ] || return 1
}

copy_modules() {
  local source="$1"
  local dest="$2"
  if cp -a "$source" "$dest" 2>/dev/null; then
    return 0
  fi
  cp -R -p -P "$source" "$dest"
}

link_modules() {
  local source="$1"
  local dest="$2"
  remove_path "$dest"
  ln -s "$source" "$dest"
}

ensure_cache_base() {
  local repo="$1"
  local cache_base="$2"
  local relock_repo=0

  if [ -d "$cache_base" ]; then
    return 0
  fi

  case "$cache_base" in
    "$repo"/.capsules/*)
      if [ ! -d "$repo/.capsules" ]; then
        if [ "$(user_write_bit "$repo")" = "0" ]; then
          chmod u+w "$repo"
          relock_repo=1
        fi
      fi
      if ! mkdir -p "$cache_base"; then
        if [ "$relock_repo" = 1 ]; then
          chmod u-w "$repo" 2>/dev/null || true
        fi
        return 1
      fi
      if [ "$relock_repo" = 1 ]; then chmod u-w "$repo" 2>/dev/null || true; fi
      ;;
    *)
      mkdir -p "$cache_base"
      ;;
  esac
}

acquire_lock() {
  local lock="$1"
  local i
  for i in $(seq 1 120); do
    if mkdir "$lock" 2>/dev/null; then
      return 0
    fi
    sleep 1
  done
  die "timed out waiting for cache lock: $lock"
}

main() {
  [ "$#" -eq 1 ] || { usage; exit 2; }
  local command="$1"
  local repo runstatus_dir lockfile package_json modules cache_base key cache_dir cache_modules
  repo="$(repo_root)"
  runstatus_dir="$repo/tools/runstatus"
  lockfile="$runstatus_dir/pnpm-lock.yaml"
  package_json="$runstatus_dir/package.json"
  modules="$runstatus_dir/node_modules"
  [ -f "$lockfile" ] || die "missing $lockfile"
  [ -f "$package_json" ] || die "missing $package_json"

  key="$(cache_key "$lockfile" "$package_json" "$(node_id)" "$(pnpm_id)")"
  cache_base="$(abs_path "$(cache_root "$repo")")"
  cache_dir="$cache_base/$key"
  cache_modules="$cache_dir/node_modules"

  case "$command" in
    key)
      printf '%s\n' "$key"
      ;;
    valid)
      valid_modules "$modules" "$key"
      ;;
    restore)
      if valid_modules "$modules" "$key"; then
        echo "runstatus deps already installed for $lockfile"
        return 0
      fi
      if ! valid_modules "$cache_modules" "$key"; then
        echo "runstatus deps cache miss for $lockfile"
        return 1
      fi
      link_modules "$cache_modules" "$modules"
      echo "restored runstatus deps from $cache_modules"
      ;;
    prepare-install)
      if [ -L "$modules" ]; then
        rm -f "$modules"
      fi
      ;;
    save)
      [ -d "$modules" ] || die "cannot cache missing $modules"
      [ ! -L "$modules" ] || return 0
      ensure_cache_base "$repo" "$cache_base"
      printf '%s\n' "$key" >"$modules/.kitsoki-cache-key"
      if valid_modules "$cache_modules" "$key"; then
        link_modules "$cache_modules" "$modules"
        echo "using existing runstatus deps cache at $cache_modules"
        return 0
      fi

      local lock tmp
      lock="$cache_base/$key.lock"
      acquire_lock "$lock"
      tmp="$(mktemp -d "$cache_base/.node_modules.$key.XXXXXX")"
      cleanup() {
        chmod -R u+w "$tmp" 2>/dev/null || true
        rm -rf "$tmp"
        rmdir "$lock" 2>/dev/null || true
      }
      trap cleanup EXIT INT TERM

      if ! valid_modules "$cache_modules" "$key"; then
        copy_modules "$modules" "$tmp/node_modules"
        printf '%s\n' "$key" >"$tmp/node_modules/.kitsoki-cache-key"
        chmod -R a-w "$tmp/node_modules" 2>/dev/null || true
        mv "$tmp" "$cache_dir"
        tmp=""
        link_modules "$cache_modules" "$modules"
        echo "saved runstatus deps cache at $cache_modules"
      else
        link_modules "$cache_modules" "$modules"
      fi
      trap - EXIT INT TERM
      cleanup

      ;;
    *)
      usage
      exit 2
      ;;
  esac
}

main "$@"
