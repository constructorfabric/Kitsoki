#!/usr/bin/env bash
# Render verdict.json (from qa-review.sh) into a human qa-report.md AND set the
# process exit code so the review can GATE a release. Pure jq/bash — no LLM,
# deterministic, testable in isolation (feed it a canned verdict.json).
#
# Gate (authoritative — recomputed here, not trusted from the model's `overall`):
#   default   a scenario fails the gate when status != pass AND required != false
#   --strict  every scenario must pass, ignoring per-scenario required:false
# Exit 0 if the gate passes, 1 if it fails, 2 on bad input.
#
# Usage: report.sh <verdict.json> [--out report.md] [--strict]
set -euo pipefail

verdict="${1:?usage: report.sh <verdict.json> [--out report.md] [--strict]}"
shift || true
out="" strict=0
while [ $# -gt 0 ]; do
  case "$1" in
    --out)    out="$2"; shift 2 ;;
    --strict) strict=1; shift ;;
    *) echo "unknown arg: $1" >&2; exit 1 ;;
  esac
done

command -v jq >/dev/null 2>&1 || { echo "jq not on PATH" >&2; exit 1; }
[ -f "$verdict" ] || { echo "no such verdict: $verdict" >&2; exit 1; }
jq -e . "$verdict" >/dev/null 2>&1 || { echo "verdict is not valid JSON: $verdict" >&2; exit 2; }
[ -n "$out" ] || out="$(dirname "$verdict")/qa-report.md"

# --- Markdown report -------------------------------------------------------
jq -r --argjson strict "$strict" '
  def icon(s): if s=="pass" then "✅" elif s=="fail" then "❌" else "⚠️" end;
  def gated(sc): if $strict==1 then (sc.status=="pass")
                 else (sc.status=="pass" or (sc.required==false)) end;
  ( [ .scenarios[] | select(gated(.)|not) ] ) as $blockers |
  "# UI demo QA report",
  "",
  ( if ($blockers|length)==0 then "**Gate: ✅ PASS**" else "**Gate: ❌ FAIL** — \(($blockers|length)) blocking scenario(s)" end ),
  "",
  "| metric | n |",
  "|---|---|",
  "| scenarios | \(.summary.scenarios_total // (.scenarios|length)) |",
  "| passed | \(.summary.passed // ([.scenarios[]|select(.status=="pass")]|length)) |",
  "| failed | \(.summary.failed // ([.scenarios[]|select(.status=="fail")]|length)) |",
  "| unsupported | \(.summary.unsupported // ([.scenarios[]|select(.status=="unsupported")]|length)) |",
  "| frames reviewed | \((.frames_reviewed // [])|length) |",
  "",
  "## Scenarios",
  ( .scenarios[] |
    "",
    "### \(icon(.status)) \(.title) `\(.id)`\(if .required==false then " _(optional)_" else "" end)",
    "",
    "| step | status | evidence | observation |",
    "|---|---|---|---|",
    ( .steps[] |
      ( (.evidence // []) | map("`\(.frame)`") | join("<br>") ) as $f |
      ( (.evidence // []) | map(.observation) | join("<br>") ) as $o |
      "| \(.text) | \(icon(.status)) | \($f) | \($o // "") |"
    )
  ),
  ""
' "$verdict" > "$out"

# --- Gate exit code (independent of the report rendering above) ------------
blockers="$(jq --argjson strict "$strict" '
  [ .scenarios[]
    | select( if $strict==1 then (.status!="pass")
              else (.status!="pass" and (.required!=false)) end ) ]
  | length' "$verdict")"

# Under --strict, an adversarial pass that was supposed to run but did NOT
# complete (adversary.status present and != "ok") is itself a blocking failure:
# the downgrade-only re-check is part of the strict guarantee, so a silent
# adversary flake must not pass. Absent field (--no-adversary / older verdict)
# is a no-op.
adv_block=0
if [ "$strict" -eq 1 ]; then
  adv_status="$(jq -r '.adversary.status // "absent"' "$verdict")"
  if [ "$adv_status" != "ok" ] && [ "$adv_status" != "absent" ]; then
    adv_block=1
    echo "strict gate: adversarial verification did not complete (adversary.status=$adv_status)" >&2
  fi
fi

echo "wrote $out  (blocking scenarios: $blockers)"
{ [ "$blockers" -eq 0 ] && [ "$adv_block" -eq 0 ]; } || exit 1
