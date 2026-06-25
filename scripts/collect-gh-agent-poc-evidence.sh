#!/usr/bin/env bash
#
# Collect read-only evidence for a live @kitsoki GitHub-agent POC run.
#
# The script does not create GitHub comments, edit issues, deploy binaries, or
# write to the VM. It writes a markdown evidence note under .context.

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

PUBLIC_BASE_URL="${KITSOKI_GH_AGENT_PUBLIC_BASE_URL:-https://kitsoki-test.slothattax.me}"
REMOTE="${KITSOKI_GH_AGENT_REMOTE:-root@206.189.84.218}"
REMOTE_DB="${KITSOKI_GH_AGENT_REMOTE_DB:-/var/lib/kitsoki-gh-agent/gh-jobs.sqlite}"
OUT_DIR="${KITSOKI_GH_AGENT_EVIDENCE_DIR:-.context}"

case_slug=""
job_id=""
source_url=""
mention_url=""
comment_url=""
notes=""
with_remote_db=0

usage() {
	cat <<'EOF'
usage: scripts/collect-gh-agent-poc-evidence.sh --case <slug> --job-id <id> [options]

Options:
  --source-url <url>    GitHub issue or PR URL used for the run.
  --mention-url <url>   GitHub mention/comment URL that triggered the run.
  --comment-url <url>   Kitsoki rolling-status comment URL, if known.
  --notes <text>        Short operator note to include in the evidence file.
  --remote-db           Include a read-only sqlite row fetched over SSH.
  -h, --help            Show this help.

Environment:
  KITSOKI_GH_AGENT_PUBLIC_BASE_URL  default https://kitsoki-test.slothattax.me
  KITSOKI_GH_AGENT_REMOTE           default root@206.189.84.218
  KITSOKI_GH_AGENT_REMOTE_DB        default /var/lib/kitsoki-gh-agent/gh-jobs.sqlite
  KITSOKI_GH_AGENT_EVIDENCE_DIR     default .context

This script is intentionally read-only with respect to GitHub and the VM. It
does fetch live URLs and, with --remote-db, runs a read-only sqlite SELECT over
SSH.
EOF
}

while [ "$#" -gt 0 ]; do
	case "$1" in
		--case)
			case_slug="${2:-}"
			shift 2
			;;
		--job-id)
			job_id="${2:-}"
			shift 2
			;;
		--source-url)
			source_url="${2:-}"
			shift 2
			;;
		--mention-url)
			mention_url="${2:-}"
			shift 2
			;;
		--comment-url)
			comment_url="${2:-}"
			shift 2
			;;
		--notes)
			notes="${2:-}"
			shift 2
			;;
		--remote-db)
			with_remote_db=1
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

if [ -z "$case_slug" ] || [ -z "$job_id" ]; then
	usage >&2
	exit 2
fi
if [[ ! "$case_slug" =~ ^[a-z0-9][a-z0-9._-]*$ ]]; then
	echo "--case must be a filesystem-safe slug" >&2
	exit 2
fi

api_url="${PUBLIC_BASE_URL%/}/api/run/$job_id"
run_url="${PUBLIC_BASE_URL%/}/run/$job_id"
out="$OUT_DIR/live-poc-$case_slug.md"
tmp_api="$(mktemp)"
tmp_run_headers="$(mktemp)"
tmp_health="$(mktemp)"
tmp_db="$(mktemp)"
trap 'rm -f "$tmp_api" "$tmp_run_headers" "$tmp_health" "$tmp_db"' EXIT

timestamp="$(date -u +"%Y-%m-%dT%H:%M:%SZ")"
head_rev="$(git rev-parse --short HEAD 2>/dev/null || true)"
branch="$(git rev-parse --abbrev-ref HEAD 2>/dev/null || true)"

