#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
script="$script_dir/protected-main-mode.sh"

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

assert_writable() {
  local path="$1"
  [ -w "$path" ] || { echo "expected writable: $path" >&2; exit 1; }
}

assert_readonly() {
  local path="$1"
  [ ! -w "$path" ] || { echo "expected read-only: $path" >&2; exit 1; }
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
mkdir -p "$repo/scripts"
cp "$script" "$repo/scripts/protected-main-mode.sh"
chmod +x "$repo/scripts/protected-main-mode.sh"

commit_file "$repo" src/app.txt source
commit_file "$repo" .context/note.md context
commit_file "$repo" .artifacts/report.txt artifact
commit_file "$repo" .kitsoki/project-profile.yaml profile
commit_file "$repo" .gitignore ".context/
.artifacts/
.temp/
.claude/
.worktrees/
.kitsoki/sessions/"
git -C "$repo" branch -M main

(
  cd "$repo"
  scripts/protected-main-mode.sh lock >"$tmp/lock.out"
)
assert_contains "$tmp/lock.out" "locked source paths"

assert_readonly "$repo"
assert_readonly "$repo/src"
assert_readonly "$repo/src/app.txt"

assert_writable "$repo/.context"
assert_writable "$repo/.context/note.md"
assert_writable "$repo/.artifacts"
assert_writable "$repo/.artifacts/report.txt"
assert_writable "$repo/.temp"
assert_writable "$repo/.claude"
assert_writable "$repo/.worktrees"
assert_writable "$repo/.kitsoki"
assert_writable "$repo/.kitsoki/project-profile.yaml"

touch "$repo/.context/new-note.md"
touch "$repo/.artifacts/new-artifact.txt"
touch "$repo/.temp/scratch.txt"
touch "$repo/.claude/local-state"
mkdir "$repo/.worktrees/new-worktree-dir"
touch "$repo/.kitsoki/local-state"

mkdir -p "$repo/.temp/deps/pkg/bin"
touch "$repo/.temp/deps/pkg/bin/tool"
chmod -R a-w "$repo/.temp/deps"
(
  cd "$repo"
  scripts/protected-main-mode.sh repair-writable-roots >"$tmp/repair.out"
)
assert_contains "$tmp/repair.out" "writable roots repaired"
assert_writable "$repo/.temp/deps"
assert_writable "$repo/.temp/deps/pkg"
assert_writable "$repo/.temp/deps/pkg/bin/tool"
assert_readonly "$repo/src"

if touch "$repo/src/new-source.txt" 2>/dev/null; then
  echo "expected source directory creation to fail while locked" >&2
  exit 1
fi

(
  cd "$repo"
  scripts/protected-main-mode.sh status >"$tmp/status.out"
)
assert_contains "$tmp/status.out" "root: read-only"
assert_contains "$tmp/status.out" ".context: writable"
assert_contains "$tmp/status.out" ".artifacts: writable"
assert_contains "$tmp/status.out" ".temp: writable"
assert_contains "$tmp/status.out" ".claude: writable"
assert_contains "$tmp/status.out" ".worktrees: writable"
assert_contains "$tmp/status.out" ".kitsoki: writable"

(
  cd "$repo"
  scripts/protected-main-mode.sh unlock >"$tmp/unlock.out"
)
assert_contains "$tmp/unlock.out" "unlocked tracked source paths"

assert_writable "$repo"
assert_writable "$repo/src"
assert_writable "$repo/src/app.txt"
assert_writable "$repo/.context"
assert_writable "$repo/.artifacts"
assert_writable "$repo/.temp"
assert_writable "$repo/.claude"
assert_writable "$repo/.worktrees"
assert_writable "$repo/.kitsoki"

echo "protected-main-mode tests passed"

merge_repo="$tmp/merge-repo"
git_init "$merge_repo"
mkdir -p "$merge_repo/scripts"
cp "$script" "$merge_repo/scripts/protected-main-mode.sh"
cp "$script_dir/merge-to-main.sh" "$merge_repo/scripts/merge-to-main.sh"
chmod +x "$merge_repo/scripts/"*.sh
git -C "$merge_repo" add scripts/protected-main-mode.sh scripts/merge-to-main.sh
git -C "$merge_repo" commit -q -m "add scripts"

commit_file "$merge_repo" src/app.txt source
commit_file "$merge_repo" .context/note.md context
commit_file "$merge_repo" .gitignore ".context/
.artifacts/
.temp/
.claude/
.worktrees/"
git -C "$merge_repo" branch -M main

git -C "$merge_repo" checkout -q -b feature
printf '%s\n' updated-source > "$merge_repo/src/app.txt"
printf '%s\n' updated-context > "$merge_repo/.context/note.md"
git -C "$merge_repo" add -u src/app.txt .context/note.md
git -C "$merge_repo" commit -q -m "feature update"
git -C "$merge_repo" checkout -q main

(
  cd "$merge_repo"
  scripts/protected-main-mode.sh lock --quiet
  scripts/merge-to-main.sh feature --force >"$tmp/merge.out"
)
assert_contains "$tmp/merge.out" "read-only guard restored"

assert_readonly "$merge_repo"
assert_readonly "$merge_repo/src"
assert_readonly "$merge_repo/src/app.txt"
assert_writable "$merge_repo/.context"
assert_writable "$merge_repo/.context/note.md"
touch "$merge_repo/.context/post-merge-note.md"

dirty_repo="$tmp/dirty-repo"
git_init "$dirty_repo"
mkdir -p "$dirty_repo/scripts"
cp "$script_dir/merge-to-main.sh" "$dirty_repo/scripts/merge-to-main.sh"
chmod +x "$dirty_repo/scripts/merge-to-main.sh"
git -C "$dirty_repo" add scripts/merge-to-main.sh
git -C "$dirty_repo" commit -q -m "add merge helper"
commit_file "$dirty_repo" keep.txt keep
git -C "$dirty_repo" branch -M main

git -C "$dirty_repo" checkout -q -b feature
commit_file "$dirty_repo" feature.txt feature
git -C "$dirty_repo" checkout -q main
printf '%s\n' local >"$dirty_repo/keep.txt"
(
  cd "$dirty_repo"
  scripts/merge-to-main.sh feature --force >"$tmp/dirty-unrelated.out"
)
assert_contains "$tmp/dirty-unrelated.out" "main ->"
[ "$(cat "$dirty_repo/feature.txt")" = "feature" ] ||
  { echo "dirty-unrelated merge skipped feature file" >&2; exit 1; }
[ "$(cat "$dirty_repo/keep.txt")" = "local" ] ||
  { echo "dirty-unrelated merge clobbered local dirty file" >&2; exit 1; }

chmod -R u+w "$dirty_repo"
git -C "$dirty_repo" checkout -q -b overlap
commit_file "$dirty_repo" conflict.txt branch
git -C "$dirty_repo" checkout -q main
printf '%s\n' local >"$dirty_repo/conflict.txt"
set +e
(
  cd "$dirty_repo"
  scripts/merge-to-main.sh overlap --force
) >"$tmp/dirty-overlap.out" 2>&1
dirty_overlap_status=$?
set -e
[ "$dirty_overlap_status" -ne 0 ] || { echo "dirty-overlap merge should fail" >&2; exit 1; }
assert_contains "$tmp/dirty-overlap.out" "conflict.txt"

staging_repo="$tmp/staging-repo"
git_init "$staging_repo"
mkdir -p "$staging_repo/scripts"
cp "$script_dir/merge-to-main.sh" "$staging_repo/scripts/merge-to-main.sh"
chmod +x "$staging_repo/scripts/merge-to-main.sh"
{
  printf 'test:\n'
  printf "\t@printf 'gate\\\\n' > %s\n" "$tmp/staging-gate-ran"
} >"$staging_repo/Makefile"
git -C "$staging_repo" add scripts/merge-to-main.sh Makefile
git -C "$staging_repo" commit -q -m "add merge helper"
commit_file "$staging_repo" base.txt base
git -C "$staging_repo" branch -M main
mkdir -p "$staging_repo/.capsules/staging"
git -C "$staging_repo" clone --no-local "$staging_repo" "$staging_repo/.capsules/staging/local" >/dev/null
git -C "$staging_repo/.capsules/staging/local" remote rename origin source
git -C "$staging_repo/.capsules/staging/local" config user.name "Test User"
git -C "$staging_repo/.capsules/staging/local" config user.email "test@example.invalid"
touch "$staging_repo/.capsules/staging/local/.kitsoki-capsule"
git -C "$staging_repo/.capsules/staging/local" switch -q -c staging/local main
printf '%s\n' staged >"$staging_repo/.capsules/staging/local/staged.txt"
git -C "$staging_repo/.capsules/staging/local" add staged.txt
git -C "$staging_repo/.capsules/staging/local" commit -q -m "staged change"
(
  cd "$staging_repo"
  scripts/merge-to-main.sh >"$tmp/staging.out"
)
assert_contains "$tmp/staging.out" "main ->"
[ "$(cat "$tmp/staging-gate-ran")" = "gate" ] ||
  { echo "default staging gate did not run" >&2; exit 1; }
[ "$(cat "$staging_repo/staged.txt")" = "staged" ] ||
  { echo "staging promotion did not update main" >&2; exit 1; }

race_repo="$tmp/race-repo"
git_init "$race_repo"
mkdir -p "$race_repo/scripts" "$race_repo/src"
cp "$script" "$race_repo/scripts/protected-main-mode.sh"
cp "$script_dir/merge-to-main.sh" "$race_repo/scripts/merge-to-main.sh"
chmod +x "$race_repo/scripts/"*.sh
git -C "$race_repo" add scripts/protected-main-mode.sh scripts/merge-to-main.sh
git -C "$race_repo" commit -q -m "add scripts"
commit_file "$race_repo" src/app.txt base
git -C "$race_repo" branch -M main
git -C "$race_repo" checkout -q -b feature
printf '%s\n' feature > "$race_repo/src/app.txt"
git -C "$race_repo" add src/app.txt
git -C "$race_repo" commit -q -m "feature update"
git -C "$race_repo" checkout -q main

shim="$tmp/git-shim"
mkdir -p "$shim"
real_git="$(command -v git)"
cat >"$shim/git" <<'SH'
#!/usr/bin/env bash
set -euo pipefail
if [ "${1:-}" = "merge" ] && [ "${2:-}" = "--ff-only" ]; then
  printf '%s\n' feature > src/app.txt
  "$REAL_GIT" add src/app.txt
  echo "fatal: update_ref failed for ref 'HEAD': cannot lock ref 'HEAD': is at race but expected base" >&2
  exit 128
fi
exec "$REAL_GIT" "$@"
SH
chmod +x "$shim/git"

set +e
(
  cd "$race_repo"
  REAL_GIT="$real_git" PATH="$shim:$PATH" scripts/merge-to-main.sh feature --force
) >"$tmp/race.out" 2>&1
race_status=$?
set -e
[ "$race_status" -ne 0 ] || { echo "race simulation should fail" >&2; exit 1; }
assert_contains "$tmp/race.out" "reset primary checkout index/worktree to HEAD"
[ -z "$(git -C "$race_repo" status --short)" ] ||
  { git -C "$race_repo" status --short >&2; exit 1; }
[ "$(cat "$race_repo/src/app.txt")" = "base" ] ||
  { echo "race failure left feature content in primary checkout" >&2; exit 1; }

echo "merge-to-main protected-mode integration tests passed"
