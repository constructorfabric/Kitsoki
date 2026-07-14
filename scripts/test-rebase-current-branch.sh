#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
script="$script_dir/rebase-current-branch.sh"

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

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

repo="$tmp/repo"
git_init "$repo"
commit_file "$repo" README.md base
git -C "$repo" branch -M main
git -C "$repo" checkout -q -b feature
commit_file "$repo" feature.txt one
commit_file "$repo" feature-two.txt two
git -C "$repo" checkout -q main
commit_file "$repo" main.txt main
git -C "$repo" checkout -q feature

out="$tmp/rebase-ok.json"
"$script" --worktree "$repo" --integration main >"$out"
jq -e '.rebase_done == true and .conflict_origin == "" and .route == "on_branch"' "$out" >/dev/null
jq -e '.pre_rebase_tag == "feature-pre-rebase-backup"' "$out" >/dev/null
git -C "$repo" merge-base --is-ancestor main HEAD
git -C "$repo" rev-parse --verify --quiet feature-pre-rebase-backup >/dev/null

conflict="$tmp/conflict"
git_init "$conflict"
commit_file "$conflict" README.md base
git -C "$conflict" branch -M main
git -C "$conflict" checkout -q -b feature
commit_file "$conflict" README.md feature
git -C "$conflict" checkout -q main
commit_file "$conflict" README.md main
git -C "$conflict" checkout -q feature

conflict_out="$tmp/rebase-conflict.json"
"$script" --worktree "$conflict" --integration main >"$conflict_out"
jq -e '.rebase_done == false and .conflict_origin == "rebase" and .route == "rebase_conflict"' "$conflict_out" >/dev/null
git -C "$conflict" diff --name-only --diff-filter=U | grep -q README.md

echo "rebase-current-branch tests passed"
