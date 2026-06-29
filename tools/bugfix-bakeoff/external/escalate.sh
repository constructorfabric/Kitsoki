#!/usr/bin/env bash
# escalate.sh — capability ladder for the bug-fix benchmark.
#
# Run a project's bugs up a cheap→expensive (model AND effort) candidate ladder,
# stopping each bug at the FIRST rung that reaches `solved` (the deterministic
# bench.py oracle verdict). This is the onboarding question — "what is the
# cheapest model/effort that fixes my bugs?" — answered as one command.
#
#   escalate.sh --project <name> [--bugs b1,b2] [--ladder default] [--dry-run]
#               [--repo-dir <local-checkout>]
#               [--rungs k1,k2,...]   # explicit ladder, overrides --ladder
#
# COST-BEARING (drives real LLMs via drive_cell.sh) — operator-run, never CI.
# --dry-run prints the per-bug plan (bugs × rungs) and spends nothing.
#
# Per (bug, rung): drive_cell.sh --project --bug --candidate --score, then read
# the cell's outcome.quality. solved ⇒ stop (record the cheapest solving rung).
# failed/partial ⇒ climb. Ladder exhausted ⇒ record best-quality rung reached.
# Pure orchestration over the free bench.py score; re-running a solved bug is a
# no-op cost (drive_cell re-scores), so the loop is safely resumable.
set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

project=""; bugs_csv=""; ladder="default"; rungs_csv=""; repo_dir=""; dry=0
while [[ $# -gt 0 ]]; do
  case "$1" in
    --project) project="$2"; shift 2;;
    --bugs)    bugs_csv="$2"; shift 2;;
    --ladder)  ladder="$2"; shift 2;;
    --rungs)   rungs_csv="$2"; shift 2;;
    --repo-dir) repo_dir="$2"; shift 2;;
    --dry-run) dry=1; shift;;
    *) echo "unknown arg: $1" >&2; exit 2;;
  esac
done
[[ -n "$project" ]] || { echo "usage: escalate.sh --project <name> [--bugs b1,b2] [--ladder default|--rungs k1,k2] [--repo-dir <local-checkout>] [--dry-run]" >&2; exit 2; }

# --- resolve bugs (default: all in the manifest) ------------------------------
if [[ -z "$bugs_csv" ]]; then
  bugs_csv="$(python3 "$HERE/bench.py" meta --project "$project" \
    | python3 -c 'import json,sys; print(",".join(json.load(sys.stdin)["bugs"]))')"
fi
IFS=',' read -ra BUGS <<< "$bugs_csv"

# --- resolve the ladder (rungs override named ladder) -------------------------
if [[ -n "$rungs_csv" ]]; then
  IFS=',' read -ra RUNGS <<< "$rungs_csv"
else
  rungs_csv="$(python3 -c '
import sys,yaml
d=yaml.safe_load(open(sys.argv[1]))
lad=d.get("ladders",{}).get(sys.argv[2])
if not lad: sys.exit(f"no ladder \"{sys.argv[2]}\" in candidates.yaml")
print(",".join(lad))' "$HERE/candidates.yaml" "$ladder")"
  IFS=',' read -ra RUNGS <<< "$rungs_csv"
fi

label=""; [[ "$dry" == 1 ]] && label=" (dry-run)"
echo "[escalate] project=$project bugs=[${BUGS[*]}] ladder=[${RUNGS[*]}]$label" >&2

if [[ "$dry" == 1 ]]; then
  echo "Plan — each bug climbs until 'solved':"
  for b in "${BUGS[@]}"; do
    echo "  $b:"
    for r in "${RUNGS[@]}"; do
      line="    -> drive_cell.sh --project $project --bug $b --candidate $r --score"
      [[ -n "$repo_dir" ]] && line="$line --repo-dir $repo_dir"
      echo "$line"
    done
  done
  exit 0
fi

CACHE_RESULTS="${EXTERNAL_BAKEOFF_CACHE:-$(cd "$HERE" && git rev-parse --show-toplevel)/.artifacts/external-bakeoff}/results"
summary="$CACHE_RESULTS/escalation-$project.tsv"
mkdir -p "$CACHE_RESULTS"
: > "$summary"
printf 'bug\tsolving_rung\trungs_tried\tbest_quality\n' >> "$summary"

quality_of() {  # read outcome.quality from a cell json, or "?" if absent
  python3 -c 'import json,sys
try: print(json.load(open(sys.argv[1]))["outcome"]["quality"])
except Exception: print("?")' "$1" 2>/dev/null || echo "?"
}

for b in "${BUGS[@]}"; do
  solving=""; tried=0; best="failed"
  for r in "${RUNGS[@]}"; do
    tried=$((tried+1))
    out="$CACHE_RESULTS/cells/$project-$b-$r-kitsoki.json"
    # Resumable: an already-`solved` cell short-circuits the (cost-bearing) drive,
    # so re-running a partial ladder never re-spends a solved rung.
    if [[ "$(quality_of "$out")" == "solved" ]]; then
      echo "[escalate] $b @ $r already solved — skip drive" >&2
      best="solved"; solving="$r"; break
    fi
    echo "[escalate] $b @ rung $r …" >&2
    args=(--project "$project" --bug "$b" --candidate "$r" --score)
    [[ -n "$repo_dir" ]] && args+=(--repo-dir "$repo_dir")
    "$HERE/drive_cell.sh" "${args[@]}" || true
    q="$(quality_of "$out")"
    echo "[escalate]   $b @ $r -> $q" >&2
    case "$q" in
      solved)  best="solved"; solving="$r"; break;;
      partial) [[ "$best" == "failed" ]] && best="partial";;
    esac
  done
  printf '%s\t%s\t%s\t%s\n' "$b" "${solving:-none}" "$tried" "$best" >> "$summary"
done

echo "[escalate] done — ladder summary:" >&2
column -t -s$'\t' "$summary"
echo "[escalate] written: $summary" >&2
