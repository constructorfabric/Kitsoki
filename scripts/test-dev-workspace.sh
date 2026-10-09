#!/usr/bin/env bash
# Focused regression tests for scripts/dev-workspace.sh. These tests create
# disposable repositories because the behavior under test is repository
# creation/merge/teardown itself.
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
dev_workspace="$script_dir/dev-workspace.sh"

tmp="$(mktemp -d "${TMPDIR:-/tmp}/kitsoki-dev-workspace-test.XXXXXX")"
cleanup() {
  rm -rf "$tmp"
}
trap cleanup EXIT

fail() {
  echo "FAIL: $*" >&2
  exit 1
}

gitc() {
  git -C "$1" -c user.name="Test User" -c user.email="test@example.com" "${@:2}"
}

stat_device_inode() {
  stat -f '%d:%i' "$1" 2>/dev/null || stat -c '%d:%i' "$1"
}

write_merge_helper() {
  local repo="$1"
  mkdir -p "$repo/scripts"
cat >"$repo/scripts/merge-to-main.sh" <<'SH'
#!/usr/bin/env bash
set -euo pipefail
branch="${1:?branch required}"
source_dir=""
while [ "$#" -gt 0 ]; do
  case "$1" in
    --source-dir)
      source_dir="${2:?--source-dir requires a value}"
      shift 2
      ;;
    --gate)
      shift 2
      ;;
    --force)
      shift
      ;;
    *)
      if [ "$1" = "$branch" ]; then
        shift
      else
        shift
      fi
      ;;
  esac
done
[ "$(git branch --show-current)" = "main" ] || { echo "not on main" >&2; exit 1; }
if [ -n "$source_dir" ]; then
  git fetch "$source_dir" "$branch:refs/heads/capsule/test-main-land" >/dev/null
  branch="capsule/test-main-land"
fi
git merge --ff-only "$branch"
SH
  chmod +x "$repo/scripts/merge-to-main.sh"
cat >"$repo/scripts/refresh-staging-local.sh" <<'SH'
#!/usr/bin/env bash
echo "stale primary refresh helper should not run" >&2
exit 77
SH
  chmod +x "$repo/scripts/refresh-staging-local.sh"
}

source_repo="$tmp/source"
mkdir -p "$source_repo"
git -C "$source_repo" init --quiet --initial-branch=main
write_merge_helper "$source_repo"
printf '.kitsoki.local.yaml\n.bootstrap-tempdir\n.artifacts/\n' >"$source_repo/.gitignore"
cat >"$source_repo/Makefile" <<'MK'
bootstrap-workspace:
	@printf '%s\n' "$(TEMP_DIR)" > .bootstrap-tempdir
	@echo "bootstrap output"

bootstrap-worktree: bootstrap-workspace

capsule-ci-quick:
	@true
MK
printf 'one\n' >"$source_repo/README.md"
printf 'harness_profiles:\n  local:\n    backend: codex\n' >"$source_repo/.kitsoki.local.yaml"
gitc "$source_repo" add -A
gitc "$source_repo" commit --quiet -m initial
gitc "$source_repo" switch -q -c staging/local
printf 'staged baseline\n' >"$source_repo/staged-baseline.txt"
gitc "$source_repo" add staged-baseline.txt
gitc "$source_repo" commit --quiet -m 'staged baseline'
gitc "$source_repo" switch -q main

root="$tmp/workspaces"

# Creation places its atomic lock beside targets, not inside the clone target.
# A concurrent status must surface that state instead of calling the target an
# unmanaged workspace, and a second create must not race it.
initializing_id="initializing-case"
initializing_path="$root/$initializing_id"
initializing_marker="$root/.initializing/$initializing_id"
mkdir -p "$initializing_marker"
cat >"$initializing_marker/metadata" <<EOF
pid=$$
path=$initializing_path
id=$initializing_id
branch=agent/$initializing_id
session_id=
EOF
if "$dev_workspace" status --repo "$source_repo" --root "$root" "$initializing_id" --json >"$tmp/initializing-status.json" 2>&1; then
  fail "status succeeded while workspace was initializing"
fi
[ "$(python3 -c 'import json,sys; print(json.load(sys.stdin)["state"])' <"$tmp/initializing-status.json")" = "initializing" ] || fail "status did not report initializing state"
[ "$(python3 -c 'import json,sys; print(json.load(sys.stdin)["initialization"])' <"$tmp/initializing-status.json")" = "active" ] || fail "status did not report active initialization"
if "$dev_workspace" create --repo "$source_repo" --root "$root" --id "$initializing_id" --branch "agent/$initializing_id" >/tmp/kitsoki-dev-workspace-initializing-create.log 2>&1; then
  fail "create raced an initializing workspace"
fi
grep -Fq "workspace is initializing" /tmp/kitsoki-dev-workspace-initializing-create.log || fail "create did not identify initializing workspace"
if "$dev_workspace" close --repo "$source_repo" --root "$root" --force "$initializing_id" >/tmp/kitsoki-dev-workspace-initializing-close.log 2>&1; then
  fail "close removed an initializing workspace"
fi
grep -Fq "workspace is initializing" /tmp/kitsoki-dev-workspace-initializing-close.log || fail "close did not protect initializing workspace"
[ ! -e "$initializing_path" ] || fail "initialization marker wrote into target before clone"
rm -rf "$initializing_marker"

# SIGKILL cannot run the script's EXIT cleanup. A stale marker keeps the
# incomplete clone explicit until an operator deliberately discards it.
stale_id="stale-incomplete"
stale_path="$root/$stale_id"
stale_marker="$root/.initializing/$stale_id"
mkdir -p "$stale_path/.git" "$stale_marker"
cat >"$stale_marker/metadata" <<EOF
pid=99999999
path=$stale_path
id=$stale_id
branch=agent/$stale_id
session_id=
EOF
if "$dev_workspace" status --repo "$source_repo" --root "$root" "$stale_id" >/tmp/kitsoki-dev-workspace-stale-status.log 2>&1; then
  fail "status succeeded for stale initialization"
fi
grep -Fq "state: initializing (stale)" /tmp/kitsoki-dev-workspace-stale-status.log || fail "status did not identify stale initialization"
if "$dev_workspace" recover --repo "$source_repo" --root "$root" "$stale_id" >/tmp/kitsoki-dev-workspace-recover.log 2>&1; then
  fail "recover discarded incomplete clone without explicit approval"
fi
grep -Fq -- "--discard-incomplete" /tmp/kitsoki-dev-workspace-recover.log || fail "recover did not explain explicit stale recovery"
"$dev_workspace" recover --repo "$source_repo" --root "$root" "$stale_id" --discard-incomplete >/dev/null
[ ! -e "$stale_path" ] || fail "recover --discard-incomplete left interrupted clone behind"
[ ! -e "$stale_marker" ] || fail "recover --discard-incomplete left stale marker behind"

create_json="$("$dev_workspace" create --repo "$source_repo" --root "$root" --id case-1 --branch agent/case-1 --json)"
workspace="$(python3 -c 'import json,sys; print(json.load(sys.stdin)["path"])' <<<"$create_json")"
[ -d "$workspace/.git" ] || fail "create did not produce a git workspace"
[ -f "$workspace/.kitsoki-capsule" ] || fail "missing capsule sentinel"

# A failed Git status probe is unknown state, never a clean workspace. A merge
# must stop before it rebases, gates, or advances the target ref.
status_json="$("$dev_workspace" create --repo "$source_repo" --root "$root" --id status-failure --branch agent/status-failure --json)"
status_workspace="$(python3 -c 'import json,sys; print(json.load(sys.stdin)["path"])' <<<"$status_json")"
printf 'must not land\n' >"$status_workspace/status-failure.txt"
"$dev_workspace" commit --repo "$source_repo" --root "$root" "$status_workspace" --message 'test: status failure stays unmerged' >/dev/null
fake_status_git_dir="$tmp/fake-status-git"
mkdir -p "$fake_status_git_dir"
cat >"$fake_status_git_dir/git" <<'SH'
#!/usr/bin/env bash
if [ "${1:-}" = -C ] && [ "${2:-}" = "${KITSOKI_FAIL_STATUS_DIR:-}" ] && [ "${3:-}" = status ]; then
  if [ -n "${KITSOKI_FAIL_STATUS_N:-}" ]; then
    count=0
    [ ! -f "${KITSOKI_STATUS_COUNT_FILE:?}" ] || count="$(cat "$KITSOKI_STATUS_COUNT_FILE")"
    count=$((count + 1))
    printf '%s\n' "$count" >"$KITSOKI_STATUS_COUNT_FILE"
    [ "$count" -ne "$KITSOKI_FAIL_STATUS_N" ] || exit 86
  else
    exit 86
  fi
