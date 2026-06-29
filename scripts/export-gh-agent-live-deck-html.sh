#!/usr/bin/env bash
#
# Export the live @kitsoki GitHub-agent Slidey deck JSON to a self-contained
# HTML deck via Slidey's bundle command.

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

DECK=".artifacts/github-agent-live/live-github-agent.slidey.json"
OUT=".artifacts/github-agent-live/live-github-agent.html"
SLIDEY_HOME="${SLIDEY_HOME:-/Users/brad/code/slidey}"
SLIDEY_CMD="${KITSOKI_SLIDEY_CMD:-}"

usage() {
	cat <<EOF
usage: scripts/export-gh-agent-live-deck-html.sh [options]

Options:
  --deck <deck.slidey.json> default $DECK
  --out <deck.html>        default $OUT
  --slidey-home <dir>      default \$SLIDEY_HOME or /Users/brad/code/slidey
  --slidey-cmd <path>      executable slidey-compatible command (tests)
  -h, --help               show this help

The command runs:
  slidey <deck.slidey.json> --validate
  slidey bundle <deck.slidey.json> <deck.html>

When --slidey-home is used, the command is:
  node <slidey-home>/src/index.js ...
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
"${SLIDEY[@]}" bundle "$DECK" "$OUT"

if [ ! -s "$OUT" ]; then
	echo "Slidey bundle did not create a non-empty HTML file: $OUT" >&2
	exit 1
fi
if ! grep -Eiq '<!doctype|<html' "$OUT"; then
	echo "Slidey bundle output does not look like HTML: $OUT" >&2
	exit 1
fi

echo "wrote $OUT"
