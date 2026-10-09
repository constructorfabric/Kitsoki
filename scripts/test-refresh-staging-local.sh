#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
script="$script_dir/refresh-staging-local.sh"
sync_script="$script_dir/sync-main-from-remote.sh"

tmp="$(mktemp -d "${TMPDIR:-/tmp}/kitsoki-refresh-staging-test.XXXXXX")"
cleanup() {
  rm -rf "$tmp"
}
trap cleanup EXIT

fail() {
  echo "FAIL: $*" >&2
  exit 1
}

git_init() {
  git init -q --initial-branch=main "$1"
  git -C "$1" config user.name "Test User"
  git -C "$1" config user.email "test@example.invalid"
}

commit_file() {
  local repo="$1"
  local file="$2"
  local content="$3"
  mkdir -p "$(dirname "$repo/$file")"
  printf '%s\n' "$content" >"$repo/$file"
  git -C "$repo" add "$file"
  git -C "$repo" commit -q -m "$content"
}

assert_contains() {
  local file="$1"
  local needle="$2"
  if ! grep -Fq -- "$needle" "$file"; then
    echo "expected '$needle' in $file" >&2
    cat "$file" >&2
    exit 1
  fi
}

copy_scripts() {
  local repo="$1"
  mkdir -p "$repo/scripts"
  cp "$script" "$repo/scripts/refresh-staging-local.sh"
  cp "$sync_script" "$repo/scripts/sync-main-from-remote.sh"
  cp "$script_dir/dev-workspace.sh" "$repo/scripts/dev-workspace.sh"
  chmod +x "$repo/scripts/"*.sh
}

# A staging patch may already be embodied in main by a different patch
# sequence. Git then drops the staging commit as empty during rebase. The
# refresh must accept the preserved tree delta instead of treating the absent
# patch-id as lost work.
already_present_repo="$tmp/already-present-refresh"
git_init "$already_present_repo"
copy_scripts "$already_present_repo"
printf 'keep\nstale\n' >"$already_present_repo/shared.txt"
printf 'mode\n' >"$already_present_repo/mode.txt"
printf 'rename\n' >"$already_present_repo/old-name.txt"
git -C "$already_present_repo" add -A
git -C "$already_present_repo" commit -q -m initial
git -C "$already_present_repo" branch staging/local
git -C "$already_present_repo" switch -q staging/local
printf 'keep\n' >"$already_present_repo/shared.txt"
git -C "$already_present_repo" add shared.txt
git -C "$already_present_repo" commit -q -m 'staging removes stale line'
git -C "$already_present_repo" switch -q main
printf 'keep\n' >"$already_present_repo/shared.txt"
printf 'main-only\n' >"$already_present_repo/main-only.txt"
git -C "$already_present_repo" add shared.txt main-only.txt
git -C "$already_present_repo" commit -q -m 'main replaces stale line differently'

mkdir -p "$already_present_repo/.capsules/staging"
git clone -q --no-local "$already_present_repo" "$already_present_repo/.capsules/staging/local"
git -C "$already_present_repo/.capsules/staging/local" remote rename origin source
git -C "$already_present_repo/.capsules/staging/local" config user.name "Test User"
git -C "$already_present_repo/.capsules/staging/local" config user.email "test@example.invalid"
git -C "$already_present_repo/.capsules/staging/local" switch -q -c staging/local source/staging/local
touch "$already_present_repo/.capsules/staging/local/.kitsoki-capsule"
commit_file "$already_present_repo/.capsules/staging/local" capsule-only.txt capsule-only
chmod +x "$already_present_repo/.capsules/staging/local/mode.txt"
git -C "$already_present_repo/.capsules/staging/local" mv old-name.txt new-name.txt
printf '\000\001\002capsule-binary\377' >"$already_present_repo/.capsules/staging/local/capsule.bin"
git -C "$already_present_repo/.capsules/staging/local" add mode.txt new-name.txt capsule.bin
git -C "$already_present_repo/.capsules/staging/local" commit -q -m 'capsule mode rename and binary delta'
capsule_binary_hash="$(git hash-object "$already_present_repo/.capsules/staging/local/capsule.bin")"
already_present_original="$(git -C "$already_present_repo/.capsules/staging/local" rev-parse HEAD)"

already_present_out="$tmp/already-present-refresh.out"
if ! (
  cd "$already_present_repo"
  scripts/refresh-staging-local.sh --skip-remote --gate 'git diff --check'
) >"$already_present_out" 2>&1; then
  cat "$already_present_out" >&2
  fail "already-present staging refresh failed"
fi
assert_contains "$already_present_out" "staging/local ->"
git -C "$already_present_repo" merge-base --is-ancestor main staging/local ||
  fail "already-present refresh did not base staging on main"
[ "$(git -C "$already_present_repo" show staging/local:shared.txt)" = "keep" ] ||
  fail "already-present refresh dropped the main replacement"
[ "$(git -C "$already_present_repo" show staging/local:main-only.txt)" = "main-only" ] ||
  fail "already-present refresh dropped the main-only file"
[ "$(git -C "$already_present_repo" show staging/local:capsule-only.txt)" = "capsule-only" ] ||
  fail "already-present refresh dropped capsule-only work"
[ "$(git -C "$already_present_repo" ls-tree staging/local mode.txt | awk '{print $1}')" = "100755" ] ||
  fail "already-present refresh dropped capsule mode change"
git -C "$already_present_repo" cat-file -e staging/local:new-name.txt ||
  fail "already-present refresh dropped capsule rename destination"
if git -C "$already_present_repo" cat-file -e staging/local:old-name.txt 2>/dev/null; then
  fail "already-present refresh retained capsule rename source"
fi
[ "$(git -C "$already_present_repo" rev-parse staging/local:capsule.bin)" = "$capsule_binary_hash" ] ||
  fail "already-present refresh changed capsule binary content"
[ "$(git -C "$already_present_repo" rev-parse staging/local)" = "$(git -C "$already_present_repo/.capsules/staging/local" rev-parse HEAD)" ] ||
  fail "already-present primary staging and capsule HEAD differ"
[ -z "$(git -C "$already_present_repo/.capsules/staging/local" for-each-ref --format='%(refname)' refs/kitsoki/refresh/)" ] ||
  fail "successful refresh left temporary capsule recovery refs"