fi
exec "${KITSOKI_REAL_GIT:?}" "$@"
SH
chmod +x "$fake_status_git_dir/git"
real_git="$(command -v git)"
status_target_before="$(git -C "$source_repo" rev-parse staging/local)"
set +e
PATH="$fake_status_git_dir:$PATH" \
  KITSOKI_REAL_GIT="$real_git" \
  KITSOKI_FAIL_STATUS_DIR="$status_workspace" \
  "$dev_workspace" merge --repo "$source_repo" --root "$root" "$status_workspace" --gate true \
  >"$tmp/status-failure-merge.out" 2>&1
status_merge_code=$?
set -e
[ "$status_merge_code" -eq 1 ] || fail "expected status failure to stop merge, got $status_merge_code"
grep -Fq "could not inspect workspace status" "$tmp/status-failure-merge.out" ||
  fail "merge did not report failed workspace status inspection"
[ "$(git -C "$source_repo" rev-parse staging/local)" = "$status_target_before" ] ||
  fail "status failure advanced staging/local"
if git -C "$source_repo" cat-file -e staging/local:status-failure.txt 2>/dev/null; then
  fail "status failure landed workspace content"
fi
set +e
PATH="$fake_status_git_dir:$PATH" \
  KITSOKI_REAL_GIT="$real_git" \
  KITSOKI_FAIL_STATUS_DIR="$status_workspace" \
  "$dev_workspace" status --repo "$source_repo" --root "$root" "$status_workspace" \
  >"$tmp/status-failure-status.out" 2>&1
status_command_code=$?
set -e
[ "$status_command_code" -eq 1 ] || fail "expected status command probe failure, got $status_command_code"
grep -Fq "could not inspect workspace status" "$tmp/status-failure-status.out" ||
  fail "status command did not report failed workspace inspection"

# A later status probe after rebase must fail closed too. Initial cleanliness
# and collision probes succeed; verify_clean_readable_checkout then fails.
status_count_file="$tmp/status-probe-count"
status_target_before="$(git -C "$source_repo" rev-parse staging/local)"
set +e
PATH="$fake_status_git_dir:$PATH" \
  KITSOKI_REAL_GIT="$real_git" \
  KITSOKI_FAIL_STATUS_DIR="$status_workspace" \
  KITSOKI_FAIL_STATUS_N=3 \
  KITSOKI_STATUS_COUNT_FILE="$status_count_file" \
  "$dev_workspace" merge --repo "$source_repo" --root "$root" "$status_workspace" --gate true \
  >"$tmp/late-status-failure-merge.out" 2>&1
late_status_merge_code=$?
set -e
[ "$late_status_merge_code" -eq 1 ] ||
  fail "expected late status failure to stop merge, got $late_status_merge_code"
grep -Fq "could not inspect checkout status" "$tmp/late-status-failure-merge.out" ||
  fail "late status failure was not reported"
[ "$(git -C "$source_repo" rev-parse staging/local)" = "$status_target_before" ] ||
  fail "late status failure advanced staging/local"

# Gates validate a captured commit; they must not move a clean branch and let
# --teardown delete the only ref to the original committed work.
gate_reset_json="$("$dev_workspace" create --repo "$source_repo" --root "$root" --id gate-reset --branch agent/gate-reset --json)"
gate_reset_workspace="$(python3 -c 'import json,sys; print(json.load(sys.stdin)["path"])' <<<"$gate_reset_json")"
printf 'must survive gate reset\n' >"$gate_reset_workspace/gate-reset.txt"
"$dev_workspace" commit --repo "$source_repo" --root "$root" "$gate_reset_workspace" --message 'test: preserve gate-reset work' >/dev/null
gate_reset_original="$(git -C "$gate_reset_workspace" rev-parse HEAD)"
gate_reset_target="$(git -C "$source_repo" rev-parse staging/local)"
set +e
"$dev_workspace" merge --repo "$source_repo" --root "$root" "$gate_reset_workspace" \
  --gate 'git reset --hard source/staging/local' --teardown >"$tmp/gate-reset.out" 2>&1
gate_reset_status=$?
set -e
[ "$gate_reset_status" -ne 0 ] || fail "gate-reset merge should fail"
grep -Fq "validation gate moved workspace HEAD" "$tmp/gate-reset.out" ||
  fail "gate-reset merge did not report committed-state mutation"
[ -d "$gate_reset_workspace/.git" ] || fail "gate-reset failure tore down the only workspace"
[ "$(git -C "$source_repo" rev-parse staging/local)" = "$gate_reset_target" ] ||
  fail "gate-reset failure advanced staging/local"
# The gate itself moved the branch, so the original must remain reachable by
# its immutable object and the workspace must remain available for recovery.
git -C "$gate_reset_workspace" cat-file -e "$gate_reset_original^{tree}" ||
  fail "gate-reset failure lost the original workspace commit object"
git -C "$gate_reset_workspace" reset --hard "$gate_reset_original" >/dev/null

# Capture the target before the gate and use it as the update-ref expected
# value. A concurrent target advance must win, and the workspace must remain.
target_race_json="$("$dev_workspace" create --repo "$source_repo" --root "$root" --id target-race --branch agent/target-race --json)"
target_race_workspace="$(python3 -c 'import json,sys; print(json.load(sys.stdin)["path"])' <<<"$target_race_json")"
printf 'workspace target race\n' >"$target_race_workspace/target-race.txt"
"$dev_workspace" commit --repo "$source_repo" --root "$root" "$target_race_workspace" --message 'test: target race workspace' >/dev/null
target_advancer="$tmp/target-race-advancer"
git clone -q --no-local "$source_repo" "$target_advancer"
git -C "$target_advancer" config user.name "Test User"
git -C "$target_advancer" config user.email "test@example.invalid"
git -C "$target_advancer" switch -q staging/local
printf 'concurrent-target\n' >"$target_advancer/target-concurrent.txt"
git -C "$target_advancer" add target-concurrent.txt
git -C "$target_advancer" commit -q -m 'concurrent target advance'
target_concurrent="$(git -C "$target_advancer" rev-parse HEAD)"
git -C "$source_repo" fetch -q "$target_advancer" "HEAD:refs/kitsoki/test-target-race"
set +e
"$dev_workspace" merge --repo "$source_repo" --root "$root" "$target_race_workspace" \
  --gate "git -C '$source_repo' update-ref refs/heads/staging/local $target_concurrent" \
  --teardown >"$tmp/target-race.out" 2>&1
target_race_status=$?
set -e
[ "$target_race_status" -ne 0 ] || fail "target-race merge should fail"
[ "$(git -C "$source_repo" rev-parse staging/local)" = "$target_concurrent" ] ||
  fail "target-race merge overwrote the concurrent target"
[ -d "$target_race_workspace/.git" ] || fail "target-race failure tore down the workspace"
if git -C "$source_repo" cat-file -e staging/local:target-race.txt 2>/dev/null; then
  fail "target-race merge landed stale workspace content"
fi

# Import the immutable proven SHA, not a moving branch. Advance the workspace
# branch at the fetch boundary and prove unvalidated work neither lands nor is
# deleted by --teardown.
branch_race_json="$("$dev_workspace" create --repo "$source_repo" --root "$root" --id branch-race --branch agent/branch-race --json)"
branch_race_workspace="$(python3 -c 'import json,sys; print(json.load(sys.stdin)["path"])' <<<"$branch_race_json")"
printf 'validated branch race\n' >"$branch_race_workspace/branch-race.txt"
"$dev_workspace" commit --repo "$source_repo" --root "$root" "$branch_race_workspace" --message 'test: branch race workspace' >/dev/null
branch_race_target="$(git -C "$source_repo" rev-parse staging/local)"
branch_race_git="$tmp/branch-race-git"
mkdir -p "$branch_race_git"
cat >"$branch_race_git/git" <<'SH'
#!/usr/bin/env bash
if [ "${1:-}" = -C ] && [ "${2:-}" = "${KITSOKI_BRANCH_RACE_REPO:-}" ] &&
  [ "${3:-}" = fetch ] && [ "${4:-}" = --no-tags ] &&
  [ "${5:-}" = "${KITSOKI_BRANCH_RACE_WORKSPACE:-}" ] &&
  [[ "${6:-}" == *:refs/heads/capsule/branch-race-land ]] &&
  [ ! -f "${KITSOKI_BRANCH_RACE_ONCE:?}" ]; then
  printf 'concurrent branch work\n' >"$KITSOKI_BRANCH_RACE_WORKSPACE/concurrent-branch.txt"
  "${KITSOKI_REAL_GIT:?}" -C "$KITSOKI_BRANCH_RACE_WORKSPACE" add concurrent-branch.txt
  "${KITSOKI_REAL_GIT:?}" -C "$KITSOKI_BRANCH_RACE_WORKSPACE" commit -q -m 'concurrent branch advance'
  touch "$KITSOKI_BRANCH_RACE_ONCE"
