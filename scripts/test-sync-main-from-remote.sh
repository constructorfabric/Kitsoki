#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
script="$script_dir/sync-main-from-remote.sh"

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

assert_contains() {
  local file="$1"
  local needle="$2"
  if ! grep -Fq "$needle" "$file"; then
    echo "expected '$needle' in $file" >&2
    cat "$file" >&2
    exit 1
  fi
}

write_stub_agents() {
  local dir="$1"
  cat >"$dir/resolve.sh" <<'EOS'
#!/usr/bin/env bash
set -euo pipefail
test -f "$KITSOKI_SYNC_PROMPT_FILE"
printf '%s\n' "$KITSOKI_SYNC_CONFLICT_FILES" | grep -q README.md
{
  echo local-conflict
  echo remote-conflict
} > README.md
git add README.md
EOS
  cat >"$dir/review.sh" <<'EOS'
#!/usr/bin/env bash
set -euo pipefail
test -f "$KITSOKI_SYNC_PROMPT_FILE"
test -z "$(git diff --name-only --diff-filter=U)"
grep -q local-conflict README.md
grep -q remote-conflict README.md
printf 'reviewed %s\n' "$KITSOKI_SYNC_BRANCH" > .artifacts/sync-main/review-marker.txt
EOS
  chmod +x "$dir/resolve.sh" "$dir/review.sh"
}

bare="$tmp/origin.git"
seed="$tmp/seed"
repo="$tmp/repo"

git init -q --bare "$bare"
git_init "$seed"
commit_file "$seed" README.md base
commit_file "$seed" .gitignore .artifacts/
git -C "$seed" branch -M main
git -C "$seed" remote add origin "$bare"
git -C "$seed" push -q -u origin main

git clone -q "$bare" "$repo"
git -C "$repo" config user.name "Test User"
git -C "$repo" config user.email "test@example.invalid"
mkdir -p "$repo/scripts"
cp "$script" "$repo/scripts/sync-main-from-remote.sh"
cp "$script_dir/merge-to-main.sh" "$repo/scripts/merge-to-main.sh"
chmod +x "$repo/scripts/"*.sh

commit_file "$repo" local.txt local
git -C "$repo" checkout -q -b remote-work origin/main
commit_file "$repo" remote.txt remote
git -C "$repo" push -q origin remote-work:main
git -C "$repo" checkout -q main

out="$tmp/clean.out"
(
  cd "$repo"
  scripts/sync-main-from-remote.sh --no-fetch --name clean-sync
) >"$out"
assert_contains "$out" "Integration branch:"
assert_contains "$out" "scripts/merge-to-main.sh"
clean_branch="$(awk '/Integration branch:/ { print $3 }' "$out")"
[ -z "$(git -C "$repo/.worktrees/clean-sync" status --short)" ] ||
  { git -C "$repo/.worktrees/clean-sync" status --short >&2; exit 1; }
git -C "$repo" merge-base --is-ancestor main "$clean_branch"
git -C "$repo" worktree remove .worktrees/clean-sync
git -C "$repo" branch -D "$clean_branch" >/dev/null

git -C "$repo" reset -q --hard origin/main
commit_file "$repo" local-only.txt local-only
ahead_out="$tmp/ahead.out"
(
  cd "$repo"
  scripts/sync-main-from-remote.sh --no-fetch
) >"$ahead_out"
assert_contains "$ahead_out" "main already contains origin/main, but local main is ahead by 1 commit."
assert_contains "$ahead_out" "Run push_main remote=origin"
if grep -Fq "Integration branch:" "$ahead_out"; then
  echo "local-ahead sync should not prepare an integration branch" >&2
  cat "$ahead_out" >&2
  exit 1
fi

