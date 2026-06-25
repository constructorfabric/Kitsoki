#!/usr/bin/env bash
#
# Orchestrate the live @kitsoki GitHub-agent POC cases.
#
# Default mode is a dry run: print the GitHub/VM mutations and follow-up proof
# commands without executing them. Live mode requires --yes-live-mutations.

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

REPO="${KITSOKI_GH_AGENT_REPO:-bsacrobatix/Kitsoki}"
REMOTE="${KITSOKI_GH_AGENT_REMOTE:-root@206.189.84.218}"
REMOTE_DB="${KITSOKI_GH_AGENT_REMOTE_DB:-/var/lib/kitsoki-gh-agent/gh-jobs.sqlite}"
PUBLIC_BASE_URL="${KITSOKI_GH_AGENT_PUBLIC_BASE_URL:-https://kitsoki-test.slothattax.me}"
RUN_STAMP="${KITSOKI_GH_AGENT_LIVE_RUN_STAMP:-$(date -u +%Y%m%dT%H%M%SZ)}"
BUG_LABEL="${KITSOKI_GH_AGENT_BUG_LABEL:-bug}"
FEATURE_LABEL="${KITSOKI_GH_AGENT_FEATURE_LABEL:-enhancement}"
WAIT_SECONDS="${KITSOKI_GH_AGENT_WAIT_SECONDS:-180}"
POLL_SECONDS="${KITSOKI_GH_AGENT_POLL_SECONDS:-5}"

YES=0
DO_DEPLOY=1
DO_CAPTURE=0
PR_URL="${KITSOKI_GH_AGENT_PR_URL:-}"
DEVELOPER_ARC_MEDIA="${KITSOKI_GH_AGENT_DEVELOPER_ARC_MEDIA:-}"

usage() {
	cat <<'EOF'
usage: scripts/run-gh-agent-live-poc.sh [options]

Dry-run by default. Live mode creates GitHub issues/comments, optionally deploys
to the VM, waits for VM gh_jobs rows, and writes .context evidence notes.

Options:
  --yes-live-mutations       actually mutate GitHub/VM state
  --repo <owner/repo>        default bsacrobatix/Kitsoki
  --pr-url <url>             required in live mode for the PR-status case
  --skip-deploy              do not call scripts/deploy-gh-agent.sh --yes
  --capture                  after evidence, record each case with Playwright
  --developer-arc-media <p>  after captures, build, export, and verify the live Slidey deck
  -h, --help                 show this help

Environment:
  KITSOKI_GH_AGENT_REPO
  KITSOKI_GH_AGENT_REMOTE
  KITSOKI_GH_AGENT_REMOTE_DB
  KITSOKI_GH_AGENT_PUBLIC_BASE_URL
  KITSOKI_GH_AGENT_LIVE_RUN_STAMP
  KITSOKI_GH_AGENT_BUG_LABEL
  KITSOKI_GH_AGENT_FEATURE_LABEL
  KITSOKI_GH_AGENT_WAIT_SECONDS
  KITSOKI_GH_AGENT_POLL_SECONDS
  KITSOKI_GH_AGENT_PR_URL
  KITSOKI_GH_AGENT_DEVELOPER_ARC_MEDIA
EOF
}

while [ "$#" -gt 0 ]; do
	case "$1" in
		--yes-live-mutations)
			YES=1
			shift
			;;
		--repo)
			REPO="${2:-}"
			shift 2
			;;
		--pr-url)
			PR_URL="${2:-}"
			shift 2
			;;
		--skip-deploy)
			DO_DEPLOY=0
			shift
			;;
		--capture)
			DO_CAPTURE=1
			shift
			;;
		--developer-arc-media)
			DEVELOPER_ARC_MEDIA="${2:-}"
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

if [ -z "$REPO" ]; then
	echo "--repo must not be empty" >&2
	exit 2
fi
if [ "$YES" -eq 1 ] && [ -z "$PR_URL" ]; then
	echo "--pr-url is required with --yes-live-mutations for the PR-status case" >&2
	exit 2
fi

print_cmd() {
	printf '%q ' "$@"
	printf '\n'
}

run_or_print() {
	if [ "$YES" -eq 1 ]; then
		"$@"
	else
		print_cmd "$@"
	fi
}

run_capture_or_print() {
	local plan="$1"
	if [ "$YES" -eq 1 ]; then
		KITSOKI_GH_AGENT_LIVE_CAPTURE=1 \
			KITSOKI_GH_AGENT_LIVE_CAPTURE_PLAN="$plan" \
			pnpm -C tools/runstatus exec playwright test github-agent-live-capture --project=chromium
	else
		printf 'KITSOKI_GH_AGENT_LIVE_CAPTURE=1 KITSOKI_GH_AGENT_LIVE_CAPTURE_PLAN=%q pnpm -C tools/runstatus exec playwright test github-agent-live-capture --project=chromium\n' "$plan"
	fi
}

