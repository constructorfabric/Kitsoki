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
resume=0
gate=""
dirty_action="auto"

usage() {
  cat >&2 <<EOF
usage:
  scripts/refresh-staging-local.sh [--remote origin] [--remote-branch main] [--base main]
                                   [--staging-branch staging/local]
                                   [--staging-capsule .capsules/staging/local]
                                   [--gate CMD] [--dirty-action ACTION]
                                   [--skip-remote] [--no-fetch] [--resume]

Refreshes the managed local staging capsule from local staging, rebases it onto
local main, and updates the primary staging ref from the capsule.

If local main does not contain <remote>/<remote-branch>, this helper runs
scripts/sync-main-from-remote.sh to prepare that reconciliation, stops, and asks
you to complete the printed sync steps before rerunning.

--skip-remote intentionally skips the remote freshness check.
--no-fetch checks existing remote-tracking refs without fetching first.
--resume atomically imports a clean, manually completed staging rebase.
--gate runs CMD inside the staging capsule after the rebase and before import.
--dirty-action controls dirty staging-capsule recovery:
  auto      prompt when interactive, otherwise stop with next steps (default)
  abort     stop with next steps
  move      commit the changes in a new managed recovery capsule, clean, and continue
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
  local dir="$1" status line
  if ! status="$(git -C "$dir" status --porcelain --untracked-files=all)"; then
    die "could not inspect staging capsule status: $dir"
  fi
  while IFS= read -r line; do
    case "$line" in
      ""|"?? $CAPSULE_SENTINEL"|"?? $CLONE_SENTINEL"|"?? capsule-manifest.json"|"?? .kitsoki-dev-workspace.json"|"?? .kitsoki-owner") ;;
      *) printf '%s\n' "$line" ;;
    esac
  done <<<"$status"
}

# Construct the exact tree expected when the net change from old_base to
# snapshot is transplanted onto new_base. merge-tree performs the three-way
# merge entirely in Git's object database: external diff/textconv/whitespace
# settings and a moving worktree/index cannot weaken the proof, and a change
# already present on new_base is handled as an ordinary clean merge.
expected_tree_after_delta() {
  local dir="$1" old_base="$2" snapshot="$3" new_base="$4" tree
  tree="$(git -C "$dir" merge-tree \
    --write-tree \
    --no-messages \
    --merge-base "$old_base" \
    "$new_base" "$snapshot")" || return 1
  printf '%s\n' "$tree"
}

materialize_expected_tree_merge() {
  local dir="$1" expected_tree="$2" first_parent="$3" second_parent="$4" label="$5"
  local reconciled tree
  reconciled="$(git -C "$dir" commit-tree "$expected_tree" \
    -p "$first_parent" \
    -p "$second_parent" \
    -m "chore: reconcile $label" \
    -m "Preserve the independently proven tree when sequential rebase replay produces a different result. The exact pre-refresh staging history remains reachable through the second parent." \
    -m "Signed-off-by: Kitsoki Agent <agent@kitsoki.dev>")" || return 1
  git -C "$dir" reset --hard "$reconciled" >/dev/null || return 1
  tree="$(git -C "$dir" rev-parse "$reconciled^{tree}")" || return 1
  [ "$tree" = "$expected_tree" ] || return 1
  printf '%s\n' "$reconciled"
}

refuse_untracked_tree_collisions() {
  local dir="$1" from_ref="$2" to_ref="$3" context="$4" output
  if ! output="$(python3 - "$dir" "$from_ref" "$to_ref" <<'PY'
import subprocess
import sys

repo, from_ref, to_ref = sys.argv[1:]
changed_run = subprocess.run(
    ["git", "-C", repo, "diff", "--name-only", "-z", "--no-renames", from_ref, to_ref],
    stdout=subprocess.PIPE,
    stderr=subprocess.PIPE,
    check=False,
)
if changed_run.returncode:
    sys.stderr.buffer.write(changed_run.stderr)
    sys.exit(changed_run.returncode)
changed = {p.decode("utf-8", "surrogateescape") for p in changed_run.stdout.split(b"\0") if p}
status_run = subprocess.run(
    [
        "git", "-C", repo, "status", "--porcelain=v1", "-z",
        "--untracked-files=all", "--ignored=traditional",
    ],
    stdout=subprocess.PIPE,
    stderr=subprocess.PIPE,
    check=False,
)
if status_run.returncode:
    sys.stderr.buffer.write(status_run.stderr)
    sys.exit(status_run.returncode)
local = set()
for entry in status_run.stdout.split(b"\0"):
    if not entry or entry[:2] not in (b"??", b"!!"):
        continue
    path = entry[3:].decode("utf-8", "surrogateescape").rstrip("/")
    if path:
        local.add(path)

def overlaps(a, b):
    return a == b or a.startswith(b + "/") or b.startswith(a + "/")

conflicts = sorted({p for p in local for c in changed if overlaps(p, c)})
for path in conflicts:
    print(path)
sys.exit(1 if conflicts else 0)
PY
)"; then
    echo "error: $context would overwrite untracked or ignored capsule paths:" >&2
    [ -z "$output" ] || printf '%s\n' "$output" >&2
    return 1
  fi
}