conflict="$tmp/conflict"
git clone -q "$bare" "$conflict"
git -C "$conflict" config user.name "Test User"
git -C "$conflict" config user.email "test@example.invalid"
mkdir -p "$conflict/scripts"
cp "$script" "$conflict/scripts/sync-main-from-remote.sh"
cp "$script_dir/merge-to-main.sh" "$conflict/scripts/merge-to-main.sh"
chmod +x "$conflict/scripts/"*.sh
commit_file "$conflict" README.md local-conflict
git -C "$seed" fetch -q origin main
git -C "$seed" checkout -q main
git -C "$seed" reset -q --hard origin/main
commit_file "$seed" README.md remote-conflict
git -C "$seed" push -q origin main

set +e
(
  cd "$conflict"
  scripts/sync-main-from-remote.sh --name conflict-sync
) >"$tmp/conflict.out" 2>&1
status=$?
set -e
[ "$status" -ne 0 ] || { echo "expected conflict status" >&2; exit 1; }
assert_contains "$tmp/conflict.out" "Merge conflicts are isolated"
git -C "$conflict/.worktrees/conflict-sync" ls-files -u | grep -q README.md
git -C "$conflict" worktree remove --force .worktrees/conflict-sync
git -C "$conflict" branch -D "$(git -C "$conflict" branch --list 'sync/main-origin-main-*' --format='%(refname:short)')" >/dev/null

agents="$tmp/agents"
mkdir -p "$agents"
write_stub_agents "$agents"
auto_out="$tmp/auto.out"
(
  cd "$conflict"
  scripts/sync-main-from-remote.sh \
    --name auto-sync \
    --auto-resolve \
    --resolver-command "$agents/resolve.sh" \
    --review-command "$agents/review.sh"
) >"$auto_out"
assert_contains "$auto_out" "Integration branch:"
assert_contains "$auto_out" "scripts/merge-to-main.sh"
auto_branch="$(awk '/Integration branch:/ { print $3 }' "$auto_out")"
[ -z "$(git -C "$conflict/.worktrees/auto-sync" diff --name-only --diff-filter=U)" ] ||
  { git -C "$conflict/.worktrees/auto-sync" status --short >&2; exit 1; }
test -f "$conflict/.worktrees/auto-sync/.artifacts/sync-main/review-marker.txt"
git -C "$conflict" merge-base --is-ancestor main "$auto_branch"

known_bare="$tmp/known-origin.git"
known_seed="$tmp/known-seed"
known_repo="$tmp/known-repo"

git init -q --bare "$known_bare"
git_init "$known_seed"
commit_file "$known_seed" README.md base
commit_file "$known_seed" .gitignore .artifacts/
commit_file "$known_seed" docs/proposals/retired.md proposal
git -C "$known_seed" branch -M main
git -C "$known_seed" remote add origin "$known_bare"
git -C "$known_seed" push -q -u origin main

git clone -q "$known_bare" "$known_repo"
git -C "$known_repo" config user.name "Test User"
git -C "$known_repo" config user.email "test@example.invalid"
mkdir -p "$known_repo/scripts"
cp "$script" "$known_repo/scripts/sync-main-from-remote.sh"
cp "$script_dir/merge-to-main.sh" "$known_repo/scripts/merge-to-main.sh"
chmod +x "$known_repo/scripts/"*.sh
git -C "$known_repo" rm -q docs/proposals/retired.md
git -C "$known_repo" commit -q -m "retire proposal"

git -C "$known_seed" reset -q --hard origin/main
commit_file "$known_seed" docs/proposals/retired.md "stale remote edit"
git -C "$known_seed" push -q origin main

known_out="$tmp/known.out"
(
  cd "$known_repo"
  scripts/sync-main-from-remote.sh --name known-sync
) >"$known_out"
assert_contains "$known_out" "known resolution: kept local retirement of docs/proposals/retired.md"
assert_contains "$known_out" "Integration branch:"
known_branch="$(awk '/Integration branch:/ { print $3 }' "$known_out")"
[ -z "$(git -C "$known_repo/.worktrees/known-sync" ls-files -u)" ] ||
  { git -C "$known_repo/.worktrees/known-sync" status --short >&2; exit 1; }
test ! -e "$known_repo/.worktrees/known-sync/docs/proposals/retired.md"
git -C "$known_repo" merge-base --is-ancestor main "$known_branch"

echo "sync-main-from-remote tests passed"
