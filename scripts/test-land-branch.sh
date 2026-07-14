#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
script="$script_dir/land-branch.sh"

tmp="$(mktemp -d)"
trap 'chmod -R u+w "$tmp" 2>/dev/null || true; rm -rf "$tmp"' EXIT

git_init() {
  git init -q "$1"
  git -C "$1" config user.name "Test User"
  git -C "$1" config user.email "test@example.invalid"
}

commit_file() {
  local repo="$1"
  local file="$2"
  local content="$3"
  mkdir -p "$(dirname "$repo/$file")"
  printf '%s\n' "$content" > "$repo/$file"
  git -C "$repo" add "$file"
  git -C "$repo" commit -q -m "$content"
}

install_scripts() {
  local repo="$1"
  mkdir -p "$repo/scripts" "$repo/.worktrees"
  cp "$script" "$repo/scripts/land-branch.sh"
  cp "$script_dir/merge-to-main.sh" "$repo/scripts/merge-to-main.sh"
  chmod +x "$repo/scripts/"*.sh
}

assert_contains() {
  local file="$1"
  local needle="$2"
  if ! grep -Fq "$needle" "$file"; then
    echo "expected '$needle' in $file" >&2
    cat "$file" >&2
    exit 1
  fi
}

repo="$tmp/repo"
git_init "$repo"
commit_file "$repo" README.md base
git -C "$repo" branch -M main
install_scripts "$repo"

git -C "$repo" checkout -q -b feature
commit_file "$repo" feature.txt feature
git -C "$repo" checkout -q main
commit_file "$repo" main.txt main

(
  cd "$repo"
  scripts/land-branch.sh feature
) >"$tmp/clean.out" 2>&1
assert_contains "$tmp/clean.out" "path: cherry-pick-and-land"
test "$(cat "$repo/feature.txt")" = "feature"
git -C "$repo" rev-parse --verify --quiet feature-land >/dev/null &&
  { echo "feature-land branch should be cleaned up" >&2; exit 1; }
test ! -e "$repo/.worktrees/feature-land"

duplicate="$tmp/duplicate"
git_init "$duplicate"
commit_file "$duplicate" bug.txt broken
git -C "$duplicate" branch -M main
install_scripts "$duplicate"

git -C "$duplicate" checkout -q -b first-fix
commit_file "$duplicate" bug.txt fixed
git -C "$duplicate" checkout -q main
commit_file "$duplicate" main-only.txt main-only
printf '%s\n' fixed >"$duplicate/bug.txt"
git -C "$duplicate" add bug.txt
git -C "$duplicate" commit -q -m "independent equivalent fix"

set +e
(
  cd "$duplicate"
  scripts/land-branch.sh first-fix
) >"$tmp/duplicate.out" 2>&1
status=$?
set -e
[ "$status" -ne 0 ] || { echo "expected stale duplicate landing to fail" >&2; exit 1; }
assert_contains "$tmp/duplicate.out" "already represented on current main"
assert_contains "$tmp/duplicate.out" "first-fix"
test ! -e "$duplicate/.worktrees/first-fix-land"

echo "land-branch tests passed"