issue_number_from_url() {
	local url="$1"
	printf '%s\n' "$url" | sed -E 's#.*/(issues|pull)/([0-9]+).*#\2#'
}

last_non_empty_line() {
	awk 'NF { line=$0 } END { print line }'
}

json_field() {
	local field="$1"
	python3 -c 'import json,sys
field=sys.argv[1]
raw=sys.stdin.read().strip()
if not raw:
    print("")
    raise SystemExit
data=json.loads(raw)
if data is None:
    print("")
else:
    print(data.get(field,""))' "$field"
}

query_job_by_origin() {
	local origin="$1"
	ssh "$REMOTE" "python3 - '$REMOTE_DB' '$origin'" <<'PY'
import json
import sqlite3
import sys

db_path = sys.argv[1]
origin_ref = sys.argv[2]
cols = [
    "job_id", "origin_ref", "repo", "object_kind", "object_number",
    "story", "state", "run_url", "comment_id", "err_msg",
]
conn = sqlite3.connect(f"file:{db_path}?mode=ro", uri=True)
conn.row_factory = sqlite3.Row
row = conn.execute(
    "select " + ", ".join(cols) + " from gh_jobs where origin_ref = ?",
    (origin_ref,),
).fetchone()
print(json.dumps(dict(row) if row else None, sort_keys=True))
PY
}

wait_for_job() {
	local origin="$1"
	local deadline=$((SECONDS + WAIT_SECONDS))
	local row
	while [ "$SECONDS" -le "$deadline" ]; do
		row="$(query_job_by_origin "$origin" 2>/dev/null || true)"
		if [ -n "$row" ] && [ "$row" != "null" ]; then
			printf '%s\n' "$row"
			return 0
		fi
		sleep "$POLL_SECONDS"
	done
	echo "timed out waiting for $origin in $REMOTE_DB" >&2
	return 1
}

ensure_label() {
	local name="$1"
	local color="$2"
	local desc="$3"
	run_or_print gh label create "$name" --repo "$REPO" --color "$color" --description "$desc" --force
}

create_issue_case() {
	local slug="$1"
	local title="$2"
	local body="$3"
	local label="$4"
	local mention="$5"
	local issue_url issue_num mention_url origin row job_id comment_url

	if [ -n "$label" ]; then
		case "$slug" in
			bug-issue) ensure_label "$label" "d73a4a" "Live @kitsoki POC bug label" ;;
			feature-issue) ensure_label "$label" "a2eeef" "Live @kitsoki POC feature label" ;;
		esac
	fi

	if [ "$YES" -eq 1 ]; then
		if [ -n "$label" ]; then
			issue_url="$(gh issue create --repo "$REPO" --title "$title" --body "$body" --label "$label" | last_non_empty_line)"
		else
			issue_url="$(gh issue create --repo "$REPO" --title "$title" --body "$body" | last_non_empty_line)"
		fi
		issue_num="$(issue_number_from_url "$issue_url")"
		mention_url="$(gh issue comment "$issue_num" --repo "$REPO" --body "$mention" | last_non_empty_line)"
		origin="github:$REPO/issue/$issue_num"
		row="$(wait_for_job "$origin")"
		job_id="$(printf '%s' "$row" | json_field job_id)"
		comment_url="$(printf '%s' "$row" | json_field comment_id)"
		scripts/collect-gh-agent-poc-evidence.sh \
			--case "$slug" \
			--job-id "$job_id" \
			--source-url "$issue_url" \
			--mention-url "$mention_url" \
			--comment-url "${comment_url:-$mention_url}" \
			--remote-db
		scripts/build-gh-agent-capture-plan.mjs --case "$slug"
	else
		if [ -n "$label" ]; then
			print_cmd gh issue create --repo "$REPO" --title "$title" --body "$body" --label "$label"
		else
			print_cmd gh issue create --repo "$REPO" --title "$title" --body "$body"
		fi
		print_cmd gh issue comment "<$slug-issue-number>" --repo "$REPO" --body "$mention"
		printf 'wait for origin_ref github:%s/issue/<%s-issue-number>\n' "$REPO" "$slug"
		print_cmd scripts/collect-gh-agent-poc-evidence.sh --case "$slug" --job-id "<$slug-job-id>" --source-url "<$slug-issue-url>" --mention-url "<$slug-mention-url>" --comment-url "<$slug-kitsoki-comment-url>" --remote-db
		print_cmd scripts/build-gh-agent-capture-plan.mjs --case "$slug"
	fi
}

