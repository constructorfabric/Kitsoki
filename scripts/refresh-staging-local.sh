#!/usr/bin/env bash
# refresh-staging-local.sh - keep local staging current with main.
#
# Run from the protected primary checkout while on main. The helper first makes
# sure local main already contains the selected remote main, delegating remote
# reconciliation to sync-main-from-remote.sh when it does not. It then refreshes
# the managed staging capsule from the primary staging branch, rebases staging
# onto local main, and imports the refreshed staging ref back into the primary
# checkout.
set -euo pipefail

# The staging make targets must be safe to run from an interactive terminal.
# Git may otherwise honor EDITOR/VISUAL/PAGER and block on vim/less during
# merge/rebase message handling or paged output.
export GIT_EDITOR=true
export GIT_SEQUENCE_EDITOR=true
export GIT_MERGE_AUTOEDIT=no
export GIT_PAGER=cat

DEFAULT_BASE="main"
DEFAULT_REMOTE="origin"
DEFAULT_REMOTE_BRANCH="main"
DEFAULT_STAGING_BRANCH="staging/local"
DEFAULT_STAGING_CAPSULE=".capsules/staging/local"
CAPSULE_SENTINEL=".kitsoki-capsule"
CLONE_SENTINEL=".kitsoki-clone"
LOCAL_CONFIG=".kitsoki.local.yaml"

base="$DEFAULT_BASE"
remote="$DEFAULT_REMOTE"
remote_branch="$DEFAULT_REMOTE_BRANCH"
staging_branch="$DEFAULT_STAGING_BRANCH"
staging_capsule="$DEFAULT_STAGING_CAPSULE"
skip_remote=0
no_fetch=0
gate=""
dirty_action="auto"

usage() {
  cat >&2 <<EOF
usage:
  scripts/refresh-staging-local.sh [--remote origin] [--remote-branch main] [--base main]
                                   [--staging-branch staging/local]
                                   [--staging-capsule .capsules/staging/local]
                                   [--gate CMD] [--dirty-action ACTION]
                                   [--skip-remote] [--no-fetch]

Refreshes the managed local staging capsule from local staging, rebases it onto
local main, and updates the primary staging ref from the capsule.

If local main does not contain <remote>/<remote-branch>, this helper runs
scripts/sync-main-from-remote.sh to prepare that reconciliation, stops, and asks
you to complete the printed sync steps before rerunning.

--skip-remote intentionally skips the remote freshness check.
--no-fetch checks existing remote-tracking refs without fetching first.
--gate runs CMD inside the staging capsule after the rebase and before import.
--dirty-action controls dirty staging-capsule recovery:
  auto      prompt when interactive, otherwise stop with next steps (default)
  abort     stop with next steps
  prompt    require an interactive prompt
  preserve  stash changes, write a patch artifact, clean, and continue
  discard   discard changes, clean, and continue
EOF
}

die() {
  echo "error: $*" >&2
  exit 1
}

