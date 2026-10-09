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

# Rename detection must not hide the preimage from the dirty-overlap guard.
# Refusal must preserve the user's local edit byte-for-byte.
commit_file "$dirty_repo" old-name.txt baseline
git -C "$dirty_repo" checkout -q -b rename-preimage
git -C "$dirty_repo" mv old-name.txt new-name.txt
git -C "$dirty_repo" commit -q -m "rename old name"
git -C "$dirty_repo" checkout -q main
printf '%s\n' 'precious local edit' >"$dirty_repo/old-name.txt"
set +e
(
  cd "$dirty_repo"
  scripts/merge-to-main.sh rename-preimage --force
) >"$tmp/dirty-rename-preimage.out" 2>&1
dirty_rename_status=$?
set -e
[ "$dirty_rename_status" -ne 0 ] || { echo "dirty rename-preimage merge should fail" >&2; exit 1; }
assert_contains "$tmp/dirty-rename-preimage.out" "old-name.txt"
[ "$(cat "$dirty_repo/old-name.txt")" = "precious local edit" ] ||
  { echo "dirty rename-preimage refusal erased the local edit" >&2; exit 1; }

# Changed-path and dirty-status transport is NUL-delimited; even a newline in
# a filename must remain protected rather than being split into fake paths.
newline_path=$'line\nbreak.txt'
printf 'baseline\n' >"$dirty_repo/$newline_path"
git -C "$dirty_repo" add "$newline_path"
git -C "$dirty_repo" commit -q -m "add newline path"
git -C "$dirty_repo" checkout -q -b newline-path-change
printf 'branch change\n' >"$dirty_repo/$newline_path"
git -C "$dirty_repo" add "$newline_path"
git -C "$dirty_repo" commit -q -m "change newline path"
git -C "$dirty_repo" checkout -q main
printf 'precious newline edit\n' >"$dirty_repo/$newline_path"
set +e
(
  cd "$dirty_repo"
  scripts/merge-to-main.sh newline-path-change --force
) >"$tmp/dirty-newline-path.out" 2>&1
dirty_newline_status=$?
set -e
[ "$dirty_newline_status" -ne 0 ] || { echo "dirty newline-path merge should fail" >&2; exit 1; }
[ "$(cat "$dirty_repo/$newline_path")" = "precious newline edit" ] ||
  { echo "dirty newline-path refusal erased the local edit" >&2; exit 1; }

# Ignored work is still work. A branch can force-track a path underneath an
# ignored directory; promotion must see the ignored preimage and refuse.
printf 'ignored/\n' >"$dirty_repo/.gitignore"
git -C "$dirty_repo" add .gitignore
git -C "$dirty_repo" commit -q -m "ignore local evidence directory"
git -C "$dirty_repo" checkout -q -b ignored-preimage
mkdir -p "$dirty_repo/ignored"
printf 'feature version\n' >"$dirty_repo/ignored/valuable.txt"
git -C "$dirty_repo" add -f ignored/valuable.txt
git -C "$dirty_repo" commit -q -m "force-track ignored path"
git -C "$dirty_repo" checkout -q main
mkdir -p "$dirty_repo/ignored"
printf 'local irreplaceable version\n' >"$dirty_repo/ignored/valuable.txt"
set +e
(
  cd "$dirty_repo"
  scripts/merge-to-main.sh ignored-preimage --force
) >"$tmp/dirty-ignored-preimage.out" 2>&1
dirty_ignored_status=$?
set -e
[ "$dirty_ignored_status" -ne 0 ] || { echo "dirty ignored-preimage merge should fail" >&2; exit 1; }
[ "$(cat "$dirty_repo/ignored/valuable.txt")" = "local irreplaceable version" ] ||
  { echo "dirty ignored-preimage refusal erased ignored local work" >&2; exit 1; }

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

# Permission guards must never follow a tracked symlink and chmod data outside
# the repository when an incoming branch replaces that link.
symlink_repo="$tmp/symlink-repo"
git_init "$symlink_repo"
mkdir -p "$symlink_repo/scripts"
cp "$script_dir/merge-to-main.sh" "$symlink_repo/scripts/merge-to-main.sh"
chmod +x "$symlink_repo/scripts/merge-to-main.sh"
external_target="$tmp/external-symlink-target.txt"
printf 'external\n' >"$external_target"
chmod 0444 "$external_target"
ln -s "$external_target" "$symlink_repo/link.txt"
git -C "$symlink_repo" add scripts/merge-to-main.sh link.txt
git -C "$symlink_repo" commit -q -m "add symlink"
git -C "$symlink_repo" branch -M main
git -C "$symlink_repo" checkout -q -b replace-symlink
rm "$symlink_repo/link.txt"
printf 'regular\n' >"$symlink_repo/link.txt"
git -C "$symlink_repo" add link.txt
git -C "$symlink_repo" commit -q -m "replace symlink"
git -C "$symlink_repo" checkout -q main
(
  cd "$symlink_repo"
  scripts/merge-to-main.sh replace-symlink --force >"$tmp/symlink-merge.out"
)
external_mode="$(stat -f '%Lp' "$external_target" 2>/dev/null || stat -c '%a' "$external_target")"
[ "$external_mode" = "444" ] || { echo "merge chmod followed symlink; external mode is $external_mode" >&2; exit 1; }