fi
exec "${KITSOKI_REAL_GIT:?}" "$@"
SH
chmod +x "$branch_race_git/git"
branch_race_repo_physical="$(cd "$source_repo" && pwd -P)"
set +e
PATH="$branch_race_git:$PATH" \
  KITSOKI_REAL_GIT="$real_git" \
  KITSOKI_BRANCH_RACE_REPO="$branch_race_repo_physical" \
  KITSOKI_BRANCH_RACE_WORKSPACE="$branch_race_workspace" \
  KITSOKI_BRANCH_RACE_ONCE="$tmp/branch-race-once" \
  "$dev_workspace" merge --repo "$source_repo" --root "$root" "$branch_race_workspace" \
    --gate true --teardown >"$tmp/branch-race.out" 2>&1
branch_race_status=$?
set -e
[ "$branch_race_status" -ne 0 ] || fail "branch-race merge should fail"
grep -Fq "workspace branch advanced while importing the proven result" "$tmp/branch-race.out" ||
  fail "branch-race merge did not report the moving branch"
[ "$(git -C "$source_repo" rev-parse staging/local)" = "$branch_race_target" ] ||
  fail "branch-race merge advanced the target"
[ -d "$branch_race_workspace/.git" ] || fail "branch-race failure tore down the workspace"
[ "$(git -C "$branch_race_workspace" show HEAD:concurrent-branch.txt)" = "concurrent branch work" ] ||
  fail "branch-race failure lost the concurrent commit"

# Close owns the final teardown boundary. Advance the clean workspace after
# the outer target-contains-tip check; close must reject the expected tip and
# keep the workspace rather than deleting the new commit.
teardown_race_json="$("$dev_workspace" create --repo "$source_repo" --root "$root" --id teardown-race --branch agent/teardown-race --json)"
teardown_race_workspace="$(python3 -c 'import json,sys; print(json.load(sys.stdin)["path"])' <<<"$teardown_race_json")"
printf 'validated teardown race\n' >"$teardown_race_workspace/teardown-race.txt"
"$dev_workspace" commit --repo "$source_repo" --root "$root" "$teardown_race_workspace" --message 'test: teardown race workspace' >/dev/null
teardown_race_git="$tmp/teardown-race-git"
mkdir -p "$teardown_race_git"
cat >"$teardown_race_git/git" <<'SH'
#!/usr/bin/env bash
if [ "${1:-}" = -C ] && [ "${2:-}" = "${KITSOKI_TEARDOWN_RACE_REPO:-}" ] &&
  [ "${3:-}" = merge-base ] && [ "${4:-}" = --is-ancestor ] &&
  [ "${6:-}" = refs/heads/staging/local ] &&
  [ ! -f "${KITSOKI_TEARDOWN_RACE_ONCE:?}" ]; then
  set +e
  "${KITSOKI_REAL_GIT:?}" "$@"
  status=$?
  set -e
  if [ "$status" -eq 0 ]; then
    printf 'late clean commit\n' >"$KITSOKI_TEARDOWN_RACE_WORKSPACE/late-teardown.txt"
    "$KITSOKI_REAL_GIT" -C "$KITSOKI_TEARDOWN_RACE_WORKSPACE" add late-teardown.txt
    "$KITSOKI_REAL_GIT" -C "$KITSOKI_TEARDOWN_RACE_WORKSPACE" commit -q -m 'late teardown advance'
    touch "$KITSOKI_TEARDOWN_RACE_ONCE"
  fi
  exit "$status"
fi
exec "${KITSOKI_REAL_GIT:?}" "$@"
SH
chmod +x "$teardown_race_git/git"
PATH="$teardown_race_git:$PATH" \
  KITSOKI_REAL_GIT="$real_git" \
  KITSOKI_TEARDOWN_RACE_REPO="$branch_race_repo_physical" \
  KITSOKI_TEARDOWN_RACE_WORKSPACE="$teardown_race_workspace" \
  KITSOKI_TEARDOWN_RACE_ONCE="$tmp/teardown-race-once" \
  "$dev_workspace" merge --repo "$source_repo" --root "$root" "$teardown_race_workspace" \
    --gate true --teardown >"$tmp/teardown-race.out" 2>&1
grep -Fq "close: workspace tip changed before teardown" "$tmp/teardown-race.out" ||
  fail "teardown-race merge did not report the late branch advance"
[ -d "$teardown_race_workspace/.git" ] || fail "teardown-race merge deleted the advanced workspace"
[ "$(git -C "$teardown_race_workspace" show HEAD:late-teardown.txt)" = "late clean commit" ] ||
  fail "teardown-race merge lost the late clean commit"
if git -C "$source_repo" cat-file -e staging/local:late-teardown.txt 2>/dev/null; then
  fail "teardown-race merge landed unvalidated late work"
fi

# Ordinary rebase flattens merge commits. A merge-resolution-only file must
# trigger the independent expected-tree proof rather than being silently lost.
resolution_json="$("$dev_workspace" create --repo "$source_repo" --root "$root" --id merge-resolution --branch agent/merge-resolution --json)"
resolution_workspace="$(python3 -c 'import json,sys; print(json.load(sys.stdin)["path"])' <<<"$resolution_json")"
resolution_base="$(git -C "$resolution_workspace" rev-parse HEAD)"
git -C "$resolution_workspace" switch -q -c resolution-side "$resolution_base"
printf 'side\n' >"$resolution_workspace/resolution-side.txt"
git -C "$resolution_workspace" add resolution-side.txt
git -C "$resolution_workspace" commit -q --signoff -m 'test: resolution side'
git -C "$resolution_workspace" switch -q agent/merge-resolution
printf 'parent\n' >"$resolution_workspace/resolution-parent.txt"
git -C "$resolution_workspace" add resolution-parent.txt
git -C "$resolution_workspace" commit -q --signoff -m 'test: resolution parent'
git -C "$resolution_workspace" merge -q --no-ff --no-commit resolution-side
printf 'resolution only\n' >"$resolution_workspace/resolution-only.txt"
git -C "$resolution_workspace" add resolution-only.txt
git -C "$resolution_workspace" commit -q --signoff -m 'test: merge resolution only'
resolution_original="$(git -C "$resolution_workspace" rev-parse HEAD)"
gitc "$source_repo" switch -q staging/local
printf 'target after resolution clone\n' >"$source_repo/resolution-target-advance.txt"
gitc "$source_repo" add resolution-target-advance.txt
gitc "$source_repo" commit --quiet -m 'advance target for merge-resolution test'
resolution_target="$(git -C "$source_repo" rev-parse HEAD)"
gitc "$source_repo" switch -q main
set +e
"$dev_workspace" merge --repo "$source_repo" --root "$root" "$resolution_workspace" \
  --gate true --teardown >"$tmp/merge-resolution.out" 2>&1
resolution_status=$?
set -e
[ "$resolution_status" -ne 0 ] || fail "merge-resolution loss should stop merge"
grep -Fq "rebased workspace tree differs from the expected preserved tree" "$tmp/merge-resolution.out" ||
  fail "merge-resolution refusal did not report its expected-tree mismatch"
[ "$(git -C "$source_repo" rev-parse staging/local)" = "$resolution_target" ] ||
  fail "merge-resolution refusal moved the target"
[ -d "$resolution_workspace/.git" ] || fail "merge-resolution refusal tore down the workspace"
[ "$(git -C "$resolution_workspace" rev-parse HEAD)" = "$resolution_original" ] ||
  fail "merge-resolution refusal did not restore the original workspace tip"