already_present_recovery_ref="refs/kitsoki/staging-capsule-recovery/$already_present_original"
[ "$(git -C "$already_present_repo" rev-parse "$already_present_recovery_ref")" = "$already_present_original" ] ||
  fail "successful refresh did not retain the original capsule in primary recovery refs"

# A completed conflict resolution lives in the capsule while primary staging
# still names the old snapshot. Resume imports it rather than replaying it.
resume_primary_before="$(git -C "$already_present_repo" rev-parse staging/local)"
commit_file "$already_present_repo/.capsules/staging/local" resumed.txt manually-resolved-rebase
resume_capsule_result="$(git -C "$already_present_repo/.capsules/staging/local" rev-parse HEAD)"
resume_out="$tmp/resume-refresh.out"
if ! (
  cd "$already_present_repo"
  scripts/refresh-staging-local.sh --skip-remote --resume --gate 'git diff --check'
) >"$resume_out" 2>&1; then
  cat "$resume_out" >&2
  fail "resume refresh failed"
fi
assert_contains "$resume_out" "(resumed)"
[ "$(git -C "$already_present_repo" rev-parse staging/local)" = "$resume_capsule_result" ] ||
  fail "resume refresh did not import the manually completed capsule rebase"
[ "$(git -C "$already_present_repo" rev-parse staging/local)" != "$resume_primary_before" ] ||
  fail "resume refresh left the old primary staging snapshot in place"

# Content-addressed recovery refs are idempotent. The first no-op refresh may
# anchor the current unpromoted staging tip; repeating the same no-op must not
# grow the recovery-ref set again.
(
  cd "$already_present_repo"
  scripts/refresh-staging-local.sh --skip-remote --gate 'git diff --check'
) >"$tmp/already-present-noop-1.out" 2>&1
noop_recovery_count="$(git -C "$already_present_repo" for-each-ref \
  --format='%(refname)' refs/kitsoki/staging-capsule-recovery/ | wc -l | tr -d ' ')"
(
  cd "$already_present_repo"
  scripts/refresh-staging-local.sh --skip-remote --gate 'git diff --check'
) >"$tmp/already-present-noop-2.out" 2>&1
[ "$(git -C "$already_present_repo" for-each-ref \
  --format='%(refname)' refs/kitsoki/staging-capsule-recovery/ | wc -l | tr -d ' ')" = "$noop_recovery_count" ] ||
  fail "repeated no-op refresh leaked a new primary recovery ref"

# The old source/staging tracking tip is state too. It can carry work not
# reachable from capsule HEAD after an interrupted refresh. Prove a successful
# refresh anchors that orphaned tip outside the capsule before force-fetching
# the tracking ref and deleting temporary capsule-local refs.
upstream_only_repo="$tmp/upstream-only-recovery"
git_init "$upstream_only_repo"
copy_scripts "$upstream_only_repo"
commit_file "$upstream_only_repo" base.txt base
git -C "$upstream_only_repo" branch staging/local
mkdir -p "$upstream_only_repo/.capsules/staging"
git clone -q --no-local "$upstream_only_repo" "$upstream_only_repo/.capsules/staging/local"
git -C "$upstream_only_repo/.capsules/staging/local" remote rename origin source
git -C "$upstream_only_repo/.capsules/staging/local" config user.name "Test User"
git -C "$upstream_only_repo/.capsules/staging/local" config user.email "test@example.invalid"
git -C "$upstream_only_repo/.capsules/staging/local" switch -q -c staging/local source/staging/local
touch "$upstream_only_repo/.capsules/staging/local/.kitsoki-capsule"
git -C "$upstream_only_repo/.capsules/staging/local" switch -q -c upstream-only
commit_file "$upstream_only_repo/.capsules/staging/local" orphaned-upstream.txt preserved
upstream_only_tip="$(git -C "$upstream_only_repo/.capsules/staging/local" rev-parse HEAD)"
git -C "$upstream_only_repo/.capsules/staging/local" update-ref \
  refs/remotes/source/staging/local "$upstream_only_tip"
git -C "$upstream_only_repo/.capsules/staging/local" switch -q staging/local
git -C "$upstream_only_repo/.capsules/staging/local" branch -D upstream-only >/dev/null
(
  cd "$upstream_only_repo"
  scripts/refresh-staging-local.sh --skip-remote --gate 'git diff --check'
) >"$tmp/upstream-only-refresh.out" 2>&1
upstream_only_recovery_ref="refs/kitsoki/staging-capsule-recovery/$upstream_only_tip"
[ "$(git -C "$upstream_only_repo" rev-parse "$upstream_only_recovery_ref")" = "$upstream_only_tip" ] ||
  fail "refresh did not retain the old capsule upstream in the primary repository"
[ "$(git -C "$upstream_only_repo" show "$upstream_only_recovery_ref:orphaned-upstream.txt")" = "preserved" ] ||
  fail "old capsule upstream recovery ref lost its unique tree content"
[ "$(git -C "$upstream_only_repo/.capsules/staging/local" rev-parse refs/remotes/source/staging/local)" = \
  "$(git -C "$upstream_only_repo" rev-parse staging/local)" ] ||
  fail "refresh did not converge the capsule tracking ref after preserving its old tip"
[ -z "$(git -C "$upstream_only_repo/.capsules/staging/local" for-each-ref \
  --format='%(refname)' refs/kitsoki/refresh/)" ] ||
  fail "successful upstream-only refresh left temporary capsule recovery refs"

