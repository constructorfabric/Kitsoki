#!/usr/bin/env bash
#
# Render the live @kitsoki GitHub-agent Slidey deck JSON to an MP4 suitable for
# the gated kitsoki-ui-qa pass.

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

DECK=".artifacts/github-agent-live/live-github-agent.deck.json"
OUT=".artifacts/github-agent-live/live-github-agent.mp4"
SLIDEY_HOME="${SLIDEY_HOME:-/Users/brad/code/slidey}"
SLIDEY_CMD="${KITSOKI_SLIDEY_CMD:-}"

usage() {
	cat <<EOF
usage: scripts/render-gh-agent-live-deck-video.sh [options]

Options:
  --deck <deck.json>       default $DECK
  --out <deck.mp4>         default $OUT
  --slidey-home <dir>      default \$SLIDEY_HOME or /Users/brad/code/slidey
  --slidey-cmd <path>      executable slidey-compatible command (tests)
  -h, --help               show this help

The command runs:
  slidey <deck.json> --validate
  slidey <deck.json> <deck.mp4>

It also requires Slidey's <deck.mp4>.chapters.json sidecar for the final QA and
proof verifier.
EOF
}

while [ "$#" -gt 0 ]; do
	case "$1" in
		--deck)
			DECK="${2:-}"
			shift 2
			;;
		--out)
			OUT="${2:-}"
			shift 2
			;;
		--slidey-home)
			SLIDEY_HOME="${2:-}"
			shift 2
			;;
		--slidey-cmd)
			SLIDEY_CMD="${2:-}"
			shift 2
			;;
		-h|--help)
			usage
			exit 0
			;;
		*)
			echo "unknown argument: $1" >&2
			usage >&2
			exit 2
			;;
	esac
done

if [ -z "$DECK" ]; then
	echo "--deck must not be empty" >&2
	exit 2
fi
if [ -z "$OUT" ]; then
	echo "--out must not be empty" >&2
	exit 2
fi
if [ ! -f "$DECK" ]; then
	echo "deck not found: $DECK" >&2
	exit 1
fi

if [ -n "$SLIDEY_CMD" ]; then
	if [ ! -x "$SLIDEY_CMD" ]; then
		echo "slidey command is not executable: $SLIDEY_CMD" >&2
		exit 1
	fi
	SLIDEY=("$SLIDEY_CMD")
elif [ -f "$SLIDEY_HOME/src/index.js" ]; then
	SLIDEY=(node "$SLIDEY_HOME/src/index.js")
elif command -v slidey >/dev/null 2>&1; then
	SLIDEY=(slidey)
else
	echo "could not find Slidey. Set SLIDEY_HOME or KITSOKI_SLIDEY_CMD." >&2
	exit 1
fi

"${SLIDEY[@]}" "$DECK" --validate
mkdir -p "$(dirname "$OUT")"
"${SLIDEY[@]}" "$DECK" "$OUT"

if [ ! -s "$OUT" ]; then
	echo "Slidey render did not create a non-empty MP4 file: $OUT" >&2
	exit 1
fi

CHAPTERS="$OUT.chapters.json"
if [ ! -s "$CHAPTERS" ]; then
	node - "$DECK" "$CHAPTERS" <<'NODE'
const fs = require("fs");
const path = require("path");
const { execFileSync } = require("child_process");

const deckPath = process.argv[2];
const outPath = process.argv[3];
const deck = JSON.parse(fs.readFileSync(deckPath, "utf8"));
const deckDir = path.dirname(path.resolve(deckPath));

function mediaDurationMs(scene) {
  const rel = scene.src || scene.rrweb || "";
  if (!rel) return 3000;
  const file = path.resolve(deckDir, rel);
  if (scene.rrweb) {
    try {
      const rrweb = JSON.parse(fs.readFileSync(file, "utf8"));
      if (Number.isFinite(rrweb.durationMs) && rrweb.durationMs > 0) return Math.round(rrweb.durationMs);
      if (Number.isFinite(rrweb.startTime) && Number.isFinite(rrweb.endTime) && rrweb.endTime > rrweb.startTime) {
        return Math.round(rrweb.endTime - rrweb.startTime);
      }
    } catch {}
    return 10000;
  }
  try {
    const raw = execFileSync("ffprobe", [
      "-v", "error",
      "-show_entries", "format=duration",
      "-of", "default=noprint_wrappers=1:nokey=1",
      file,
    ], { encoding: "utf8" }).trim();
    const seconds = Number.parseFloat(raw);
    if (Number.isFinite(seconds) && seconds > 0) return Math.round(seconds * 1000);
  } catch {}
  return 10000;
}

function sceneDurationMs(scene) {
  if (Number.isFinite(scene.duration) && scene.duration > 0) return Math.round(scene.duration * 1000);
  if (scene.type === "video") return mediaDurationMs(scene);
  if (scene.type === "cta") return 8000;
  return 3000;
}

const scenes = Array.isArray(deck.scenes) ? deck.scenes : [];
let cursor = 0;
const chapters = scenes.map((scene, index) => {
  const duration = Math.max(1000, sceneDurationMs(scene));
  const id = scene.id || `${scene.type || "scene"}-${index + 1}`;
  const label = scene.title || scene.eyebrow || id;
  const chapter = {
    index,
    id,
    label,
    start_ms: cursor,
    end_ms: cursor + duration,
    source_ref: {
      kind: "slidey",
      spec_path: path.relative(process.cwd(), path.resolve(deckPath)),
      step_id: id,
    },
  };
  cursor += duration;
  return chapter;
});

fs.writeFileSync(outPath, `${JSON.stringify(chapters, null, 2)}\n`);
NODE
fi
if [ ! -s "$CHAPTERS" ]; then
	echo "Slidey render did not create a non-empty chapter sidecar: $CHAPTERS" >&2
	exit 1
fi
node - "$CHAPTERS" <<'NODE'
const fs = require("fs");
const file = process.argv[2];
let parsed;
try {
  parsed = JSON.parse(fs.readFileSync(file, "utf8"));
} catch (err) {
  console.error(`chapter sidecar is not valid JSON: ${file}: ${err.message}`);
  process.exit(1);
}
if (!Array.isArray(parsed) || parsed.length < 8) {
  console.error(`chapter sidecar should contain at least 8 chapters: ${file}`);
  process.exit(1);
}
NODE

echo "wrote $OUT"
