#!/usr/bin/env bash
#
# Optionally render the live @kitsoki GitHub-agent Slidey source deck to an MP4
# export. The .slidey.json deck is the primary review artifact; this script is
# only for explicit video QA/share requests.

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

DECK=".artifacts/github-agent-live/live-github-agent.slidey.json"
OUT=".artifacts/github-agent-live/live-github-agent.mp4"
SLIDEY_HOME="${SLIDEY_HOME:-/Users/brad/code/slidey}"
SLIDEY_CMD="${KITSOKI_SLIDEY_CMD:-}"

usage() {
	cat <<EOF
usage: scripts/render-gh-agent-live-deck-video.sh [options]

Options:
  --deck <deck.slidey.json> default $DECK
  --out <deck.mp4>         default $OUT
  --slidey-home <dir>      default \$SLIDEY_HOME or /Users/brad/code/slidey
  --slidey-cmd <path>      executable slidey-compatible command (tests)
  -h, --help               show this help

The command runs:
  slidey <deck.slidey.json> --validate
  slidey <deck.slidey.json> <deck.mp4>

The source .slidey.json deck is the primary output. The MP4 and chapter sidecar
are optional rendered exports for video QA/share workflows.
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
node - "$DECK" <<'NODE'
const fs = require("fs");
const path = require("path");
const { execFileSync } = require("child_process");

const deckPath = process.argv[2];
const deck = JSON.parse(fs.readFileSync(deckPath, "utf8"));
const deckDir = path.dirname(path.resolve(deckPath));
const fps = Number.isFinite(deck.fps) && deck.fps > 0 ? deck.fps : 30;

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
  if (scene.type === "video") {
    const sourceDuration = mediaDurationMs(scene);
    const startMs = Number.isFinite(scene.start) && scene.start > 0 ? Math.round(scene.start * 1000) : 0;
    const endMs = Number.isFinite(scene.end) && scene.end > 0 ? Math.round(scene.end * 1000) : sourceDuration;
    const speed = Number.isFinite(scene.speed) && scene.speed > 0 ? scene.speed : 1;
    return Math.max(1, Math.round(Math.max(0, Math.min(endMs, sourceDuration) - startMs) / speed));
  }
  if (scene.type === "cta") return 8000;
  return 3000;
}

function fmt(ms) {
  const seconds = Math.max(0, Math.round(ms / 1000));
  const m = Math.floor(seconds / 60);
  const s = seconds % 60;
  return m > 0 ? `${m}m ${String(s).padStart(2, "0")}s` : `${s}s`;
}

const scenes = Array.isArray(deck.scenes) ? deck.scenes : [];
let videoMs = 0;
let rrwebMs = 0;
let rrwebScenes = 0;
let rrwebFrames = 0;
for (const scene of scenes) {
  const duration = Math.max(1000, sceneDurationMs(scene));
  videoMs += duration;
  if (scene && scene.type === "video" && scene.rrweb) {
    rrwebScenes += 1;
    rrwebMs += duration;
    rrwebFrames += Math.max(1, Math.round((duration / 1000) * fps));
  }
}
const frames = Math.max(1, Math.round((videoMs / 1000) * fps));

// Empirical, intentionally conservative. Most scenes are cheap; rrweb scenes pay
// a browser seek+screenshot cost per rrweb frame before the final ffmpeg pass.
// On this path the main deck frame count can sit still while a nested
// slidey-rrweb-ras-* directory is being populated, so the upper bound is wide.
const cheapFrames = Math.max(0, frames - rrwebFrames);
const lowMs = 20000 + cheapFrames * 8 + rrwebFrames * 150 + rrwebScenes * 2000;
const highMs = 45000 + cheapFrames * 16 + rrwebFrames * 650 + rrwebScenes * 7000;
console.log(`[kitsoki] Render estimate: ${scenes.length} scenes, ${fmt(videoMs)} video, ~${frames} frames @ ${fps}fps.`);
console.log(`[kitsoki] rrweb work: ${rrwebScenes} scenes, ${fmt(rrwebMs)} rrweb, ~${rrwebFrames} raster frames.`);
console.log(`[kitsoki] Expected wall time: about ${fmt(lowMs)}-${fmt(highMs)} on this local render path.`);
console.log(`[kitsoki] Note: during rrweb rasterization, main deck frames may pause while slidey-rrweb-ras-* temp frames advance; investigate only if both stop changing for several minutes.`);
NODE
"${SLIDEY[@]}" "$DECK" "$OUT"

if [ ! -s "$OUT" ]; then
	echo "Slidey render did not create a non-empty MP4 file: $OUT" >&2
	exit 1
fi

CHAPTERS="$OUT.chapters.json"
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
  if (scene.type === "video") {
    const sourceDuration = mediaDurationMs(scene);
    const startMs = Number.isFinite(scene.start) && scene.start > 0 ? Math.round(scene.start * 1000) : 0;
    const endMs = Number.isFinite(scene.end) && scene.end > 0 ? Math.round(scene.end * 1000) : sourceDuration;
    const speed = Number.isFinite(scene.speed) && scene.speed > 0 ? scene.speed : 1;
    return Math.max(1, Math.round(Math.max(0, Math.min(endMs, sourceDuration) - startMs) / speed));
  }
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