run_pr_case() {
	local pr_num mention mention_url origin row job_id comment_url
	mention="@kitsoki please read PR status for this live POC run. stamp: $RUN_STAMP"
	if [ "$YES" -eq 1 ]; then
		pr_num="$(issue_number_from_url "$PR_URL")"
		mention_url="$(gh issue comment "$pr_num" --repo "$REPO" --body "$mention" | last_non_empty_line)"
		origin="github:$REPO/pr/$pr_num"
		row="$(wait_for_job "$origin")"
		job_id="$(printf '%s' "$row" | json_field job_id)"
		comment_url="$(printf '%s' "$row" | json_field comment_id)"
		scripts/collect-gh-agent-poc-evidence.sh \
			--case pr-status \
			--job-id "$job_id" \
			--source-url "$PR_URL" \
			--mention-url "$mention_url" \
			--comment-url "${comment_url:-$mention_url}" \
			--remote-db
		scripts/build-gh-agent-capture-plan.mjs --case pr-status
	else
		print_cmd gh issue comment "<pr-number-from---pr-url>" --repo "$REPO" --body "$mention"
		printf 'wait for origin_ref github:%s/pr/<pr-number>\n' "$REPO"
		print_cmd scripts/collect-gh-agent-poc-evidence.sh --case pr-status --job-id "<pr-status-job-id>" --source-url "${PR_URL:-<pr-url>}" --mention-url "<pr-mention-url>" --comment-url "<pr-kitsoki-comment-url>" --remote-db
		print_cmd scripts/build-gh-agent-capture-plan.mjs --case pr-status
	fi
}

cat <<EOF
run-gh-agent-live-poc:
  mode:        $([ "$YES" -eq 1 ] && echo live-mutations || echo dry-run)
  repo:        $REPO
  stamp:       $RUN_STAMP
  public_url:  $PUBLIC_BASE_URL
  remote:      $REMOTE
EOF

if [ "$DO_DEPLOY" -eq 1 ]; then
	run_or_print scripts/deploy-gh-agent.sh --yes
fi

body_common="Temporary live @kitsoki GitHub-agent POC issue.

Run stamp: $RUN_STAMP

This issue can be closed after the demo evidence is captured."

create_issue_case \
	bug-issue \
	"bug: live @kitsoki POC bug issue $RUN_STAMP" \
	"$body_common" \
	"$BUG_LABEL" \
	"@kitsoki please handle this as the live bug issue POC. stamp: $RUN_STAMP"

create_issue_case \
	feature-issue \
	"feature: live @kitsoki POC feature issue $RUN_STAMP" \
	"$body_common" \
	"$FEATURE_LABEL" \
	"@kitsoki please handle this as the live feature issue POC. stamp: $RUN_STAMP"

create_issue_case \
	guidance \
	"live @kitsoki POC ambiguous guidance issue $RUN_STAMP" \
	"$body_common

No bug or feature label is intentionally applied; kitsoki should ask for guidance." \
	"" \
	"@kitsoki please take a look. stamp: $RUN_STAMP"

run_pr_case

if [ "$DO_CAPTURE" -eq 1 ]; then
	for case_slug in bug-issue feature-issue guidance pr-status; do
		run_capture_or_print ".artifacts/github-agent-live/capture-plan-$case_slug.json"
	done
fi

if [ -n "$DEVELOPER_ARC_MEDIA" ]; then
	run_or_print scripts/build-gh-agent-live-deck.mjs --developer-arc-media "$DEVELOPER_ARC_MEDIA"
	run_or_print scripts/export-gh-agent-live-deck-html.sh
	run_or_print scripts/verify-gh-agent-live-poc.mjs --developer-arc-media "$DEVELOPER_ARC_MEDIA"
else
	print_cmd scripts/build-gh-agent-live-deck.mjs --developer-arc-media "<path-to-slidey-developer-arc-mp4-or-rrweb>"
	print_cmd scripts/export-gh-agent-live-deck-html.sh
	print_cmd scripts/verify-gh-agent-live-poc.mjs --developer-arc-media "<path-to-slidey-developer-arc-mp4-or-rrweb>"
fi

cat <<'EOF'

Next manual review points:
  - Mark PASS/FAIL in each .context/live-poc-*.md evidence note.
  - Inspect generated .artifacts/github-agent-live/* screenshots/MP4s.
  - Render/QA the Slidey deck only after the verifier passes.
EOF