print_dirty_next_steps() {
  local dir="$1"
  cat >&2 <<EOF

Refresh needs a clean staging capsule before it can rebase or import staging.

Useful next steps:
  inspect:   git -C "$dir" diff --stat HEAD && git -C "$dir" diff HEAD
  move:      scripts/refresh-staging-local.sh --dirty-action move
  preserve:  scripts/refresh-staging-local.sh --dirty-action preserve
  discard:   scripts/refresh-staging-local.sh --dirty-action discard
  manual:    cd "$dir" and commit, stash, or remove the changes, then rerun refresh
EOF
}

clean_dirty_staging_capsule() {
  local dir="$1"
  git -c submodule.recurse=false -C "$dir" reset --hard HEAD >/dev/null
  git -C "$dir" clean -fd \
    -e "$CAPSULE_SENTINEL" \
    -e "$CLONE_SENTINEL" \
    -e capsule-manifest.json \
    -e .kitsoki-dev-workspace.json \
    -e .kitsoki-owner >/dev/null
}

preserve_dirty_staging_capsule() {
  local dir="$1"
  local patch_dir patch stamp status_file output stash_oid primary_snapshot_ref

  stamp="$(date +%Y%m%dT%H%M%S)"
  patch_dir="$repo_root/.artifacts/staging-local-preserved"
  mkdir -p "$patch_dir"
  patch="$patch_dir/staging-capsule-dirty-$stamp-$$.patch"
  status_file="$patch_dir/staging-capsule-dirty-$stamp-$$.status.txt"

  git -C "$dir" status --short --untracked-files=all >"$status_file"
  # Management sentinels are ignored, so --include-untracked leaves them (and
  # all other ignored evidence) in place. Supplying a pathspec here makes Git
  # stage the ignored sentinels while constructing the stash on older Git
  # versions, which can leave the user's changes staged instead of preserved.
  if ! output="$(git -C "$dir" stash push --include-untracked \
    -m "pre-refresh dirty staging capsule $stamp" 2>&1)"; then
    echo "$output" >&2
    return 1
  fi

  if ! git -C "$dir" rev-parse --verify --quiet refs/stash >/dev/null; then
    echo "error: git stash did not create a preserved change set" >&2
    return 1
  fi
  stash_oid="$(git -C "$dir" rev-parse refs/stash)"
  primary_snapshot_ref="refs/kitsoki/dirty-snapshot/$stash_oid"
  if git -C "$repo_root" rev-parse --verify --quiet "$primary_snapshot_ref" >/dev/null; then
    [ "$(git -C "$repo_root" rev-parse "$primary_snapshot_ref")" = "$stash_oid" ] || {
      echo "error: content-addressed preserve ref points at the wrong object" >&2
      return 1
    }
  else
    git -C "$repo_root" fetch --no-tags "$dir" "$stash_oid:$primary_snapshot_ref"
  fi
  git -C "$repo_root" fsck --connectivity-only --no-dangling "$primary_snapshot_ref" >/dev/null 2>&1 || {
    echo "error: preserved staging snapshot is not fully readable from the primary repository" >&2
    return 1
  }

  {
    printf 'staging capsule: %s\n' "$dir"
    printf 'branch: %s\n' "$(git -C "$dir" branch --show-current)"
    printf 'head: %s\n' "$(git -C "$dir" rev-parse HEAD)"
    printf 'stash: stash@{0}\n'
    printf 'snapshot ref: %s\n' "$primary_snapshot_ref"
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
  echo "  snapshot ref: $primary_snapshot_ref" >&2
  echo "  patch: $patch" >&2
}

move_dirty_staging_capsule() {
  local dir="$1"
  local stamp recovery_id recovery_branch recovery_dir patch_dir status_file
  local untracked_file untracked_tar current_untracked_file current_untracked_tar
  local snapshot_oid snapshot_tree snapshot_index_tree current_oid current_tree current_index_tree
  local recovery_parent recovery_snapshot_ref expected_tree recovery_tree recovery_commit
  local primary_snapshot_ref primary_recovery_ref quarantine_root quarantine_dir staging_root staging_id replacement_bootstrap

  stamp="$(date +%Y%m%dT%H%M%S)"
  recovery_id="staging-dirty-recovery-$stamp-$$"
  recovery_branch="agent/$recovery_id"
  recovery_dir="$repo_root/.capsules/workspaces/$recovery_id"
  patch_dir="$repo_root/.artifacts/staging-local-recovery"
  mkdir -p "$patch_dir"
  status_file="$patch_dir/$recovery_id.status.txt"
  untracked_file="$patch_dir/$recovery_id.untracked"
  untracked_tar="$patch_dir/$recovery_id.untracked.tar"
  current_untracked_file="$(mktemp "${TMPDIR:-/tmp}/kitsoki-staging-current-untracked.XXXXXX")"
  current_untracked_tar="$(mktemp "${TMPDIR:-/tmp}/kitsoki-staging-current-untracked-tar.XXXXXX")"

  git -C "$dir" status --short --untracked-files=all >"$status_file"
  snapshot_oid="$(git -C "$dir" stash create "kitsoki staging dirty snapshot $stamp")"
  [ -n "$snapshot_oid" ] || snapshot_oid="$(git -C "$dir" rev-parse HEAD)"
  snapshot_tree="$(git -C "$dir" rev-parse "$snapshot_oid^{tree}")"
  snapshot_index_tree="$(git -C "$dir" rev-parse "$snapshot_oid^2^{tree}" 2>/dev/null || git -C "$dir" rev-parse HEAD^{tree})"
  git -C "$dir" ls-files --others --exclude-standard -z >"$untracked_file"
  if [ -s "$untracked_file" ]; then
    tar -C "$dir" --null --files-from="$untracked_file" -cf "$untracked_tar"
  fi

  if ! scripts/dev-workspace.sh create \
    --id "$recovery_id" \
    --branch "$recovery_branch" \
    --base "$staging_branch" \
    --target "$staging_branch" \
    --no-bootstrap >/dev/null; then
    echo "error: could not create recovery capsule; staging capsule left unchanged" >&2
    return 1
  fi

  recovery_parent="$(git -C "$recovery_dir" rev-parse HEAD)"
  recovery_snapshot_ref="refs/kitsoki/dirty-snapshot/$snapshot_oid"
  git -C "$recovery_dir" fetch --no-tags "$dir" "$snapshot_oid:$recovery_snapshot_ref"
  git -C "$recovery_dir" reset --hard "$recovery_snapshot_ref" >/dev/null
  if [ -s "$untracked_file" ]; then
    tar -C "$recovery_dir" -xf "$untracked_tar"
  fi
  git -C "$recovery_dir" add -A
  expected_tree="$(git -C "$recovery_dir" write-tree)"
  git -C "$recovery_dir" reset --soft "$recovery_parent"
  if ! git -C "$recovery_dir" commit --allow-empty --signoff \
    -m "chore: recover dirty staging capsule changes" >/dev/null; then
    echo "error: could not commit recovery capsule; staging capsule left unchanged" >&2
    return 1
  fi
  recovery_tree="$(git -C "$recovery_dir" rev-parse HEAD^{tree})"
  [ "$recovery_tree" = "$expected_tree" ] || {
    echo "error: recovery capsule tree does not match the captured dirty snapshot; staging capsule left unchanged" >&2
    return 1
  }
  recovery_commit="$(git -C "$recovery_dir" rev-parse HEAD)"
  primary_snapshot_ref="refs/kitsoki/dirty-snapshot/$snapshot_oid"
  primary_recovery_ref="refs/kitsoki/dirty-recovery/$recovery_commit"
  git -C "$repo_root" fetch --no-tags "$dir" "$snapshot_oid:$primary_snapshot_ref"
  git -C "$repo_root" fetch --no-tags "$recovery_dir" "$recovery_commit:$primary_recovery_ref"
  [ "$(git -C "$repo_root" rev-parse "$primary_snapshot_ref")" = "$snapshot_oid" ] ||
    die "dirty staging snapshot was not anchored in the primary repository"
  [ "$(git -C "$repo_root" rev-parse "$primary_recovery_ref^{tree}")" = "$expected_tree" ] ||
    die "dirty staging recovery tree was not anchored in the primary repository"
  git -C "$recovery_dir" update-ref -d "$recovery_snapshot_ref" >/dev/null 2>&1 || true

  # Recheck both tracked/index state and untracked bytes immediately before
  # cleaning. A concurrent writer must leave the original capsule untouched.
  current_oid="$(git -C "$dir" stash create "kitsoki staging dirty snapshot recheck $stamp")"
  [ -n "$current_oid" ] || current_oid="$(git -C "$dir" rev-parse HEAD)"
  current_tree="$(git -C "$dir" rev-parse "$current_oid^{tree}")"
  current_index_tree="$(git -C "$dir" rev-parse "$current_oid^2^{tree}" 2>/dev/null || git -C "$dir" rev-parse HEAD^{tree})"
  git -C "$dir" ls-files --others --exclude-standard -z >"$current_untracked_file"
  if [ -s "$current_untracked_file" ]; then
    tar -C "$dir" --null --files-from="$current_untracked_file" -cf "$current_untracked_tar"
  fi
  if [ "$current_tree" != "$snapshot_tree" ] ||
    [ "$current_index_tree" != "$snapshot_index_tree" ] ||
    ! cmp -s "$untracked_file" "$current_untracked_file" ||
    ! cmp -s "$untracked_tar" "$current_untracked_tar"; then
    echo "error: staging capsule changed while its recovery snapshot was being committed; original left unchanged" >&2
    return 1
  fi

  # Atomically remove the dirty capsule from the active path. Keep the full
  # checkout as an ignored quarantine so late writers and ignored evidence are
  # preserved; recreate a clean managed staging capsule at the original path.
  quarantine_root="$(dirname "$dir")"
  quarantine_dir="$quarantine_root/closed-recovered-$recovery_id"
  mkdir -p "$quarantine_root"
  [ ! -e "$quarantine_dir" ] || die "staging dirty-source quarantine already exists: $quarantine_dir"
  mv "$dir" "$quarantine_dir"
  staging_root="$(dirname "$dir")"
  staging_id="$(basename "$dir")"
  replacement_bootstrap="--no-bootstrap"
  if make -C "$repo_root" -n bootstrap-workspace >/dev/null 2>&1 ||
    make -C "$repo_root" -n bootstrap-worktree >/dev/null 2>&1; then
    replacement_bootstrap="--bootstrap"
  fi
  if ! scripts/dev-workspace.sh create \
    --repo "$repo_root" \
    --root "$staging_root" \
    --id "$staging_id" \
    --branch "$staging_branch" \
    --base "$staging_branch" \
    --target "$base" \
    "$replacement_bootstrap" >/dev/null; then
    [ ! -e "$dir" ] && mv "$quarantine_dir" "$dir" >/dev/null 2>&1 || true
    echo "error: recovery is anchored, but a clean staging capsule could not be recreated" >&2
    return 1
  fi
  if ! scripts/dev-workspace.sh seal-recovered-quarantine \
    --repo "$repo_root" \
    --root "$staging_root" \
    --snapshot-ref "$primary_snapshot_ref" \
    --recovery-ref "$primary_recovery_ref" \
    "$quarantine_dir" >/dev/null; then
    echo "error: clean staging capsule was recreated and recovery refs are anchored, but the source quarantine could not be sealed" >&2
    return 1
  fi
  rm -f "$current_untracked_file" "$current_untracked_tar"
  echo "refresh-staging-local: moved dirty staging-capsule changes to a committed recovery capsule:" >&2
  echo "  workspace: $recovery_dir" >&2
  echo "  branch: $recovery_branch" >&2
  echo "  snapshot tree: $expected_tree" >&2
  echo "  snapshot ref: $primary_snapshot_ref" >&2
  echo "  recovery ref: $primary_recovery_ref" >&2
  echo "  original quarantine: $quarantine_dir" >&2
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
    move)
      move_dirty_staging_capsule "$dir"
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
  1) Move changes to a new managed capsule, commit them, clean, and continue (default)
  2) Show the diff
  3) Preserve changes to a stash plus .artifacts patch, clean, and continue
  4) Discard changes, clean, and continue
  5) Stop and leave changes in place