[ "$(git -C "$resolution_workspace" show HEAD:resolution-only.txt)" = "resolution only" ] ||
  fail "merge-resolution refusal lost resolution-only content"
[ "$(git -C "$source_repo" rev-parse "refs/kitsoki/workspace-merge-recovery/$resolution_original")" = "$resolution_original" ] ||
  fail "merge-resolution refusal did not anchor the original in the primary repository"

# A target that starts tracking an ignored local path must be refused before
# rebase overwrites the workspace bytes.
gitc "$source_repo" switch -q staging/local
printf 'ignored-dev/\n' >>"$source_repo/.gitignore"
gitc "$source_repo" add .gitignore
gitc "$source_repo" commit --quiet -m 'ignore dev collision fixture'
gitc "$source_repo" switch -q main
ignored_dev_json="$("$dev_workspace" create --repo "$source_repo" --root "$root" --id ignored-collision --branch agent/ignored-collision --json)"
ignored_dev_workspace="$(python3 -c 'import json,sys; print(json.load(sys.stdin)["path"])' <<<"$ignored_dev_json")"
mkdir -p "$ignored_dev_workspace/ignored-dev"
printf 'workspace local evidence\n' >"$ignored_dev_workspace/ignored-dev/valuable.txt"
ignored_dev_head="$(git -C "$ignored_dev_workspace" rev-parse HEAD)"
gitc "$source_repo" switch -q staging/local
mkdir -p "$source_repo/ignored-dev"
printf 'target tracked version\n' >"$source_repo/ignored-dev/valuable.txt"
gitc "$source_repo" add -f ignored-dev/valuable.txt
gitc "$source_repo" commit --quiet -m 'track ignored dev collision path'
ignored_dev_target="$(git -C "$source_repo" rev-parse HEAD)"
gitc "$source_repo" switch -q main
set +e
"$dev_workspace" merge --repo "$source_repo" --root "$root" "$ignored_dev_workspace" \
  --gate true --teardown >"$tmp/ignored-dev-collision.out" 2>&1
ignored_dev_status=$?
set -e
[ "$ignored_dev_status" -ne 0 ] || fail "ignored dev collision merge should fail"
grep -Fq "would overwrite untracked or ignored workspace paths" "$tmp/ignored-dev-collision.out" ||
  fail "ignored dev collision did not report the protected path"
[ "$(cat "$ignored_dev_workspace/ignored-dev/valuable.txt")" = "workspace local evidence" ] ||
  fail "ignored dev collision overwrote local workspace evidence"
[ "$(git -C "$ignored_dev_workspace" rev-parse HEAD)" = "$ignored_dev_head" ] ||
  fail "ignored dev collision moved the workspace branch"
[ "$(git -C "$source_repo" rev-parse staging/local)" = "$ignored_dev_target" ] ||
  fail "ignored dev collision moved the target"

# Teardown is the final safety net for old writers: flat tickets and the retired
# <id>/issue.md shape must be imported into the source checkout before removal.
legacy_json="$("$dev_workspace" create --repo "$source_repo" --root "$root" --id legacy-ticket --branch agent/legacy-ticket --json)"
legacy_workspace="$(python3 -c 'import json,sys; print(json.load(sys.stdin)["path"])' <<<"$legacy_json")"
mkdir -p "$legacy_workspace/.artifacts/issues/bugs/legacy-nested" "$legacy_workspace/.artifacts/issues/bugs/legacy-flat.artifacts"
printf '%s\n' '# nested ticket' >"$legacy_workspace/.artifacts/issues/bugs/legacy-nested/issue.md"
printf '%s\n' 'nested evidence' >"$legacy_workspace/.artifacts/issues/bugs/legacy-nested/trace.json"
printf '%s\n' '# flat ticket' >"$legacy_workspace/.artifacts/issues/bugs/legacy-flat.md"
printf '%s\n' 'flat evidence' >"$legacy_workspace/.artifacts/issues/bugs/legacy-flat.artifacts/trace.json"
"$dev_workspace" close --repo "$source_repo" --root "$root" "$legacy_workspace" >/dev/null
[ -f "$source_repo/.artifacts/issues/bugs/legacy-nested.md" ] || fail "close lost nested legacy ticket"
[ -f "$source_repo/.artifacts/issues/bugs/legacy-nested.artifacts/trace.json" ] || fail "close lost nested legacy evidence"
[ -f "$source_repo/.artifacts/issues/bugs/legacy-flat.md" ] || fail "close lost flat workspace ticket"
[ -f "$source_repo/.artifacts/issues/bugs/legacy-flat.artifacts/trace.json" ] || fail "close lost flat ticket evidence"
[ -f "$workspace/capsule-manifest.json" ] || fail "missing capsule manifest"
[ -f "$workspace/.kitsoki-clone" ] || fail "missing clone sentinel"
[ "$(git -C "$workspace" branch --show-current)" = "agent/case-1" ] || fail "workspace branch mismatch"
[ "$(python3 -c 'import json,sys; print(json.load(sys.stdin)["base"])' <"$workspace/.kitsoki-dev-workspace.json")" = "staging/local" ] || fail "default base was not recorded as staging/local"
[ "$(cat "$workspace/staged-baseline.txt")" = "staged baseline" ] || fail "default create did not start from staging/local"
# An interruption after manifests are durable leaves a valid workspace. Recovery
# must only release its stale marker, never discard the completed clone.
completed_marker="$root/.initializing/case-1"
mkdir -p "$completed_marker"
cat >"$completed_marker/metadata" <<EOF
pid=99999999
path=$workspace
id=case-1
branch=agent/case-1
session_id=
EOF
"$dev_workspace" recover --repo "$source_repo" --root "$root" case-1 >/dev/null
[ -f "$workspace/.kitsoki-dev-workspace.json" ] || fail "recover discarded a completed managed workspace"
[ ! -e "$completed_marker" ] || fail "recover left stale marker on completed workspace"
readme_blob="$(git -C "$source_repo" rev-parse HEAD:README.md)"
source_readme_obj="$source_repo/.git/objects/${readme_blob:0:2}/${readme_blob:2}"
workspace_readme_obj="$workspace/.git/objects/${readme_blob:0:2}/${readme_blob:2}"
if [ -f "$source_readme_obj" ] && [ -f "$workspace_readme_obj" ]; then
  [ "$(stat_device_inode "$source_readme_obj")" = "$(stat_device_inode "$workspace_readme_obj")" ] || fail "create did not hardlink local git objects"
fi

"$dev_workspace" create --repo "$source_repo" --root "$root" --id local-config --branch agent/local-config --bootstrap >/dev/null
local_config_workspace="$root/local-config"
[ -f "$local_config_workspace/.kitsoki.local.yaml" ] || fail "bootstrap did not copy .kitsoki.local.yaml"
cmp "$source_repo/.kitsoki.local.yaml" "$local_config_workspace/.kitsoki.local.yaml" >/dev/null || fail "bootstrap copied the wrong .kitsoki.local.yaml content"
bootstrap_temp="$(cat "$local_config_workspace/.bootstrap-tempdir")"
case "$bootstrap_temp" in
  /tmp/kitsoki-bootstrap.*) ;;
  *) fail "bootstrap did not use a compact temp root: $bootstrap_temp" ;;
esac
[ ! -e "$bootstrap_temp" ] || fail "bootstrap did not remove its temporary root"
"$dev_workspace" close --repo "$source_repo" --root "$root" "$local_config_workspace" >/dev/null

json_bootstrap="$("$dev_workspace" create --repo "$source_repo" --root "$root" --id json-bootstrap --branch agent/json-bootstrap --bootstrap --json)"
json_bootstrap_workspace="$(python3 -c 'import json,sys; print(json.load(sys.stdin)["path"])' <<<"$json_bootstrap")"
[ -d "$json_bootstrap_workspace" ] || fail "bootstrap --json did not preserve a machine-readable response"
"$dev_workspace" close --repo "$source_repo" --root "$root" "$json_bootstrap_workspace" >/dev/null

