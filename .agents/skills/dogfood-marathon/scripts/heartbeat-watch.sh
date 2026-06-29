#!/usr/bin/env bash
# heartbeat-watch.sh <progress-file> [stall-seconds] [poll-seconds]
#
# An ACTIVITY-based watchdog for a long background agent (e.g. a live kitsoki
# dogfood). It does NOT cap wall-clock time — a run that keeps making EXTERNAL
# progress can run for hours and this never fires. It only trips when the agent
# is TRULY STUCK: the progress file stops growing.
#
# How to use it (the pattern):
#   1. Launch the long agent in the background, with a trace/progress file it
#      appends to as it works (a kitsoki dogfood: `--trace-out <path>` or the
#      driver's `.artifacts/<run>/trace.jsonl`).
#   2. Launch THIS in the background too, pointed at that same file.
#   3. When it exits non-zero (STALLED), the orchestrator is re-invoked — it then
#      `TaskStop`s the stuck agent and reports, instead of waiting forever.
#   A healthy run never trips it: every drive turn appends to the trace, refreshing
#   the mtime well within `stall-seconds`.
#
# Why mtime of a trace file and not the MCP: read-only studio calls block behind a
# concurrent live turn (filed bug), so the FILESYSTEM is the non-blocking progress
# signal. The trace JSONL grows on every turn/agent-call event.
#
# Exit: 0 = file vanished / clean stop; 7 = STALLED (no growth for stall-seconds);
#       2 = usage error.
set -euo pipefail

FILE="${1:?usage: heartbeat-watch.sh <progress-file> [stall-seconds] [poll-seconds]}"
STALL="${2:-600}"   # 10 min of no trace growth = stuck (a single LLM turn is minutes, not 10)
POLL="${3:-60}"

mtime() { stat -f %m "$FILE" 2>/dev/null || stat -c %Y "$FILE" 2>/dev/null || echo 0; }

# Wait up to ~15 min for the file to first appear (session.new + first turn).
for _ in $(seq 1 15); do [ -e "$FILE" ] && break; sleep "$POLL"; done
if [ ! -e "$FILE" ]; then
  echo "STALLED: $FILE never appeared after $((15 * POLL))s — agent likely hung before its first turn at $(date)"
  exit 7
fi

while [ -e "$FILE" ]; do
  now="$(date +%s)"; m="$(mtime)"
  idle=$(( now - m ))
  if [ "$idle" -ge "$STALL" ]; then
    echo "STALLED: $(basename "$FILE") untouched for ${idle}s (>= ${STALL}s) at $(date) — agent is stuck, not working"
    exit 7
  fi
  sleep "$POLL"
done
echo "progress file gone — agent presumably finished"
exit 0
