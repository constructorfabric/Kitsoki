#!/usr/bin/env bash
# bad_git_repo.sh — reusable real-git fixture factory for git-ops tests.
#
# Each scenario creates a throwaway repository with nonsense files only. The
# harness is intentionally shell-native because the git-ops rooms themselves are
# shell snippets; tests can source this file and call bad_git_create, or execute
# it directly:
#
#   bash stories/git-ops/tests/lib/bad_git_repo.sh list
#   repo=$(bash stories/git-ops/tests/lib/bad_git_repo.sh create pull-rebase-conflict)
#   bash stories/git-ops/tests/lib/bad_git_repo.sh assert pull-rebase-conflict "$repo"

set -euo pipefail

bad_git_cases() {
  cat <<'CASES'
no-upstream
pull-rebase-conflict
rebase-in-progress-clean
rebase-second-conflict
linked-worktree-branch
dirty-feature-worktree
merge-conflict-in-progress
detached-head
diverged-upstream-clean
dirty-main-before-pull
untracked-overwrite-blocker
stale-worktree-entry
cherry-pick-in-progress
bisect-in-progress
rename-delete-conflict
modify-delete-conflict
binary-rebase-conflict
local-tags-conflict
CASES
}

bad_git_note() {
  if [ "${BAD_GIT_VERBOSE:-0}" = "1" ]; then
    printf '  %s\n' "$*" >&2
  fi
}

bad_git_require_case() {
  case "$1" in
    no-upstream|pull-rebase-conflict|rebase-in-progress-clean|rebase-second-conflict|linked-worktree-branch|dirty-feature-worktree|merge-conflict-in-progress|detached-head|diverged-upstream-clean|dirty-main-before-pull|untracked-overwrite-blocker|stale-worktree-entry|cherry-pick-in-progress|bisect-in-progress|rename-delete-conflict|modify-delete-conflict|binary-rebase-conflict|local-tags-conflict)
      ;;
    *)
      printf 'unknown bad git scenario: %s\nknown scenarios:\n' "$1" >&2
      bad_git_cases >&2
      return 2
      ;;
  esac
}

bad_git_configure_env() {
  export NO_COLOR=1 GIT_TERMINAL_PROMPT=0 GIT_PAGER=cat
  export GIT_AUTHOR_NAME="${GIT_AUTHOR_NAME:-Bad Git Harness}"
  export GIT_AUTHOR_EMAIL="${GIT_AUTHOR_EMAIL:-bad-git@example.test}"
  export GIT_COMMITTER_NAME="${GIT_COMMITTER_NAME:-Bad Git Harness}"
  export GIT_COMMITTER_EMAIL="${GIT_COMMITTER_EMAIL:-bad-git@example.test}"
}

bad_git_init_repo() {
  local repo="$1"
  mkdir -p "$(dirname "$repo")"
  git init -q -b main "$repo"
  git -C "$repo" config rerere.enabled false
  git -C "$repo" config user.name "$GIT_AUTHOR_NAME"
  git -C "$repo" config user.email "$GIT_AUTHOR_EMAIL"
  printf 'base\n' >"$repo/alpha.txt"
  printf 'base\n' >"$repo/bravo.txt"
  printf 'base\n' >"$repo/charlie.txt"
  git -C "$repo" add alpha.txt bravo.txt charlie.txt
  git -C "$repo" commit -qm "base"
}

bad_git_commit_file() {
  local repo="$1"
  local file="$2"
  local body="$3"
  local msg="$4"
  mkdir -p "$(dirname "$repo/$file")"
  printf '%s\n' "$body" >"$repo/$file"
  git -C "$repo" add "$file"
  git -C "$repo" commit -qm "$msg"
}

bad_git_write_manifest() {
  local repo="$1"
  local scenario="$2"
  cat >"$repo/.bad-git-scenario" <<EOF
scenario=$scenario
repo=$repo
EOF
}

