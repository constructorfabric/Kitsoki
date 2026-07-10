#!/usr/bin/env bash
#
# Build a self-contained Slidey HTML viewer for a raw rrweb event log.
# The site stages the resulting HTML; VitePress never needs a Slidey checkout.

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT" || exit 2

if [ "${1:-}" = "" ]; then
	echo "usage: scripts/build-rrweb-viewer.sh <in.rrweb.json> [out.html]" >&2
	exit 2
fi

RRWEB="$1"
OUT="${2:-${RRWEB%.rrweb.json}.html}"

if [ ! -f "$RRWEB" ]; then
	echo "build-rrweb-viewer: missing rrweb log: $RRWEB" >&2
	exit 1
fi

SLIDEY_HOME="${SLIDEY_HOME:-}"
SLIDEY_CMD="${KITSOKI_SLIDEY_CMD:-${SLIDEY_CMD:-}}"
if [ -n "$SLIDEY_CMD" ]; then
	SLIDEY=("$SLIDEY_CMD")
elif [ -n "$SLIDEY_HOME" ] && [ -f "$SLIDEY_HOME/src/index.js" ]; then
	SLIDEY=(node "$SLIDEY_HOME/src/index.js")
elif [ -f "../studio-slidey/src/index.js" ]; then
	SLIDEY=(node "../studio-slidey/src/index.js")
elif [ -f "../slidey/src/index.js" ]; then
	SLIDEY=(node "../slidey/src/index.js")
elif [ -f "$ROOT/../../../../studio-slidey/src/index.js" ]; then
	SLIDEY=(node "$ROOT/../../../../studio-slidey/src/index.js")
elif [ -f "$ROOT/../../../../slidey/src/index.js" ]; then
	SLIDEY=(node "$ROOT/../../../../slidey/src/index.js")
elif command -v slidey >/dev/null 2>&1; then
	SLIDEY=(slidey)
else
	SLIDEY=()
fi

mkdir -p "$(dirname "$OUT")"
if [ "${#SLIDEY[@]}" -gt 0 ]; then
	"${SLIDEY[@]}" bundle "$RRWEB" "$OUT"
else
	node tools/runstatus/scripts/rrweb-viewer-bundle.mjs "$RRWEB" "$OUT"
fi
echo "build-rrweb-viewer: wrote $OUT"