# Ignored local evidence is invisible to ordinary status but Git can overwrite
# it when a refreshed branch begins tracking the same path. Refuse before the
# first rebase and leave both sides intact.
ignored_collision_repo="$tmp/ignored-collision-refresh"
git_init "$ignored_collision_repo"
copy_scripts "$ignored_collision_repo"
printf 'ignored/\n' >"$ignored_collision_repo/.gitignore"
git -C "$ignored_collision_repo" add .gitignore
git -C "$ignored_collision_repo" commit -q -m initial
git -C "$ignored_collision_repo" branch staging/local
mkdir -p "$ignored_collision_repo/.capsules/staging"
git clone -q --no-local "$ignored_collision_repo" "$ignored_collision_repo/.capsules/staging/local"
git -C "$ignored_collision_repo/.capsules/staging/local" remote rename origin source
git -C "$ignored_collision_repo/.capsules/staging/local" config user.name "Test User"
git -C "$ignored_collision_repo/.capsules/staging/local" config user.email "test@example.invalid"
git -C "$ignored_collision_repo/.capsules/staging/local" switch -q -c staging/local source/staging/local
touch "$ignored_collision_repo/.capsules/staging/local/.kitsoki-capsule"
mkdir -p "$ignored_collision_repo/.capsules/staging/local/ignored"
printf 'local irreplaceable version\n' >"$ignored_collision_repo/.capsules/staging/local/ignored/valuable.txt"
git -C "$ignored_collision_repo" switch -q staging/local
mkdir -p "$ignored_collision_repo/ignored"
printf 'staging tracked version\n' >"$ignored_collision_repo/ignored/valuable.txt"
git -C "$ignored_collision_repo" add -f ignored/valuable.txt
git -C "$ignored_collision_repo" commit -q -m 'track formerly ignored path'
ignored_collision_staging="$(git -C "$ignored_collision_repo" rev-parse HEAD)"
git -C "$ignored_collision_repo" switch -q main
ignored_collision_capsule="$(git -C "$ignored_collision_repo/.capsules/staging/local" rev-parse HEAD)"
set +e
(
  cd "$ignored_collision_repo"
  scripts/refresh-staging-local.sh --skip-remote --gate 'git diff --check'
) >"$tmp/ignored-collision-refresh.out" 2>&1
ignored_collision_status=$?
set -e
[ "$ignored_collision_status" -ne 0 ] || fail "ignored collision refresh should fail"
assert_contains "$tmp/ignored-collision-refresh.out" "would overwrite untracked or ignored capsule paths"
[ "$(cat "$ignored_collision_repo/.capsules/staging/local/ignored/valuable.txt")" = "local irreplaceable version" ] ||
  fail "ignored collision refresh overwrote local capsule evidence"
[ "$(git -C "$ignored_collision_repo/.capsules/staging/local" rev-parse HEAD)" = "$ignored_collision_capsule" ] ||
  fail "ignored collision refresh moved the capsule branch"
[ "$(git -C "$ignored_collision_repo" rev-parse staging/local)" = "$ignored_collision_staging" ] ||
  fail "ignored collision refresh moved primary staging"

# A failed status probe is unknown state, never evidence that the capsule is
# clean. Simulate ENOSPC/lock-style Git failure and prove refresh stops before
# touching either staging ref.
fake_git_dir="$tmp/fake-git"
mkdir -p "$fake_git_dir"
cat >"$fake_git_dir/git" <<'SH'
#!/usr/bin/env bash
if [ "${1:-}" = -C ] && [ "${2:-}" = "${KITSOKI_FAIL_STATUS_DIR:-}" ] && [ "${3:-}" = status ]; then
  exit 86
fi
exec "${KITSOKI_REAL_GIT:?}" "$@"
SH
chmod +x "$fake_git_dir/git"
real_git="$(command -v git)"
status_failure_primary="$(git -C "$already_present_repo" rev-parse staging/local)"
status_failure_capsule="$(git -C "$already_present_repo/.capsules/staging/local" rev-parse HEAD)"
status_failure_out="$tmp/status-failure-refresh.out"
status_failure_capsule_dir="$(cd "$already_present_repo/.capsules/staging/local" && pwd -P)"
set +e
(
  cd "$already_present_repo"
  PATH="$fake_git_dir:$PATH" \
    KITSOKI_REAL_GIT="$real_git" \
    KITSOKI_FAIL_STATUS_DIR="$status_failure_capsule_dir" \
    scripts/refresh-staging-local.sh --skip-remote
) >"$status_failure_out" 2>&1
status_failure_code=$?
set -e
[ "$status_failure_code" -eq 1 ] ||
  fail "expected status-probe failure to stop refresh, got $status_failure_code"
assert_contains "$status_failure_out" "could not inspect staging capsule status"
[ "$(git -C "$already_present_repo" rev-parse staging/local)" = "$status_failure_primary" ] ||
  fail "status-probe failure moved primary staging"
[ "$(git -C "$already_present_repo/.capsules/staging/local" rev-parse HEAD)" = "$status_failure_capsule" ] ||
  fail "status-probe failure moved capsule staging"

# The tree-delta fallback must not weaken the fail-closed behavior. A gate that
# deliberately rewinds the capsule to main removes the captured staging delta.
# It also recreates the staged file only as an uncommitted worktree change;
# that must not fool the result-commit proof. Refresh must refuse to import it
# and leave the primary staging ref untouched.
lost_delta_repo="$tmp/lost-delta-refresh"
git_init "$lost_delta_repo"
copy_scripts "$lost_delta_repo"
printf 'base\n' >"$lost_delta_repo/README.md"
git -C "$lost_delta_repo" add -A
git -C "$lost_delta_repo" commit -q -m initial
git -C "$lost_delta_repo" branch staging/local
git -C "$lost_delta_repo" switch -q staging/local
commit_file "$lost_delta_repo" staged.txt staged-delta
lost_delta_start="$(git -C "$lost_delta_repo" rev-parse staging/local)"
git -C "$lost_delta_repo" switch -q main
commit_file "$lost_delta_repo" main.txt main-delta

mkdir -p "$lost_delta_repo/.capsules/staging"
git clone -q --no-local "$lost_delta_repo" "$lost_delta_repo/.capsules/staging/local"
git -C "$lost_delta_repo/.capsules/staging/local" remote rename origin source
git -C "$lost_delta_repo/.capsules/staging/local" config user.name "Test User"
git -C "$lost_delta_repo/.capsules/staging/local" config user.email "test@example.invalid"
git -C "$lost_delta_repo/.capsules/staging/local" switch -q -c staging/local source/staging/local
touch "$lost_delta_repo/.capsules/staging/local/.kitsoki-capsule"