bad_git_create() {
  bad_git_configure_env
  local scenario="$1"
  local root="${2:-}"
  bad_git_require_case "$scenario"
  if [ -z "$root" ]; then
    root="$(mktemp -d "${TMPDIR:-/tmp}/kitsoki-bad-git.XXXXXX")"
  fi
  mkdir -p "$root"
  local repo="$root/repo"

  case "$scenario" in
    no-upstream)
      bad_git_init_repo "$repo"
      git -C "$repo" checkout -q -b feature/no-upstream
      bad_git_commit_file "$repo" alpha.txt "feature without upstream" "feature without upstream"
      ;;

    pull-rebase-conflict)
      mkdir -p "$root/remote.git"
      git init -q --bare "$root/remote.git"
      bad_git_init_repo "$root/seed"
      git -C "$root/seed" remote add origin "$root/remote.git"
      git -C "$root/seed" push -q -u origin main
      git clone -q "$root/remote.git" "$repo"
      git -C "$repo" checkout -q main
      bad_git_commit_file "$root/seed" alpha.txt "remote says alpha" "remote alpha"
      git -C "$root/seed" push -q origin main
      bad_git_commit_file "$repo" alpha.txt "local says alpha" "local alpha"
      git -C "$repo" pull --rebase >/dev/null 2>&1 && {
        printf 'expected pull --rebase to conflict, but it succeeded\n' >&2
        return 1
      }
      ;;

    rebase-in-progress-clean)
      bad_git_init_repo "$repo"
      git -C "$repo" checkout -q -b feature/clean-rebase
      bad_git_commit_file "$repo" alpha.txt "feature says alpha" "feature alpha"
      git -C "$repo" checkout -q main
      bad_git_commit_file "$repo" alpha.txt "main says alpha" "main alpha"
      git -C "$repo" checkout -q feature/clean-rebase
      git -C "$repo" rebase main >/dev/null 2>&1 && {
        printf 'expected rebase to conflict, but it succeeded\n' >&2
        return 1
      }
      printf 'resolved alpha\n' >"$repo/alpha.txt"
      git -C "$repo" add alpha.txt
      ;;

    rebase-second-conflict)
      bad_git_init_repo "$repo"
      git -C "$repo" checkout -q -b feature/two-conflicts
      bad_git_commit_file "$repo" alpha.txt "feature alpha 1" "feature alpha"
      bad_git_commit_file "$repo" bravo.txt "feature bravo 2" "feature bravo"
      git -C "$repo" checkout -q main
      bad_git_commit_file "$repo" alpha.txt "main alpha 1" "main alpha"
      bad_git_commit_file "$repo" bravo.txt "main bravo 2" "main bravo"
      git -C "$repo" checkout -q feature/two-conflicts
      git -C "$repo" rebase main >/dev/null 2>&1 && {
        printf 'expected first rebase step to conflict, but it succeeded\n' >&2
        return 1
      }
      ;;

    linked-worktree-branch)
      bad_git_init_repo "$repo"
      git -C "$repo" worktree add -q -b feature/linked "$root/linked-feature"
      bad_git_commit_file "$root/linked-feature" alpha.txt "linked feature" "linked feature"
      ;;

    dirty-feature-worktree)
      bad_git_init_repo "$repo"
      git -C "$repo" worktree add -q -b feature/dirty "$root/dirty-feature"
      mkdir -p "$root/dirty-feature/nonsense"
      printf 'untracked\n' >"$root/dirty-feature/nonsense/untracked.txt"
      printf 'modified but unstaged\n' >"$root/dirty-feature/alpha.txt"
      ;;

    merge-conflict-in-progress)
      bad_git_init_repo "$repo"
      git -C "$repo" checkout -q -b feature/merge-conflict
      bad_git_commit_file "$repo" alpha.txt "feature merge alpha" "feature merge alpha"
      git -C "$repo" checkout -q main
      bad_git_commit_file "$repo" alpha.txt "main merge alpha" "main merge alpha"
      git -C "$repo" merge feature/merge-conflict >/dev/null 2>&1 && {
        printf 'expected merge to conflict, but it succeeded\n' >&2
        return 1
      }
      ;;

    detached-head)
      bad_git_init_repo "$repo"
      bad_git_commit_file "$repo" alpha.txt "second detached base" "second"
      local first
      first="$(git -C "$repo" rev-list --max-parents=0 HEAD)"
      git -C "$repo" checkout -q --detach "$first"
      ;;

    diverged-upstream-clean)
      mkdir -p "$root/remote.git"
      git init -q --bare "$root/remote.git"
      bad_git_init_repo "$root/seed"
      git -C "$root/seed" remote add origin "$root/remote.git"
      git -C "$root/seed" push -q -u origin main
      git clone -q "$root/remote.git" "$repo"
      git -C "$repo" checkout -q main
      bad_git_commit_file "$root/seed" bravo.txt "remote independent change" "remote independent"
      git -C "$root/seed" push -q origin main
      bad_git_commit_file "$repo" alpha.txt "local independent change" "local independent"
      git -C "$repo" fetch -q origin
      ;;

    dirty-main-before-pull)
      mkdir -p "$root/remote.git"
      git init -q --bare "$root/remote.git"
      bad_git_init_repo "$root/seed"
      git -C "$root/seed" remote add origin "$root/remote.git"
      git -C "$root/seed" push -q -u origin main
      git clone -q "$root/remote.git" "$repo"
      git -C "$repo" checkout -q main
      bad_git_commit_file "$root/seed" alpha.txt "remote dirty blocker" "remote dirty blocker"
      git -C "$root/seed" push -q origin main
      printf 'local unstaged dirty blocker\n' >"$repo/alpha.txt"
      ;;

    untracked-overwrite-blocker)
      bad_git_init_repo "$repo"
      git -C "$repo" checkout -q -b feature/untracked-blocker
      bad_git_commit_file "$repo" generated/report.txt "feature wants tracked report" "track generated report"
      git -C "$repo" checkout -q main
      mkdir -p "$repo/generated"
      printf 'operator local report\n' >"$repo/generated/report.txt"
      ;;

    stale-worktree-entry)
      bad_git_init_repo "$repo"
      git -C "$repo" worktree add -q -b feature/stale "$root/stale-feature"
      rm -rf "$root/stale-feature"
      ;;

    cherry-pick-in-progress)
      bad_git_init_repo "$repo"
      git -C "$repo" checkout -q -b feature/cherry-source
      bad_git_commit_file "$repo" alpha.txt "feature cherry alpha" "feature cherry alpha"
      git -C "$repo" checkout -q main
      bad_git_commit_file "$repo" alpha.txt "main cherry alpha" "main cherry alpha"
      git -C "$repo" cherry-pick feature/cherry-source >/dev/null 2>&1 && {
        printf 'expected cherry-pick to conflict, but it succeeded\n' >&2
        return 1
      }
      ;;

    bisect-in-progress)
      bad_git_init_repo "$repo"
      bad_git_commit_file "$repo" alpha.txt "first suspect" "suspect one"
      bad_git_commit_file "$repo" bravo.txt "second suspect" "suspect two"
      git -C "$repo" bisect start >/dev/null
      git -C "$repo" bisect bad HEAD >/dev/null
      git -C "$repo" bisect good "$(git -C "$repo" rev-list --max-parents=0 HEAD)" >/dev/null
      ;;

    rename-delete-conflict)
      bad_git_init_repo "$repo"
      git -C "$repo" checkout -q -b feature/rename-delete
      git -C "$repo" mv alpha.txt renamed-alpha.txt
      git -C "$repo" commit -qm "rename alpha"
      git -C "$repo" checkout -q main
      git -C "$repo" rm -q alpha.txt
      git -C "$repo" commit -qm "delete alpha"
      git -C "$repo" merge feature/rename-delete >/dev/null 2>&1 && {
        printf 'expected rename/delete merge to conflict, but it succeeded\n' >&2
        return 1
      }
      ;;

    modify-delete-conflict)
      bad_git_init_repo "$repo"
      git -C "$repo" checkout -q -b feature/modify-delete
      bad_git_commit_file "$repo" alpha.txt "feature modifies alpha before delete" "modify alpha"
      git -C "$repo" checkout -q main
      git -C "$repo" rm -q alpha.txt
      git -C "$repo" commit -qm "delete alpha"
      git -C "$repo" merge feature/modify-delete >/dev/null 2>&1 && {
        printf 'expected modify/delete merge to conflict, but it succeeded\n' >&2
        return 1
      }
      ;;

    binary-rebase-conflict)
      bad_git_init_repo "$repo"
      printf 'base-binary\n' >"$repo/blob.bin"
      git -C "$repo" add blob.bin
      git -C "$repo" commit -qm "add binary"
      git -C "$repo" checkout -q -b feature/binary-conflict
      printf '\000feature-binary\n' >"$repo/blob.bin"
      git -C "$repo" add blob.bin
      git -C "$repo" commit -qm "feature binary"
      git -C "$repo" checkout -q main
      printf '\000main-binary\n' >"$repo/blob.bin"
      git -C "$repo" add blob.bin
      git -C "$repo" commit -qm "main binary"
      git -C "$repo" checkout -q feature/binary-conflict
      git -C "$repo" rebase main >/dev/null 2>&1 && {
        printf 'expected binary rebase to conflict, but it succeeded\n' >&2
        return 1
      }
      ;;

    local-tags-conflict)
      mkdir -p "$root/remote.git"
      git init -q --bare "$root/remote.git"
      bad_git_init_repo "$root/seed"
      git -C "$root/seed" tag release
      git -C "$root/seed" remote add origin "$root/remote.git"
      git -C "$root/seed" push -q origin main release
      git clone -q "$root/remote.git" "$repo"
      bad_git_commit_file "$root/seed" charlie.txt "remote retag target" "remote retag target"
      git -C "$root/seed" tag -f release HEAD >/dev/null
      git -C "$root/seed" push -q --force origin release
      bad_git_commit_file "$repo" alpha.txt "local tag target" "local tag target"
      git -C "$repo" tag -f release HEAD >/dev/null
      ;;
  esac

  bad_git_write_manifest "$repo" "$scenario"
  printf '%s\n' "$repo"
}