EOF
        printf 'Choose [1-5] (default 1): ' >&2
        IFS= read -r choice || choice=1
        case "$choice" in
          1|m|M|move|"")
            move_dirty_staging_capsule "$dir"
            break
            ;;
          2|s|S|show)
            git -C "$dir" --no-pager status --short --untracked-files=all >&2
            git -C "$dir" --no-pager diff --stat HEAD >&2 || true
            git -C "$dir" --no-pager diff HEAD >&2 || true
            ;;
          3|p|P|preserve)
            preserve_dirty_staging_capsule "$dir"
            break
            ;;
          4|d|D|discard)
            printf "Type 'discard' to remove these staging-capsule changes: " >&2
            IFS= read -r confirm || confirm=
            if [ "$confirm" = discard ]; then
              clean_dirty_staging_capsule "$dir"
              echo "refresh-staging-local: discarded dirty staging-capsule changes" >&2
              break
            fi
            echo "refresh-staging-local: discard not confirmed" >&2
            ;;
          5|q|Q|quit)
            echo "refresh-staging-local: stopped; staging capsule left unchanged" >&2
            return 1
            ;;
          *)
            echo "refresh-staging-local: choose 1, 2, 3, 4, or 5" >&2
            ;;
        esac
      done
      ;;
    *)
      die "invalid --dirty-action: $dirty_action (expected auto, abort, move, prompt, preserve, or discard)"
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
    --resume)
      resume=1
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

