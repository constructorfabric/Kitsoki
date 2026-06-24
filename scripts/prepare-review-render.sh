#!/usr/bin/env bash
#
# prepare-review-render.sh — create the deterministic walkthrough MP4 consumed
# by the mockup-video and /review demos.
#
# The story flows intentionally exercise the real artifacts_dir and /review
# resolver path, but their host.slidey.render calls are stubbed to this repo-local
# artifact. Keep the artifact generated, not committed, so GitHub Pages CI does
# not depend on an external Slidey checkout.

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
OUT="$ROOT/.artifacts/review-video/render/walkthrough.mp4"
CHAPTERS="$OUT.chapters.json"

if [ "${1:-}" != "--force" ] && [ -f "$OUT" ] && [ -f "$CHAPTERS" ]; then
	echo "prepare-review-render: fresh $OUT"
	exit 0
fi

command -v ffmpeg >/dev/null 2>&1 || {
	echo "prepare-review-render: ffmpeg is required" >&2
	exit 2
}

mkdir -p "$(dirname "$OUT")"

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

make_segment() {
	local out="$1"
	local color="$2"
	local src="$3"
	ffmpeg -y -loglevel error \
		-f lavfi -i "color=c=$color:s=1600x900:d=3:r=30" \
		-f lavfi -i "$src=s=960x540:d=3:r=30" \
		-filter_complex "[1:v]format=yuv420p[fg];[0:v][fg]overlay=(W-w)/2:(H-h)/2,format=yuv420p" \
		-c:v libx264 -preset veryfast -crf 20 -an "$out"
}

make_segment "$tmp/intro.mp4" "0x07111f" "testsrc2"
make_segment "$tmp/run-view.mp4" "0x102015" "smptebars"
make_segment "$tmp/feedback.mp4" "0x201018" "testsrc2"

printf "file '%s'\nfile '%s'\nfile '%s'\n" "$tmp/intro.mp4" "$tmp/run-view.mp4" "$tmp/feedback.mp4" >"$tmp/list.txt"
ffmpeg -y -loglevel error -f concat -safe 0 -i "$tmp/list.txt" -c copy -movflags +faststart "$OUT"

cat >"$CHAPTERS" <<'JSON'
[
  {
    "index": 0,
    "id": "intro",
    "label": "Story anatomy",
    "start_ms": 0,
    "end_ms": 3000,
    "source_ref": {
      "kind": "slidey",
      "spec_path": "docs/decks/arch-and-usage.json",
      "scene_id": "intro"
    }
  },
  {
    "index": 1,
    "id": "run_view",
    "label": "Run view",
    "start_ms": 3000,
    "end_ms": 6000,
    "source_ref": {
      "kind": "slidey",
      "spec_path": "docs/decks/arch-and-usage.json",
      "scene_id": "run_view"
    }
  },
  {
    "index": 2,
    "id": "feedback",
    "label": "Feedback loop",
    "start_ms": 6000,
    "end_ms": 9000,
    "source_ref": {
      "kind": "slidey",
      "spec_path": "docs/decks/arch-and-usage.json",
      "scene_id": "feedback"
    }
  }
]
JSON

echo "prepare-review-render: wrote $OUT and $CHAPTERS"