abs_path() {
  python3 - "$1" <<'PY'
import os
import sys

print(os.path.abspath(sys.argv[1]))
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

source_dirty() {
  local dir="$1"
  git -C "$dir" status --porcelain --untracked-files=all |
    grep -Ev '^[?][?] (\.kitsoki-capsule|\.kitsoki-clone|capsule-manifest.json|\.kitsoki-dev-workspace.json|\.kitsoki-owner)$' ||
    true
}

staging_sentinel_files=(
  "$CAPSULE_SENTINEL"
  "$CLONE_SENTINEL"
  "capsule-manifest.json"
  ".kitsoki-dev-workspace.json"
  ".kitsoki-owner"
)

print_dirty_next_steps() {
  local dir="$1"
  cat >&2 <<EOF

Refresh needs a clean staging capsule before it can rebase or import staging.

Useful next steps:
  inspect:   git -C "$dir" diff --stat HEAD && git -C "$dir" diff HEAD
  preserve:  scripts/refresh-staging-local.sh --dirty-action preserve
  discard:   scripts/refresh-staging-local.sh --dirty-action discard
  manual:    cd "$dir" and commit, stash, or remove the changes, then rerun refresh
EOF
}

clean_dirty_staging_capsule() {
  local dir="$1"
  git -C "$dir" reset --hard HEAD >/dev/null
  git -C "$dir" clean -fd \
    -e "$CAPSULE_SENTINEL" \
    -e "$CLONE_SENTINEL" \
    -e capsule-manifest.json \
    -e .kitsoki-dev-workspace.json \
    -e .kitsoki-owner >/dev/null
}

preserve_dirty_staging_capsule() {
  local dir="$1"
  local patch_dir patch stamp status_file sentinel_tmp sentinel moved output

  stamp="$(date +%Y%m%dT%H%M%S)"
  patch_dir="$repo_root/.artifacts/staging-local-preserved"
  mkdir -p "$patch_dir"
  patch="$patch_dir/staging-capsule-dirty-$stamp-$$.patch"
  status_file="$patch_dir/staging-capsule-dirty-$stamp-$$.status.txt"
  sentinel_tmp="$(mktemp -d "${TMPDIR:-/tmp}/kitsoki-staging-sentinels.XXXXXX")"

  git -C "$dir" status --short --untracked-files=all >"$status_file"
  for sentinel in "${staging_sentinel_files[@]}"; do
    if [ -e "$dir/$sentinel" ]; then
      mv "$dir/$sentinel" "$sentinel_tmp/$sentinel"
    fi
  done
  if ! output="$(git -C "$dir" stash push --include-untracked \
    -m "pre-refresh dirty staging capsule $stamp" 2>&1)"; then
    for moved in "$sentinel_tmp"/*; do
      [ -e "$moved" ] || continue
      mv "$moved" "$dir/$(basename "$moved")"
    done
    rm -rf "$sentinel_tmp"
    echo "$output" >&2
    return 1
  fi
  for moved in "$sentinel_tmp"/*; do
    [ -e "$moved" ] || continue
    mv "$moved" "$dir/$(basename "$moved")"
  done
  rm -rf "$sentinel_tmp"

  if ! git -C "$dir" rev-parse --verify --quiet refs/stash >/dev/null; then
    echo "error: git stash did not create a preserved change set" >&2
    return 1
  fi

  {
    printf 'staging capsule: %s\n' "$dir"
    printf 'branch: %s\n' "$(git -C "$dir" branch --show-current)"
    printf 'head: %s\n' "$(git -C "$dir" rev-parse HEAD)"
    printf 'stash: stash@{0}\n'
    printf '\n'
    printf 'status before preserve:\n'
    cat "$status_file"
    printf '\n'
    git -C "$dir" stash show --stat --include-untracked 'stash@{0}' || true
    printf '\n'
    git -C "$dir" stash show -p --binary --include-untracked 'stash@{0}' || true
  } >"$patch"

  echo "refresh-staging-local: preserved dirty staging-capsule changes:" >&2
  echo "  stash: git -C \"$dir\" stash show -p --include-untracked 'stash@{0}'" >&2
  echo "  patch: $patch" >&2
}

handle_dirty_staging_capsule() {
  local dir="$1"
  local dirty="$2"
  local action="$dirty_action"
  local choice confirm remaining

  echo "error: staging capsule has uncommitted changes: $dir" >&2
  printf '%s\n' "$dirty" >&2

  if [ "$action" = auto ]; then
    if [ -t 0 ] && [ -t 1 ]; then
      action=prompt
    else
      action=abort
    fi
  fi

  case "$action" in
    abort)
      print_dirty_next_steps "$dir"
      return 1
      ;;
    preserve)
      preserve_dirty_staging_capsule "$dir"
      ;;
    discard)
      clean_dirty_staging_capsule "$dir"
      echo "refresh-staging-local: discarded dirty staging-capsule changes" >&2
      ;;
    prompt)
      if [ ! -t 0 ] || [ ! -t 1 ]; then
        echo "error: --dirty-action prompt requires an interactive terminal" >&2
        print_dirty_next_steps "$dir"
        return 1
      fi
      while true; do
        cat >&2 <<EOF

Refresh needs a clean staging capsule. What do you want to do?
  1) Show the diff
  2) Preserve changes to a stash plus .artifacts patch, clean, and continue
  3) Discard changes, clean, and continue
  4) Stop and leave changes in place
EOF
        printf 'Choose [1-4]: ' >&2
        IFS= read -r choice || choice=4
        case "$choice" in
          1|s|S|show)
            git -C "$dir" --no-pager status --short --untracked-files=all >&2
            git -C "$dir" --no-pager diff --stat HEAD >&2 || true
            git -C "$dir" --no-pager diff HEAD >&2 || true
            ;;
          2|p|P|preserve)
            preserve_dirty_staging_capsule "$dir"
            break
            ;;
          3|d|D|discard)
            printf "Type 'discard' to remove these staging-capsule changes: " >&2
            IFS= read -r confirm || confirm=
            if [ "$confirm" = discard ]; then
              clean_dirty_staging_capsule "$dir"
              echo "refresh-staging-local: discarded dirty staging-capsule changes" >&2
              break
            fi
            echo "refresh-staging-local: discard not confirmed" >&2
            ;;
          4|q|Q|quit|"")
            echo "refresh-staging-local: stopped; staging capsule left unchanged" >&2
            return 1
            ;;
          *)
            echo "refresh-staging-local: choose 1, 2, 3, or 4" >&2
            ;;
        esac
      done
      ;;
    *)
      die "invalid --dirty-action: $dirty_action (expected auto, abort, prompt, preserve, or discard)"
      ;;
  esac

  remaining="$(source_dirty "$dir")"
  if [ -n "$remaining" ]; then
    echo "error: staging capsule is still dirty after --dirty-action $dirty_action:" >&2
    printf '%s\n' "$remaining" >&2
    return 1
  fi
}

ensure_managed_capsule() {
  local dir="$1"
  [ -d "$dir/.git" ] || die "staging capsule is not a git checkout: $dir"
  if [ ! -f "$dir/$CAPSULE_SENTINEL" ] && [ ! -f "$dir/$CLONE_SENTINEL" ]; then
    die "staging capsule is not managed: $dir (missing $CAPSULE_SENTINEL or $CLONE_SENTINEL)"
  fi
}

copy_local_config() {
  local src="$repo_root/$LOCAL_CONFIG"
  local dst="$staging_capsule/$LOCAL_CONFIG"
  local relock_file=0
  [ -f "$src" ] || return 0
  if [ -e "$dst" ] && [ "$(user_write_bit "$dst")" = "0" ]; then
    chmod u+w "$dst"
    relock_file=1
  fi
  if ! cp -p "$src" "$dst"; then
    if [ "$relock_file" = 1 ]; then
      chmod u-w "$dst" 2>/dev/null || true
    fi
    return 1
  fi
  if [ "$relock_file" = 1 ]; then
    chmod u-w "$dst" 2>/dev/null || true
  fi
  echo "refresh-staging-local: copied $LOCAL_CONFIG into staging capsule" >&2
}

while [ "$#" -gt 0 ]; do
  case "$1" in
    --base)
      base="${2:?--base requires a value}"
      shift 2
      ;;
    --remote)
      remote="${2:?--remote requires a value}"
      shift 2
      ;;
    --remote-branch)
      remote_branch="${2:?--remote-branch requires a value}"
      shift 2
      ;;
    --staging-branch)
      staging_branch="${2:?--staging-branch requires a value}"
      shift 2
      ;;
    --staging-capsule|--source-dir)
      staging_capsule="${2:?$1 requires a value}"
      shift 2
      ;;
    --gate)
      gate="${2:?--gate requires a value}"
      shift 2
      ;;
    --dirty-action)
      dirty_action="${2:?--dirty-action requires a value}"
      shift 2
      ;;
    --skip-remote)
      skip_remote=1
      shift
      ;;
    --no-fetch)
      no_fetch=1
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      usage
      die "unknown argument: $1"
      ;;
  esac
done

repo_root="$(git rev-parse --show-toplevel)"
cd "$repo_root"

if [ "$(git rev-parse --git-dir)" != "$(git rev-parse --git-common-dir)" ]; then
  die "run this in the primary checkout, not a linked worktree or secondary checkout"
fi
current_branch="$(git symbolic-ref --quiet --short HEAD 2>/dev/null || true)"
if [ "$current_branch" != "$base" ]; then
  die "primary checkout is on '$current_branch', expected '$base'"
fi
git check-ref-format --branch "$base" >/dev/null || die "invalid base branch: $base"
git check-ref-format --branch "$staging_branch" >/dev/null || die "invalid staging branch: $staging_branch"
if [ "$staging_branch" = "$base" ]; then
  die "staging branch and base branch are both $base"
fi
if [ "$(git symbolic-ref --quiet --short HEAD 2>/dev/null || true)" = "$staging_branch" ]; then
  die "primary checkout is on $staging_branch; switch to $base before refreshing staging"
fi

if [ "$skip_remote" -eq 0 ]; then
  if git remote get-url "$remote" >/dev/null 2>&1; then
    remote_ref="refs/remotes/$remote/$remote_branch"
    if [ "$no_fetch" -eq 0 ]; then
      git fetch "$remote" "$remote_branch:$remote_ref"
    fi
    git rev-parse --verify --quiet "$remote_ref" >/dev/null ||
      die "remote ref not found: $remote_ref"
    if git merge-base --is-ancestor "$remote_ref" "$base"; then
      ahead="$(git rev-list --count "$remote_ref..$base")"
      if [ "$ahead" -gt 0 ]; then
        if [ "$ahead" -eq 1 ]; then
          commit_word=commit
        else
          commit_word=commits
        fi
        echo "$base already contains $remote/$remote_branch, but local $base is ahead by $ahead $commit_word."
      else
        echo "$base already contains $remote/$remote_branch"
      fi
    else
      echo "refresh-staging-local: local $base does not contain $remote/$remote_branch; preparing remote sync first." >&2
      sync_args=(--remote "$remote" --remote-branch "$remote_branch" --base "$base")
      if [ "$no_fetch" -eq 1 ]; then
        sync_args+=(--no-fetch)
      fi
      scripts/sync-main-from-remote.sh "${sync_args[@]}"
      cat >&2 <<EOF

refresh-staging-local: complete the remote-main sync steps above, then rerun:
  scripts/refresh-staging-local.sh --remote "$remote" --remote-branch "$remote_branch" --base "$base" --staging-branch "$staging_branch" --staging-capsule "$staging_capsule"
EOF
      exit 2
    fi
  else
    echo "warning: remote '$remote' is not configured; skipping remote freshness check" >&2
  fi
fi

if [ "${staging_capsule#/}" = "$staging_capsule" ]; then
  staging_capsule="$repo_root/$staging_capsule"
fi
staging_capsule="$(abs_path "$staging_capsule")"
ensure_managed_capsule "$staging_capsule"

source_branch="$(git -C "$staging_capsule" branch --show-current)"
if [ "$source_branch" != "$staging_branch" ]; then
  die "staging capsule $staging_capsule is on $source_branch, expected $staging_branch"
fi
dirty="$(source_dirty "$staging_capsule")"
if [ -n "$dirty" ]; then
  handle_dirty_staging_capsule "$staging_capsule" "$dirty" || exit 1
fi
git -C "$staging_capsule" remote get-url source >/dev/null 2>&1 ||
  die "staging capsule has no 'source' remote: $staging_capsule"

git -C "$repo_root" rev-parse --verify --quiet "refs/heads/$base" >/dev/null ||
  die "base branch not found: $base"
git -C "$repo_root" rev-parse --verify --quiet "refs/heads/$staging_branch" >/dev/null ||
  die "staging branch not found: $staging_branch"

echo "refresh-staging-local: fetching local $staging_branch and $base into staging capsule" >&2
git -C "$staging_capsule" fetch source \
  "refs/heads/$staging_branch:refs/remotes/source/$staging_branch" \
  "refs/heads/$base:refs/remotes/source/$base"

echo "refresh-staging-local: rebasing staging capsule onto source/$staging_branch" >&2
git -C "$staging_capsule" rebase "source/$staging_branch"

echo "refresh-staging-local: rebasing staging capsule onto source/$base" >&2
git -C "$staging_capsule" rebase "source/$base"

copy_local_config

if [ -n "$gate" ]; then
  echo "refresh-staging-local: running gate in $staging_capsule: $gate" >&2
  (cd "$staging_capsule" && sh -c "$gate")
fi

git fetch "$staging_capsule" "+$staging_branch:refs/heads/$staging_branch"

printf '%s -> %s\n' "$staging_branch" "$(git rev-parse --short "$staging_branch")"
printf 'staging capsule: %s\n' "$staging_capsule"
printf 'base: %s (%s)\n' "$base" "$(git rev-parse --short "$base")"