bad_git_assert() {
  local scenario="$1"
  local repo="$2"
  bad_git_require_case "$scenario"
  [ -d "$repo/.git" ] || [ -f "$repo/.git" ] || {
    printf 'not a git worktree: %s\n' "$repo" >&2
    return 1
  }

  case "$scenario" in
    no-upstream)
      if git -C "$repo" rev-parse --abbrev-ref --symbolic-full-name '@{u}' >/dev/null 2>&1; then
        printf 'expected no upstream, but @{u} resolved\n' >&2
        return 1
      fi
      ;;

    pull-rebase-conflict)
      [ -d "$repo/.git/rebase-merge" ] || [ -d "$repo/.git/rebase-apply" ] || {
        printf 'expected pull rebase metadata in %s\n' "$repo" >&2
        return 1
      }
      git -C "$repo" diff --diff-filter=U --name-only | grep -qx 'alpha.txt'
      ;;

    rebase-in-progress-clean)
      [ -d "$repo/.git/rebase-merge" ] || [ -d "$repo/.git/rebase-apply" ] || {
        printf 'expected rebase metadata in %s\n' "$repo" >&2
        return 1
      }
      if git -C "$repo" diff --diff-filter=U --name-only | grep -q .; then
        printf 'expected no unmerged files after staged resolution\n' >&2
        return 1
      fi
      git -C "$repo" status --porcelain | grep -q .
      ;;

    rebase-second-conflict)
      [ -d "$repo/.git/rebase-merge" ] || [ -d "$repo/.git/rebase-apply" ] || {
        printf 'expected rebase metadata in %s\n' "$repo" >&2
        return 1
      }
      git -C "$repo" diff --diff-filter=U --name-only | grep -qx 'alpha.txt'
      ;;

    linked-worktree-branch)
      git -C "$repo" worktree list --porcelain | grep -q 'branch refs/heads/feature/linked'
      local checkout_err
      checkout_err="$(mktemp "${TMPDIR:-/tmp}/kitsoki-bad-git-checkout.XXXXXX")"
      git -C "$repo" checkout feature/linked >"$checkout_err" 2>&1 && {
        rm -f "$checkout_err"
        printf 'expected checkout of linked branch to fail\n' >&2
        return 1
      }
      grep -qi 'already used by worktree' "$checkout_err"
      rm -f "$checkout_err"
      ;;

    dirty-feature-worktree)
      local wt
      wt="$(git -C "$repo" worktree list --porcelain | awk '$1=="worktree"{w=$2} $1=="branch" && $2=="refs/heads/feature/dirty"{print w; exit}')"
      [ -n "$wt" ] || {
        printf 'expected dirty feature worktree\n' >&2
        return 1
      }
      git -C "$wt" status --porcelain | grep -q .
      ;;

    merge-conflict-in-progress)
      [ -f "$repo/.git/MERGE_HEAD" ] || {
        printf 'expected MERGE_HEAD in %s\n' "$repo" >&2
        return 1
      }
      git -C "$repo" diff --diff-filter=U --name-only | grep -qx 'alpha.txt'
      ;;

    detached-head)
      [ "$(git -C "$repo" symbolic-ref -q --short HEAD || true)" = "" ] || {
        printf 'expected detached HEAD\n' >&2
        return 1
      }
      ;;

    diverged-upstream-clean)
      git -C "$repo" rev-parse --abbrev-ref --symbolic-full-name '@{u}' | grep -qx 'origin/main'
      local ahead behind
      ahead="$(git -C "$repo" rev-list --count '@{u}..HEAD')"
      behind="$(git -C "$repo" rev-list --count 'HEAD..@{u}')"
      [ "$ahead" -gt 0 ] && [ "$behind" -gt 0 ] || {
        printf 'expected branch to be ahead and behind upstream, got ahead=%s behind=%s\n' "$ahead" "$behind" >&2
        return 1
      }
      git -C "$repo" diff --quiet && git -C "$repo" diff --cached --quiet || {
        printf 'expected no tracked worktree/index changes for diverged-upstream-clean\n' >&2
        return 1
      }
      ;;

    dirty-main-before-pull)
      git -C "$repo" rev-parse --abbrev-ref --symbolic-full-name '@{u}' | grep -qx 'origin/main'
      git -C "$repo" status --porcelain | grep -q '^ M alpha.txt'
      git -C "$repo" fetch -q origin
      [ "$(git -C "$repo" rev-list --count 'HEAD..@{u}')" -gt 0 ] || {
        printf 'expected upstream to be ahead for dirty-main-before-pull\n' >&2
        return 1
      }
      ;;

    untracked-overwrite-blocker)
      git -C "$repo" status --porcelain | grep -q '^?? generated/'
      local blocker_err
      blocker_err="$(mktemp "${TMPDIR:-/tmp}/kitsoki-bad-git-blocker.XXXXXX")"
      git -C "$repo" checkout feature/untracked-blocker >"$blocker_err" 2>&1 && {
        rm -f "$blocker_err"
        printf 'expected checkout to be blocked by untracked file\n' >&2
        return 1
      }
      grep -qi 'untracked working tree files would be overwritten' "$blocker_err"
      rm -f "$blocker_err"
      ;;

    stale-worktree-entry)
      git -C "$repo" worktree list --porcelain | grep -q 'branch refs/heads/feature/stale'
      git -C "$repo" worktree list --porcelain | grep -q 'prunable'
      ;;

    cherry-pick-in-progress)
      [ -f "$repo/.git/CHERRY_PICK_HEAD" ] || {
        printf 'expected CHERRY_PICK_HEAD in %s\n' "$repo" >&2
        return 1
      }
      git -C "$repo" diff --diff-filter=U --name-only | grep -qx 'alpha.txt'
      ;;

    bisect-in-progress)
      [ -f "$repo/.git/BISECT_LOG" ] || {
        printf 'expected BISECT_LOG in %s\n' "$repo" >&2
        return 1
      }
      git -C "$repo" bisect log | grep -q 'git bisect start'
      ;;

    rename-delete-conflict)
      [ -f "$repo/.git/MERGE_HEAD" ] || {
        printf 'expected MERGE_HEAD for rename/delete conflict\n' >&2
        return 1
      }
      git -C "$repo" status --porcelain | grep -Eq '^(DU|UD|UA|AU) '
      git -C "$repo" status --porcelain | grep -q 'renamed-alpha.txt'
      ;;

    modify-delete-conflict)
      [ -f "$repo/.git/MERGE_HEAD" ] || {
        printf 'expected MERGE_HEAD for modify/delete conflict\n' >&2
        return 1
      }
      git -C "$repo" status --porcelain | grep -Eq '^(DU|UD) alpha.txt'
      ;;

    binary-rebase-conflict)
      [ -d "$repo/.git/rebase-merge" ] || [ -d "$repo/.git/rebase-apply" ] || {
        printf 'expected rebase metadata for binary conflict\n' >&2
        return 1
      }
      git -C "$repo" diff --diff-filter=U --name-only | grep -qx 'blob.bin'
      [ "$(git -C "$repo" ls-files -u -- blob.bin | wc -l | tr -d ' ')" = "3" ] || {
        printf 'expected three unmerged index stages for binary conflict\n' >&2
        return 1
      }
      ;;

    local-tags-conflict)
      local origin_tag local_tag
      git -C "$repo" fetch -q origin '+refs/tags/release:refs/tags/origin-release-fixture'
      origin_tag="$(git -C "$repo" rev-parse origin-release-fixture)"
      local_tag="$(git -C "$repo" rev-parse release)"
      [ "$origin_tag" != "$local_tag" ] || {
        printf 'expected local release tag to differ from remote release tag\n' >&2
        return 1
      }
      ;;
  esac
}

bad_git_usage() {
  cat >&2 <<'USAGE'
usage:
  bad_git_repo.sh list
  bad_git_repo.sh create <scenario> [root]
  bad_git_repo.sh assert <scenario> <repo>
USAGE
}

if [ "${BASH_SOURCE[0]}" = "$0" ]; then
  cmd="${1:-}"
  case "$cmd" in
    list)
      bad_git_cases
      ;;
    create)
      [ "$#" -ge 2 ] || { bad_git_usage; exit 2; }
      bad_git_create "$2" "${3:-}"
      ;;
    assert)
      [ "$#" -eq 3 ] || { bad_git_usage; exit 2; }
      bad_git_assert "$2" "$3"
      ;;
    *)
      bad_git_usage
      exit 2
      ;;
  esac
fi
