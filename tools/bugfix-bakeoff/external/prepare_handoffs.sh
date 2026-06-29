#!/usr/bin/env bash
# prepare_handoffs.sh — no-cost prepared-cell handoff setup for a selected matrix.
#
# Runs drive_cell.sh --no-drive for every selected bug x candidate, then audits
# the prepared metadata and MCP prompts for missing files and hidden-oracle leaks.
# It never drives a live model.
set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$HERE" && git rev-parse --show-toplevel)"

project=""
bugs=""
candidates=""
repo_dir=""
markdown=""
results="../../../.artifacts/external-bakeoff/results"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --project) project="$2"; shift 2;;
    --bug) bugs="$2"; shift 2;;
    --candidate) candidates="$2"; shift 2;;
    --repo-dir) repo_dir="$2"; shift 2;;
    --results) results="$2"; shift 2;;
    --markdown) markdown="$2"; shift 2;;
    *) echo "unknown arg: $1" >&2; exit 2;;
  esac
done

[[ -n "$project" && -n "$bugs" && -n "$candidates" ]] || {
  echo "usage: prepare_handoffs.sh --project <name> --bug <ids> --candidate <keys> [--repo-dir <checkout>] [--markdown <path>]" >&2
  exit 2
}

repo_args=()
[[ -n "$repo_dir" ]] && repo_args=(--repo-dir "$repo_dir")

for bug in $(printf '%s' "$bugs" | tr ',' ' '); do
  for candidate in $(printf '%s' "$candidates" | tr ',' ' '); do
    "$HERE/drive_cell.sh" \
      --project "$project" \
      --bug "$bug" \
      --candidate "$candidate" \
      "${repo_args[@]}" \
      --no-drive \
      1>&2
  done
done

audit_args=(
  audit-handoffs
  --project "$project"
  --bug "$bugs"
  --candidate "$candidates"
  --results "$results"
)
[[ -n "$markdown" ]] && audit_args+=(--markdown "$markdown")

cd "$REPO_ROOT"
python3 "$HERE/bench.py" "${audit_args[@]}"