# A conflict deliberately leaves the managed capsule in place so its rebase can
# be resolved there. An ordinary retry would otherwise replay that work from
# the old primary staging snapshot. Resume is explicit to avoid treating an
# arbitrary divergent capsule as a resolution.
if [ "$resume" -eq 1 ]; then
  staging_start="$(git -C "$repo_root" rev-parse "refs/heads/$staging_branch")"
  base_start="$(git -C "$repo_root" rev-parse "refs/heads/$base")"
  resumed_result="$(git -C "$staging_capsule" rev-parse "refs/heads/$staging_branch")"
  git -C "$staging_capsule" merge-base --is-ancestor "$base_start" "$resumed_result" ||
    die "cannot resume: staging capsule is not based on current $base; finish or abort its rebase first"
  if [ -n "$gate" ]; then
    echo "refresh-staging-local: running resume gate in $staging_capsule: $gate" >&2
    (cd "$staging_capsule" && sh -c "$gate")
  fi
  post_gate_dirty="$(source_dirty "$staging_capsule")"
  [ -z "$post_gate_dirty" ] || die "cannot resume: gate left uncommitted staging-capsule changes"
  resume_import_ref="refs/kitsoki/refresh/resume-$(date -u +%Y%m%dT%H%M%SZ)-$$/import"
  git -C "$repo_root" fetch "$staging_capsule" "$resumed_result:$resume_import_ref"
  [ "$(git -C "$staging_capsule" rev-parse "refs/heads/$staging_branch")" = "$resumed_result" ] ||
    die "staging capsule advanced while importing resumed result; rerun resume"
  [ "$(git -C "$repo_root" rev-parse "refs/heads/$base")" = "$base_start" ] ||
    die "$base advanced while importing resumed staging; rerun refresh"
  if ! {
    printf 'start\n'
    printf 'verify refs/heads/%s %s\n' "$base" "$base_start"
    printf 'update refs/heads/%s %s %s\n' "$staging_branch" "$resumed_result" "$staging_start"
    printf 'prepare\n'
    printf 'commit\n'
  } | git -C "$repo_root" update-ref --stdin; then
    die "$base or $staging_branch changed while importing resumed staging; refusing to overwrite newer work"
  fi
  git -C "$repo_root" update-ref -d "$resume_import_ref" >/dev/null 2>&1 || true
  printf '%s -> %s (resumed)\n' "$staging_branch" "$(git rev-parse --short "$staging_branch")"
  printf 'staging capsule: %s\n' "$staging_capsule"
  printf 'base: %s (%s)\n' "$base" "$(git rev-parse --short "$base")"
  exit 0