lost_delta_out="$tmp/lost-delta-refresh.out"
set +e
(
  cd "$lost_delta_repo"
  scripts/refresh-staging-local.sh --skip-remote --gate 'git reset --hard source/main && printf "staged-delta\n" >staged.txt'
) >"$lost_delta_out" 2>&1
lost_delta_status=$?
set -e
[ "$lost_delta_status" -eq 1 ] ||
  fail "expected lost-delta refresh to stop with exit 1, got $lost_delta_status"
assert_contains "$lost_delta_out" "staging refresh gate left uncommitted changes"
[ "$(git -C "$lost_delta_repo" rev-parse staging/local)" = "$lost_delta_start" ] ||
  fail "lost-delta refresh overwrote primary staging"
[ "$(git -C "$lost_delta_repo" show staging/local:staged.txt)" = "staged-delta" ] ||
  fail "lost-delta refresh removed captured staging content"

# Preserve capsule-only commits independently from the primary snapshot. A gate
# can reset to the exact captured staging commit, satisfying the primary
# ancestry check while still discarding pre-refresh capsule work.
git -C "$lost_delta_repo/.capsules/staging/local" reset --hard source/staging/local >/dev/null
git -C "$lost_delta_repo/.capsules/staging/local" clean -fd >/dev/null
touch "$lost_delta_repo/.capsules/staging/local/.kitsoki-capsule"
commit_file "$lost_delta_repo/.capsules/staging/local" capsule-only.txt capsule-only-delta
capsule_loss_original="$(git -C "$lost_delta_repo/.capsules/staging/local" rev-parse HEAD)"
capsule_loss_out="$tmp/capsule-loss-refresh.out"
set +e
(
  cd "$lost_delta_repo"
  scripts/refresh-staging-local.sh --skip-remote --gate 'git reset --hard source/staging/local'
) >"$capsule_loss_out" 2>&1
capsule_loss_status=$?
set -e
[ "$capsule_loss_status" -eq 1 ] ||
  fail "expected capsule-loss refresh to stop with exit 1, got $capsule_loss_status"
assert_contains "$capsule_loss_out" "refresh gate moved capsule HEAD"
[ "$(git -C "$lost_delta_repo" rev-parse staging/local)" = "$lost_delta_start" ] ||
  fail "capsule-loss refresh moved primary staging"
[ "$(git -C "$lost_delta_repo" show staging/local:staged.txt)" = "staged-delta" ] ||
  fail "capsule-loss refresh removed primary staging content"
[ "$(git -C "$lost_delta_repo/.capsules/staging/local" rev-parse HEAD)" = "$capsule_loss_original" ] ||
  fail "capsule-loss refusal did not restore the active capsule branch"
[ "$(git -C "$lost_delta_repo/.capsules/staging/local" show HEAD:capsule-only.txt)" = "capsule-only-delta" ] ||
  fail "capsule-loss refusal hid the original capsule-only content"
(
  cd "$lost_delta_repo"
  scripts/refresh-staging-local.sh --skip-remote --gate 'git diff --check'
) >"$tmp/capsule-loss-rerun.out" 2>&1
[ "$(git -C "$lost_delta_repo" show staging/local:capsule-only.txt)" = "capsule-only-delta" ] ||
  fail "rerun after capsule-loss refusal omitted the restored capsule-only work"

# Ordinary rebase flattens merge commits. If a staging merge carries
# resolution-only content, finding all non-merge parent patches is not enough:
# the complete captured tree delta must replace a flattened rebase result with
# a signed reconciliation merge that preserves the resolution and both parent
# histories.
merge_delta_repo="$tmp/merge-delta-refresh"
git_init "$merge_delta_repo"
copy_scripts "$merge_delta_repo"
printf 'base\n' >"$merge_delta_repo/README.md"
git -C "$merge_delta_repo" add -A
git -C "$merge_delta_repo" commit -q -m initial
git -C "$merge_delta_repo" branch staging/local
git -C "$merge_delta_repo" branch side
git -C "$merge_delta_repo" switch -q staging/local
commit_file "$merge_delta_repo" staged-parent.txt staged-parent
git -C "$merge_delta_repo" switch -q side
commit_file "$merge_delta_repo" side-parent.txt side-parent
git -C "$merge_delta_repo" switch -q staging/local
git -C "$merge_delta_repo" merge -q --no-ff --no-commit side
printf 'merge-resolution-only\n' >"$merge_delta_repo/resolution-only.txt"
git -C "$merge_delta_repo" add resolution-only.txt
git -C "$merge_delta_repo" commit -q -m 'merge side with resolution-only content'
merge_delta_start="$(git -C "$merge_delta_repo" rev-parse staging/local)"
git -C "$merge_delta_repo" switch -q main
commit_file "$merge_delta_repo" main.txt main-after-merge

mkdir -p "$merge_delta_repo/.capsules/staging"
git clone -q --no-local "$merge_delta_repo" "$merge_delta_repo/.capsules/staging/local"
git -C "$merge_delta_repo/.capsules/staging/local" remote rename origin source
git -C "$merge_delta_repo/.capsules/staging/local" config user.name "Test User"
git -C "$merge_delta_repo/.capsules/staging/local" config user.email "test@example.invalid"
git -C "$merge_delta_repo/.capsules/staging/local" switch -q -c staging/local source/staging/local
touch "$merge_delta_repo/.capsules/staging/local/.kitsoki-capsule"

merge_delta_out="$tmp/merge-delta-refresh.out"
if ! (
  cd "$merge_delta_repo"
  scripts/refresh-staging-local.sh --skip-remote --gate 'git diff --check'
) >"$merge_delta_out" 2>&1; then
  cat "$merge_delta_out" >&2
  fail "merge-resolution reconciliation refresh failed"
fi
assert_contains "$merge_delta_out" "materializing a signed reconciliation merge"
git -C "$merge_delta_repo" merge-base --is-ancestor main staging/local ||
  fail "merge-resolution reconciliation is not based on main"
[ "$(git -C "$merge_delta_repo" show staging/local:resolution-only.txt)" = "merge-resolution-only" ] ||
  fail "merge-resolution reconciliation removed resolution-only content"
[ "$(git -C "$merge_delta_repo" show staging/local:main.txt)" = "main-after-merge" ] ||
  fail "merge-resolution reconciliation dropped main content"
