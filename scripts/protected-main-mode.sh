#!/usr/bin/env bash
# protected-main-mode.sh - toggle the protected checkout chmod guard.
#
# The protected-main workflow keeps the primary checkout's source files and
# source directories read-only, while leaving local state/scratch roots writable
# so tools can still write notes, artifacts, temp files, Claude links, worktrees,
# and Kitsoki local state.
#
# Usage:
#   scripts/protected-main-mode.sh status
#   scripts/protected-main-mode.sh lock [--quiet] [--force]
#   scripts/protected-main-mode.sh unlock [--quiet] [--force]
#   scripts/protected-main-mode.sh repair-writable-roots [--quiet] [--force]
#
# By default, lock/unlock refuse to mutate a linked worktree. Use --force only
# for repair or tests where the current checkout is intentionally the target.
set -euo pipefail

DEFAULT_WRITABLE_ROOTS=".context .artifacts .temp .claude .kitsoki"
DEFAULT_TOP_WRITABLE_ROOTS=".capsules .worktrees"

usage() {
  cat >&2 <<'EOF'
usage: scripts/protected-main-mode.sh <status|lock|unlock|repair-writable-roots> [--quiet] [--force]

Commands:
  status                  Report root/source/scratch writability.
  lock                    Make tracked source paths read-only and keep scratch roots writable.
  unlock                  Make tracked source paths writable for maintenance.
  repair-writable-roots   Restore writable-root exceptions without changing source mode.

Environment:
  KITSOKI_PROTECTED_MAIN_WRITABLE_ROOTS
      Space-separated recursive writable roots. Defaults to:
      .context .artifacts .temp .claude .kitsoki

  KITSOKI_PROTECTED_MAIN_EXTRA_WRITABLE_ROOTS
      Extra recursive writable roots appended to the default/override list.

  KITSOKI_PROTECTED_MAIN_TOP_WRITABLE_ROOTS
      Space-separated top-level writable roots that should not be recursed into.
      Defaults to: .capsules .worktrees
EOF
}

die() {
  echo "error: $*" >&2
  exit 1
}

mode="${1:-status}"
case "$mode" in
  status|lock|unlock|repair-writable-roots) shift || true ;;
  -h|--help) usage; exit 0 ;;
  *) usage; exit 1 ;;
esac

quiet=0
force=0
while [ "$#" -gt 0 ]; do
  case "$1" in
    --quiet)
      quiet=1
      shift
      ;;
    --force)
      force=1
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      die "unknown argument: $1"
      ;;
  esac
done

repo_root="$(git rev-parse --show-toplevel 2>/dev/null || true)"
[ -n "$repo_root" ] || die "not inside a git repository"
cd "$repo_root"

git_dir="$(git rev-parse --git-dir)"
git_common_dir="$(git rev-parse --git-common-dir)"

if [ "$mode" != "status" ] && [ "$force" -ne 1 ] && [ "$git_dir" != "$git_common_dir" ]; then
  die "refusing to $mode a linked worktree; run from the primary checkout or pass --force"
fi

writable_roots="${KITSOKI_PROTECTED_MAIN_WRITABLE_ROOTS:-$DEFAULT_WRITABLE_ROOTS}"
if [ -n "${KITSOKI_PROTECTED_MAIN_EXTRA_WRITABLE_ROOTS:-}" ]; then
  writable_roots="$writable_roots $KITSOKI_PROTECTED_MAIN_EXTRA_WRITABLE_ROOTS"
fi
top_writable_roots="${KITSOKI_PROTECTED_MAIN_TOP_WRITABLE_ROOTS:-$DEFAULT_TOP_WRITABLE_ROOTS}"

normalize_path() {
  local p="$1"
  p="${p#./}"
  p="${p%/}"
  printf '%s\n' "$p"
}

validate_roots() {
  local root normalized
  for root in $writable_roots $top_writable_roots; do
    normalized="$(normalize_path "$root")"
    case "$normalized" in
      ""|"."|".."|/*|../*|*/../*|*/..)
        die "unsafe writable root: $root"
        ;;
    esac
  done
}

