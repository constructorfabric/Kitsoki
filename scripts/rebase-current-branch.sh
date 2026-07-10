#!/usr/bin/env bash
# rebase-current-branch.sh - rebase the current branch onto a local integration ref.
#
# This is the script boundary behind the git-ops `rebase` intent. It emits the
# JSON envelope that stories/git-ops/rooms/rebase.yaml binds.
set -euo pipefail

usage() {
  cat <<'EOF'
usage: scripts/rebase-current-branch.sh [--integration main] [--worktree DIR]

Rebase the current branch in DIR onto the local integration ref. The script does
not fetch or pull; update the integration branch first when remote freshness is
required. On conflict it first tries git rerere and only reports a conflict when
rerere cannot complete the rebase.
EOF
}

die() {
  echo "error: $*" >&2
  exit 1
}

integration=main
worktree=.

while [ "$#" -gt 0 ]; do
  case "$1" in
    --integration)
      integration="${2:?--integration requires a value}"
      shift 2
      ;;
    --worktree)
      worktree="${2:?--worktree requires a value}"
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

export NO_COLOR=1 GIT_TERMINAL_PROMPT=0 GIT_PAGER=cat

if ! git -C "$worktree" rev-parse --is-inside-work-tree >/dev/null 2>&1; then
  die "$worktree is not a git worktree"
fi
if ! git -C "$worktree" rev-parse --verify --quiet "$integration" >/dev/null; then
  die "integration ref not found: $integration"
fi

branch="$(git -C "$worktree" rev-parse --abbrev-ref HEAD)"
if [ "$branch" = "HEAD" ] || [ -z "$branch" ]; then
  die "current worktree is detached; rebase requires a branch"
fi

ahead="$(git -C "$worktree" rev-list --count "${integration}..HEAD" 2>/dev/null || echo 0)"
tag=""
if [ "$ahead" -gt 1 ]; then
  tag="${branch}-pre-rebase-backup"
  git -C "$worktree" tag -f "$tag" HEAD 2>/dev/null || true
fi

rebase_in_progress() {
  [ -d "$(git -C "$worktree" rev-parse --git-path rebase-merge 2>/dev/null)" ] || \
  [ -d "$(git -C "$worktree" rev-parse --git-path rebase-apply 2>/dev/null)" ]
}

exit_code=0
output="$(git -C "$worktree" rebase "$integration" --no-autostash 2>&1)" || exit_code=$?

if [ "$exit_code" -ne 0 ] && rebase_in_progress; then
  guard=0
  while rebase_in_progress && [ "$guard" -lt 100 ]; do
    guard=$((guard + 1))
    git -C "$worktree" rerere 2>/dev/null || true
    while IFS= read -r file; do
      [ -n "$file" ] || continue
      [ -f "$worktree/$file" ] || continue
      if ! grep -qE '^(<<<<<<<|>>>>>>>)' "$worktree/$file" 2>/dev/null; then
        git -C "$worktree" add -- "$file" 2>/dev/null || true
      fi
    done < <(git -C "$worktree" diff --diff-filter=U --name-only 2>/dev/null)
    if git -C "$worktree" diff --diff-filter=U --name-only 2>/dev/null | grep -q .; then
      break
    fi
    GIT_EDITOR=true git -C "$worktree" rebase --continue >/dev/null 2>&1 || true
  done
  if ! rebase_in_progress; then
    exit_code=0
    output="conflicts auto-resolved from rerere cache"
  fi
fi

merge_base="$(git -C "$worktree" merge-base HEAD "$integration" 2>/dev/null || echo "")"
if [ "$exit_code" -eq 0 ]; then
  jq -n --arg tag "$tag" --arg out "$output" --arg mb "$merge_base" \
    '{pre_rebase_tag:$tag,last_op_output:$out,rebase_base_sha:$mb,rebase_done:true,conflict_origin:"",route:"on_branch"}'
else
  jq -n --arg tag "$tag" --arg out "$output" --arg mb "$merge_base" \
    '{pre_rebase_tag:$tag,last_op_output:$out,rebase_base_sha:$mb,rebase_done:false,conflict_origin:"rebase",route:"rebase_conflict"}'
fi