printf 'local scratch\n' >"$source_repo/untracked-primary.txt"
printf 'two\n' >"$workspace/feature.txt"
"$dev_workspace" commit --repo "$source_repo" --root "$root" "$workspace" --message 'add feature' >/dev/null
git -C "$workspace" log -1 --format=%B | grep -Fq 'Signed-off-by: Kitsoki Agent <agent@kitsoki.dev>' || fail "commit omitted required DCO sign-off"
default_merge_log="$tmp/default-merge.log"
"$dev_workspace" merge --repo "$source_repo" --root "$root" "$workspace" --teardown >"$default_merge_log" 2>&1
[ ! -e "$workspace" ] || fail "merge --teardown left workspace behind"
[ ! -e "$source_repo/feature.txt" ] || fail "default merge should not modify primary main working tree"
[ "$(git -C "$source_repo" show staging/local:feature.txt)" = "two" ] || fail "merge did not fast-forward staging/local"
[ "$(git -C "$source_repo" show staging/local:staged-baseline.txt)" = "staged baseline" ] || fail "merge dropped existing staging baseline"
grep -Fq "using default staging gate: make capsule-ci-quick" "$default_merge_log" || fail "default staging merge did not select capsule-ci-quick"
[ "$(git -C "$source_repo" branch --show-current)" = "main" ] || fail "default merge moved primary checkout off main"
rm "$source_repo/untracked-primary.txt"

from_staging_json="$("$dev_workspace" create --repo "$source_repo" --root "$root" --id from-staging --branch agent/from-staging --json)"
from_staging_workspace="$(python3 -c 'import json,sys; print(json.load(sys.stdin)["path"])' <<<"$from_staging_json")"
[ "$(cat "$from_staging_workspace/feature.txt")" = "two" ] || fail "default create did not start from staging branch"
"$dev_workspace" close --repo "$source_repo" --root "$root" "$from_staging_workspace" >/dev/null

# A staging landing can contain the refresh fix itself while protected main's
# working tree still has an older helper. Post-landing convergence must execute
# the immutable helper from the landed workspace, not the stale primary copy.
refresh_source_json="$("$dev_workspace" create --repo "$source_repo" --root "$root" --id refresh-source --branch agent/refresh-source --json)"
refresh_source_workspace="$(python3 -c 'import json,sys; print(json.load(sys.stdin)["path"])' <<<"$refresh_source_json")"
cat >"$refresh_source_workspace/scripts/refresh-staging-local.sh" <<SH
#!/usr/bin/env bash
printf 'workspace refresh helper\n' >"$source_repo/.refresh-helper-used"
SH
chmod +x "$refresh_source_workspace/scripts/refresh-staging-local.sh"
"$dev_workspace" commit --repo "$source_repo" --root "$root" "$refresh_source_workspace" --message 'fix staging refresh helper' >/dev/null
mkdir -p "$source_repo/.capsules/staging/local/.git"
"$dev_workspace" merge --repo "$source_repo" --root "$root" "$refresh_source_workspace" --gate true --teardown >/dev/null
[ "$(cat "$source_repo/.refresh-helper-used")" = "workspace refresh helper" ] ||
  fail "post-landing convergence did not use the landed workspace refresh helper"
rm -rf "$source_repo/.capsules/staging" "$source_repo/.refresh-helper-used"

park_json="$("$dev_workspace" create --repo "$source_repo" --root "$root" --id park-source --branch agent/park-source --json)"
park_workspace="$(python3 -c 'import json,sys; print(json.load(sys.stdin)["path"])' <<<"$park_json")"
git -C "$park_workspace" config diff.external /usr/bin/true
printf 'staged park state\n' >"$park_workspace/README.md"
git -C "$park_workspace" add README.md
printf 'parked work\n' >"$park_workspace/README.md"
printf 'untracked park\n' >"$park_workspace/parked.txt"
park_result="$("$dev_workspace" park --repo "$source_repo" --root "$root" --session-id sess-park --json "$park_workspace")"
park_recovery="$(python3 -c 'import json,sys; print(json.load(sys.stdin)["recovery_workspace"])' <<<"$park_result")"
park_commit="$(python3 -c 'import json,sys; print(json.load(sys.stdin)["recovery_commit"])' <<<"$park_result")"
park_quarantine="$(python3 -c 'import json,sys; print(json.load(sys.stdin)["source_quarantine"])' <<<"$park_result")"
park_snapshot_ref="$(python3 -c 'import json,sys; print(json.load(sys.stdin)["snapshot_ref"])' <<<"$park_result")"
park_recovery_ref="$(python3 -c 'import json,sys; print(json.load(sys.stdin)["recovery_ref"])' <<<"$park_result")"
[ "$(python3 -c 'import json,sys; print(json.load(sys.stdin)["parked"])' <<<"$park_result")" = "True" ] || fail "park did not report a parked workspace"
[ "$(python3 -c 'import json,sys; print(json.load(sys.stdin)["cleaned"])' <<<"$park_result")" = "True" ] || fail "park did not report cleaning the source workspace"
[ "$(cat "$park_workspace/README.md")" = "one" ] || fail "park did not clean the source workspace back to HEAD"
[ ! -e "$park_workspace/parked.txt" ] || fail "park did not remove the source untracked file"
[ -d "$park_recovery/.git" ] || fail "park did not create a recovery workspace"
[ "$(cat "$park_recovery/README.md")" = "parked work" ] || fail "park did not preserve the tracked change in the recovery workspace"
[ "$(cat "$park_recovery/parked.txt")" = "untracked park" ] || fail "park did not preserve the untracked file in the recovery workspace"
[ "$(git -C "$park_recovery" rev-parse HEAD)" = "$park_commit" ] || fail "park JSON commit did not match the recovery workspace HEAD"
[ "$(git -C "$source_repo" show "$park_recovery_ref:README.md")" = "parked work" ] ||
  fail "primary dirty-recovery ref did not retain tracked park work"
[ "$(git -C "$source_repo" show "$park_recovery_ref:parked.txt")" = "untracked park" ] ||
  fail "primary dirty-recovery ref did not retain untracked park work"
[ "$(git -C "$source_repo" show "$park_snapshot_ref^2:README.md")" = "staged park state" ] ||
  fail "primary dirty-snapshot ref did not retain exact staged park state"
[ "$(cat "$park_quarantine/README.md")" = "parked work" ] ||
  fail "park did not retain the original source quarantine"
case "$(basename "$park_quarantine")" in
  closed-recovered-*) ;;
  *) fail "park quarantine is outside the managed closed-recovered lifecycle" ;;
esac
[ -f "$park_quarantine/.kitsoki-recovered-quarantine.json" ] ||
  fail "park quarantine is missing its recovery ownership manifest"
[ -z "$(git -C "$park_quarantine" status --porcelain --untracked-files=all)" ] ||
  fail "sealed park quarantine is not cleanup-eligible"
park_quarantine_tip_ref="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["tip_ref"])' "$park_quarantine/.kitsoki-recovered-quarantine.json")"
[ "$(git -C "$source_repo" rev-parse "$park_quarantine_tip_ref")" = "$(git -C "$park_quarantine" rev-parse HEAD)" ] ||
  fail "park quarantine tip is not anchored in the primary repository"

# A seal that contains work beyond the signed recovery tree must never be
# purged, even when its exact late tip is anchored. Restore the fixture after
# proving both the late bytes and tip remain reachable.
park_quarantine_marker="$park_quarantine/.kitsoki-recovered-quarantine.json"
park_quarantine_marker_backup="$tmp/park-quarantine-marker.json"
park_quarantine_sealed_head="$(git -C "$park_quarantine" rev-parse HEAD)"
cp "$park_quarantine_marker" "$park_quarantine_marker_backup"
printf 'late sealed work\n' >"$park_quarantine/late-sealed.txt"
gitc "$park_quarantine" add late-sealed.txt
gitc "$park_quarantine" commit --quiet --signoff -m 'late work after recovery snapshot'
late_quarantine_head="$(git -C "$park_quarantine" rev-parse HEAD)"
late_quarantine_tree="$(git -C "$park_quarantine" rev-parse HEAD^{tree})"
late_quarantine_tip_ref="refs/kitsoki/recovered-quarantine/$late_quarantine_head"
git -C "$source_repo" fetch --quiet --no-tags "$park_quarantine" "$late_quarantine_head:$late_quarantine_tip_ref"
python3 - "$park_quarantine_marker" "$late_quarantine_head" "$late_quarantine_tree" "$late_quarantine_tip_ref" <<'PY'
import json
import os
import sys

path, head, tree, tip_ref = sys.argv[1:]
with open(path, encoding="utf-8") as f:
    marker = json.load(f)
marker.update({"head": head, "tree": tree, "tip_ref": tip_ref})
temp = path + ".tmp"
with open(temp, "w", encoding="utf-8") as f:
    json.dump(marker, f, indent=2, sort_keys=True)
    f.write("\n")
