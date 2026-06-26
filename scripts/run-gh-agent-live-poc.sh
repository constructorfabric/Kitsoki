#!/usr/bin/env bash
#
# Orchestrate the live @kitsoki GitHub-agent POC cases.
#
# Default mode is a dry run: print the GitHub/VM mutations and follow-up proof
# commands without executing them. Live mode requires --yes-live-mutations.

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

REPO="${KITSOKI_GH_AGENT_REPO:-}"
REMOTE="${KITSOKI_GH_AGENT_REMOTE:-}"
REMOTE_DB="${KITSOKI_GH_AGENT_REMOTE_DB:-/var/lib/kitsoki-gh-agent/gh-jobs.sqlite}"
PUBLIC_BASE_URL="${KITSOKI_GH_AGENT_PUBLIC_BASE_URL:-}"
RUN_STAMP="${KITSOKI_GH_AGENT_LIVE_RUN_STAMP:-$(date -u +%Y%m%dT%H%M%SZ)}"
BUG_LABEL="${KITSOKI_GH_AGENT_BUG_LABEL:-bug}"
FEATURE_LABEL="${KITSOKI_GH_AGENT_FEATURE_LABEL:-enhancement}"
WAIT_SECONDS="${KITSOKI_GH_AGENT_WAIT_SECONDS:-180}"
POLL_SECONDS="${KITSOKI_GH_AGENT_POLL_SECONDS:-5}"
EVIDENCE_DIR="${KITSOKI_GH_AGENT_EVIDENCE_DIR:-.context}"
MEDIA_ROOT="${KITSOKI_GH_AGENT_MEDIA_ROOT:-.artifacts/github-agent-live}"
DECK_JSON="${KITSOKI_GH_AGENT_DECK_JSON:-.artifacts/github-agent-live/live-github-agent.slidey.json}"
DECK_VIDEO="${KITSOKI_GH_AGENT_DECK_VIDEO:-.artifacts/github-agent-live/live-github-agent.mp4}"
RENDER_MP4="${KITSOKI_GH_AGENT_RENDER_MP4:-0}"
SUMMARY="${KITSOKI_GH_AGENT_LIVE_SUMMARY:-.context/live-poc-run-$RUN_STAMP.md}"

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
  --repo <owner/repo>        required unless KITSOKI_GH_AGENT_REPO is set
  --pr-url <url>             required in live mode for the PR-status case
  --skip-deploy              do not call scripts/deploy-gh-agent.sh --yes
  --capture                  after evidence, record each case with Playwright
  --developer-arc-media <p>  after captures, build/verify the live Slidey deck
  --render-mp4              also render an optional MP4 export and QA plan
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
  KITSOKI_GH_AGENT_EVIDENCE_DIR
  KITSOKI_GH_AGENT_MEDIA_ROOT
  KITSOKI_GH_AGENT_DECK_JSON
  KITSOKI_GH_AGENT_DECK_VIDEO
  KITSOKI_GH_AGENT_RENDER_MP4=1
  KITSOKI_GH_AGENT_LIVE_SUMMARY
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
		--render-mp4)
			RENDER_MP4=1
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

if [ -z "$REPO" ]; then
	echo "--repo must not be empty" >&2
	exit 2
fi
if [ -z "$PUBLIC_BASE_URL" ]; then
	echo "KITSOKI_GH_AGENT_PUBLIC_BASE_URL is required" >&2
	exit 2
