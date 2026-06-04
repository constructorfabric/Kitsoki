#!/usr/bin/env bash
# recap.sh — distill the *most recent* Claude Code transcripts for the current
# repo into small action-traces, ready for a "what have we been working on?"
# recap. Thin recency-first wrapper over prep.py; NEVER redacts (local recap,
# stays in /tmp). Prints the trace dir and one absolute trace path per line so a
# caller (the `session-recap` agent) can read exactly those files and nothing
# else.
#
# Usage:
#   recap.sh [--max N] [--since DURATION] [--grep WORD]... [--dir PATH] [--min-bytes N]
#
#   --max N        how many recent sessions to distill (default 8)
#   --since DUR    only sessions modified within DUR, e.g. 24h, 3d, 90m (default: none)
#   --grep WORD    cheap topical prefilter — keep only sessions whose raw jsonl
#                  contains WORD (repeatable, OR). Use for "...working on <topic>".
#   --dir PATH     repo/worktree whose sessions to read (default: git root, else cwd)
#   --min-bytes N  skip raw sessions smaller than N bytes (default 30000)
#
# The Claude Code projects dir is derived from the absolute path by replacing
# every '/' and '.' with '-' (so /home/u/code/app/.worktrees/x becomes
# -home-u-code-app--worktrees-x). That encoding is the whole reason this wrapper
# exists — get it wrong and you read the wrong repo's history.
set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
MAX=8
SINCE=""
DIR=""
MIN_BYTES=30000
GREP_ARGS=()

while [ $# -gt 0 ]; do
  case "$1" in
    --max)       MAX="$2"; shift 2 ;;
    --since)     SINCE="$2"; shift 2 ;;
    --grep)      GREP_ARGS+=(--grep "$2"); shift 2 ;;
    --dir)       DIR="$2"; shift 2 ;;
    --min-bytes) MIN_BYTES="$2"; shift 2 ;;
    *) echo "recap.sh: unknown arg: $1" >&2; exit 2 ;;
  esac
done

# Resolve the repo/worktree path.
if [ -z "$DIR" ]; then
  DIR="$(git rev-parse --show-toplevel 2>/dev/null || pwd)"
fi
DIR="$(cd "$DIR" && pwd)"

# Encode to the Claude Code projects slug: '/' and '.' both become '-'.
SLUG="$(printf '%s' "$DIR" | sed 's#[/.]#-#g')"
PROJ="$HOME/.claude/projects/$SLUG"

if [ ! -d "$PROJ" ]; then
  echo "recap.sh: no Claude Code session history for $DIR" >&2
  echo "  (looked in $PROJ)" >&2
  exit 1
fi

# --since: prune to sessions touched within the window, by symlinking the
# survivors into a scratch dir we hand to prep.py. find -newermt does the
# duration math; we translate e.g. 24h/3d/90m into an `ago` expression.
SRC="$PROJ"
if [ -n "$SINCE" ]; then
  num="${SINCE%[a-zA-Z]}"; unit="${SINCE##*[0-9]}"
  case "$unit" in
    h|H) ago="$num hours ago" ;;
    d|D) ago="$num days ago" ;;
    m|M) ago="$num minutes ago" ;;
    *)   echo "recap.sh: bad --since '$SINCE' (use 24h, 3d, 90m)" >&2; exit 2 ;;
  esac
  SRC="$(mktemp -d /tmp/recap-since.XXXXXX)"
  found=0
  while IFS= read -r f; do
    ln -s "$f" "$SRC/"; found=$((found+1))
  done < <(find "$PROJ" -maxdepth 1 -name '*.jsonl' -newermt "$ago" 2>/dev/null)
  if [ "$found" -eq 0 ]; then
    echo "recap.sh: no sessions in the last $SINCE for $DIR" >&2
    exit 1
  fi
fi

OUT="/tmp/sm-recap-$SLUG"
# prep.py: recency-first, capped at --max, no --redact (local recap).
python3 "$HERE/prep.py" "$SRC" \
  --out "$OUT" \
  --sample recency \
  --max "$MAX" \
  --min-bytes "$MIN_BYTES" \
  "${GREP_ARGS[@]}" >&2

TRACES="$OUT/traces"
echo "TRACEDIR=$TRACES"
# Newest first, so the agent reads the most recent work at the top.
find "$TRACES" -name '*.txt' -print0 2>/dev/null \
  | xargs -0 -r ls -t