health_status="not checked"
if curl -fsS "${PUBLIC_BASE_URL%/}/healthz" >"$tmp_health" 2>&1; then
	health_status="$(tr '\n' ' ' <"$tmp_health" | sed 's/[[:space:]]*$//')"
else
	health_status="FAILED: $(tr '\n' ' ' <"$tmp_health" | sed 's/[[:space:]]*$//')"
fi

api_status="not checked"
if curl -fsS "$api_url" >"$tmp_api" 2>&1; then
	api_status="ok"
else
	api_status="FAILED: $(tr '\n' ' ' <"$tmp_api" | sed 's/[[:space:]]*$//')"
fi

run_status="not checked"
if curl -fsSI "$run_url" >"$tmp_run_headers" 2>&1; then
	run_status="$(sed -n '1p' "$tmp_run_headers")"
else
	run_status="FAILED: $(tr '\n' ' ' <"$tmp_run_headers" | sed 's/[[:space:]]*$//')"
fi

db_status="not requested"
if [ "$with_remote_db" -eq 1 ]; then
	if ssh "$REMOTE" "python3 - '$REMOTE_DB' '$job_id'" >"$tmp_db" 2>&1 <<'PY'
import json
import sqlite3
import sys

db_path = sys.argv[1]
job_id = sys.argv[2]
cols = [
    "job_id", "origin_ref", "repo", "object_kind", "object_number",
    "story", "state", "worker_id", "run_id", "run_url", "comment_id",
    "err_msg", "created_at", "updated_at",
]
conn = sqlite3.connect(f"file:{db_path}?mode=ro", uri=True)
conn.row_factory = sqlite3.Row
row = conn.execute(
    "select " + ", ".join(cols) + " from gh_jobs where job_id = ?",
    (job_id,),
).fetchone()
print(json.dumps(dict(row) if row else None, indent=2, sort_keys=True))
PY
	then
		db_status="ok"
	else
		db_status="FAILED"
	fi
fi

mkdir -p "$OUT_DIR"
{
	printf '# Live POC: %s\n\n' "$case_slug"
	printf -- '- Collected: `%s`\n' "$timestamp"
	printf -- '- Local branch/head: `%s` / `%s`\n' "$branch" "$head_rev"
	printf -- '- Public base URL: `%s`\n' "$PUBLIC_BASE_URL"
	printf -- '- Job ID: `%s`\n' "$job_id"
	printf -- '- Source URL: %s\n' "${source_url:-"-"}"
	printf -- '- Mention URL: %s\n' "${mention_url:-"-"}"
	printf -- '- Run URL: %s\n' "$run_url"
	printf -- '- API URL: %s\n' "$api_url"
	printf -- '- Kitsoki comment URL: %s\n' "${comment_url:-"-"}"
	printf -- '- Notes: %s\n\n' "${notes:-"-"}"

	printf '## Checks\n\n'
	printf -- '- Health: `%s`\n' "$health_status"
	printf -- '- Run page: `%s`\n' "$run_status"
	printf -- '- API JSON: `%s`\n' "$api_status"
	printf -- '- Remote DB: `%s`\n\n' "$db_status"

	printf '## `/api/run/%s`\n\n' "$job_id"
	printf '```json\n'
	if [ "$api_status" = "ok" ]; then
		cat "$tmp_api"
		printf '\n'
	else
		printf '%s\n' "$(cat "$tmp_api")"
	fi
	printf '```\n\n'

	printf '## `/run/%s` Headers\n\n' "$job_id"
	printf '```text\n'
	cat "$tmp_run_headers"
	printf '```\n\n'

	if [ "$with_remote_db" -eq 1 ]; then
		printf '## `gh_jobs` Row\n\n'
		printf '```json\n'
		cat "$tmp_db"
		printf '\n```\n\n'
	fi

	printf '## Result\n\n'
	printf -- '- PASS/FAIL:\n'
	printf -- '- Reviewer notes:\n'
} >"$out"

echo "wrote $out"