# Source-capsule rebases must protect ignored local bytes before a newer main
# starts tracking the same path.
source_collision_repo="$tmp/source-collision-repo"
git_init "$source_collision_repo"
mkdir -p "$source_collision_repo/scripts"
cp "$script_dir/merge-to-main.sh" "$source_collision_repo/scripts/merge-to-main.sh"
chmod +x "$source_collision_repo/scripts/merge-to-main.sh"
printf 'ignored-source/\n' >"$source_collision_repo/.gitignore"
git -C "$source_collision_repo" add scripts/merge-to-main.sh .gitignore
git -C "$source_collision_repo" commit -q -m "initial source collision"
git -C "$source_collision_repo" branch -M main
git -C "$source_collision_repo" branch staging/local
mkdir -p "$source_collision_repo/.capsules/staging"
git -C "$source_collision_repo" clone --no-local "$source_collision_repo" \
  "$source_collision_repo/.capsules/staging/local" >/dev/null
git -C "$source_collision_repo/.capsules/staging/local" remote rename origin source
git -C "$source_collision_repo/.capsules/staging/local" config user.name "Test User"
git -C "$source_collision_repo/.capsules/staging/local" config user.email "test@example.invalid"
git -C "$source_collision_repo/.capsules/staging/local" switch -q -c staging/local source/staging/local
touch "$source_collision_repo/.capsules/staging/local/.kitsoki-capsule"
mkdir -p "$source_collision_repo/.capsules/staging/local/ignored-source"
printf 'capsule local evidence\n' >"$source_collision_repo/.capsules/staging/local/ignored-source/valuable.txt"
mkdir -p "$source_collision_repo/ignored-source"
printf 'main tracked version\n' >"$source_collision_repo/ignored-source/valuable.txt"
git -C "$source_collision_repo" add -f ignored-source/valuable.txt
git -C "$source_collision_repo" commit -q -m "main tracks ignored source path"
source_collision_main="$(git -C "$source_collision_repo" rev-parse main)"
set +e
(
  cd "$source_collision_repo"
  scripts/merge-to-main.sh --gate true
) >"$tmp/source-collision.out" 2>&1
source_collision_status=$?
set -e
[ "$source_collision_status" -ne 0 ] || { echo "source ignored collision promotion should fail" >&2; exit 1; }
assert_contains "$tmp/source-collision.out" "would overwrite untracked or ignored source paths"
[ "$(cat "$source_collision_repo/.capsules/staging/local/ignored-source/valuable.txt")" = "capsule local evidence" ] ||
  { echo "source rebase overwrote ignored capsule evidence" >&2; exit 1; }
[ "$(git -C "$source_collision_repo" rev-parse main)" = "$source_collision_main" ] ||
  { echo "source collision refusal moved main" >&2; exit 1; }

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
git -C "$staging_repo" fetch -q "$staging_repo/.capsules/staging/local" staging/local:refs/heads/staging-candidate
git -C "$staging_repo" update-ref refs/heads/staging/local "$(git -C "$staging_repo/.capsules/staging/local" rev-parse HEAD)"

# A failed late status probe is unknown state, not evidence that the staging
# capsule stayed clean through the gate. Fail the second source-dir status
# call and prove promotion stops before protected main moves.
status_shim="$tmp/source-status-shim"
status_count_file="$tmp/source-status-count"
mkdir -p "$status_shim"
real_git="$(command -v git)"
cat >"$status_shim/git" <<'SH'
#!/usr/bin/env bash
set -euo pipefail
if [ "${1:-}" = -C ] && [ "${2:-}" = "${KITSOKI_FAIL_SOURCE_STATUS_DIR:-}" ] && [ "${3:-}" = status ]; then
  count=0
  [ ! -f "${KITSOKI_SOURCE_STATUS_COUNT_FILE:?}" ] || count="$(cat "$KITSOKI_SOURCE_STATUS_COUNT_FILE")"
  count=$((count + 1))
  printf '%s\n' "$count" >"$KITSOKI_SOURCE_STATUS_COUNT_FILE"
  [ "$count" -ne "${KITSOKI_FAIL_SOURCE_STATUS_N:?}" ] || exit 86
fi
exec "${KITSOKI_REAL_GIT:?}" "$@"
SH
chmod +x "$status_shim/git"
staging_status_main_before="$(git -C "$staging_repo" rev-parse main)"
staging_status_source_dir="$(git -C "$staging_repo/.capsules/staging/local" rev-parse --show-toplevel)"
set +e
(
  cd "$staging_repo"
  PATH="$status_shim:$PATH" \
    KITSOKI_REAL_GIT="$real_git" \
    KITSOKI_FAIL_SOURCE_STATUS_DIR="$staging_status_source_dir" \
    KITSOKI_SOURCE_STATUS_COUNT_FILE="$status_count_file" \
    KITSOKI_FAIL_SOURCE_STATUS_N=3 \
    scripts/merge-to-main.sh --gate true
) >"$tmp/staging-status-failure.out" 2>&1
staging_status_failure=$?
set -e
[ "$staging_status_failure" -ne 0 ] || { echo "source status failure should stop promotion" >&2; exit 1; }
assert_contains "$tmp/staging-status-failure.out" "could not inspect source dir status"
[ "$(git -C "$staging_repo" rev-parse main)" = "$staging_status_main_before" ] ||
  { echo "source status failure advanced main" >&2; exit 1; }