is_recursive_writable_path() {
  local p root
  p="$(normalize_path "$1")"
  for root in $writable_roots; do
    root="$(normalize_path "$root")"
    if [ "$p" = "$root" ]; then
      return 0
    fi
    case "$p" in
      "$root"/*) return 0 ;;
    esac
  done
  return 1
}

is_top_writable_path() {
  local p root
  p="$(normalize_path "$1")"
  for root in $top_writable_roots; do
    root="$(normalize_path "$root")"
    [ "$p" = "$root" ] && return 0
  done
  return 1
}

is_writable_exception_path() {
  is_recursive_writable_path "$1" || is_top_writable_path "$1"
}

tracked_dirs() {
  local file dir
  {
    printf '.\n'
    git ls-files | while IFS= read -r file; do
      case "$file" in
        */*) dir="${file%/*}" ;;
        *) continue ;;
      esac
      while [ -n "$dir" ] && [ "$dir" != "." ] && [ "$dir" != "/" ]; do
        printf '%s\n' "$dir"
        case "$dir" in
          */*) dir="${dir%/*}" ;;
          *) break ;;
        esac
      done
    done
  } | sort -u
}

chmod_nul() {
  local mode="$1"
  xargs -0 chmod "$mode" 2>/dev/null || true
}

tracked_writable_root_files() {
  # Writable roots are validated as simple relative paths, so intentional word
  # splitting is fine here and lets git restrict the scan to the small exception
  # set instead of the whole repository.
  # shellcheck disable=SC2086
  git ls-files -z -- $writable_roots
}

make_tracked_writable_root_files() {
  local file
  tracked_writable_root_files | while IFS= read -r -d '' file; do
    [ -e "$file" ] && printf '%s\0' "$file"
  done | chmod_nul u+rw
}

make_tracked_writable_root_dirs() {
  local file dir
  tracked_writable_root_files | while IFS= read -r -d '' file; do
    case "$file" in
      */*) dir="${file%/*}" ;;
      *) continue ;;
    esac
    while [ -n "$dir" ] && [ "$dir" != "." ] && [ "$dir" != "/" ]; do
      [ -e "$dir" ] && printf '%s\0' "$dir"
      case "$dir" in
        */*) dir="${dir%/*}" ;;
        *) break ;;
      esac
    done
  done | chmod_nul u+rwx
}

make_writable_roots() {
  local root
  for root in $writable_roots; do
    root="$(normalize_path "$root")"
    mkdir -p "$root"
    chmod u+rwx "$root" 2>/dev/null || true
  done
  for root in $top_writable_roots; do
    root="$(normalize_path "$root")"
    mkdir -p "$root"
    chmod u+rwx "$root" 2>/dev/null || true
  done
  make_tracked_writable_root_dirs
  make_tracked_writable_root_files
}

make_recursive_writable_root_contents() {
  local root
  for root in $writable_roots; do
    root="$(normalize_path "$root")"
    [ -d "$root" ] || continue
    chmod -R u+rwX "$root" 2>/dev/null || true
  done
}

lock_checkout() {
  local file dir

  validate_roots
  chmod u+rwx . 2>/dev/null || true
  make_writable_roots

  git ls-files -z | chmod_nul a-w

  tracked_dirs | while IFS= read -r dir; do
    [ "$dir" = "." ] && continue
    is_writable_exception_path "$dir" && continue
    [ -e "$dir" ] && printf '%s\0' "$dir"
  done | chmod_nul a-w

  chmod a-w . 2>/dev/null || true
  make_writable_roots

  if [ "$quiet" -ne 1 ]; then
    echo "protected-main mode: locked source paths; writable roots preserved"
  fi
}

unlock_checkout() {
  local file dir

  validate_roots
  chmod u+rwx . 2>/dev/null || true

  tracked_dirs | while IFS= read -r dir; do
    [ -e "$dir" ] && printf '%s\0' "$dir"
  done | chmod_nul u+rwx
  git ls-files -z | chmod_nul u+rw
  make_writable_roots

  if [ "$quiet" -ne 1 ]; then
    echo "protected-main mode: unlocked tracked source paths"
  fi
}

repair_writable_roots() {
  local root_was_writable

  validate_roots
  root_was_writable=0
  [ -w . ] && root_was_writable=1
  chmod u+rwx . 2>/dev/null || true
  make_writable_roots
  make_recursive_writable_root_contents
  [ "$root_was_writable" -eq 1 ] || chmod a-w . 2>/dev/null || true

  if [ "$quiet" -ne 1 ]; then
    echo "protected-main mode: writable roots repaired"
  fi
}

status_checkout() {
  local file dir root tracked_writable=0 tracked_readonly=0 dir_writable=0 dir_readonly=0

  validate_roots

  while IFS= read -r -d '' file; do
    [ -e "$file" ] || continue
    if [ -w "$file" ]; then
      tracked_writable=$((tracked_writable + 1))
    else
      tracked_readonly=$((tracked_readonly + 1))
    fi
  done < <(git ls-files -z)

  while IFS= read -r dir; do
    [ "$dir" = "." ] && continue
    is_writable_exception_path "$dir" && continue
    [ -e "$dir" ] || continue
    if [ -w "$dir" ]; then
      dir_writable=$((dir_writable + 1))
    else
      dir_readonly=$((dir_readonly + 1))
    fi
  done < <(tracked_dirs)

  if [ -w . ]; then
    echo "root: writable"
  else
    echo "root: read-only"
  fi
  echo "tracked files: $tracked_writable writable, $tracked_readonly read-only"
  echo "tracked source dirs: $dir_writable writable, $dir_readonly read-only"
  echo "recursive writable roots:"
  for root in $writable_roots; do
    root="$(normalize_path "$root")"
    if [ -d "$root" ]; then
      if [ -w "$root" ]; then
        echo "  $root: writable"
      else
        echo "  $root: read-only"
      fi
    else
      echo "  $root: missing"
    fi
  done
  echo "top writable roots:"
  for root in $top_writable_roots; do
    root="$(normalize_path "$root")"
    if [ -d "$root" ]; then
      if [ -w "$root" ]; then
        echo "  $root: writable"
      else
        echo "  $root: read-only"
      fi
    else
      echo "  $root: missing"
    fi
  done
}

case "$mode" in
  status) status_checkout ;;
  lock) lock_checkout ;;
  unlock) unlock_checkout ;;
  repair-writable-roots) repair_writable_roots ;;
esac