os.replace(temp, path)
PY
if "$dev_workspace" close --repo "$source_repo" --root "$(dirname "$park_quarantine")" --purge-quarantine "$park_quarantine" >"$tmp/late-recovery-purge.log" 2>&1; then
  fail "provider purge accepted a sealed tree beyond the signed recovery tree"
fi
grep -Fq "work beyond the signed recovery tree" "$tmp/late-recovery-purge.log" ||
  fail "provider purge did not explain its recovery-tree refusal"
[ "$(cat "$park_quarantine/late-sealed.txt")" = "late sealed work" ] ||
  fail "recovery-tree refusal lost late sealed work"
[ "$(git -C "$source_repo" show "$late_quarantine_tip_ref:late-sealed.txt")" = "late sealed work" ] ||
  fail "recovery-tree refusal lost the exact late quarantine tip"
git -C "$park_quarantine" reset --quiet --hard "$park_quarantine_sealed_head"
cp "$park_quarantine_marker_backup" "$park_quarantine_marker"
git -C "$source_repo" update-ref -d "$late_quarantine_tip_ref"

# Purge preserves every ignored file, not only known review roots. It also
# repeats the process-activity proof after atomic isolation and restores the
# quarantine on activity or deletion failure.
printf '/arbitrary-ignored.dat\n' >>"$park_quarantine/.git/info/exclude"
printf 'arbitrary ignored state\n' >"$park_quarantine/arbitrary-ignored.dat"

# Clean status does not imply that the repository database has no unique work.
# Preserve a stash, a secondary ref, a reflog-only commit, and an unreachable
# blob through the Git recovery archive.
printf 'stash-only state\n' >"$park_quarantine/stash-secret.txt"
git -C "$park_quarantine" stash push --quiet --include-untracked -m 'stash recovery proof'
stash_oid="$(git -C "$park_quarantine" rev-parse refs/stash)"
quarantine_tree="$(git -C "$park_quarantine" rev-parse HEAD^{tree})"
secondary_oid="$(printf 'secondary ref recovery proof\n' | git -C "$park_quarantine" \
  -c user.name='Test User' -c user.email='test@example.com' \
  commit-tree "$quarantine_tree" -p HEAD)"
git -C "$park_quarantine" update-ref refs/heads/secondary-local "$secondary_oid"
reflog_only_oid="$(printf 'reflog recovery proof\n' | git -C "$park_quarantine" \
  -c user.name='Test User' -c user.email='test@example.com' \
  commit-tree "$quarantine_tree" -p HEAD)"
park_quarantine_branch="$(git -C "$park_quarantine" branch --show-current)"
git -C "$park_quarantine" update-ref -m 'temporary reflog proof' \
  "refs/heads/$park_quarantine_branch" "$reflog_only_oid" "$park_quarantine_sealed_head"
git -C "$park_quarantine" update-ref -m 'restore sealed quarantine tip' \
  "refs/heads/$park_quarantine_branch" "$park_quarantine_sealed_head" "$reflog_only_oid"
unreachable_blob_oid="$(printf 'unreachable object recovery proof\n' | git -C "$park_quarantine" hash-object -w --stdin)"
[ -z "$(git -C "$park_quarantine" status --porcelain --untracked-files=all)" ] ||
  fail "Git recovery fixture made the recovered quarantine dirty"

purge_fakebin="$tmp/purge-fakebin"
mkdir -p "$purge_fakebin"
cat >"$purge_fakebin/lsof" <<'SH'
#!/usr/bin/env bash
if [ -n "${KITSOKI_TEST_LSOF_COUNT_FILE:-}" ]; then
  printf 'probe\n' >>"$KITSOKI_TEST_LSOF_COUNT_FILE"
fi
if [ "${KITSOKI_TEST_LSOF_WARNING:-0}" = "1" ]; then
  printf 'lsof: incomplete recursive scan\n' >&2
  exit 1
fi
if [ "${KITSOKI_TEST_LSOF_ACTIVE:-0}" = "1" ]; then
  printf '4242\n'
  exit 0
fi
exit 1
SH
chmod +x "$purge_fakebin/lsof"
if PATH="$purge_fakebin:$PATH" KITSOKI_TEST_LSOF_WARNING=1 \
  "$dev_workspace" close --repo "$source_repo" --root "$(dirname "$park_quarantine")" --purge-quarantine "$park_quarantine" >"$tmp/warning-recovery-purge.log" 2>&1; then
  fail "provider purge accepted an inconclusive lsof warning"
fi
grep -Fq "activity probe is unavailable or inconclusive" "$tmp/warning-recovery-purge.log" ||
  { cat "$tmp/warning-recovery-purge.log" >&2; fail "provider purge did not report the inconclusive lsof warning"; }
[ -d "$park_quarantine/.git" ] || fail "inconclusive activity refusal did not restore the recovered quarantine"
if PATH="$purge_fakebin:$PATH" KITSOKI_TEST_LSOF_ACTIVE=1 \
  "$dev_workspace" close --repo "$source_repo" --root "$(dirname "$park_quarantine")" --purge-quarantine "$park_quarantine" >"$tmp/active-recovery-purge.log" 2>&1; then
  fail "provider purge succeeded despite post-isolation process activity"
fi
grep -Fq "active process IDs after isolation (4242)" "$tmp/active-recovery-purge.log" ||
  fail "provider purge did not report post-isolation process activity"
[ -d "$park_quarantine/.git" ] || fail "activity refusal did not restore the recovered quarantine"
[ "$(cat "$park_quarantine/arbitrary-ignored.dat")" = "arbitrary ignored state" ] ||
  fail "activity refusal lost arbitrary ignored state"

