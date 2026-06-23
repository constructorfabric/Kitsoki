#!/usr/bin/env bash
# added-diff.sh <base_ref>
#
# Print the ADDED lines of the branch diff against <base_ref> (additions only;
# the leading '+' stripped, the '+++' file header dropped). Used by the
# generating room's compute-diff call so the maker sees WHAT the branch added.
#
# Contract: this NEVER errors the loop. A missing/unknown base, a base that is
# not an ancestor, or no git at all all degrade to empty output + exit 0. The
# room binds stdout into world.added_diff; a blank diff is a valid "(none)".
set -euo pipefail

base_ref="${1:-main}"

# No git here? Degrade quietly.
if ! command -v git >/dev/null 2>&1; then
  exit 0
fi
if ! git rev-parse --git-dir >/dev/null 2>&1; then
  exit 0
fi

# --merge-base diffs against the common ancestor of <base_ref> and HEAD, which
# tolerates <base_ref> not being a direct ancestor and ignores divergent
# base-side changes (conflict/merge noise). If <base_ref> is unknown the diff is
# skipped (|| true) rather than failing the loop.
diff_out="$(git diff --merge-base "${base_ref}" HEAD 2>/dev/null || true)"

# Keep added lines only: unified-diff additions start with a single '+', except
# the '+++ ' file header which we drop.
printf '%s\n' "${diff_out}" \
  | grep -E '^\+' \
  | grep -vE '^\+\+\+ ' \
  | sed -E 's/^\+//' \
  || true

exit 0
