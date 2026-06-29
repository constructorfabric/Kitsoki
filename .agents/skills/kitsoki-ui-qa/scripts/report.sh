#!/usr/bin/env bash
# Render verdict.json (from qa-review.sh) into a human qa-report.md AND set the
# process exit code so the review can GATE a release. Pure jq/bash — no LLM,
# deterministic, testable in isolation (feed it a canned verdict.json).
#
# Gate (authoritative — recomputed here, not trusted from the model's `overall`):
#   default        a scenario fails the gate when status != pass AND required != false
#   --strict       every scenario must pass, ignoring per-scenario required:false
#   visual_issues  (LLM, context-aware) ALWAYS block — a blank where content belongs
#   annotation_issues (LLM, context-aware) ALWAYS block — MIXED narration styles
#                  (tour-popover in some frames AND banner/caption in others)
#   --blank-scan   optional blank-scan.json (deterministic monochrome scan). Its
#                  flags are ADVISORY (rendered, never block) unless --blank-strict.
#   --edge-scan    optional edge-scan.json. Any flag blocks; content touching a
#                  frame edge means text/UI is likely clipped in the final MP4.
#   --pacing-scan  optional pacing-scan.json (deterministic chapter-duration scan).
#                  Its flags are ADVISORY (rendered, never block) unless
#                  --pacing-strict — a popover that flashes by too fast to read.
# Exit 0 if the gate passes, 1 if it fails, 2 on bad input.
#
# Usage: report.sh <verdict.json> [--out report.md] [--strict]
#          [--blank-scan blank-scan.json] [--blank-strict]
#          [--pacing-scan pacing-scan.json] [--pacing-strict]
set -euo pipefail

verdict="${1:?usage: report.sh <verdict.json> [--out report.md] [--strict]}"
shift || true
out="" strict=0 blank_scan="" blank_strict=0 edge_scan="" pacing_scan="" pacing_strict=0 rrweb_scan="" rrweb_strict=0 scroll_scan="" scroll_strict=0
while [ $# -gt 0 ]; do
  case "$1" in
    --out)           out="$2"; shift 2 ;;
    --strict)        strict=1; shift ;;
    --blank-scan)    blank_scan="$2"; shift 2 ;;
    --blank-strict)  blank_strict=1; shift ;;
    --edge-scan)     edge_scan="$2"; shift 2 ;;
    --pacing-scan)   pacing_scan="$2"; shift 2 ;;
    --pacing-strict) pacing_strict=1; shift ;;
    --rrweb-scan)    rrweb_scan="$2"; shift 2 ;;
    --rrweb-strict)  rrweb_strict=1; shift ;;
    --scroll-scan)   scroll_scan="$2"; shift 2 ;;
    --scroll-strict) scroll_strict=1; shift ;;
    *) echo "unknown arg: $1" >&2; exit 1 ;;
  esac
done

command -v jq >/dev/null 2>&1 || { echo "jq not on PATH" >&2; exit 1; }
[ -f "$verdict" ] || { echo "no such verdict: $verdict" >&2; exit 1; }
jq -e . "$verdict" >/dev/null 2>&1 || { echo "verdict is not valid JSON: $verdict" >&2; exit 2; }
[ -n "$out" ] || out="$(dirname "$verdict")/qa-report.md"

bf="$(mktemp)"; ef="$(mktemp)"; pf="$(mktemp)"; vf="$(mktemp)"; rf="$(mktemp)"; sf="$(mktemp)"
trap 'rm -f "$bf" "$ef" "$pf" "$vf" "$rf" "$sf"' EXIT

# A slurpable blank-scan file (empty object when absent/invalid → no warnings).
if [ -n "$blank_scan" ] && jq -e . "$blank_scan" >/dev/null 2>&1; then
  cp "$blank_scan" "$bf"
else
  echo '{}' > "$bf"
fi

if [ -n "$edge_scan" ] && jq -e . "$edge_scan" >/dev/null 2>&1; then
  cp "$edge_scan" "$ef"
else
  echo '{}' > "$ef"
fi

# A slurpable pacing-scan file (empty object when absent/invalid → no warnings).
if [ -n "$pacing_scan" ] && jq -e . "$pacing_scan" >/dev/null 2>&1; then
  cp "$pacing_scan" "$pf"
else
  echo '{}' > "$pf"
fi

# A slurpable rrweb-pacing-scan file (empty object when absent/invalid → none).
if [ -n "$rrweb_scan" ] && jq -e . "$rrweb_scan" >/dev/null 2>&1; then
  cp "$rrweb_scan" "$rf"
else
  echo '{}' > "$rf"