real_mv="$(command -v mv)"
cat >"$purge_fakebin/mv" <<SH
#!/usr/bin/env bash
case "\${KITSOKI_TEST_FAIL_ARTIFACT_PUBLICATION:-}:\$*" in
  ignored:*workspace-close/*-ignored/ignored-root.tar) exit 1 ;;
  git:*workspace-close/*-git/git-metadata.tar) exit 1 ;;
esac
exec "$real_mv" "\$@"
SH
chmod +x "$purge_fakebin/mv"
if PATH="$purge_fakebin:$PATH" KITSOKI_TEST_FAIL_ARTIFACT_PUBLICATION=ignored \
  "$dev_workspace" close --repo "$source_repo" --root "$(dirname "$park_quarantine")" --purge-quarantine "$park_quarantine" >"$tmp/failed-ignored-publication.log" 2>&1; then
  fail "provider purge succeeded after ignored-archive publication failed"
fi
grep -Fq "could not publish the complete ignored-tree archive" "$tmp/failed-ignored-publication.log" ||
  fail "provider purge did not report ignored-archive publication failure"
[ -d "$park_quarantine/.git" ] || fail "ignored-archive publication failure did not restore the quarantine"
if PATH="$purge_fakebin:$PATH" KITSOKI_TEST_FAIL_ARTIFACT_PUBLICATION=git \
  "$dev_workspace" close --repo "$source_repo" --root "$(dirname "$park_quarantine")" --purge-quarantine "$park_quarantine" >"$tmp/failed-git-publication.log" 2>&1; then
  fail "provider purge succeeded after Git-archive publication failed"
fi
grep -Fq "could not publish Git repository recovery metadata" "$tmp/failed-git-publication.log" ||
  fail "provider purge did not report Git-archive publication failure"
[ -d "$park_quarantine/.git" ] || fail "Git-archive publication failure did not restore the quarantine"
rm "$purge_fakebin/mv"

real_rm="$(command -v rm)"
cat >"$purge_fakebin/rm" <<SH
#!/usr/bin/env bash
case "\$*" in
  *closed-recovered-purging-*) exit 1 ;;
esac
exec "$real_rm" "\$@"
SH
chmod +x "$purge_fakebin/rm"
if PATH="$purge_fakebin:$PATH" \
  "$dev_workspace" close --repo "$source_repo" --root "$(dirname "$park_quarantine")" --purge-quarantine "$park_quarantine" >"$tmp/failed-rm-recovery-purge.log" 2>&1; then
  fail "provider purge reported success after deletion failed"
fi
grep -Fq "quarantine purge failed" "$tmp/failed-rm-recovery-purge.log" ||
  fail "provider purge did not report deletion failure"
[ -d "$park_quarantine/.git" ] || fail "deletion failure did not restore the recovered quarantine"
[ "$(cat "$park_quarantine/arbitrary-ignored.dat")" = "arbitrary ignored state" ] ||
  fail "deletion failure lost arbitrary ignored state"
rm "$purge_fakebin/rm"

"$dev_workspace" close --repo "$source_repo" --root "$root" "$park_workspace" >/dev/null
"$dev_workspace" close --repo "$source_repo" --root "$root" "$park_recovery" >/dev/null
purge_probe_count="$tmp/purge-probe-count"
: >"$purge_probe_count"
PATH="$purge_fakebin:$PATH" KITSOKI_TEST_LSOF_COUNT_FILE="$purge_probe_count" \
  "$dev_workspace" close --repo "$source_repo" --root "$(dirname "$park_quarantine")" --purge-quarantine "$park_quarantine" >/dev/null
[ "$(wc -l <"$purge_probe_count" | tr -d ' ')" = "2" ] ||
  fail "successful provider purge did not prove inactivity before and after preservation"
[ ! -e "$park_quarantine" ] || fail "provider purge left the recovered park quarantine"
[ "$(git -C "$source_repo" rev-parse "$park_quarantine_tip_ref")" = "$park_quarantine_sealed_head" ] ||
  fail "provider purge removed the durable exact quarantine-tip ref"
git -C "$source_repo" rev-parse --verify "$park_snapshot_ref" >/dev/null ||
  fail "provider purge removed the canonical dirty snapshot ref"
git -C "$source_repo" rev-parse --verify "$park_recovery_ref" >/dev/null ||
  fail "provider purge removed the signed recovery ref"
ignored_archive=""
while IFS= read -r candidate; do
  if tar -tf "$candidate" | grep -Fxq 'arbitrary-ignored.dat'; then
    ignored_archive="$candidate"
    break
  fi
done < <(find "$source_repo/.artifacts/workspace-close" -name ignored-root.tar -type f -print)
[ -n "$ignored_archive" ] || fail "provider purge did not retain a complete ignored-tree archive"
[ "$(tar -xOf "$ignored_archive" arbitrary-ignored.dat)" = "arbitrary ignored state" ] ||
  fail "complete ignored-tree archive changed arbitrary ignored bytes"
git_recovery_archive=""
while IFS= read -r candidate; do
  if grep -Fq "refs/stash" "$candidate/refs.tsv" 2>/dev/null &&
    grep -Fq "refs/heads/secondary-local" "$candidate/refs.tsv" 2>/dev/null; then
    git_recovery_archive="$candidate"
    break
  fi
done < <(find "$source_repo/.artifacts/workspace-close" -name git-metadata.tar -type f -exec dirname {} \;)
[ -n "$git_recovery_archive" ] || fail "provider purge did not retain complete Git repository metadata"
[ -s "$git_recovery_archive/unique-object-ids.txt" ] || fail "Git recovery archive omitted unique object inventory"
git -C "$source_repo" cat-file -e "$stash_oid^{commit}" >/dev/null 2>&1 &&
  fail "stash recovery fixture unexpectedly existed in the primary object database"
git -C "$source_repo" reflog expire --expire=now --expire-unreachable=now --all
git -C "$source_repo" gc --prune=now
[ "$(git -C "$source_repo" rev-parse "$park_quarantine_tip_ref")" = "$park_quarantine_sealed_head" ] ||
  fail "primary pruning removed the durable exact quarantine tip"
git_restore="$tmp/git-state-restore"
mkdir -p "$git_restore"
tar -C "$git_restore" -xf "$git_recovery_archive/git-metadata.tar"
mkdir -p "$git_restore/.git/objects/pack"
printf '%s\n' "$source_repo/.git/objects" >"$git_restore/.git/objects/info/alternates"
cp "$git_recovery_archive"/pack-*.pack "$git_restore/.git/objects/pack/"
cp "$git_recovery_archive"/pack-*.idx "$git_restore/.git/objects/pack/"
[ "$(git -C "$git_restore" show refs/stash^3:stash-secret.txt)" = "stash-only state" ] ||
  fail "Git recovery archive did not restore stash-only bytes"
[ "$(git -C "$git_restore" rev-parse refs/heads/secondary-local)" = "$secondary_oid" ] ||
  fail "Git recovery archive did not restore the secondary local ref"
git -C "$git_restore" cat-file -e "$reflog_only_oid^{commit}" ||
  fail "Git recovery archive did not restore the reflog-only commit"
[ "$(git -C "$git_restore" cat-file blob "$unreachable_blob_oid")" = "unreachable object recovery proof" ] ||
  fail "Git recovery archive did not restore the unreachable blob"
git -C "$git_restore" fsck --full --no-dangling >/dev/null ||
  fail "Git recovery archive did not reconstruct a connected repository"

# A Studio MCP server can itself run inside an agent capsule. That clone tracks
# staging/local as source/staging/local and intentionally has no local
# staging/local branch. Nested workspace creation and merge must preserve that
# tracked staging base rather than failing creation or falling back to main.
nested_repo="$tmp/nested-agent-capsule"
git clone -q --local --origin source "$source_repo" "$nested_repo"
gitc "$nested_repo" switch -q -c agent/nested source/staging/local
[ -z "$(git -C "$nested_repo" branch --list staging/local)" ] || fail "nested fixture unexpectedly has local staging/local"
nested_root="$nested_repo/.capsules/workspaces"
nested_json="$("$dev_workspace" create --repo "$nested_repo" --root "$nested_root" --id nested-child --branch agent/nested-child --json)"
nested_workspace="$(python3 -c 'import json,sys; print(json.load(sys.stdin)["path"])' <<<"$nested_json")"
[ "$(cat "$nested_workspace/feature.txt")" = "two" ] || fail "nested create did not resolve source/staging/local"
printf 'nested\n' >"$nested_workspace/nested.txt"
"$dev_workspace" commit --repo "$nested_repo" --root "$nested_root" "$nested_workspace" --message 'add nested feature' >/dev/null
"$dev_workspace" merge --repo "$nested_repo" --root "$nested_root" "$nested_workspace" --teardown >/dev/null
[ "$(git -C "$nested_repo" show staging/local:nested.txt)" = "nested" ] || fail "nested merge did not land on staging/local"

second_json="$("$dev_workspace" create --repo "$source_repo" --root "$root" --id case-2 --branch agent/case-2 --json)"
second_workspace="$(python3 -c 'import json,sys; print(json.load(sys.stdin)["path"])' <<<"$second_json")"
[ "$(cat "$second_workspace/feature.txt")" = "two" ] || fail "second default create did not include current staging content"
printf 'three\n' >"$second_workspace/feature2.txt"
"$dev_workspace" commit --repo "$source_repo" --root "$root" "$second_workspace" --message 'add second feature' >/dev/null
printf '.context/\n' >>"$second_workspace/.git/info/exclude"
mkdir -p "$second_workspace/.context" "$second_workspace/.artifacts"
printf 'review evidence\n' >"$second_workspace/.context/review.md"
printf 'generated evidence\n' >"$second_workspace/.artifacts/evidence.txt"
"$dev_workspace" merge --repo "$source_repo" --root "$root" "$second_workspace" --teardown >/dev/null
[ ! -e "$second_workspace" ] || fail "second merge --teardown left workspace behind"
[ "$(git -C "$source_repo" show staging/local:feature.txt)" = "two" ] || fail "second merge dropped existing staging/local content"
[ "$(git -C "$source_repo" show staging/local:feature2.txt)" = "three" ] || fail "second merge did not advance existing staging/local"
preserved_review="$(find "$source_repo/.artifacts/workspace-close" -path '*case-2-*/.context/review.md' -print -quit)"
preserved_evidence="$(find "$source_repo/.artifacts/workspace-close" -path '*case-2-*/.artifacts/evidence.txt' -print -quit)"
[ -n "$preserved_review" ] && [ "$(cat "$preserved_review")" = "review evidence" ] ||
  fail "teardown did not preserve ignored .context work"
[ -n "$preserved_evidence" ] && [ "$(cat "$preserved_evidence")" = "generated evidence" ] ||
  fail "teardown did not preserve ignored .artifacts work"