[ "$(git -C "$merge_delta_repo" rev-parse staging/local)" = "$(git -C "$merge_delta_repo/.capsules/staging/local" rev-parse HEAD)" ] ||
  fail "merge-resolution reconciliation left primary staging and capsule apart"
merge_delta_second_parent="$(git -C "$merge_delta_repo" rev-parse staging/local^2)"
[ "$merge_delta_second_parent" = "$merge_delta_start" ] ||
  fail "merge-resolution reconciliation did not retain exact staging history as second parent"

# Capsule-only merge resolutions need protection during the first rebase too.
# Advance primary staging after cloning, then put a resolution-only merge in the
# stale capsule. Rebasing that capsule onto the newer snapshot flattens the
# merge; refresh must materialize the independently proven tree and retain the
# exact original capsule history as a durable primary recovery ref.
capsule_merge_repo="$tmp/capsule-merge-refresh"
git_init "$capsule_merge_repo"
copy_scripts "$capsule_merge_repo"
printf 'base\n' >"$capsule_merge_repo/README.md"
git -C "$capsule_merge_repo" add -A
git -C "$capsule_merge_repo" commit -q -m initial
git -C "$capsule_merge_repo" branch staging/local
mkdir -p "$capsule_merge_repo/.capsules/staging"
git clone -q --no-local "$capsule_merge_repo" "$capsule_merge_repo/.capsules/staging/local"
git -C "$capsule_merge_repo/.capsules/staging/local" remote rename origin source
git -C "$capsule_merge_repo/.capsules/staging/local" config user.name "Test User"
git -C "$capsule_merge_repo/.capsules/staging/local" config user.email "test@example.invalid"
git -C "$capsule_merge_repo/.capsules/staging/local" switch -q -c staging/local source/staging/local
touch "$capsule_merge_repo/.capsules/staging/local/.kitsoki-capsule"
git -C "$capsule_merge_repo/.capsules/staging/local" branch capsule-side
commit_file "$capsule_merge_repo/.capsules/staging/local" capsule-parent.txt capsule-parent
git -C "$capsule_merge_repo/.capsules/staging/local" switch -q capsule-side
commit_file "$capsule_merge_repo/.capsules/staging/local" capsule-side.txt capsule-side
git -C "$capsule_merge_repo/.capsules/staging/local" switch -q staging/local
git -C "$capsule_merge_repo/.capsules/staging/local" merge -q --no-ff --no-commit capsule-side
printf 'capsule-resolution-only\n' >"$capsule_merge_repo/.capsules/staging/local/capsule-resolution-only.txt"
git -C "$capsule_merge_repo/.capsules/staging/local" add capsule-resolution-only.txt
git -C "$capsule_merge_repo/.capsules/staging/local" commit -q -m 'capsule merge with resolution-only content'
capsule_merge_original="$(git -C "$capsule_merge_repo/.capsules/staging/local" rev-parse HEAD)"
git -C "$capsule_merge_repo" switch -q staging/local
commit_file "$capsule_merge_repo" primary-advance.txt primary-advance
capsule_merge_staging="$(git -C "$capsule_merge_repo" rev-parse staging/local)"
git -C "$capsule_merge_repo" switch -q main

capsule_merge_out="$tmp/capsule-merge-refresh.out"
if ! (
  cd "$capsule_merge_repo"
  scripts/refresh-staging-local.sh --skip-remote --gate 'git diff --check'
) >"$capsule_merge_out" 2>&1; then
  cat "$capsule_merge_out" >&2
  fail "capsule merge-resolution reconciliation refresh failed"
fi
assert_contains "$capsule_merge_out" "materializing a signed reconciliation merge"
[ "$(git -C "$capsule_merge_repo" show staging/local:capsule-resolution-only.txt)" = "capsule-resolution-only" ] ||
  fail "capsule merge-resolution reconciliation dropped resolution-only content"
[ "$(git -C "$capsule_merge_repo" show staging/local:primary-advance.txt)" = "primary-advance" ] ||
  fail "capsule merge-resolution reconciliation dropped newer primary staging content"
capsule_merge_recovery="refs/kitsoki/staging-capsule-recovery/$capsule_merge_original"
[ "$(git -C "$capsule_merge_repo" rev-parse "$capsule_merge_recovery")" = "$capsule_merge_original" ] ||
  fail "capsule merge-resolution reconciliation did not retain the original capsule ref"

local_repo="$tmp/local-refresh"
git_init "$local_repo"
copy_scripts "$local_repo"
printf '.kitsoki.local.yaml\n' >"$local_repo/.gitignore"
cat >"$local_repo/Makefile" <<'MK'
test:
	@true
web-dev:
	@true
install:
	@true
site-dev:
	@true
MK
printf 'base\n' >"$local_repo/README.md"
printf 'default_profile: codex-native\nharness_profiles:\n  codex-native:\n    backend: codex\n' >"$local_repo/.kitsoki.local.yaml"
git -C "$local_repo" add -A
git -C "$local_repo" commit -q -m initial
git -C "$local_repo" branch staging/local

(
  cd "$local_repo"
  scripts/dev-workspace.sh create --root .capsules/staging --id local \
    --branch staging/local --base staging/local --target main --no-bootstrap >/dev/null
)

commit_file "$local_repo/.capsules/staging/local" staged.txt staged
# A long-lived staging capsule can retain a rebased source/staging tracking
# ref.  Make it point to a commit which diverges from the primary staging ref:
# the refresh must force-update only that capsule-private remote-tracking ref,
# then still import through the existing snapshot and primary CAS checks.
stale_source="$tmp/stale-staging-source"
git clone -q --no-local "$local_repo" "$stale_source"
git -C "$stale_source" config user.name "Test User"
git -C "$stale_source" config user.email "test@example.invalid"
git -C "$stale_source" switch -q staging/local
commit_file "$stale_source" stale-tracking.txt stale-source-tracking-divergence
stale_tracking_head="$(git -C "$stale_source" rev-parse HEAD)"
git -C "$local_repo/.capsules/staging/local" fetch -q "$stale_source" \
  "HEAD:refs/kitsoki/test-stale-source-staging"
git -C "$local_repo/.capsules/staging/local" update-ref \
  refs/remotes/source/staging/local "$stale_tracking_head"

commit_file "$local_repo" main.txt main-update
primary_staging_start="$(git -C "$local_repo" rev-parse staging/local)"