fi

# Capture the exact staging input before touching the capsule.  A workspace
# merge may advance staging/local while this helper is rebasing; that new work
# is authoritative and must never be replaced by a stale staging capsule.
staging_start="$(git -C "$repo_root" rev-parse "refs/heads/$staging_branch")"
base_primary_start="$(git -C "$repo_root" rev-parse "refs/heads/$base")"
capsule_upstream_start="$(git -C "$staging_capsule" rev-parse "refs/remotes/source/$staging_branch")" ||
  die "staging capsule cannot resolve its pre-refresh source/$staging_branch"
capsule_original_start="$(git -C "$staging_capsule" rev-parse HEAD)"
primary_recovery_ref="refs/kitsoki/staging-capsule-recovery/$capsule_original_start"
primary_upstream_recovery_ref="refs/kitsoki/staging-capsule-recovery/$capsule_upstream_start"

# Failed refreshes intentionally retain recovery refs. Use a timestamp, PID,
# and collision suffix so PID reuse can never overwrite older recovery state.
refresh_stamp="$(date -u +%Y%m%dT%H%M%SZ)"
refresh_attempt=0
while :; do
  refresh_id="$refresh_stamp-$$-$refresh_attempt"
  capsule_original_ref="refs/kitsoki/refresh/$refresh_id/capsule-original"
  if ! git -C "$staging_capsule" rev-parse --verify --quiet "$capsule_original_ref" >/dev/null; then
    break
  fi
  refresh_attempt=$((refresh_attempt + 1))
  [ "$refresh_attempt" -lt 1000 ] || die "could not allocate a unique staging refresh id"