fi
if [ "$YES" -eq 1 ] && [ -z "$REMOTE" ]; then
	echo "KITSOKI_GH_AGENT_REMOTE is required for live execution" >&2
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
	local capture_plan="$plan"
	if [[ "$capture_plan" != /* ]]; then
		capture_plan="$ROOT/$capture_plan"
	fi
	if [ "$YES" -eq 1 ]; then
		KITSOKI_GH_AGENT_LIVE_CAPTURE=1 \
			KITSOKI_GH_AGENT_LIVE_CAPTURE_PLAN="$capture_plan" \
			pnpm -C tools/runstatus exec playwright test github-agent-live-capture --project=chromium
	else
		printf 'KITSOKI_GH_AGENT_LIVE_CAPTURE=1 KITSOKI_GH_AGENT_LIVE_CAPTURE_PLAN=%q pnpm -C tools/runstatus exec playwright test github-agent-live-capture --project=chromium\n' "$capture_plan"
	fi
}

issue_number_from_url() {
	local url="$1"
	if [[ "$url" =~ ^https://github\.com/[^/]+/[^/]+/(issues|pull)/([0-9]+)/?$ ]]; then
		printf '%s\n' "${BASH_REMATCH[2]}"
		return 0
	fi
	echo "could not extract GitHub issue/PR number from URL: $url" >&2
	return 1
}

validate_live_preflight() {
	local url_repo=""
	local pr_num=""

	if [ "$YES" -ne 1 ]; then
		return 0
	fi
	if [ -z "$PR_URL" ]; then
		echo "--pr-url is required with --yes-live-mutations for the PR-status case" >&2
		exit 2
	fi
	if [[ "$PR_URL" =~ ^https://github\.com/([^/]+/[^/]+)/pull/([0-9]+)/?$ ]]; then
		url_repo="${BASH_REMATCH[1]}"
		pr_num="${BASH_REMATCH[2]}"
	else
		echo "--pr-url must be a GitHub pull request URL like https://github.com/$REPO/pull/123" >&2
		exit 2
	fi
	if [ "$url_repo" != "$REPO" ]; then
		echo "--pr-url repo $url_repo does not match --repo $REPO" >&2
		exit 2
	fi
	if [ -z "$pr_num" ]; then
		echo "--pr-url is missing a pull request number" >&2
		exit 2
	fi
	if [ -n "$DEVELOPER_ARC_MEDIA" ] && [ ! -f "$DEVELOPER_ARC_MEDIA" ]; then
		echo "--developer-arc-media does not exist: $DEVELOPER_ARC_MEDIA" >&2
		exit 2
	fi
	if [ "$DO_CAPTURE" -eq 1 ] && ! command -v pnpm >/dev/null 2>&1; then
		echo "--capture requires pnpm on PATH before live mutations start" >&2
		exit 2
	fi
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

init_summary() {
	if [ "$YES" -ne 1 ]; then
		return 0
	fi
	mkdir -p "$(dirname "$SUMMARY")"
	local head branch
	head="$(git rev-parse --short HEAD 2>/dev/null || true)"
	branch="$(git rev-parse --abbrev-ref HEAD 2>/dev/null || true)"
	cat >"$SUMMARY" <<EOF
# Live GitHub Agent POC Run $RUN_STAMP

- Repo: \`$REPO\`
- Branch/head: \`$branch\` / \`$head\`
- Public base URL: \`$PUBLIC_BASE_URL\`
- Webhook URL: \`${PUBLIC_BASE_URL%/}/gh-agent/webhook\`
- Remote: \`$REMOTE\`
- Remote DB: \`$REMOTE_DB\`
- Deploy: \`$([ "$DO_DEPLOY" -eq 1 ] && echo yes || echo skipped)\`
- Capture: \`$([ "$DO_CAPTURE" -eq 1 ] && echo yes || echo no)\`
- PR URL: ${PR_URL:-"-"}
- Developer arc media: ${DEVELOPER_ARC_MEDIA:-"-"}

## Cases

EOF
}

append_case_summary() {
	if [ "$YES" -ne 1 ]; then
		return 0
	fi
	local slug="$1"
	local source_url="$2"
	local mention_url="$3"
	local comment_url="$4"
	local job_id="$5"
	local evidence="$EVIDENCE_DIR/live-poc-$slug.md"
	local plan="$MEDIA_ROOT/capture-plan-$slug.json"
	cat >>"$SUMMARY" <<EOF
### $slug

- Source URL: $source_url
- Mention URL: $mention_url
- Kitsoki comment URL: ${comment_url:-"-"}
- Job ID: \`$job_id\`
- Evidence: \`$evidence\`
- Capture plan: \`$plan\`

EOF
}

finish_summary() {
	if [ "$YES" -ne 1 ]; then
		return 0
	fi
	if [ -n "$DEVELOPER_ARC_MEDIA" ]; then
		cat >>"$SUMMARY" <<EOF
## Final Artifacts

- Deck spec: \`$DECK_JSON\`
$(if [ "$RENDER_MP4" = "1" ]; then cat <<EOF2
- Rendered deck MP4: \`$DECK_VIDEO\`
- QA feature: \`.context/qa-gh-agent-live-feature.md\`
- QA scenarios: \`.context/qa-gh-agent-live-scenarios.yaml\`
EOF2
else cat <<'EOF2'
- Rendered deck MP4: not generated; source deck is the primary artifact.
EOF2
fi)

EOF
	else
		cat >>"$SUMMARY" <<EOF
## Final Artifacts

Not generated in this run because \`--developer-arc-media\` was not supplied.

EOF
	fi
	cat >>"$SUMMARY" <<EOF
## Gates

\`\`\`sh
scripts/verify-gh-agent-live-poc.mjs --evidence-dir "$EVIDENCE_DIR" --media-root "$MEDIA_ROOT" --deck "$DECK_JSON" --developer-arc-media ${DEVELOPER_ARC_MEDIA:-'<path-to-slidey-developer-arc-mp4-or-rrweb>'}
$(if [ "$RENDER_MP4" = "1" ]; then cat <<EOF2
scripts/verify-gh-agent-live-poc.mjs --evidence-dir "$EVIDENCE_DIR" --media-root "$MEDIA_ROOT" --deck "$DECK_JSON" --deck-video "$DECK_VIDEO" --developer-arc-media ${DEVELOPER_ARC_MEDIA:-'<path-to-slidey-developer-arc-mp4-or-rrweb>'}
scripts/write-gh-agent-live-qa-plan.mjs
.agents/skills/kitsoki-ui-qa/scripts/qa.sh "$DECK_VIDEO" --feature .context/qa-gh-agent-live-feature.md --scenarios .context/qa-gh-agent-live-scenarios.yaml --strict --pacing-strict
EOF2
fi)
\`\`\`

## Review Notes

- PASS/FAIL:
- Non-blocking advisories:
EOF
	echo "wrote $SUMMARY"
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
	local expected_state="$2"
	local deadline=$((SECONDS + WAIT_SECONDS))
	local row
	while [ "$SECONDS" -le "$deadline" ]; do
		row="$(query_job_by_origin "$origin" 2>/dev/null || true)"
		if [ -n "$row" ] && [ "$row" != "null" ]; then
			if job_row_ready "$row" "$expected_state"; then
				printf '%s\n' "$row"
				return 0
			fi
		fi
		sleep "$POLL_SECONDS"
	done
	echo "timed out waiting for $origin in $REMOTE_DB to reach $expected_state with run_url and comment_id" >&2
	return 1
}

job_row_ready() {
	local row="$1"
	local expected_state="$2"
	python3 -c 'import json
import sys

expected_state = sys.argv[1]
try:
    row = json.loads(sys.stdin.read())
except Exception:
    raise SystemExit(1)
if not isinstance(row, dict):
    raise SystemExit(1)
if row.get("state") != expected_state:
    if row.get("state") == "failed":
        err_msg = row.get("err_msg", "")
        print(f"job failed before reaching {expected_state}: {err_msg}", file=sys.stderr)
    raise SystemExit(1)
for key in ("job_id", "run_url", "comment_id"):
    if not str(row.get(key) or "").strip():
        raise SystemExit(1)
raise SystemExit(0)
' "$expected_state" <<<"$row"
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
	local issue_url issue_num mention_url origin row job_id comment_url expected_state
	expected_state="done"
	if [ "$slug" = "guidance" ]; then
		expected_state="awaiting_guidance"
	fi

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
		row="$(wait_for_job "$origin" "$expected_state")"
		job_id="$(printf '%s' "$row" | json_field job_id)"
		comment_url="$(printf '%s' "$row" | json_field comment_id)"
		scripts/collect-gh-agent-poc-evidence.sh \
			--case "$slug" \
			--job-id "$job_id" \
			--source-url "$issue_url" \
			--mention-url "$mention_url" \
			--comment-url "${comment_url:-$mention_url}" \
			--remote-db
		scripts/build-gh-agent-capture-plan.mjs \
			--case "$slug" \
			--evidence "$EVIDENCE_DIR/live-poc-$slug.md" \
			--out "$MEDIA_ROOT/capture-plan-$slug.json"
		append_case_summary "$slug" "$issue_url" "$mention_url" "${comment_url:-$mention_url}" "$job_id"
	else
		if [ -n "$label" ]; then
			print_cmd gh issue create --repo "$REPO" --title "$title" --body "$body" --label "$label"
		else
			print_cmd gh issue create --repo "$REPO" --title "$title" --body "$body"
		fi
		print_cmd gh issue comment "<$slug-issue-number>" --repo "$REPO" --body "$mention"
		printf 'wait for origin_ref github:%s/issue/<%s-issue-number> to reach %s with run_url and comment_id\n' "$REPO" "$slug" "$expected_state"
		print_cmd scripts/collect-gh-agent-poc-evidence.sh --case "$slug" --job-id "<$slug-job-id>" --source-url "<$slug-issue-url>" --mention-url "<$slug-mention-url>" --comment-url "<$slug-kitsoki-comment-url>" --remote-db
		print_cmd scripts/build-gh-agent-capture-plan.mjs --case "$slug" --evidence "$EVIDENCE_DIR/live-poc-$slug.md" --out "$MEDIA_ROOT/capture-plan-$slug.json"
	fi
}

create_guidance_resume_case() {
	local slug="guidance-resume"
	local title="Webhook deliveries occasionally double-post the run comment"
	local body="We've seen the rolling run comment land twice on a single issue once or twice
this week. It's intermittent and I can't pin down whether it's GitHub retrying a
delivery or a real bug in the comment-upsert path that mints a second comment.

I don't want to mislabel it, so I'm holding off on a label until we agree on how to
classify it. Once you've triaged, I'll add the right one and you can pick it up.

<!-- kitsoki POC run stamp: $RUN_STAMP -->

@kitsoki can you triage this? I'll add the bug label once you confirm it's a real
double-post and not just a delivery retry — then please resume from there."
	local issue_url issue_num origin guidance_row guidance_job guidance_comment row job_id comment_url

	ensure_label "$BUG_LABEL" "d73a4a" "Live @kitsoki POC bug label"

	if [ "$YES" -eq 1 ]; then
		issue_url="$(gh issue create --repo "$REPO" --title "$title" --body "$body" | last_non_empty_line)"
		issue_num="$(issue_number_from_url "$issue_url")"
		origin="github:$REPO/issue/$issue_num"
		guidance_row="$(wait_for_job "$origin" "awaiting_guidance")"
		guidance_job="$(printf '%s' "$guidance_row" | json_field job_id)"
		guidance_comment="$(printf '%s' "$guidance_row" | json_field comment_id)"
		gh issue edit "$issue_num" --repo "$REPO" --add-label "$BUG_LABEL" >/dev/null
		row="$(wait_for_job "$origin" "done")"
		job_id="$(printf '%s' "$row" | json_field job_id)"
		comment_url="$(printf '%s' "$row" | json_field comment_id)"
		if [ "$job_id" != "$guidance_job" ]; then
			echo "guidance resume minted a new job: initial=$guidance_job final=$job_id" >&2
			exit 1
		fi
		if [ "$comment_url" != "$guidance_comment" ]; then
			echo "guidance resume changed rolling comment: initial=$guidance_comment final=$comment_url" >&2
			exit 1
		fi
		scripts/collect-gh-agent-poc-evidence.sh \
			--case "$slug" \
			--job-id "$job_id" \
			--source-url "$issue_url" \
			--mention-url "$issue_url" \
			--comment-url "$comment_url" \
			--notes "Started at awaiting_guidance, then resumed the same job/comment after adding label $BUG_LABEL." \
			--remote-db
		scripts/build-gh-agent-capture-plan.mjs \
			--case "$slug" \
			--evidence "$EVIDENCE_DIR/live-poc-$slug.md" \
			--out "$MEDIA_ROOT/capture-plan-$slug.json"
		append_case_summary "$slug" "$issue_url" "$issue_url" "$comment_url" "$job_id"
	else
		print_cmd gh issue create --repo "$REPO" --title "$title" --body "$body"
		printf 'wait for origin_ref github:%s/issue/<%s-issue-number> to reach awaiting_guidance with run_url and comment_id\n' "$REPO" "$slug"
		print_cmd gh issue edit "<$slug-issue-number>" --repo "$REPO" --add-label "$BUG_LABEL"
		printf 'wait for the same origin_ref/job/comment to reach done after label %s\n' "$BUG_LABEL"
		print_cmd scripts/collect-gh-agent-poc-evidence.sh --case "$slug" --job-id "<$slug-job-id>" --source-url "<$slug-issue-url>" --mention-url "<$slug-issue-url>" --comment-url "<$slug-kitsoki-comment-url>" --notes "Started at awaiting_guidance, then resumed the same job/comment after adding label $BUG_LABEL." --remote-db
		print_cmd scripts/build-gh-agent-capture-plan.mjs --case "$slug" --evidence "$EVIDENCE_DIR/live-poc-$slug.md" --out "$MEDIA_ROOT/capture-plan-$slug.json"
	fi
}

run_pr_case() {
	local pr_num mention mention_url origin row job_id comment_url
	mention="@kitsoki can you give this PR a status read before I merge? Want to make sure the run links and state line up with what's on the branch."
	if [ "$YES" -eq 1 ]; then
		pr_num="$(issue_number_from_url "$PR_URL")"
		mention_url="$(gh issue comment "$pr_num" --repo "$REPO" --body "$mention" | last_non_empty_line)"
		origin="github:$REPO/pr/$pr_num"
		row="$(wait_for_job "$origin" "done")"
		job_id="$(printf '%s' "$row" | json_field job_id)"
		comment_url="$(printf '%s' "$row" | json_field comment_id)"
		scripts/collect-gh-agent-poc-evidence.sh \
			--case pr-status \
			--job-id "$job_id" \
			--source-url "$PR_URL" \
			--mention-url "$mention_url" \
			--comment-url "${comment_url:-$mention_url}" \
			--remote-db
		scripts/build-gh-agent-capture-plan.mjs \
			--case pr-status \
			--evidence "$EVIDENCE_DIR/live-poc-pr-status.md" \
			--out "$MEDIA_ROOT/capture-plan-pr-status.json"
		append_case_summary pr-status "$PR_URL" "$mention_url" "${comment_url:-$mention_url}" "$job_id"
	else
		print_cmd gh issue comment "<pr-number-from---pr-url>" --repo "$REPO" --body "$mention"
		printf 'wait for origin_ref github:%s/pr/<pr-number> to reach done with run_url and comment_id\n' "$REPO"
		print_cmd scripts/collect-gh-agent-poc-evidence.sh --case pr-status --job-id "<pr-status-job-id>" --source-url "${PR_URL:-<pr-url>}" --mention-url "<pr-mention-url>" --comment-url "<pr-kitsoki-comment-url>" --remote-db
		print_cmd scripts/build-gh-agent-capture-plan.mjs --case pr-status --evidence "$EVIDENCE_DIR/live-poc-pr-status.md" --out "$MEDIA_ROOT/capture-plan-pr-status.json"
	fi
}

validate_live_preflight

cat <<EOF
run-gh-agent-live-poc:
  mode:        $([ "$YES" -eq 1 ] && echo live-mutations || echo dry-run)
  repo:        $REPO
  stamp:       $RUN_STAMP
  public_url:  $PUBLIC_BASE_URL
  remote:      $REMOTE
  evidence:    $EVIDENCE_DIR
  media:       $MEDIA_ROOT
  deck_json:   $DECK_JSON
  deck_video:  $DECK_VIDEO
  summary:     $SUMMARY
EOF

init_summary

if [ "$DO_DEPLOY" -eq 1 ]; then
	run_or_print scripts/deploy-gh-agent.sh --yes
fi

# A hidden HTML comment carries the run stamp for cleanup/traceability without
# polluting the rendered issue. The origin_ref is keyed on the issue number, so
# the stamp is purely cosmetic — the bodies can read like real reports.
stamp_marker="<!-- kitsoki POC run stamp: $RUN_STAMP -->"

bug_body="## What happens

\`kitsoki inbox sync --github\` against a busy repo intermittently comes back with an
empty inbox even when there are open issues that mention the agent. It's not every
run — maybe one in five — and there's no error printed, so it just looks like
there's nothing to do.

## Steps to reproduce

1. Have at least one open issue and one open PR that mention \`@kitsoki\`.
2. Run \`kitsoki inbox sync --github\` a handful of times in a row.
3. Every so often the result is empty, with exit code 0 and no warning.

## Expected

The sync should return the full set of mentions, or fail loudly. Silently
dropping everything is the worst case — we skip real work and don't know it.

## Hunch

I suspect the PR-search half of the query is timing out and the error is being
swallowed, so the whole sync short-circuits to empty.

$stamp_marker"

create_issue_case \
	bug-issue \
	"Inbox sync silently returns empty when the PR search query times out" \
	"$bug_body" \
	"$BUG_LABEL" \
	"@kitsoki can you take a look at this one? It's been biting us on every release cut and I'd love a real fix rather than a retry."

feature_body="## Problem

When kitsoki posts a run link on an issue, reviewers open the run status page and
then almost always want to paste that exact URL into Slack. Today they have to
select the address bar by hand, which is fiddly on the hosted view and easy to
get wrong (people grab the \`/assets\` sub-path instead of the canonical run URL).

## Proposal

Add a small **Copy run URL** button next to the job-state header on the run
status page. One click copies the canonical \`/run/<job-id>\` URL to the clipboard
and shows a brief \"Copied\" confirmation.

## Why it's worth it

It's a tiny affordance, but sharing the proof link is the single most common thing
anyone does on that page. Removing the manual select-and-copy step makes the whole
\"kitsoki replied, here's the receipt\" loop feel finished.

$stamp_marker"

create_issue_case \
	feature-issue \
	"Add a \"Copy run URL\" button to the run status page" \
	"$feature_body" \
	"$FEATURE_LABEL" \
	"@kitsoki want to pick this up? Should be a small front-end change on the run status page — happy to review."

guidance_body="After yesterday's redeploy, a couple of run links pointed at what looked like a
stale job for a second or two before correcting themselves. I genuinely can't tell
whether this is a real caching bug in the run page or just expected behaviour while
the service does a rolling restart.

Could someone take a look and tell me which it is? If it's a bug I'll open it
properly; if it's just deploy churn I'll stop worrying about it.

$stamp_marker"

create_issue_case \
	guidance \
	"Run page briefly shows a stale job after a redeploy — bug or expected?" \
	"$guidance_body" \
	"" \
	"@kitsoki not sure if this is a real bug or just how rolling redeploys look — can you take a look and tell me how to classify it?"

create_guidance_resume_case

run_pr_case

if [ "$DO_CAPTURE" -eq 1 ]; then
	for case_slug in bug-issue feature-issue guidance guidance-resume pr-status; do
		run_capture_or_print "$MEDIA_ROOT/capture-plan-$case_slug.json"
	done
fi

if [ -n "$DEVELOPER_ARC_MEDIA" ]; then
	run_or_print scripts/build-gh-agent-live-deck.mjs --evidence-dir "$EVIDENCE_DIR" --media-root "$MEDIA_ROOT" --out "$DECK_JSON" --developer-arc-media "$DEVELOPER_ARC_MEDIA"
	if [ "$RENDER_MP4" = "1" ]; then
		run_or_print scripts/render-gh-agent-live-deck-video.sh --deck "$DECK_JSON" --out "$DECK_VIDEO"
		run_or_print scripts/verify-gh-agent-live-poc.mjs --evidence-dir "$EVIDENCE_DIR" --media-root "$MEDIA_ROOT" --deck "$DECK_JSON" --deck-video "$DECK_VIDEO" --developer-arc-media "$DEVELOPER_ARC_MEDIA"
		run_or_print scripts/write-gh-agent-live-qa-plan.mjs --video "$DECK_VIDEO"
	else
		run_or_print scripts/verify-gh-agent-live-poc.mjs --evidence-dir "$EVIDENCE_DIR" --media-root "$MEDIA_ROOT" --deck "$DECK_JSON" --developer-arc-media "$DEVELOPER_ARC_MEDIA"
	fi
	finish_summary
else
	print_cmd scripts/build-gh-agent-live-deck.mjs --evidence-dir "$EVIDENCE_DIR" --media-root "$MEDIA_ROOT" --out "$DECK_JSON" --developer-arc-media "<path-to-slidey-developer-arc-mp4-or-rrweb>"
	print_cmd scripts/verify-gh-agent-live-poc.mjs --evidence-dir "$EVIDENCE_DIR" --media-root "$MEDIA_ROOT" --deck "$DECK_JSON" --developer-arc-media "<path-to-slidey-developer-arc-mp4-or-rrweb>"
	if [ "$RENDER_MP4" = "1" ]; then
		print_cmd scripts/render-gh-agent-live-deck-video.sh --deck "$DECK_JSON" --out "$DECK_VIDEO"
		print_cmd scripts/verify-gh-agent-live-poc.mjs --evidence-dir "$EVIDENCE_DIR" --media-root "$MEDIA_ROOT" --deck "$DECK_JSON" --deck-video "$DECK_VIDEO" --developer-arc-media "<path-to-slidey-developer-arc-mp4-or-rrweb>"
		print_cmd scripts/write-gh-agent-live-qa-plan.mjs --video "$DECK_VIDEO"
	fi
	finish_summary
fi

cat <<'EOF'

Next manual review points:
  - Mark PASS/FAIL in each .context/live-poc-*.md evidence note.
  - Inspect the generated .slidey.json deck directly in VS Code.
  - Render/run video QA only when an MP4 export was explicitly requested.
EOF
