#!/usr/bin/env bash
# Render verdict.json (from qa-review.sh) into a human qa-report.md AND set the
# process exit code so the review can GATE a release. Pure jq/bash — no LLM,
# deterministic, testable in isolation (feed it a canned verdict.json).
#
# Gate (authoritative — recomputed here, not trusted from the model's `overall`):
#   default        a scenario fails the gate when status != pass AND required != false
#   --strict       every scenario must pass, ignoring per-scenario required:false
#   visual_issues  (LLM, context-aware) ALWAYS block — a blank where content belongs
#   --blank-scan   optional blank-scan.json (deterministic monochrome scan). Its
#                  flags are ADVISORY (rendered, never block) unless --blank-strict.
# Exit 0 if the gate passes, 1 if it fails, 2 on bad input.
#
# Usage: report.sh <verdict.json> [--out report.md] [--strict]
#          [--blank-scan blank-scan.json] [--blank-strict]
set -euo pipefail

verdict="${1:?usage: report.sh <verdict.json> [--out report.md] [--strict]}"
shift || true
out="" strict=0 blank_scan="" blank_strict=0
while [ $# -gt 0 ]; do
  case "$1" in
    --out)          out="$2"; shift 2 ;;
    --strict)       strict=1; shift ;;
    --blank-scan)   blank_scan="$2"; shift 2 ;;
    --blank-strict) blank_strict=1; shift ;;
    *) echo "unknown arg: $1" >&2; exit 1 ;;
  esac
done

command -v jq >/dev/null 2>&1 || { echo "jq not on PATH" >&2; exit 1; }
[ -f "$verdict" ] || { echo "no such verdict: $verdict" >&2; exit 1; }
jq -e . "$verdict" >/dev/null 2>&1 || { echo "verdict is not valid JSON: $verdict" >&2; exit 2; }
[ -n "$out" ] || out="$(dirname "$verdict")/qa-report.md"

# A slurpable blank-scan file (empty object when absent/invalid → no warnings).
bf="$(mktemp)"; trap 'rm -f "$bf"' EXIT
if [ -n "$blank_scan" ] && jq -e . "$blank_scan" >/dev/null 2>&1; then
  cp "$blank_scan" "$bf"
else
  echo '{}' > "$bf"
fi

# --- Markdown report -------------------------------------------------------
jq -r --argjson strict "$strict" --argjson blank_strict "$blank_strict" \
      --slurpfile blank "$bf" '
  def icon(s): if s=="pass" then "✅" elif s=="fail" then "❌" else "⚠️" end;
  def gated(sc): if $strict==1 then (sc.status=="pass")
                 else (sc.status=="pass" or (sc.required==false)) end;
  ( [ .scenarios[] | select(gated(.)|not) ] ) as $blockers |
  ( [ .visual_issues[]? ] ) as $vis |
  ( ($blank[0].flagged // []) ) as $bl |
  ( ($blank_strict==1) and (($bl|length) > 0) ) as $blank_block |
  ( (($blockers|length) + ($vis|length))==0 and ($blank_block|not) ) as $pass |
  "# UI demo QA report",
  "",
  ( if $pass then "**Gate: ✅ PASS**\(if ($bl|length)>0 then " — \($bl|length) advisory blank-scan warning(s)" else "" end)"
    else "**Gate: ❌ FAIL** — \(($blockers|length)) blocking scenario(s), \(($vis|length)) visual issue(s)\(if $blank_block then ", \($bl|length) blank-scan flag(s)" else "" end)" end ),
  "",
  "| metric | n |",
  "|---|---|",
  "| scenarios | \(.summary.scenarios_total // (.scenarios|length)) |",
  "| passed | \(.summary.passed // ([.scenarios[]|select(.status=="pass")]|length)) |",
  "| failed | \(.summary.failed // ([.scenarios[]|select(.status=="fail")]|length)) |",
  "| unsupported | \(.summary.unsupported // ([.scenarios[]|select(.status=="unsupported")]|length)) |",
  "| visual issues | \($vis|length) |",
  "| blank-scan warnings | \($bl|length)\(if $blank_strict==1 then " (blocking)" else " (advisory)" end) |",
  "| frames reviewed | \((.frames_reviewed // [])|length) |",
  "",
  ( if ($vis|length) > 0 then
      ( "## ❌ Visual issues (blank / broken renders)",
        "",
        "| frame | region | issue |",
        "|---|---|---|",
        ( $vis[] | "| `\(.frame // "?")` | \(.region // "") | \(.issue // "") |" ),
        "" )
    else empty end ),
  ( if ($bl|length) > 0 then
      ( "## \(if $blank_strict==1 then "❌" else "⚠️" end) Blank-scan warnings (deterministic monochrome regions\(if $blank_strict==1 then "" else " — advisory, review by eye" end))",
        "",
        "| frame | largest flat region | issue |",
        "|---|---|---|",
        ( $bl[]
          | (if (.block.coverage // 0) > 0 then .block else .background end) as $r
          | "| `\(.frame // "?")` | \($r.color // "?") @ \((($r.coverage // 0)*100)|floor)% | \(.issue // "") |" ),
        "" )
    else empty end ),
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

# A blank/broken render where visual content is expected is a real defect: any
# reported visual_issue blocks the gate, at every effort level (not just strict).
vis_block="$(jq '[ .visual_issues[]? ] | length' "$verdict")"
if [ "$vis_block" -gt 0 ]; then
  echo "gate: $vis_block visual issue(s) — blank/broken render where content was expected" >&2
fi

# Deterministic blank-scan flags are advisory by default (surfaced, never block);
# --blank-strict promotes them to blocking.
blank_n="$(jq '(.flagged // []) | length' "$bf")"
blank_block=0
if [ "$blank_n" -gt 0 ]; then
  if [ "$blank_strict" -eq 1 ]; then
    blank_block=1
    echo "gate: $blank_n blank-scan flag(s) blocking (--blank-strict)" >&2
  else
    echo "advisory: $blank_n blank-scan flag(s) — large monochrome region(s), review by eye" >&2
  fi
fi

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

echo "wrote $out  (blocking scenarios: $blockers, visual issues: $vis_block, blank-scan: $blank_n$([ "$blank_strict" -eq 1 ] && echo ' blocking' || echo ' advisory'))"
{ [ "$blockers" -eq 0 ] && [ "$adv_block" -eq 0 ] && [ "$vis_block" -eq 0 ] && [ "$blank_block" -eq 0 ]; } || exit 1
