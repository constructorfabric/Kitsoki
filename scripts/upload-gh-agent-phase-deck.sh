#!/usr/bin/env bash
#
# Bundle a per-phase Slidey deck and upload the self-contained HTML to the
# hosted GitHub-agent service so it is served at
# <public-base-url>/run/<job-id>/assets/deck.html.
#
# The upload uses the service's PUT asset API
# (PUT <public-base-url>/api/run/<job-id>/assets/deck.html). The server writes
# both the on-disk blob and the gh_job_assets metadata row in one code path,
# which is exactly what the GET /run/<job-id>/assets/<name> handler reads back.
# Uploading the file by other means (e.g. scp) is unsupported: the GET handler
# requires the DB row, so a bare file copy would 404.
#
# Without --dry-run the script validates, bundles, and PUTs the HTML. With
# --dry-run it prints what it would do.

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

DECK=""
JOB_ID=""
ASSET_NAME="deck.html"
PUBLIC_BASE_URL="${KITSOKI_GH_AGENT_PUBLIC_BASE_URL:-}"
SLIDEY_HOME="${SLIDEY_HOME:-/Users/brad/code/slidey}"
SLIDEY_CMD="${KITSOKI_SLIDEY_CMD:-}"
DRY_RUN=0

usage() {
	cat <<EOF
usage: scripts/upload-gh-agent-phase-deck.sh [options]

Options:
  --deck <deck.slidey.json>   path to the Slidey JSON deck (required)
  --job-id <id>               job ID for the asset path (required)
  --asset-name <name>         served asset filename (default $ASSET_NAME)
  --public-base-url <url>     default \$KITSOKI_GH_AGENT_PUBLIC_BASE_URL
  --dry-run                   print commands without executing
  -h, --help                  show this help

The command runs:
  slidey <deck.slidey.json> --validate
  slidey bundle <deck.slidey.json> <deck.html>
  curl -fsS -X PUT --data-binary @<deck.html> \\
    -H 'Content-Type: text/html' \\
    <public-base-url>/api/run/<job-id>/assets/<asset-name>

The deck is then served at:
  <public-base-url>/run/<job-id>/assets/<asset-name>
EOF
}

while [ "$#" -gt 0 ]; do
	case "$1" in
		--deck)
			DECK="${2:-}"
			shift 2
			;;
		--job-id)
			JOB_ID="${2:-}"
			shift 2
			;;
		--asset-name)
			ASSET_NAME="${2:-}"
			shift 2
			;;
		--public-base-url)
			PUBLIC_BASE_URL="${2:-}"
			shift 2
			;;
		--dry-run)
			DRY_RUN=1
			shift
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
	echo "--deck is required" >&2
	exit 2
fi
if [ -z "$JOB_ID" ]; then
	echo "--job-id is required" >&2
	exit 2
fi
if [ -z "$PUBLIC_BASE_URL" ]; then
	echo "--public-base-url or KITSOKI_GH_AGENT_PUBLIC_BASE_URL is required" >&2
	exit 2
fi
if [ ! -f "$DECK" ]; then
	echo "deck not found: $DECK" >&2
	exit 1
fi

# Locate Slidey — same logic as export-gh-agent-live-deck-html.sh.
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

DECK_HTML="${DECK%.slidey.json}.html"
UPLOAD_URL="${PUBLIC_BASE_URL%/}/api/run/$JOB_ID/assets/$ASSET_NAME"
SERVE_URL="${PUBLIC_BASE_URL%/}/run/$JOB_ID/assets/$ASSET_NAME"

print_cmd() {
	printf '%q ' "$@"
	printf '\n'
}

# Validate
"${SLIDEY[@]}" "$DECK" --validate

# Bundle
"${SLIDEY[@]}" bundle "$DECK" "$DECK_HTML"

if [ ! -s "$DECK_HTML" ]; then
	echo "Slidey bundle did not create a non-empty HTML file: $DECK_HTML" >&2
	exit 1
fi

if [ "$DRY_RUN" -eq 1 ]; then
	print_cmd curl -fsS -X PUT --data-binary "@$DECK_HTML" \
		-H "Content-Type: text/html" "$UPLOAD_URL"
else
	curl -fsS -X PUT --data-binary "@$DECK_HTML" \
		-H "Content-Type: text/html" "$UPLOAD_URL"
fi

echo "$SERVE_URL"