fi

# A slurpable rrweb-scroll-scan file (empty object when absent/invalid → none).
if [ -n "$scroll_scan" ] && jq -e . "$scroll_scan" >/dev/null 2>&1; then
  cp "$scroll_scan" "$sf"
else
  echo '{}' > "$sf"
fi

# Snapshot the verdict ONCE. The rendered "Gate:" line and the process exit code
# below both read THIS snapshot (not the live file), so a verdict rewritten by a
# concurrent qa run can never make the report and the exit code disagree.
cp "$verdict" "$vf"

# --- Gate decision (SINGLE source of truth) --------------------------------
# Computed once here, then used for BOTH the rendered gate line and the exit
# code — they can no longer diverge (the historical bug: the render omitted the
# strict adversary-incomplete block, so a report could read "PASS" while the
# script exited 1).
blockers="$(jq --argjson strict "$strict" '
  [ .scenarios[]
    | select( if $strict==1 then (.status!="pass")
              else (.status!="pass" and (.required!=false)) end ) ]
  | length' "$vf")"
vis_block="$(jq '[ .visual_issues[]? ] | length' "$vf")"
ann_block="$(jq '[ .annotation_issues[]? ] | length' "$vf")"
blank_n="$(jq '(.flagged // []) | length' "$bf")"
edge_n="$(jq '(.flagged // []) | length' "$ef")"
pacing_n="$(jq '(.flagged // []) | length' "$pf")"
# rrweb scan flags are nested per-clip; total rushed reveals across all clips.
rrweb_n="$(jq '[ .clips[]?.flagged[]? ] | length' "$rf")"
# scroll scan flag is per-clip boolean; count clips flagged unfollowable.
scroll_n="$(jq '[ .clips[]? | select(.flagged==true) ] | length' "$sf")"
blank_block=0;  [ "$blank_n"  -gt 0 ] && [ "$blank_strict"  -eq 1 ] && blank_block=1
edge_block=0;   [ "$edge_n"   -gt 0 ] && edge_block=1
pacing_block=0; [ "$pacing_n" -gt 0 ] && [ "$pacing_strict" -eq 1 ] && pacing_block=1
rrweb_block=0;  [ "$rrweb_n"  -gt 0 ] && [ "$rrweb_strict"  -eq 1 ] && rrweb_block=1
scroll_block=0; [ "$scroll_n" -gt 0 ] && [ "$scroll_strict" -eq 1 ] && scroll_block=1
adv_block=0
if [ "$strict" -eq 1 ]; then
  adv_status="$(jq -r '.adversary.status // "absent"' "$vf")"
  if [ "$adv_status" != "ok" ] && [ "$adv_status" != "absent" ]; then adv_block=1; fi
fi
gate_pass=1
for n in "$blockers" "$vis_block" "$ann_block" "$blank_block" "$edge_block" "$pacing_block" "$rrweb_block" "$scroll_block" "$adv_block"; do
  [ "$n" -eq 0 ] || gate_pass=0
done

