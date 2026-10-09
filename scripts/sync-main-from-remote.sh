#!/usr/bin/env bash
# sync-main-from-remote.sh - reconcile protected local main with a remote main.
#
# This does not pull into the primary checkout. It creates a writable integration
# worktree under .worktrees, merges the selected remote branch there, and prints
# the protected fast-forward command to run after validation. If the merge
# conflicts, the conflicted files stay isolated in that integration worktree.
set -euo pipefail

usage() {
  cat <<'EOF'
usage:
  scripts/sync-main-from-remote.sh [--remote origin] [--remote-branch main] [--base main] [--name NAME] [--no-fetch] [--auto-resolve] [--no-known-resolutions]
  scripts/sync-main-from-remote.sh --continue <branch> [--review-command CMD]

Prepare mode:
  Fetches the remote, creates .worktrees/<name> from local main, and merges
  <remote>/<remote-branch> there. If the merge is clean, validate in that
  worktree and land with:

    scripts/merge-to-main.sh <branch>

  If conflicts occur, resolve and commit them in the integration worktree, then
  run:

    scripts/sync-main-from-remote.sh --continue <branch>

  With --auto-resolve, the helper runs a resolver command after conflicts and
  then runs the --continue path. Before calling an external resolver, the helper
  applies known deterministic resolutions, such as keeping local deletion for
  retired proposal files under docs/proposals/. The resolver command defaults
  to KITSOKI_SYNC_RESOLVE_CMD, and the reviewer command defaults to
  KITSOKI_SYNC_REVIEW_CMD. Agent commands run with cwd set to the integration
  worktree and receive KITSOKI_SYNC_* environment variables plus a prompt file.
  If no reviewer command is configured, a built-in no-LLM review checks for
  unresolved conflicts, whitespace errors, and staged scratch artifacts.

Continue mode:
  Applies the same known deterministic resolutions, verifies the integration
  branch has no unresolved conflicts, runs the built-in or configured review
  gate, commits an in-progress merge if needed, and prints validation and
  landing commands.
EOF
}

die() {
  echo "error: $*" >&2
  exit 1
}

remote=origin
remote_branch=main
base=main
name=
do_fetch=1
continue_branch=
auto_resolve=0
known_resolutions=1
resolve_command="${KITSOKI_SYNC_RESOLVE_CMD:-}"
review_command="${KITSOKI_SYNC_REVIEW_CMD:-}"

while [ "$#" -gt 0 ]; do
  case "$1" in
    --remote)
      remote="${2:?--remote requires a value}"
      shift 2
      ;;
    --remote-branch)
      remote_branch="${2:?--remote-branch requires a value}"
      shift 2
      ;;
    --base)
      base="${2:?--base requires a value}"
      shift 2
      ;;
    --name)
      name="${2:?--name requires a value}"
      shift 2
      ;;
    --no-fetch)
      do_fetch=0
      shift
      ;;
    --auto-resolve)
      auto_resolve=1
      shift
      ;;
    --no-known-resolutions)
      known_resolutions=0
      shift
      ;;
    --resolver-command)
      resolve_command="${2:?--resolver-command requires a value}"
      shift 2
      ;;
    --review-command)
      review_command="${2:?--review-command requires a value}"
      shift 2
      ;;
    --continue)
      continue_branch="${2:?--continue requires a branch name}"
      shift 2
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      die "unknown argument: $1"
      ;;
  esac
done

repo_root="$(git rev-parse --show-toplevel)"
cd "$repo_root"

if [ "$(git rev-parse --git-dir)" != "$(git rev-parse --git-common-dir)" ]; then
  die "run this from the primary checkout, not a linked worktree"
fi
current_branch="$(git symbolic-ref --quiet --short HEAD 2>/dev/null || true)"
if [ "$current_branch" != "$base" ]; then
  die "primary checkout is on '$current_branch', expected '$base'"
fi

find_worktree_for_branch() {
  target_ref="refs/heads/$1"
  git worktree list --porcelain | awk -v target="$target_ref" '
    /^worktree / { path=substr($0, 10) }
    /^branch / && substr($0, 8) == target { print path; found=1 }
    END { if (!found) exit 1 }
  '
}

remote_ref_for_branch() {
  wt="$1"
  if [ -f "$wt/.artifacts/sync-main/metadata.env" ]; then
    sed -n 's/^remote_ref=//p' "$wt/.artifacts/sync-main/metadata.env" | tail -1
    return
  fi
  printf 'refs/remotes/%s/%s\n' "$remote" "$remote_branch"
}

print_next_steps() {
  branch="$1"
  wt="$2"
  cat <<EOF
Integration branch: $branch
Integration worktree: $wt

Validate in the integration worktree, for example:
  (cd "$wt" && go test ./...)

Then land into protected local $base from the primary checkout:
  scripts/merge-to-main.sh $branch
EOF
}