done
snapshot_ref="refs/kitsoki/refresh/$refresh_id/staging-snapshot"
base_snapshot_ref="refs/kitsoki/refresh/$refresh_id/base-snapshot"
capsule_upstream_ref="refs/kitsoki/refresh/$refresh_id/capsule-upstream"
capsule_ref="refs/kitsoki/refresh/$refresh_id/capsule-post-snapshot"
import_ref="refs/kitsoki/refresh/$refresh_id/import"
capsule_failed_ref="refs/kitsoki/refresh/$refresh_id/capsule-failed-head"
refresh_succeeded=0
restore_original_on_failure=0
restore_failed_capsule_branch() {
  local primary_current current_branch clean current_tip
  [ "$restore_original_on_failure" = "1" ] || return 0
  primary_current="$(git -C "$repo_root" rev-parse --verify "refs/heads/$staging_branch" 2>/dev/null || true)"
  # Once primary staging moved, leave any newer capsule branch in place; the
  # next refresh can converge it without hiding concurrent work.
  [ "$primary_current" = "$staging_start" ] || return 0
  current_branch="$(git -C "$staging_capsule" branch --show-current 2>/dev/null || true)"
  [ "$current_branch" = "$staging_branch" ] || return 0
  if ! clean="$(source_dirty "$staging_capsule")"; then
    return 0
  fi
  [ -z "$clean" ] || return 0
  current_tip="$(git -C "$staging_capsule" rev-parse --verify "refs/heads/$staging_branch" 2>/dev/null || true)"
  [ -n "$current_tip" ] || return 0
  [ "$current_tip" != "$capsule_original_start" ] || return 0
  git -C "$staging_capsule" update-ref "$capsule_failed_ref" "$current_tip" || return 0
  if git -C "$staging_capsule" update-ref \
    "refs/heads/$staging_branch" "$capsule_original_start" "$current_tip"; then
    git -c submodule.recurse=false -C "$staging_capsule" reset --hard "$capsule_original_start" >/dev/null || return 0
    echo "refresh-staging-local: restored active capsule branch to its original tip after refusal" >&2
    echo "refresh-staging-local: preserved refused capsule tip at $capsule_failed_ref" >&2
  fi
}
cleanup_refresh_refs() {
  git -C "$staging_capsule" update-ref -d "$snapshot_ref" >/dev/null 2>&1 || true
  git -C "$staging_capsule" update-ref -d "$base_snapshot_ref" >/dev/null 2>&1 || true
  if [ "$refresh_succeeded" -eq 1 ]; then
    git -C "$repo_root" update-ref -d "$import_ref" >/dev/null 2>&1 || true
    git -C "$staging_capsule" update-ref -d "$capsule_original_ref" >/dev/null 2>&1 || true
    git -C "$staging_capsule" update-ref -d "$capsule_upstream_ref" >/dev/null 2>&1 || true
    git -C "$staging_capsule" update-ref -d "$capsule_ref" >/dev/null 2>&1 || true
  else
    restore_failed_capsule_branch
    if [ "$(git -C "$repo_root" rev-parse --verify --quiet "$primary_recovery_ref" 2>/dev/null || true)" = "$capsule_original_start" ]; then
      echo "refresh-staging-local: durable original capsule backup: $primary_recovery_ref" >&2
    fi
    if [ "$(git -C "$repo_root" rev-parse --verify --quiet "$primary_upstream_recovery_ref" 2>/dev/null || true)" = "$capsule_upstream_start" ]; then
      echo "refresh-staging-local: durable old capsule upstream backup: $primary_upstream_recovery_ref" >&2
    fi
    if git -C "$staging_capsule" rev-parse --verify --quiet "$capsule_original_ref" >/dev/null; then
      echo "refresh-staging-local: preserved original capsule work at $capsule_original_ref" >&2
    fi
    if git -C "$staging_capsule" rev-parse --verify --quiet "$capsule_upstream_ref" >/dev/null; then
      echo "refresh-staging-local: preserved old capsule upstream at $capsule_upstream_ref" >&2
    fi
    if git -C "$staging_capsule" rev-parse --verify --quiet "$capsule_ref" >/dev/null; then
      echo "refresh-staging-local: preserved post-snapshot capsule work at $capsule_ref" >&2
    fi
    if git -C "$staging_capsule" rev-parse --verify --quiet "$capsule_failed_ref" >/dev/null; then
      echo "refresh-staging-local: preserved refused capsule head at $capsule_failed_ref" >&2
    fi
    if git -C "$repo_root" rev-parse --verify --quiet "$import_ref" >/dev/null; then
      echo "refresh-staging-local: preserved proven import candidate at $import_ref" >&2
    fi
  fi
}
trap cleanup_refresh_refs EXIT
git -C "$staging_capsule" update-ref "$capsule_original_ref" "$capsule_original_start"
git -C "$staging_capsule" update-ref "$capsule_upstream_ref" "$capsule_upstream_start"

