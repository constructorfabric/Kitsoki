#!/usr/bin/env bash
# cell_status.sh — one-command status for all cells in a bake-off cache dir.
#
# drive_cell.sh cells are long-running (many minutes) and drive_cell.sh writes
# no single "am I alive/stuck/done" file — checking meant grep'ing `ps aux`
# and `stat`-ing trace files by hand. This reads only the files drive_cell.sh
# already produces (traces/, drive-logs/, results/cells/) plus a live `ps`
# snapshot, and prints one line per cell: state, how stale its trace is, and
# the scored outcome once available. No manual polling required.
#
#   cell_status.sh <cache_dir>          # e.g. $EXTERNAL_BAKEOFF_CACHE, or the
#                                        # default .artifacts/external-bakeoff
set -euo pipefail

cache="${1:?usage: cell_status.sh <cache_dir> (the EXTERNAL_BAKEOFF_CACHE used to drive cells)}"
[[ -d "$cache" ]] || { echo "cell_status.sh: no such cache dir: $cache" >&2; exit 2; }

now="$(date +%s)"
# One field per running cellkey ("$project-$bug-$candidate", matching
# drive_cell.sh's own `cellkey="$project-$bug-$cand"`), derived from each
# live process's --project/--bug/--candidate argv rather than string-matching
# raw ps output — candidate keys like "glm-5.2" contain dashes too, so this is
# the only reliable way to reconstruct the same key drive_cell.sh uses.
running_keys="$(ps ax -o command= 2>/dev/null | python3 -c '
import re, sys
for line in sys.stdin:
    if "drive_cell.sh" not in line:
        continue
    proj = re.search(r"--project\s+(\S+)", line)
    bug = re.search(r"--bug\s+(\S+)", line)
    cand = re.search(r"--candidate\s+(\S+)", line)
    if proj and bug and cand:
        print(f"{proj.group(1)}-{bug.group(1)}-{cand.group(1)}")
' 2>/dev/null || true)"

shopt -s nullglob
traces=("$cache"/traces/*.jsonl)
shopt -u nullglob

if [[ ${#traces[@]} -eq 0 ]]; then
  echo "cell_status.sh: no traces under $cache/traces/ — nothing driven yet" >&2
  exit 0
fi

printf '%-38s %-10s %8s %8s %6s  %s\n' "CELL" "STATE" "TRACE_S" "COST" "CALLS" "NOTES"

for trace in "${traces[@]}"; do
  cellkey="$(basename "$trace" .jsonl)"
  trace_mtime="$(stat -f "%m" "$trace" 2>/dev/null || stat -c "%Y" "$trace" 2>/dev/null)"
  trace_age=$((now - trace_mtime))

  result="$cache/results/cells/${cellkey}-kitsoki.json"
  err_file="$cache/drive-logs/${cellkey}.err"

  is_running=0
  if printf '%s\n' "$running_keys" | grep -qxF "$cellkey" 2>/dev/null; then
    is_running=1
  fi

  state="unknown"
  cost="-"
  calls="-"
  notes=""

  if [[ -f "$result" ]]; then
    read -r state cost calls notes < <(python3 - "$result" <<'PY'
import json, sys
d = json.load(open(sys.argv[1]))
outcome = d.get("outcome", {})
metrics = d.get("metrics", {})
pass_ = outcome.get("oracle_pass")
cost = metrics.get("cost_usd", 0)
calls = metrics.get("agent_calls", 0)
state = "PASS" if pass_ else "FAIL"
note = "-"
if cost in (0, 0.0) and calls in (0, None):
    note = "infra:zero-cost-check-trace"
print(f"{state} {cost} {calls} {note}")
PY
)
  elif [[ $is_running -eq 1 ]]; then
    if [[ $trace_age -gt 900 ]]; then
      state="STALE?"
      notes="no trace growth in ${trace_age}s while process alive — investigate"
    else
      state="running"
    fi
  else
    state="gone"
    notes="no result + no live process"
    if [[ -f "$err_file" ]] && grep -q "WATCHDOG" "$err_file" 2>/dev/null; then
      notes="WATCHDOG killed (stalled) — see $err_file"
    fi
  fi

  printf '%-38s %-10s %8s %8s %6s  %s\n' "$cellkey" "$state" "${trace_age}s" "$cost" "$calls" "$notes"
done