(
  cd "$staging_repo"
  scripts/merge-to-main.sh >"$tmp/staging.out"
)
assert_contains "$tmp/staging.out" "main ->"
[ "$(cat "$tmp/staging-gate-ran")" = "gate" ] ||
  { echo "default staging gate did not run" >&2; exit 1; }
[ "$(cat "$staging_repo/staged.txt")" = "staged" ] ||
  { echo "staging promotion did not update main" >&2; exit 1; }

staging_race_repo="$tmp/staging-race-repo"
git_init "$staging_race_repo"
mkdir -p "$staging_race_repo/scripts"
cp "$script_dir/merge-to-main.sh" "$staging_race_repo/scripts/merge-to-main.sh"
chmod +x "$staging_race_repo/scripts/merge-to-main.sh"
git -C "$staging_race_repo" add scripts/merge-to-main.sh
git -C "$staging_race_repo" commit -q -m "add merge helper"
commit_file "$staging_race_repo" base.txt base
git -C "$staging_race_repo" branch -M main
git -C "$staging_race_repo" branch staging/local
mkdir -p "$staging_race_repo/.capsules/staging"
git -C "$staging_race_repo" clone --no-local "$staging_race_repo" "$staging_race_repo/.capsules/staging/local" >/dev/null
git -C "$staging_race_repo/.capsules/staging/local" remote rename origin source
git -C "$staging_race_repo/.capsules/staging/local" config user.name "Test User"
git -C "$staging_race_repo/.capsules/staging/local" config user.email "test@example.invalid"
touch "$staging_race_repo/.capsules/staging/local/.kitsoki-capsule"
git -C "$staging_race_repo/.capsules/staging/local" switch -q -c staging/local source/staging/local
printf '%s\n' staged >"$staging_race_repo/.capsules/staging/local/staged.txt"
git -C "$staging_race_repo/.capsules/staging/local" add staged.txt
git -C "$staging_race_repo/.capsules/staging/local" commit -q -m "staged change"
staging_snapshot="$(git -C "$staging_race_repo/.capsules/staging/local" rev-parse HEAD)"
git -C "$staging_race_repo" fetch -q "$staging_race_repo/.capsules/staging/local" staging/local:refs/heads/staging-candidate
git -C "$staging_race_repo" update-ref refs/heads/staging/local "$staging_snapshot"

staging_advancer="$tmp/staging-advancer"
git -C "$staging_race_repo" clone --no-local "$staging_race_repo" "$staging_advancer" >/dev/null
git -C "$staging_advancer" config user.name "Test User"
git -C "$staging_advancer" config user.email "test@example.invalid"
git -C "$staging_advancer" switch -q -c staging/local origin/staging/local
printf '%s\n' concurrent >"$staging_advancer/concurrent.txt"
git -C "$staging_advancer" add concurrent.txt
git -C "$staging_advancer" commit -q -m "concurrent staging advance"
concurrent_staging="$(git -C "$staging_advancer" rev-parse HEAD)"
git -C "$staging_race_repo" fetch -q "$staging_advancer" HEAD:refs/heads/staging-race-advance
main_before_staging_race="$(git -C "$staging_race_repo" rev-parse main)"
set +e
(
  cd "$staging_race_repo"
  scripts/merge-to-main.sh --gate "git -C '$staging_race_repo' update-ref refs/heads/staging/local $concurrent_staging"
) >"$tmp/staging-race.out" 2>&1
staging_race_status=$?
set -e
[ "$staging_race_status" -ne 0 ] || { echo "staging race promotion should fail" >&2; exit 1; }
assert_contains "$tmp/staging-race.out" "staging/local advanced during promotion gate"
[ "$(git -C "$staging_race_repo" rev-parse main)" = "$main_before_staging_race" ] ||
  { echo "staging race advanced main" >&2; exit 1; }
[ "$(git -C "$staging_race_repo" rev-parse staging/local)" = "$concurrent_staging" ] ||
  { echo "staging race clobbered concurrent staging advancement" >&2; exit 1; }

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
assert_contains "$tmp/race.out" "restored only the proven-clean branch paths"
[ -z "$(git -C "$race_repo" status --short)" ] ||
  { git -C "$race_repo" status --short >&2; exit 1; }
[ "$(cat "$race_repo/src/app.txt")" = "base" ] ||
  { echo "race failure left feature content in primary checkout" >&2; exit 1; }

echo "merge-to-main protected-mode integration tests passed"
