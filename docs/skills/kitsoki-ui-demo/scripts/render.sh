#!/usr/bin/env bash
# One-shot post-production: from a recorded demo .webm, produce the shareable
# MP4 + GIF and a contact sheet of the sibling NN-*.png scene screenshots.
#
# This does NOT run Playwright — record first (see SKILL.md), then point this at
# the resulting *-demo.webm.
#
# Usage: render.sh <demo.webm> [--gif-width W] [--no-gif]
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
webm="${1:?usage: render.sh <demo.webm> [--gif-width W] [--no-gif]}"
shift || true

gif_width=900
make_gif=1
while [ $# -gt 0 ]; do
  case "$1" in
    --gif-width) gif_width="$2"; shift 2 ;;
    --no-gif)    make_gif=0; shift ;;
    *)           echo "unknown arg: $1" >&2; exit 1 ;;
  esac
done

[ -f "$webm" ] || { echo "no such file: $webm" >&2; exit 1; }
dir="$(dirname "$webm")"

echo "▸ MP4"
"$here/webm-to-mp4.sh" "$webm"
if [ "$make_gif" -eq 1 ]; then
  echo "▸ GIF"
  "$here/webm-to-gif.sh" "$webm" --width "$gif_width"
fi
echo "▸ contact sheet"
"$here/contact-sheet.sh" "$dir" || echo "  (skipped — no NN-*.png screenshots)"

echo "done → $dir"