local_out="$tmp/local-refresh.out"
(
  cd "$local_repo"
  scripts/refresh-staging-local.sh --skip-remote --gate 'test "$GIT_EDITOR" = true && test "$GIT_SEQUENCE_EDITOR" = true && test "$GIT_MERGE_AUTOEDIT" = no && test "$GIT_PAGER" = cat && git diff --check'
) >"$local_out" 2>&1
assert_contains "$local_out" "staging/local ->"
assert_contains "$local_out" "copied .kitsoki.local.yaml into staging capsule"
git -C "$local_repo" merge-base --is-ancestor main staging/local ||
  fail "refreshed staging/local is not based on local main"
[ "$(git -C "$local_repo" show staging/local:main.txt)" = "main-update" ] ||
  fail "refreshed staging/local did not include local main"
[ "$(git -C "$local_repo" show staging/local:staged.txt)" = "staged" ] ||
  fail "refreshed staging/local dropped staged work"
[ "$(git -C "$local_repo" rev-parse staging/local)" = "$(git -C "$local_repo/.capsules/staging/local" rev-parse HEAD)" ] ||
  fail "primary staging/local and staging capsule HEAD differ"
[ "$(git -C "$local_repo/.capsules/staging/local" rev-parse refs/remotes/source/staging/local)" = "$primary_staging_start" ] ||
  fail "refresh did not converge the capsule-private source/staging tracking ref"
cmp "$local_repo/.kitsoki.local.yaml" "$local_repo/.capsules/staging/local/.kitsoki.local.yaml" >/dev/null ||
  fail "refresh did not copy .kitsoki.local.yaml into staging capsule"

# Main and staging are one atomic refresh input. If main advances during the
# gate, refuse to publish a staging result based on the captured older main.
commit_file "$local_repo/.capsules/staging/local" after-main-snapshot.txt capsule-after-main-snapshot
main_advancer="$tmp/main-advancer"
git clone -q --no-local "$local_repo" "$main_advancer"
git -C "$main_advancer" config user.name "Test User"
git -C "$main_advancer" config user.email "test@example.invalid"
git -C "$main_advancer" switch -q main
commit_file "$main_advancer" concurrent-main.txt concurrent-main-advance
concurrent_main="$(git -C "$main_advancer" rev-parse HEAD)"
git -C "$local_repo" fetch -q "$main_advancer" "HEAD:refs/kitsoki/test-concurrent-main"
main_race_staging="$(git -C "$local_repo" rev-parse staging/local)"
main_race_out="$tmp/concurrent-main-advance.out"
set +e
(
  cd "$local_repo"
  scripts/refresh-staging-local.sh --skip-remote --gate "git -C '$local_repo' update-ref refs/heads/main $concurrent_main"
) >"$main_race_out" 2>&1
main_race_status=$?
set -e
[ "$main_race_status" -eq 1 ] ||
  fail "expected concurrent main advance to stop refresh, got $main_race_status"
assert_contains "$main_race_out" "main advanced during refresh"
[ "$(git -C "$local_repo" rev-parse main)" = "$concurrent_main" ] ||
  fail "concurrent main advance did not remain authoritative"
[ "$(git -C "$local_repo" rev-parse staging/local)" = "$main_race_staging" ] ||
  fail "concurrent main advance imported stale staging"
main_race_candidate="$(git -C "$local_repo" for-each-ref \
  --format='%(refname)' refs/kitsoki/refresh/ | grep '/import$' | tail -1)"
[ -n "$main_race_candidate" ] ||
  fail "concurrent main refusal did not retain its proven import candidate"
git -C "$local_repo" rev-parse --verify "$main_race_candidate^{tree}" >/dev/null ||
  fail "retained main-race import candidate has no readable tree"
assert_contains "$main_race_out" "preserved proven import candidate at $main_race_candidate"

# A refresh must preserve the captured primary staging input.  Create a newer
# primary staging commit, advance it from the gate (after the snapshot), and
# prove refresh refuses rather than replacing it with the stale capsule result.
commit_file "$local_repo/.capsules/staging/local" after-snapshot.txt capsule-after-snapshot
advancer="$tmp/staging-advancer"
git clone -q --no-local "$local_repo" "$advancer"
git -C "$advancer" config user.name "Test User"
git -C "$advancer" config user.email "test@example.invalid"
git -C "$advancer" switch -q staging/local
commit_file "$advancer" concurrent.txt concurrent-primary-advance
concurrent_head="$(git -C "$advancer" rev-parse HEAD)"
# A real workspace merge has already imported the object before it advances the
# primary ref.  Make the deterministic race use the same ordering.
git -C "$local_repo" fetch -q "$advancer" "HEAD:refs/kitsoki/test-concurrent-staging"
concurrent_out="$tmp/concurrent-staging-advance.out"
set +e
(
  cd "$local_repo"
  scripts/refresh-staging-local.sh --skip-remote --gate "git -C '$local_repo' update-ref refs/heads/staging/local $concurrent_head"
) >"$concurrent_out" 2>&1
concurrent_status=$?
set -e
[ "$concurrent_status" -eq 1 ] ||
  fail "expected concurrent staging advance to stop refresh with exit 1, got $concurrent_status"
assert_contains "$concurrent_out" "staging branch advanced during refresh"
[ "$(git -C "$local_repo" rev-parse staging/local)" = "$concurrent_head" ] ||
  fail "refresh overwrote the newer primary staging ref"
[ "$(git -C "$local_repo" show staging/local:concurrent.txt)" = "concurrent-primary-advance" ] ||
  fail "refresh dropped the concurrent primary staging commit"

printf 'default_profile: updated\n' >"$local_repo/.kitsoki.local.yaml"
chmod u-w "$local_repo/.capsules/staging/local/.kitsoki.local.yaml"
(
  cd "$local_repo"
  scripts/refresh-staging-local.sh --skip-remote --gate 'git diff --check'
) >"$local_out" 2>&1
cmp "$local_repo/.kitsoki.local.yaml" "$local_repo/.capsules/staging/local/.kitsoki.local.yaml" >/dev/null ||
  fail "refresh did not overwrite read-only .kitsoki.local.yaml in staging capsule"
