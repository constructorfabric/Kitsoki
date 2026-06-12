#!/usr/bin/env bash
# One-shot UI-demo QA — the inverse of the kitsoki-ui-demo recording pipeline.
#
#   extract frames → contact sheet → grounded vision review → gated report
#
# Reliability comes from a deterministic frame set, an evidence-cited verdict,
# and an adversarial downgrade-only pass (see SKILL.md). Exit code is the gate:
# 0 = pass, 1 = a blocking scenario failed, 2 = pipeline error.
#
# Usage: qa.sh <video> --feature <file> --scenarios <file>
#          [--frames <dir>] [--out <dir>] [--model M]
#          [--max-frames N] [--no-adversary] [--strict]
#
#   --frames <dir>  use existing labeled frames (e.g. the kitsoki-ui-demo skill's
#                   NN-<scene>.png) as ground truth instead of extracting. Highest
#                   fidelity when available.
#   --out <dir>     artifact dir (default .artifacts/ui-qa/<video-stem>)
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
demo_scripts="$here/../../kitsoki-ui-demo/scripts"   # reuse the recorder's contact sheet

video="${1:?usage: qa.sh <video> --feature <f> --scenarios <f> [opts]}"
shift || true

feature="" scenarios="" frames="" outdir="" model="" max=48
adv_flag="" strict_flag="" blank_strict_flag=""
while [ $# -gt 0 ]; do
  case "$1" in
    --feature)     feature="$2"; shift 2 ;;
    --scenarios)   scenarios="$2"; shift 2 ;;
    --frames)      frames="$2"; shift 2 ;;
    --out)         outdir="$2"; shift 2 ;;
    --model)       model="$2"; shift 2 ;;
    --max-frames)  max="$2"; shift 2 ;;
    --no-adversary) adv_flag="--no-adversary"; shift ;;
    --strict)      strict_flag="--strict"; shift ;;
    --blank-strict) blank_strict_flag="--blank-strict"; shift ;;
    *) echo "unknown arg: $1" >&2; exit 1 ;;
  esac
done

[ -f "$feature" ]   || { echo "--feature file required" >&2; exit 2; }
[ -f "$scenarios" ] || { echo "--scenarios file required" >&2; exit 2; }
if [ -z "$frames" ]; then
  [ -f "$video" ] || { echo "no such video: $video" >&2; exit 2; }
fi

stem="$(basename "${video%.*}")"
[ -n "$outdir" ] || outdir=".artifacts/ui-qa/$stem"
mkdir -p "$outdir"
frames_dir="$outdir/frames"

# 1. Frames — prefer caller-supplied labeled set, else extract deterministically.
if [ -n "$frames" ]; then
  [ -d "$frames" ] || { echo "no such --frames dir: $frames" >&2; exit 2; }
  echo "▸ using labeled frames from $frames"
  frames_dir="$frames"
else
  echo "▸ extracting frames → $frames_dir"
  "$here/extract-frames.sh" "$video" "$frames_dir" --max "$max"
fi

# 2. Contact sheet (best-effort; reuses the recorder's tiler). Non-fatal.
if [ -x "$demo_scripts/contact-sheet.sh" ]; then
  "$demo_scripts/contact-sheet.sh" "$frames_dir" "$outdir/contact-sheet.png" \
    && echo "▸ contact sheet → $outdir/contact-sheet.png" \
    || echo "  (contact sheet skipped)"
fi

# 2b. Deterministic blank/solid-region scan (no LLM). Advisory by default —
#     surfaces frames with a large solid white/black block for human review;
#     --blank-strict promotes them to blocking. Never aborts the run itself.
blank_scan="$outdir/blank-scan.json"
"$here/blank-scan.sh" "$frames_dir" --out "$blank_scan" || true

# 3. Grounded, adversarially-verified vision review → verdict.json
verdict="$outdir/verdict.json"
review_args=( --frames "$frames_dir" --feature "$feature" \
              --scenarios "$scenarios" --out "$verdict" )
[ -n "$model" ]    && review_args+=( --model "$model" )
[ -n "$adv_flag" ] && review_args+=( "$adv_flag" )
"$here/qa-review.sh" "${review_args[@]}"

# 4. Gated report — exit code propagates as the QA gate.
echo
"$here/report.sh" "$verdict" --out "$outdir/qa-report.md" $strict_flag \
  --blank-scan "$blank_scan" $blank_strict_flag
rc=$?
echo
echo "QA artifacts in $outdir/ : verdict.json, qa-report.md, contact-sheet.png, frames/"
exit $rc