# Anchor both pieces of pre-refresh capsule state outside the capsule before
# mutating it. The remote-tracking tip can contain work not reachable from the
# checked-out branch, especially after an interrupted or manually recovered
# refresh. Content-addressed refs are idempotent and survive successful refresh
# and capsule replacement.
anchor_capsule_state() {
  local source_ref="$1" expected="$2" recovery_ref="$3" label="$4"
  if git -C "$repo_root" rev-parse --verify --quiet "$recovery_ref" >/dev/null; then
    [ "$(git -C "$repo_root" rev-parse "$recovery_ref")" = "$expected" ] ||
      die "content-addressed $label recovery ref points at the wrong object"
  else
    git -C "$repo_root" fetch --no-tags "$staging_capsule" \
      "$source_ref:$recovery_ref"
  fi
  [ "$(git -C "$repo_root" rev-parse "$recovery_ref")" = "$expected" ] ||
    die "primary $label recovery ref does not match captured capsule state"
}
anchor_capsule_state \
  "$capsule_original_ref" "$capsule_original_start" "$primary_recovery_ref" "original capsule"
anchor_capsule_state \
  "$capsule_upstream_ref" "$capsule_upstream_start" "$primary_upstream_recovery_ref" "old capsule upstream"

echo "refresh-staging-local: fetching staging snapshot $staging_start and local $base into staging capsule" >&2
# The staging capsule is long-lived, so its source/staging ref can have been
# rebased independently from the primary staging ref.  That ref is private to
# the capsule: force-refresh it from the primary source, then pin the exact
# OID we started with under a private snapshot ref for the rest of this run.
# Never fetch directly into a primary ref.
git -C "$staging_capsule" fetch source \
  "+refs/heads/$staging_branch:refs/remotes/source/$staging_branch" \
  "refs/heads/$base:refs/remotes/source/$base"
snapshot_fetched="$(git -C "$staging_capsule" rev-parse "refs/remotes/source/$staging_branch")"
if [ "$snapshot_fetched" != "$staging_start" ]; then
  die "staging branch advanced before the capsule could fetch its snapshot ($staging_start -> $snapshot_fetched); rerun refresh"
fi
git -C "$staging_capsule" update-ref "$snapshot_ref" "$snapshot_fetched"
capsule_delta_base_start="$(git -C "$staging_capsule" \
  merge-base "$capsule_original_start" "$staging_start")" ||
  die "could not determine the capsule/new-staging merge base"
base_start="$(git -C "$staging_capsule" rev-parse "refs/remotes/source/$base")"
[ "$base_start" = "$base_primary_start" ] ||
  die "$base advanced before the capsule fetched it ($base_primary_start -> $base_start); rerun refresh"
git -C "$staging_capsule" update-ref "$base_snapshot_ref" "$base_start"

expected_capsule_tree="$(expected_tree_after_delta \
  "$staging_capsule" \
  "$capsule_delta_base_start" \
  "$capsule_original_start" \
  "$staging_start")" ||
  die "could not construct the expected post-snapshot capsule tree"
refuse_untracked_tree_collisions \
  "$staging_capsule" "$capsule_original_start" "$expected_capsule_tree" \
  "staging-snapshot rebase" ||
  die "move or preserve the reported local paths before retrying"

echo "refresh-staging-local: rebasing staging capsule onto the captured staging snapshot" >&2
git -C "$staging_capsule" rebase "$snapshot_ref"
capsule_start="$(git -C "$staging_capsule" rev-parse HEAD)"
capsule_tree="$(git -C "$staging_capsule" rev-parse "$capsule_start^{tree}")"
if [ "$capsule_tree" != "$expected_capsule_tree" ]; then
  echo "refresh-staging-local: sequential snapshot rebase changed the independently proven tree; materializing a signed reconciliation merge" >&2
  if ! capsule_start="$(materialize_expected_tree_merge \
    "$staging_capsule" "$expected_capsule_tree" "$staging_start" \
    "$capsule_original_start" "capsule work onto staging snapshot")"; then
    restore_original_on_failure=1
    die "refusing to continue: could not materialize the expected post-snapshot capsule tree"
  fi
  capsule_tree="$(git -C "$staging_capsule" rev-parse "$capsule_start^{tree}")"
fi
git -C "$staging_capsule" update-ref "$capsule_ref" "$capsule_start"

combined_base="$(git -C "$staging_capsule" merge-base "$staging_start" "$base_start")" ||
  die "could not find the staging/base merge base"
expected_result_tree="$(expected_tree_after_delta \
  "$staging_capsule" \
  "$combined_base" \
  "$capsule_start" \
  "$base_start")" ||
  die "could not construct the expected staging-on-base tree"