conflict_files_for_worktree() {
  git -C "$1" diff --name-only --diff-filter=U | sort -u
}

apply_known_resolutions() {
  wt="$1"
  [ "$known_resolutions" -eq 1 ] || return 0

  conflict_files_for_worktree "$wt" | while IFS= read -r path; do
    [ -n "$path" ] || continue
    status="$(git -C "$wt" status --short -- "$path" | sed -n '1s/^\(..\).*/\1/p')"
    case "$status:$path" in
      DU:docs/proposals/*.md)
        git -C "$wt" rm -- "$path" >/dev/null
        echo "known resolution: kept local retirement of $path"
        ;;
    esac
  done
}

write_sync_metadata() {
  wt="$1"
  branch_name="$2"
  remote_ref_name="$3"
  prompt_dir="$wt/.artifacts/sync-main"
  mkdir -p "$prompt_dir"
  {
    echo "branch=$branch_name"
    echo "base=$base"
    echo "remote_ref=$remote_ref_name"
  } >"$prompt_dir/metadata.env"
}

write_resolve_prompt() {
  wt="$1"
  branch_name="$2"
  remote_ref_name="$3"
  prompt_dir="$wt/.artifacts/sync-main"
  mkdir -p "$prompt_dir"
  prompt="$prompt_dir/resolve-conflicts.md"
  {
    echo "# Resolve remote sync conflicts"
    echo
    echo "Working directory: $wt"
    echo "Integration branch: $branch_name"
    echo "Local base branch: $base"
    echo "Remote branch: $remote_ref_name"
    echo
    echo "Resolve the current Git merge conflicts in this worktree."
    echo "Preserve all intentional local work from $base and all intentional remote work from $remote_ref_name."
    echo "Do not use AskUserQuestion. Make a concrete resolution, stage the resolved files, and leave the merge uncommitted."
    echo
    echo "Unresolved files:"
    conflict_files_for_worktree "$wt"
    echo
    echo "Useful checks:"
    echo "- git status --short"
    echo "- git diff --name-only --diff-filter=U"
    echo "- git diff --cached --stat"
  } >"$prompt"
  printf '%s\n' "$prompt"
}

write_review_prompt() {
  wt="$1"
  branch_name="$2"
  remote_ref_name="$3"
  prompt_dir="$wt/.artifacts/sync-main"
  mkdir -p "$prompt_dir"
  prompt="$prompt_dir/review-lost-work.md"
  {
    echo "# Review remote sync resolution for lost work"
    echo
    echo "Working directory: $wt"
    echo "Integration branch: $branch_name"
    echo "Local base branch: $base"
    echo "Remote branch: $remote_ref_name"
    echo
    echo "Review the resolved merge before it is committed."
    echo "Fail with a non-zero exit code if any local-only work from $base or remote-only work from $remote_ref_name was dropped unintentionally."
    echo "Also fail if unresolved conflict markers remain or generated/scratch artifacts are staged unexpectedly."
    echo
    echo "Useful checks:"
    echo "- git status --short"
    echo "- git diff --check"
    echo "- git diff --name-only --diff-filter=U"
    echo "- git diff --cached --stat"
    echo "- git diff --stat $base...$remote_ref_name"
    echo "- git diff --stat $base"
  } >"$prompt"
  printf '%s\n' "$prompt"
}

run_agent_command() {
  kind="$1"
  command="$2"
  wt="$3"
  branch_name="$4"
  remote_ref_name="$5"
  prompt="$6"
  case "$kind" in
    RESOLVE) option_name="--resolver-command" ;;
    REVIEW) option_name="--review-command" ;;
    *) option_name="--agent-command" ;;
  esac
  [ -n "$command" ] || die "$kind command is required; set KITSOKI_SYNC_${kind}_CMD or pass $option_name"
  (
    cd "$wt"
    export KITSOKI_SYNC_WORKTREE="$wt"
    export KITSOKI_SYNC_BRANCH="$branch_name"
    export KITSOKI_SYNC_BASE="$base"
    export KITSOKI_SYNC_REMOTE_REF="$remote_ref_name"
    export KITSOKI_SYNC_PROMPT_FILE="$prompt"
    export KITSOKI_SYNC_CONFLICT_FILES
    KITSOKI_SYNC_CONFLICT_FILES="$(conflict_files_for_worktree "$wt")"
    sh -c "$command"
  )
}

run_review_gate() {
  wt="$1"
  branch_name="$2"
  remote_ref_name="$3"
  if [ -z "$review_command" ]; then
    if [ -n "$(git -C "$wt" ls-files -u)" ]; then
      die "unresolved conflicts remain in $wt"
    fi
    git -C "$wt" diff --check
    if git -C "$wt" diff --cached --name-only | grep -E '^(\.artifacts/|\.context/)' >/dev/null; then
      die "scratch artifacts are staged in $wt"
    fi
    return 0
  fi
  prompt="$(write_review_prompt "$wt" "$branch_name" "$remote_ref_name")"
  run_agent_command REVIEW "$review_command" "$wt" "$branch_name" "$remote_ref_name" "$prompt"
}

if [ -n "$continue_branch" ]; then
  git rev-parse --verify --quiet "$continue_branch" >/dev/null ||
    die "no such branch: $continue_branch"
  wt="$(find_worktree_for_branch "$continue_branch")" ||
    die "no worktree found for branch: $continue_branch"

  apply_known_resolutions "$wt"
  if [ -n "$(git -C "$wt" ls-files -u)" ]; then
    die "unresolved conflicts remain in $wt"
  fi
  remote_ref="$(remote_ref_for_branch "$wt")"
  git rev-parse --verify --quiet "$remote_ref" >/dev/null ||
    die "remote ref not found for review: $remote_ref"
  run_review_gate "$wt" "$continue_branch" "$remote_ref"
  git_dir="$(git -C "$wt" rev-parse --git-dir)"
  if [ -e "$git_dir/MERGE_HEAD" ]; then
    git -C "$wt" commit --no-edit
  fi
  if ! git merge-base --is-ancestor "$base" "$continue_branch"; then
    die "$continue_branch is not based on $base"
  fi
  print_next_steps "$continue_branch" "$wt"
  exit 0
fi

remote_ref="refs/remotes/$remote/$remote_branch"
if [ "$do_fetch" -eq 1 ]; then
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
    cat <<EOF
$base already contains $remote/$remote_branch, but local $base is ahead by $ahead $commit_word.
Run push_main remote=$remote or open/merge a PR before treating the remote as synchronized.
EOF
  else
    echo "$base already contains $remote/$remote_branch"
  fi
  exit 0
fi

stamp="$(date -u +%Y%m%dT%H%M%SZ)"
safe_remote_branch="${remote_branch//\//-}"
if [ -z "$name" ]; then
  name="sync-main-${remote}-${safe_remote_branch}-${stamp}"
fi
branch="sync/main-${remote}-${safe_remote_branch}-${stamp}"
worktree=".worktrees/$name"

git rev-parse --verify --quiet "$branch" >/dev/null &&
  die "branch already exists: $branch"
[ -e "$worktree" ] && die "worktree path already exists: $worktree"

if git merge-base --is-ancestor "$base" "$remote_ref"; then
  git branch "$branch" "$remote_ref"
  git worktree add "$worktree" "$branch"
  write_sync_metadata "$repo_root/$worktree" "$branch" "$remote_ref"
  echo "$remote/$remote_branch is a fast-forward of $base."
  print_next_steps "$branch" "$repo_root/$worktree"
  exit 0
fi

git worktree add "$worktree" -b "$branch" "$base"
write_sync_metadata "$repo_root/$worktree" "$branch" "$remote_ref"
set +e
git -C "$worktree" merge --no-ff --no-edit "$remote_ref"
merge_status=$?
set -e

if [ "$merge_status" -eq 0 ]; then
  if [ "$auto_resolve" -eq 1 ]; then
    run_review_gate "$repo_root/$worktree" "$branch" "$remote_ref"
  fi
  print_next_steps "$branch" "$repo_root/$worktree"
  exit 0
fi

wt="$repo_root/$worktree"
apply_known_resolutions "$wt"

if [ -z "$(git -C "$wt" ls-files -u)" ]; then
  run_review_gate "$wt" "$branch" "$remote_ref"
  git_dir="$(git -C "$wt" rev-parse --git-dir)"
  if [ -e "$git_dir/MERGE_HEAD" ]; then
    git -C "$wt" commit --no-edit
  fi
  print_next_steps "$branch" "$wt"
  exit 0
fi

if [ "$auto_resolve" -eq 1 ]; then
  if [ -n "$(git -C "$wt" ls-files -u)" ]; then
    prompt="$(write_resolve_prompt "$wt" "$branch" "$remote_ref")"
    run_agent_command RESOLVE "$resolve_command" "$wt" "$branch" "$remote_ref" "$prompt"
  fi
  if [ -n "$(git -C "$wt" ls-files -u)" ]; then
    die "resolver command completed but unresolved conflicts remain in $wt"
  fi
  run_review_gate "$wt" "$branch" "$remote_ref"
  git_dir="$(git -C "$wt" rev-parse --git-dir)"
  if [ -e "$git_dir/MERGE_HEAD" ]; then
    git -C "$wt" commit --no-edit
  fi
  print_next_steps "$branch" "$wt"
  exit 0
fi

cat <<EOF
Merge conflicts are isolated in:
  $repo_root/$worktree

Resolve conflicts there, then run from the primary checkout:
  scripts/sync-main-from-remote.sh --continue $branch
EOF
exit "$merge_status"
