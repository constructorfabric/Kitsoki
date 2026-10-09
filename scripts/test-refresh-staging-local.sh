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
  chmod +x "$repo/scripts/"*.sh
}

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

mkdir -p "$local_repo/.capsules/staging"
git clone -q --no-local "$local_repo" "$local_repo/.capsules/staging/local"
git -C "$local_repo/.capsules/staging/local" remote rename origin source
git -C "$local_repo/.capsules/staging/local" config user.name "Test User"
git -C "$local_repo/.capsules/staging/local" config user.email "test@example.invalid"
git -C "$local_repo/.capsules/staging/local" switch -q -c staging/local source/staging/local
touch "$local_repo/.capsules/staging/local/.kitsoki-capsule"

commit_file "$local_repo/.capsules/staging/local" staged.txt staged
commit_file "$local_repo" main.txt main-update

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
cmp "$local_repo/.kitsoki.local.yaml" "$local_repo/.capsules/staging/local/.kitsoki.local.yaml" >/dev/null ||
  fail "refresh did not copy .kitsoki.local.yaml into staging capsule"

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

preserve_out="$tmp/dirty-capsule-preserve.out"
(
  cd "$local_repo"
  scripts/refresh-staging-local.sh --skip-remote --dirty-action preserve --gate 'git diff --check'
) >"$preserve_out" 2>&1
assert_contains "$preserve_out" "preserved dirty staging-capsule changes"
assert_contains "$preserve_out" "staging/local ->"
[ ! -e "$local_repo/.capsules/staging/local/scratch.txt" ] ||
  fail "preserve did not clean the untracked file"
preserve_patch="$(ls "$local_repo/.artifacts/staging-local-preserved"/staging-capsule-dirty-*.patch | tail -1)"
assert_contains "$preserve_patch" "scratch.txt"
remaining_dirty="$(git -C "$local_repo/.capsules/staging/local" status --porcelain --untracked-files=all |
  grep -Ev '^[?][?] (\.kitsoki-capsule|\.kitsoki-clone|capsule-manifest.json|\.kitsoki-dev-workspace.json|\.kitsoki-owner)$' ||
  true)"
[ -z "$remaining_dirty" ] ||
  fail "preserve left staging capsule dirty: $remaining_dirty"

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