# --- Markdown report -------------------------------------------------------
jq -r --argjson strict "$strict" --argjson blank_strict "$blank_strict" \
      --argjson pacing_strict "$pacing_strict" --argjson rrweb_strict "$rrweb_strict" \
      --argjson scroll_strict "$scroll_strict" \
      --argjson gate_pass "$gate_pass" --argjson adv_block "$adv_block" \
      --slurpfile blank "$bf" --slurpfile edge "$ef" --slurpfile pacing "$pf" \
      --slurpfile rrweb "$rf" --slurpfile scroll "$sf" '
  def icon(s): if s=="pass" then "✅" elif s=="fail" then "❌" else "⚠️" end;
  def gated(sc): if $strict==1 then (sc.status=="pass")
                 else (sc.status=="pass" or (sc.required==false)) end;
  ( [ .scenarios[] | select(gated(.)|not) ] ) as $blockers |
  ( [ .visual_issues[]? ] ) as $vis |
  ( [ .annotation_issues[]? ] ) as $ann |
  ( ($blank[0].flagged // []) ) as $bl |
  ( ($edge[0].flagged // []) ) as $ed |
  ( ($blank_strict==1) and (($bl|length) > 0) ) as $blank_block |
  ( ($pacing[0].flagged // []) ) as $pc |
  ( ($pacing_strict==1) and (($pc|length) > 0) ) as $pacing_block |
  ( [ ($rrweb[0].clips // [])[] as $c | ($c.flagged // [])[] | . + { clip: ($c.clip // "?") } ] ) as $rw |
  ( ($rrweb_strict==1) and (($rw|length) > 0) ) as $rrweb_block |
  ( [ ($scroll[0].clips // [])[] | select(.flagged==true) ] ) as $sw |
  ( ($scroll_strict==1) and (($sw|length) > 0) ) as $scroll_block |
  # The gate decision is computed ONCE in bash and injected here so the rendered
  # line can never disagree with the process exit code.
  ( $gate_pass==1 ) as $pass |
  "# UI demo QA report",
  "",
  ( if $pass then "**Gate: ✅ PASS**\([ (if ($bl|length)>0 then "\($bl|length) advisory blank-scan warning(s)" else empty end), (if ($pc|length)>0 then "\($pc|length) advisory pacing warning(s)" else empty end) ] | if length>0 then " — " + join(", ") else "" end)"
    else "**Gate: ❌ FAIL** — \(($blockers|length)) blocking scenario(s), \(($vis|length)) visual issue(s), \(($ann|length)) annotation issue(s)\(if ($ed|length)>0 then ", \($ed|length) edge-clipping issue(s)" else "" end)\(if $adv_block==1 then ", adversarial verification incomplete" else "" end)\(if $blank_block then ", \($bl|length) blank-scan flag(s)" else "" end)\(if $pacing_block then ", \($pc|length) pacing flag(s)" else "" end)\(if $rrweb_block then ", \($rw|length) rrweb-pacing flag(s)" else "" end)\(if $scroll_block then ", \($sw|length) scroll-followability flag(s)" else "" end)" end ),
  "",
  "| metric | n |",
  "|---|---|",
  "| scenarios | \(.summary.scenarios_total // (.scenarios|length)) |",
  "| passed | \(.summary.passed // ([.scenarios[]|select(.status=="pass")]|length)) |",
  "| failed | \(.summary.failed // ([.scenarios[]|select(.status=="fail")]|length)) |",
  "| unsupported | \(.summary.unsupported // ([.scenarios[]|select(.status=="unsupported")]|length)) |",
  "| visual issues | \($vis|length) |",
  "| annotation issues | \($ann|length) |",
  "| blank-scan warnings | \($bl|length)\(if $blank_strict==1 then " (blocking)" else " (advisory)" end) |",
  "| edge-clipping issues | \($ed|length) (blocking) |",
  "| pacing warnings | \($pc|length)\(if $pacing_strict==1 then " (blocking)" else " (advisory)" end) |",
  "| rrweb-pacing warnings | \($rw|length)\(if $rrweb_strict==1 then " (blocking)" else " (advisory)" end) |",
  "| scroll-followability warnings | \($sw|length)\(if $scroll_strict==1 then " (blocking)" else " (advisory)" end) |",
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
  ( if ($ann|length) > 0 then
      ( "## ❌ Annotation issues (mixed narration styles)",
        "",
        "| frame | styles seen | issue |",
        "|---|---|---|",
        ( $ann[] | "| `\(.frame // "?")` | \((.styles_seen // [])|join(", ")) | \(.issue // "") |" ),
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
  ( if ($ed|length) > 0 then
      ( "## ❌ Edge-clipping issues (deterministic frame-edge scan)",
        "",
        "| frame | issue |",
        "|---|---|",
        ( $ed[] | "| `\(.frame // "?")` | \(.issue // "") |" ),
        "" )
    else empty end ),
  ( if ($pc|length) > 0 then
      ( "## \(if $pacing_strict==1 then "❌" else "⚠️" end) Pacing warnings (deterministic chapter-duration scan\(if $pacing_strict==1 then "" else " — advisory" end))",
        "",
        "Chapters that flash by below the readable-window floor (total narrated span \($pacing[0].total_ms // 0)ms, median \($pacing[0].median_ms // 0)ms). A demo recorded at fast-validation pace (WEB_CHAT_PACE=0) collapses every dwell — re-record at watch speed.",
        "",
        "| chapter | on screen | issue |",
        "|---|---|---|",
        ( $pc[] | "| `\(.id // "?")` | \(.window_ms // 0)ms | \(.issue // "") |" ),
        "" )
    else empty end ),
  ( if ($rw|length) > 0 then
      ( "## \(if $rrweb_strict==1 then "❌" else "⚠️" end) rrweb-pacing warnings (deterministic embedded-tour timeline scan\(if $rrweb_strict==1 then "" else " — advisory" end))",
        "",
        "Content reveals inside an embedded rrweb tour that flash by below the readable dwell — the frame sampler and vision review cannot see this (each end frame looks correct); only the event timeline does. A burst in the final seconds is the rushed-last-messages defect — give the capture an end-of-conversation dwell or re-pace the clip.",
        "",
        "| clip | at | dwell | issue |",
        "|---|---|---|---|",
        ( $rw[] | "| `\((.clip|split("/")|last))` | \((.atMs/1000)|.*10|floor|./10)s\(if .inTail then " (tail)" else "" end) | \(.dwellMs)ms | \(.issue // "") |" ),
        "" )
    else empty end ),
  ( if ($sw|length) > 0 then
      ( "## \(if $scroll_strict==1 then "❌" else "⚠️" end) Scroll-followability warnings (deterministic rrweb scroll-stream scan\(if $scroll_strict==1 then "" else " — advisory" end))",
        "",
        "Embedded conversation clip(s) whose transcript SNAPS to the bottom on every message instead of easing through it — user inputs and the tops of long replies flash off-camera. The time-only rrweb-pacing scan and the frame sampler are both blind to this (it lives in the scroll-event stream). Re-render the clip with the conversation reveal track (revealTurn / `slidey rrweb-reveal`).",
        "",
        "| clip | snap jumps | eased runs | issue |",
        "|---|---|---|---|",
        ( $sw[] | "| `\((.clip|split("/")|last))` | \(.snap_runs) | \(.eased_runs) | \(.issue // "") |" ),
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
' "$vf" > "$out"

# --- Gate exit code -------------------------------------------------------
# Uses the SAME flags computed once above — no recomputation, no second read of
# the verdict, so the exit code always matches the rendered "Gate:" line.
#
# A blank/broken render where visual content is expected, and mixed narration
# styles, are real defects: any reported visual_issue / annotation_issue blocks
# at every effort level. Blank/pacing scan flags are advisory unless their
# --*-strict flag is set. Under --strict an adversarial pass that was supposed
# to run but did not complete (adversary.status present and != "ok") also blocks.
[ "$vis_block"  -gt 0 ] && echo "gate: $vis_block visual issue(s) — blank/broken render where content was expected" >&2
[ "$ann_block"  -gt 0 ] && echo "gate: $ann_block annotation issue(s) — mixed narration styles within one video" >&2
[ "$edge_n"     -gt 0 ] && echo "gate: $edge_n edge-clipping issue(s) — content touches the rendered frame edge" >&2
if [ "$blank_n" -gt 0 ]; then
  [ "$blank_strict" -eq 1 ] \
    && echo "gate: $blank_n blank-scan flag(s) blocking (--blank-strict)" >&2 \
    || echo "advisory: $blank_n blank-scan flag(s) — large monochrome region(s), review by eye" >&2
fi
if [ "$pacing_n" -gt 0 ]; then
  [ "$pacing_strict" -eq 1 ] \
    && echo "gate: $pacing_n pacing flag(s) blocking (--pacing-strict) — popover(s) too fast to read" >&2 \
    || echo "advisory: $pacing_n pacing flag(s) — narrated moment(s) flash by, review pacing" >&2
fi
if [ "$rrweb_n" -gt 0 ]; then
  [ "$rrweb_strict" -eq 1 ] \
    && echo "gate: $rrweb_n rrweb-pacing flag(s) blocking (--rrweb-strict) — embedded tour content too fast to read" >&2 \
    || echo "advisory: $rrweb_n rrweb-pacing flag(s) — embedded tour reveal(s) flash by, review pacing" >&2
fi
if [ "$scroll_n" -gt 0 ]; then
  [ "$scroll_strict" -eq 1 ] \
    && echo "gate: $scroll_n scroll-followability flag(s) blocking (--scroll-strict) — conversation snaps to bottom, unfollowable" >&2 \
    || echo "advisory: $scroll_n scroll-followability flag(s) — conversation clip(s) snap to bottom, re-render with the reveal track" >&2
fi
[ "$adv_block" -eq 1 ] && echo "strict gate: adversarial verification did not complete (adversary.status=${adv_status:-absent})" >&2

echo "wrote $out  (blocking scenarios: $blockers, visual issues: $vis_block, annotation issues: $ann_block, edge-clipping: $edge_n blocking, blank-scan: $blank_n$([ "$blank_strict" -eq 1 ] && echo ' blocking' || echo ' advisory'), pacing: $pacing_n$([ "$pacing_strict" -eq 1 ] && echo ' blocking' || echo ' advisory'), rrweb-pacing: $rrweb_n$([ "$rrweb_strict" -eq 1 ] && echo ' blocking' || echo ' advisory'), scroll-followability: $scroll_n$([ "$scroll_strict" -eq 1 ] && echo ' blocking' || echo ' advisory'))"
[ "$gate_pass" -eq 1 ] || exit 1
