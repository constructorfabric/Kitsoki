#!/usr/bin/env bash
# prepare.sh — create ONE hermetic per-cell git worktree for a bake-off cell.
#
#   Usage: prepare.sh <bug-id> <candidate-key> <treatment>
#   Example: prepare.sh bug1 opus-4.8 single
#
# Each grid cell gets its OWN worktree, checked out at the bug's baseline_sha
# (the real fix's PARENT commit — bug present, hermetic). Cells must NEVER share
# a checkout: a shared checkout is literally concurrent-checkout bug #9 that this
# study covers, so we hard-isolate by (bug, candidate, treatment).
#
# Worktree path:  <repo>/.worktrees/bakeoff-<bug>-<candidate>-<treatment>
# Idempotent:     re-running with an existing CLEAN worktree at the right SHA is
#                 a no-op (prints the path). A dirty or wrong-SHA worktree is
#                 refused unless --force is passed (then it is removed + recreated).
#
# baseline_sha is parsed from bakeoff.yaml with python3 (no yq dependency).
# This script makes NO LLM calls and is safe to run in CI.
set -euo pipefail

usage() { echo "usage: $(basename "$0") <bug-id> <candidate-key> <treatment> [--force]" >&2; exit 2; }

[ $# -ge 3 ] || usage
BUG="$1"; CAND="$2"; TREAT="$3"; FORCE=""
[ "${4:-}" = "--force" ] && FORCE=1

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
MANIFEST="$SCRIPT_DIR/bakeoff.yaml"
# Repo root = the git toplevel that owns this tools/ tree.
REPO_ROOT="$(cd "$SCRIPT_DIR" && git rev-parse --show-toplevel)"

[ -f "$MANIFEST" ] || { echo "prepare: manifest not found: $MANIFEST" >&2; exit 1; }

# --- parse baseline_sha for <bug> from bakeoff.yaml -------------------------
read_field() {  # read_field <bug-id> <field>
  python3 - "$MANIFEST" "$1" "$2" <<'PY'
import sys, yaml
manifest, bug, field = sys.argv[1], sys.argv[2], sys.argv[3]
with open(manifest) as fh:
    doc = yaml.safe_load(fh)
for b in doc.get("bugs", []):
    if b.get("id") == bug:
        print(b.get(field, ""))
        sys.exit(0)
sys.stderr.write("prepare: unknown bug id %r in %s\n" % (bug, manifest))
sys.exit(3)
PY
}

BASELINE_SHA="$(read_field "$BUG" baseline_sha)"
[ -n "$BASELINE_SHA" ] || { echo "prepare: no baseline_sha for $BUG" >&2; exit 1; }

WT_NAME="bakeoff-${BUG}-${CAND}-${TREAT}"
WT_PATH="$REPO_ROOT/.worktrees/$WT_NAME"

# --- idempotency ------------------------------------------------------------
if [ -d "$WT_PATH" ]; then
  if [ -n "$FORCE" ]; then
    echo "prepare: --force: removing existing worktree $WT_PATH" >&2
    git -C "$REPO_ROOT" worktree remove --force "$WT_PATH" 2>/dev/null || rm -rf "$WT_PATH"
    git -C "$REPO_ROOT" worktree prune
  else
    HEAD_SHA="$(git -C "$WT_PATH" rev-parse HEAD 2>/dev/null || echo "")"
    DIRTY="$(git -C "$WT_PATH" status --porcelain 2>/dev/null || echo "ERR")"
    BASE_FULL="$(git -C "$REPO_ROOT" rev-parse "$BASELINE_SHA" 2>/dev/null || echo "")"
    if [ "$HEAD_SHA" = "$BASE_FULL" ] && [ -z "$DIRTY" ]; then
      # Already prepared, clean, at the right baseline: idempotent no-op.
      echo "$WT_PATH"
      exit 0
    fi
    echo "prepare: refusing — $WT_PATH exists but is dirty or off-baseline." >&2
    echo "         HEAD=$HEAD_SHA want=$BASE_FULL dirty=$([ -n "$DIRTY" ] && echo yes || echo no)" >&2
    echo "         Re-run with --force to discard and recreate." >&2
    exit 1
  fi
fi

# --- create the worktree (detached HEAD at baseline_sha) --------------------
# Detached so we never collide on branch names across the 40 cells.
mkdir -p "$REPO_ROOT/.worktrees"
git -C "$REPO_ROOT" worktree add --detach "$WT_PATH" "$BASELINE_SHA" >&2

echo "$WT_PATH"