refuse_untracked_tree_collisions \
  "$staging_capsule" "$capsule_start" "$expected_result_tree" \
  "$base rebase" ||
  die "move or preserve the reported local paths before retrying"

echo "refresh-staging-local: rebasing staging capsule onto source/$base" >&2
git -C "$staging_capsule" rebase "$base_start"
rebased_result="$(git -C "$staging_capsule" rev-parse HEAD)"
rebased_result_tree="$(git -C "$staging_capsule" rev-parse "$rebased_result^{tree}")"
if [ "$rebased_result_tree" != "$expected_result_tree" ]; then
  echo "refresh-staging-local: sequential base rebase changed the independently proven tree; materializing a signed reconciliation merge" >&2
  if ! rebased_result="$(materialize_expected_tree_merge \
    "$staging_capsule" "$expected_result_tree" "$base_start" \
    "$capsule_start" "staging snapshot onto $base")"; then
    restore_original_on_failure=1
    die "refusing to continue: could not materialize the expected staging-on-base tree"
  fi
  rebased_result_tree="$(git -C "$staging_capsule" rev-parse "$rebased_result^{tree}")"
fi
git -C "$staging_capsule" merge-base --is-ancestor "$base_start" "$rebased_result" ||
  die "refusing to continue: rebased staging is not based on captured $base"

copy_local_config

if [ -n "$gate" ]; then
  echo "refresh-staging-local: running gate in $staging_capsule: $gate" >&2
  (cd "$staging_capsule" && sh -c "$gate")
fi
post_gate_dirty="$(source_dirty "$staging_capsule")"
if [ -n "$post_gate_dirty" ]; then
  echo "error: staging refresh gate left uncommitted changes:" >&2
  printf '%s\n' "$post_gate_dirty" >&2
  exit 1
fi

staging_result="$(git -C "$staging_capsule" rev-parse HEAD)"
git -C "$staging_capsule" rev-parse --verify --quiet "$staging_result^{tree}" >/dev/null ||
  die "staging capsule result has no readable tree: $staging_result"
if [ "$staging_result" != "$rebased_result" ]; then
  restore_original_on_failure=1
  die "refusing to import staging result because the refresh gate moved capsule HEAD"
fi
git -C "$staging_capsule" merge-base --is-ancestor "$base_start" "$staging_result" ||
  die "refusing to import staging result that is not based on captured $base"

capsule_current="$(git -C "$staging_capsule" rev-parse "refs/heads/$staging_branch")"
[ "$capsule_current" = "$staging_result" ] ||
  die "staging capsule branch advanced before import ($staging_result -> $capsule_current); rerun refresh"

# Import the object under a private ref first.  Do not fetch directly into the
# primary staging ref: doing so is a forceful ref mutation that can silently
# discard a successful concurrent workspace merge.
git -C "$repo_root" fetch "$staging_capsule" "$staging_result:$import_ref"
[ "$(git -C "$staging_capsule" rev-parse "refs/heads/$staging_branch")" = "$staging_result" ] ||
  die "staging capsule branch advanced while importing the proven result; rerun refresh"
staging_current="$(git -C "$repo_root" rev-parse "refs/heads/$staging_branch")"
if [ "$staging_current" != "$staging_start" ]; then
  die "staging branch advanced during refresh ($staging_start -> $staging_current); refusing to overwrite newer work"
fi
base_current="$(git -C "$repo_root" rev-parse "refs/heads/$base")"
if [ "$base_current" != "$base_start" ]; then
  die "$base advanced during refresh ($base_start -> $base_current); refusing to import stale staging"
fi
if ! {
  printf 'start\n'
  printf 'verify refs/heads/%s %s\n' "$base" "$base_start"
  printf 'update refs/heads/%s %s %s\n' "$staging_branch" "$staging_result" "$staging_start"
  printf 'prepare\n'
  printf 'commit\n'
} | git -C "$repo_root" update-ref --stdin; then
  die "$base or $staging_branch changed during the atomic staging import; refusing to overwrite newer work"
fi
[ "$(git -C "$staging_capsule" rev-parse "refs/heads/$staging_branch")" = "$staging_result" ] ||
  die "staging capsule advanced after import; primary result is preserved, rerun refresh to converge"
refresh_succeeded=1

printf '%s -> %s\n' "$staging_branch" "$(git rev-parse --short "$staging_branch")"
printf 'staging capsule: %s\n' "$staging_capsule"
printf 'base: %s (%s)\n' "$base" "$(git rev-parse --short "$base")"
printf 'original capsule backup: %s\n' "$primary_recovery_ref"
printf 'old capsule upstream backup: %s\n' "$primary_upstream_recovery_ref"