config_mode="$(python3 - "$local_repo/.capsules/staging/local/.kitsoki.local.yaml" <<'PY'
import os
import stat
import sys
print("w" if os.stat(sys.argv[1]).st_mode & stat.S_IWUSR else "-")
PY
)"
[ "$config_mode" = "-" ] || fail "refresh did not restore read-only mode on .kitsoki.local.yaml"

printf 'scratch\n' >"$local_repo/.capsules/staging/local/scratch.txt"
dirty_out="$tmp/dirty-capsule.out"
set +e
(
  cd "$local_repo"
  scripts/refresh-staging-local.sh --skip-remote
) >"$dirty_out" 2>&1
dirty_status=$?
set -e
[ "$dirty_status" -eq 1 ] ||
  fail "expected dirty staging capsule to stop with exit 1, got $dirty_status"
assert_contains "$dirty_out" "staging capsule has uncommitted changes"
assert_contains "$dirty_out" "Useful next steps"
assert_contains "$dirty_out" "--dirty-action preserve"
[ -f "$local_repo/.capsules/staging/local/scratch.txt" ] ||
  fail "dirty abort removed the untracked file"

default_out="$tmp/dirty-capsule-default.out"
if ! printf '\n' | script -q /dev/null bash -c '
  cd "$1"
  scripts/refresh-staging-local.sh --skip-remote --dirty-action prompt --gate "git diff --check"
' bash "$local_repo" >"$default_out" 2>&1; then
  cat "$default_out" >&2
  fail "interactive default move refresh failed"
fi
assert_contains "$default_out" "Move changes to a new managed capsule, commit them, clean, and continue (default)"
assert_contains "$default_out" "moved dirty staging-capsule changes to a committed recovery capsule"
default_recovery_dir="$(sed -n 's/^  workspace: //p' "$default_out" | tail -1 | tr -d '\r')"
[ -n "$default_recovery_dir" ] || fail "default move did not report a recovery workspace"
[ "$(git -C "$default_recovery_dir" show HEAD:scratch.txt)" = "scratch" ] ||
  fail "default move did not commit the untracked staging file"

printf 'scratch-explicit\n' >"$local_repo/.capsules/staging/local/scratch.txt"
printf 'staged move state\n' >"$local_repo/.capsules/staging/local/README.md"
git -C "$local_repo/.capsules/staging/local" add README.md
printf 'tracked move survives external diff\n' >"$local_repo/.capsules/staging/local/README.md"
git -C "$local_repo/.capsules/staging/local" config diff.external /usr/bin/true
move_out="$tmp/dirty-capsule-move.out"
(
  cd "$local_repo"
  scripts/refresh-staging-local.sh --skip-remote --dirty-action move --gate 'git diff --check'
) >"$move_out" 2>&1
assert_contains "$move_out" "moved dirty staging-capsule changes to a committed recovery capsule"
recovery_dir="$(sed -n 's/^  workspace: //p' "$move_out" | tail -1)"
move_snapshot_ref="$(sed -n 's/^  snapshot ref: //p' "$move_out" | tail -1)"
move_recovery_ref="$(sed -n 's/^  recovery ref: //p' "$move_out" | tail -1)"
move_quarantine="$(sed -n 's/^  original quarantine: //p' "$move_out" | tail -1)"
[ -n "$recovery_dir" ] || fail "move did not report a recovery workspace"
[ -f "$recovery_dir/.kitsoki-dev-workspace.json" ] ||
  fail "move did not create a managed recovery workspace"
[ "$(git -C "$recovery_dir" show HEAD:scratch.txt)" = "scratch-explicit" ] ||
  fail "move did not commit the untracked staging file"
[ "$(git -C "$recovery_dir" show HEAD:README.md)" = "tracked move survives external diff" ] ||
  fail "move lost a tracked edit hidden by diff.external"
[ "$(git -C "$local_repo" show "$move_recovery_ref:README.md")" = "tracked move survives external diff" ] ||
  fail "primary dirty-recovery ref lost tracked staging work"
[ "$(git -C "$local_repo" show "$move_recovery_ref:scratch.txt")" = "scratch-explicit" ] ||
  fail "primary dirty-recovery ref lost untracked staging work"
[ "$(git -C "$local_repo" show "$move_snapshot_ref^2:README.md")" = "staged move state" ] ||
  fail "primary dirty-snapshot ref lost exact staged staging state"
[ "$(cat "$move_quarantine/README.md")" = "tracked move survives external diff" ] ||
  fail "move did not retain the original staging quarantine"
[ "$(git -C "$local_repo/.capsules/staging/local" show HEAD:README.md)" = \
  "$(cat "$local_repo/.capsules/staging/local/README.md")" ] ||
  fail "move did not clean the original tracked file back to HEAD after recovery"
git -C "$local_repo/.capsules/staging/local" config --unset-all diff.external >/dev/null 2>&1 || true
[ -z "$(git -C "$local_repo/.capsules/staging/local" status --porcelain --untracked-files=all |
  grep -Ev '^[?][?] (\.kitsoki-capsule|\.kitsoki-clone|capsule-manifest.json|\.kitsoki-dev-workspace.json|\.kitsoki-owner)$' || true)" ] ||
  fail "move left the staging capsule dirty"

printf 'scratch-again\n' >"$local_repo/.capsules/staging/local/scratch.txt"
preserve_out="$tmp/dirty-capsule-preserve.out"
if ! (
  cd "$local_repo"
  scripts/refresh-staging-local.sh --skip-remote --dirty-action preserve --gate 'git diff --check'
) >"$preserve_out" 2>&1; then
  cat "$preserve_out" >&2
  fail "preserve refresh failed"
fi
assert_contains "$preserve_out" "preserved dirty staging-capsule changes"
assert_contains "$preserve_out" "staging/local ->"
preserve_snapshot_ref="$(sed -n 's/^  snapshot ref: //p' "$preserve_out" | tail -1)"
[ -n "$preserve_snapshot_ref" ] || fail "preserve did not report its durable snapshot ref"
[ "$(git -C "$local_repo" show "$preserve_snapshot_ref^3:scratch.txt")" = "scratch-again" ] ||
  fail "preserve snapshot ref did not retain the untracked file"
