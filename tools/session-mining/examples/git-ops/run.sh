#!/usr/bin/env bash
# run.sh — the git-ops coverage-mining FLAGSHIP demo. Runs the whole deterministic
# spine over the committed example corpus with NO LLM and NO cost, then prints the
# two reports and the coverage worksheet. This is the worked, runnable answer to
# "how does story coverage mining actually work?".
#
# The one LLM step (B, intents.workflow.js) is represented here by the committed
# agent.json — so the C->F chain + coverage_prep run end-to-end, repeatably.
#
#   ./run.sh           run into a scratch dir, print the reports + worksheet
#   ./run.sh --keep D  write the artifacts into D and keep them
#
# Requires: python3 (stdlib only) and jq (for the trace-fidelity check + the
# pretty-prints). validate_reports needs `jsonschema` (pip3 install --user jsonschema);
# it is skipped with a note if absent.
set -euo pipefail

HERE="$(cd "$(dirname "$0")" && pwd)"
TOOL="$(cd "$HERE/../.." && pwd)"
PROFILE="$(cd "$TOOL/../.." && pwd)/stories/git-ops/mining.profile.yaml"

OUT=""
if [ "${1:-}" = "--keep" ] && [ -n "${2:-}" ]; then
  OUT="$2"; mkdir -p "$OUT"
else
  OUT="$(mktemp -d)"; trap 'rm -rf "$OUT"' EXIT
fi

say() { printf '\n\033[1m== %s ==\033[0m\n' "$1"; }

say "0. Trace fidelity — regenerate traces from the real distill.jq and diff"
if command -v jq >/dev/null 2>&1; then
  mkdir -p "$OUT/traces.regen"
  for f in "$HERE"/raw/*.jsonl; do
    sid="$(basename "$f" .jsonl)"
    jq -r -f "$TOOL/distill.jq" "$f" > "$OUT/traces.regen/$sid.txt"
    if diff -q "$HERE/traces/$sid.txt" "$OUT/traces.regen/$sid.txt" >/dev/null; then
      echo "  ok   $sid.txt  (committed trace == distill.jq output)"
    else
      echo "  DRIFT $sid.txt — committed trace differs from distill.jq!"; exit 1
    fi
  done
else
  echo "  (jq not found — skipping fidelity regeneration)"
fi

say "C. ground — validate the agent hypothesis against the traces"
python3 "$TOOL/ground.py" --agent "$HERE/agent.json" --traces "$HERE/traces" --out "$OUT/grounded.json"

say "D+E. tag_score — validate tags, cluster, score determinism"
python3 "$TOOL/tag_score.py" --grounded "$OUT/grounded.json" --traces "$HERE/traces" --out "$OUT/scored.json"

say "E'. outcomes — recover the real result of every tool call from raw jsonl"
python3 "$TOOL/outcomes.py" --raw "$HERE/raw" --out "$OUT/outcomes.json"

say "F. emit — the two linked reports, with --outcomes (outcome + satisfaction)"
python3 "$TOOL/emit.py" --scored "$OUT/scored.json" --traces "$HERE/traces" \
  --raw "$HERE/raw" --outcomes "$OUT/outcomes.json" --out-dir "$OUT" --job gitops-flagship

say "verify cross-link + schema"
python3 "$TOOL/verify_link.py" "$OUT"
python3 "$TOOL/validate_reports.py" "$OUT" 2>&1 || echo "  (validate_reports skipped/failed — needs jsonschema)"

say "coverage_prep — scope-filter + arg-aware dedup + candidate rooms + outcome inlining"
python3 "$TOOL/coverage_prep.py" --job-dir "$OUT" --profile "$PROFILE" --out-dir "$OUT"

say "RESULT 1 — the conformance lenses (analysis.json)"
if command -v jq >/dev/null 2>&1; then
  jq -r '.instances[] | "• \(.instance_id)  [\(.determinism)]" + "\n" +
    ( [.actions[] | "    \(.signature)  ->  " +
        (if .outcome == null then "(no outcome)"
         elif .outcome.is_error then "✗ ERROR  " + ((.outcome.stderr_head // .outcome.stdout_head)|.[0:46])
         else "✓ ok  " + (.outcome.stdout_head|.[0:46]) end) ] | join("\n") ) +
    (if .satisfaction.corrected then "\n    ⚠ SATISFACTION: corrected — " + (.satisfaction.corrective_ops|join(", ")) else "" end)' \
    "$OUT/analysis.json"
fi

say "RESULT 2 — the coverage worksheet skeleton (coverage.md)"
cat "$OUT/coverage.md"

say "DONE"
echo "Artifacts in: $OUT"
echo "The worked worksheet (verdicts filled by the human/LLM map step) is committed at:"
echo "  $HERE/coverage.worked.md"