# The target can advance after a capsule clone is created. Merge must fetch
# and prove the target graph inside the capsule before rebase, run the gate on
# the rebased clean checkout, then land and tear the capsule down successfully.
integrity_json="$("$dev_workspace" create --repo "$source_repo" --root "$root" --id merge-object-integrity --branch agent/merge-object-integrity --json)"
integrity_workspace="$(python3 -c 'import json,sys; print(json.load(sys.stdin)["path"])' <<<"$integrity_json")"
printf 'workspace feature\n' >"$integrity_workspace/object-integrity-feature.txt"
"$dev_workspace" commit --repo "$source_repo" --root "$root" "$integrity_workspace" --message 'add object integrity feature' >/dev/null
gitc "$source_repo" switch -q staging/local
printf 'target advanced\n' >"$source_repo/target-advanced-after-clone.txt"
gitc "$source_repo" add target-advanced-after-clone.txt
gitc "$source_repo" commit --quiet -m 'advance target after capsule creation'
advanced_target="$(git -C "$source_repo" rev-parse staging/local)"
gitc "$source_repo" switch -q main
gate_order="$tmp/merge-object-integrity-gate"
"$dev_workspace" merge --repo "$source_repo" --root "$root" "$integrity_workspace" --gate "test '\$(cat target-advanced-after-clone.txt)' = 'target advanced'; test -z '\$(git status --porcelain)'; printf rebased-and-clean > '$gate_order'" --teardown >/dev/null
[ "$(cat "$gate_order")" = "rebased-and-clean" ] || fail "merge gate did not run after target fetch and rebase"
[ ! -e "$integrity_workspace" ] || fail "object-integrity merge --teardown left workspace behind"
[ -z "$(git -C "$source_repo" branch --list capsule/merge-object-integrity-land)" ] || fail "object-integrity merge left temporary landing branch behind"
[ "$(git -C "$source_repo" merge-base "$advanced_target" staging/local)" = "$advanced_target" ] || fail "object-integrity merge dropped target advancement"
[ "$(git -C "$source_repo" show staging/local:target-advanced-after-clone.txt)" = "target advanced" ] || fail "object-integrity merge cannot read advanced target object"
[ "$(git -C "$source_repo" show staging/local:object-integrity-feature.txt)" = "workspace feature" ] || fail "object-integrity merge did not land workspace feature"
git -C "$source_repo" fsck --connectivity-only --no-dangling staging/local >/dev/null || fail "object-integrity merge left unreadable target objects"

# Teardown happens after the target update. A cleanup failure must leave a
# truthful successful merge result (with a warning), not tell callers to retry
# a landing that already advanced the target.
truthful_json="$("$dev_workspace" create --repo "$source_repo" --root "$root" --id truthful-teardown --branch agent/truthful-teardown --json)"
truthful_workspace="$(python3 -c 'import json,sys; print(json.load(sys.stdin)["path"])' <<<"$truthful_json")"
printf 'landed despite teardown failure\n' >"$truthful_workspace/truthful-teardown.txt"
"$dev_workspace" commit --repo "$source_repo" --root "$root" "$truthful_workspace" --message 'add truthful teardown feature' >/dev/null
fakebin="$tmp/fakebin"
mkdir -p "$fakebin"
real_mv="$(command -v mv)"
cat >"$fakebin/mv" <<EOF
#!/usr/bin/env bash
if [ "\${1:-}" = "$truthful_workspace" ]; then
  echo "forced teardown failure" >&2
  exit 1
fi
exec "$real_mv" "\$@"
EOF
chmod +x "$fakebin/mv"
if ! PATH="$fakebin:$PATH" "$dev_workspace" merge --repo "$source_repo" --root "$root" "$truthful_workspace" --teardown >"$tmp/truthful-teardown.log" 2>&1; then
  fail "merge reported failure after target advanced and teardown failed"
fi
grep -Fq "warning: merge landed but teardown failed" "$tmp/truthful-teardown.log" || fail "merge did not warn about post-landing teardown failure"
grep -Fq "merged: agent/truthful-teardown -> staging/local" "$tmp/truthful-teardown.log" || fail "merge did not report successful target landing"
[ "$(git -C "$source_repo" show staging/local:truthful-teardown.txt)" = "landed despite teardown failure" ] || fail "truthful merge did not advance target"
[ -e "$truthful_workspace" ] || fail "forced teardown failure unexpectedly removed workspace"
"$dev_workspace" close --repo "$source_repo" --root "$root" "$truthful_workspace" >/dev/null

main_json="$("$dev_workspace" create --repo "$source_repo" --root "$root" --id case-main --branch agent/case-main --base main --target main --json)"
main_workspace="$(python3 -c 'import json,sys; print(json.load(sys.stdin)["path"])' <<<"$main_json")"
[ "$(python3 -c 'import json,sys; print(json.load(sys.stdin)["target"])' <"$main_workspace/.kitsoki-dev-workspace.json")" = "main" ] || fail "create --target main was not recorded in workspace manifest"
printf 'main landing\n' >"$main_workspace/main-feature.txt"
"$dev_workspace" commit --repo "$source_repo" --root "$root" "$main_workspace" --message 'add main feature' >/dev/null
"$dev_workspace" merge --repo "$source_repo" --root "$root" "$main_workspace" --target main --gate true --teardown >/dev/null
[ ! -e "$main_workspace" ] || fail "merge --target main --teardown left workspace behind"
[ "$(cat "$source_repo/main-feature.txt")" = "main landing" ] || fail "explicit main merge did not update primary checkout"

unrelated_json="$("$dev_workspace" create --repo "$source_repo" --root "$root" --id unrelated-dirty --branch agent/unrelated-dirty --json)"
unrelated_workspace="$(python3 -c 'import json,sys; print(json.load(sys.stdin)["path"])' <<<"$unrelated_json")"
printf 'branch\n' >"$unrelated_workspace/branch-only.txt"
"$dev_workspace" commit --repo "$source_repo" --root "$root" "$unrelated_workspace" --message 'add branch-only file' >/dev/null
printf 'local\n' >"$source_repo/local-only.txt"
"$dev_workspace" merge --repo "$source_repo" --root "$root" "$unrelated_workspace" --teardown >/dev/null
[ ! -e "$unrelated_workspace" ] || fail "merge with unrelated dirty primary left workspace behind"
[ "$(git -C "$source_repo" show staging/local:branch-only.txt)" = "branch" ] || fail "merge skipped branch-only file"
[ "$(cat "$source_repo/local-only.txt")" = "local" ] || fail "merge clobbered unrelated dirty primary file"

overlap_json="$("$dev_workspace" create --repo "$source_repo" --root "$root" --id overlapping-dirty --branch agent/overlapping-dirty --base main --target main --json)"
overlap_workspace="$(python3 -c 'import json,sys; print(json.load(sys.stdin)["path"])' <<<"$overlap_json")"
printf 'branch\n' >"$overlap_workspace/conflict.txt"
"$dev_workspace" commit --repo "$source_repo" --root "$root" "$overlap_workspace" --message 'add conflict file' >/dev/null
printf 'local\n' >"$source_repo/conflict.txt"
if "$dev_workspace" merge --repo "$source_repo" --root "$root" "$overlap_workspace" --target main --gate true --teardown >/tmp/kitsoki-dev-workspace-overlap.log 2>&1; then
  fail "merge succeeded despite overlapping dirty primary file"
fi
grep -Fq "conflict.txt" /tmp/kitsoki-dev-workspace-overlap.log || fail "overlap failure did not name conflicting file"
"$dev_workspace" close --repo "$source_repo" --root "$root" --force "$overlap_workspace" >/dev/null

dirty_json="$("$dev_workspace" create --repo "$source_repo" --root "$root" --id dirty --branch agent/dirty --json)"
dirty_workspace="$(python3 -c 'import json,sys; print(json.load(sys.stdin)["path"])' <<<"$dirty_json")"
printf 'dirty\n' >"$dirty_workspace/untracked.txt"
git -C "$dirty_workspace" config status.showUntrackedFiles no
if "$dev_workspace" close --repo "$source_repo" --root "$root" "$dirty_workspace" >/tmp/kitsoki-dev-workspace-close.log 2>&1; then
  fail "close succeeded on a dirty workspace"
fi
[ "$(cat "$dirty_workspace/untracked.txt")" = "dirty" ] ||
  fail "close lost an untracked file hidden by status.showUntrackedFiles=no"
"$dev_workspace" close --repo "$source_repo" --root "$root" --force "$dirty_workspace" >/dev/null
[ ! -e "$dirty_workspace" ] || fail "close --force left workspace behind"

echo "dev-workspace tests passed"