[ ! -e "$local_repo/.capsules/staging/local/scratch.txt" ] ||
  fail "preserve did not clean the untracked file"
[ -f "$local_repo/.capsules/staging/local/.kitsoki-capsule" ] ||
  fail "preserve dropped the managed capsule sentinel"
preserve_patch="$(ls "$local_repo/.artifacts/staging-local-preserved"/staging-capsule-dirty-*.patch | tail -1)"
assert_contains "$preserve_patch" "scratch.txt"
remaining_dirty="$(git -C "$local_repo/.capsules/staging/local" status --porcelain --untracked-files=all |
  grep -Ev '^[?][?] (\.kitsoki-capsule|\.kitsoki-clone|capsule-manifest.json|\.kitsoki-dev-workspace.json|\.kitsoki-owner)$' ||
  true)"
[ -z "$remaining_dirty" ] ||
  fail "preserve left staging capsule dirty: $remaining_dirty"

# Import the immutable proven SHA, then recheck the capsule branch. A concurrent
# clean commit during that fetch must stop before primary staging moves.
capsule_race_git_dir="$tmp/capsule-race-git"
mkdir -p "$capsule_race_git_dir"
cat >"$capsule_race_git_dir/git" <<'SH'
#!/usr/bin/env bash
if [ "${1:-}" = -C ] && [ "${2:-}" = "${KITSOKI_RACE_PRIMARY:-}" ] &&
  [ "${3:-}" = fetch ] && [ "${4:-}" = "${KITSOKI_RACE_CAPSULE:-}" ] &&
  [[ "${5:-}" == *:refs/kitsoki/refresh/*/import ]] &&
  [ ! -f "${KITSOKI_RACE_ONCE:?}" ]; then
  printf 'race\n' >"$KITSOKI_RACE_CAPSULE/capsule-race.txt"
  "${KITSOKI_REAL_GIT:?}" -C "$KITSOKI_RACE_CAPSULE" add capsule-race.txt
  "${KITSOKI_REAL_GIT:?}" -C "$KITSOKI_RACE_CAPSULE" commit -q -m 'concurrent capsule advance'
  touch "$KITSOKI_RACE_ONCE"
fi
exec "${KITSOKI_REAL_GIT:?}" "$@"
SH
chmod +x "$capsule_race_git_dir/git"
capsule_race_primary="$(cd "$local_repo" && pwd -P)"
capsule_race_capsule="$(cd "$local_repo/.capsules/staging/local" && pwd -P)"
capsule_race_staging="$(git -C "$local_repo" rev-parse staging/local)"
capsule_race_out="$tmp/capsule-race.out"
set +e
(
  cd "$local_repo"
  PATH="$capsule_race_git_dir:$PATH" \
    KITSOKI_REAL_GIT="$real_git" \
    KITSOKI_RACE_PRIMARY="$capsule_race_primary" \
    KITSOKI_RACE_CAPSULE="$capsule_race_capsule" \
    KITSOKI_RACE_ONCE="$tmp/capsule-race-once" \
    scripts/refresh-staging-local.sh --skip-remote --gate 'git diff --check'
) >"$capsule_race_out" 2>&1
capsule_race_status=$?
set -e
[ "$capsule_race_status" -eq 1 ] ||
  fail "expected concurrent capsule advance to stop refresh, got $capsule_race_status"
assert_contains "$capsule_race_out" "staging capsule branch advanced while importing the proven result"
[ "$(git -C "$local_repo" rev-parse staging/local)" = "$capsule_race_staging" ] ||
  fail "concurrent capsule advance moved primary staging"
[ "$(git -C "$local_repo/.capsules/staging/local" show HEAD:capsule-race.txt)" = "race" ] ||
  fail "concurrent capsule commit was not preserved"

make_out="$tmp/make-staging.out"
(
  cd "$script_dir/.."
  make -n test-staging STAGING_CAPSULE="$local_repo/.capsules/staging/local" STAGING_REFRESH_GATE=true
) >"$make_out" 2>&1
assert_contains "$make_out" "GIT_EDITOR=true GIT_SEQUENCE_EDITOR=true GIT_MERGE_AUTOEDIT=no GIT_PAGER=cat"
assert_contains "$make_out" "scripts/refresh-staging-local.sh --staging-branch"
assert_contains "$make_out" " --gate \"true\""
assert_contains "$make_out" "$local_repo/.capsules/staging/local\" test"
for target in web-dev-staging install-staging site-dev-staging; do
  target_out="$tmp/$target.out"
  (
    cd "$script_dir/.."
    make -n "$target" STAGING_CAPSULE="$local_repo/.capsules/staging/local" STAGING_REFRESH_GATE=true
  ) >"$target_out" 2>&1
  assert_contains "$target_out" "GIT_EDITOR=true GIT_SEQUENCE_EDITOR=true GIT_MERGE_AUTOEDIT=no GIT_PAGER=cat"
done

bare="$tmp/origin.git"
seed="$tmp/seed"
remote_repo="$tmp/remote-drift"
updater="$tmp/updater"

git init -q --bare --initial-branch=main "$bare"
git_init "$seed"
copy_scripts "$seed"
printf 'base\n' >"$seed/README.md"
git -C "$seed" add -A
git -C "$seed" commit -q -m initial
git -C "$seed" remote add origin "$bare"
git -C "$seed" push -q -u origin main

git clone -q "$bare" "$remote_repo"
git -C "$remote_repo" config user.name "Test User"
git -C "$remote_repo" config user.email "test@example.invalid"

git clone -q "$bare" "$updater"
git -C "$updater" config user.name "Test User"
git -C "$updater" config user.email "test@example.invalid"
commit_file "$updater" remote.txt remote-update
git -C "$updater" push -q origin main

remote_out="$tmp/remote-drift.out"
set +e
(
  cd "$remote_repo"
  scripts/refresh-staging-local.sh
) >"$remote_out" 2>&1
remote_status=$?
set -e
[ "$remote_status" -eq 2 ] ||
  fail "expected remote drift to stop with exit 2, got $remote_status"
assert_contains "$remote_out" "local main does not contain origin/main"
assert_contains "$remote_out" "Integration branch:"
assert_contains "$remote_out" "complete the remote-main sync steps above"

echo "refresh-staging-local tests passed"
