# GitHub App setup — the `@kitsoki` agent

This runbook stands up the GitHub App that authenticates the `@kitsoki` agent
against **live** GitHub. The auth seam lives in
[`internal/ghagent/githubapp`](../../internal/ghagent/githubapp/githubapp.go):
it mints a short-lived **App JWT** (RS256, stdlib crypto), exchanges it for an
**installation access token**, and exports that token as `GH_TOKEN` so the
existing `gh`/`git` CLI path (the `host.cliExec` seam) authenticates as the
installation — no per-user PATs.

Test install examples may use a throwaway org/repo, but production uses the
same commands with `KITSOKI_GH_AGENT_REPO` and
`KITSOKI_GH_AGENT_PUBLIC_BASE_URL` set for the target installation. See
[`docs/proposals/kitsoki-github-agent.md`](../proposals/kitsoki-github-agent.md)
shared decision #1 for the auth/permissions decision.
The controlled live proof flow is
[`docs/scenarios/github-agent-live-poc.md`](../scenarios/github-agent-live-poc.md).

The hosted webhook service is parameterized by environment:

- Public base URL: `$KITSOKI_GH_AGENT_PUBLIC_BASE_URL`
- Webhook URL: `$KITSOKI_GH_AGENT_PUBLIC_BASE_URL/gh-agent/webhook`
- Systemd service: `kitsoki-gh-agent.service`
- Durable job DB: `/var/lib/kitsoki-gh-agent/gh-jobs.sqlite`

## a. Create the App

Target org → **Settings → Developer settings → GitHub Apps → New GitHub
App**.

- **GitHub App name:** `kitsoki` (or `kitsoki-test`).
- **Homepage URL:** any (e.g. your repo URL).
- **Repository permissions** (the floor — shared decision #1):
  - **Issues:** Read & write
  - **Pull requests:** Read & write
  - **Contents:** Read & write  *(rebase/push)*
  - **Checks:** Read-only
- **Subscribe to events**:
  `Issues`, `Issue comment`, `Pull request`, `Pull request review comment`,
  `Check suite`.

## b. Webhook

- **Webhook URL:** `$KITSOKI_GH_AGENT_PUBLIC_BASE_URL/gh-agent/webhook`
- **Webhook secret:** generate one and save it; it becomes
  `KITSOKI_GH_WEBHOOK_SECRET`. Payloads are HMAC-verified by
  `githubapp.VerifyWebhookSignature` against `X-Hub-Signature-256`.

## c. Private key + env

After creating the App:

1. Note the **App ID** (top of the App's settings page).
2. **Generate a private key** → downloads a `.pem`. Store it outside the repo,
   readable only by you:

   ```
   mkdir -p ~/.config/kitsoki
   mv ~/Downloads/kitsoki.*.private-key.pem ~/.config/kitsoki/gh-app.pem
   chmod 600 ~/.config/kitsoki/gh-app.pem
   ```

3. Export the env (the key is referenced by **file path**, never inlined):

   ```
   export KITSOKI_GH_APP_ID=<app-id>
   export KITSOKI_GH_APP_INSTALLATION_ID=<installation-id>   # from step d
   export KITSOKI_GH_APP_PRIVATE_KEY_FILE=~/.config/kitsoki/gh-app.pem
   export KITSOKI_GH_WEBHOOK_SECRET=<secret>                 # round 2 only
   ```

## d. Install on a test repo + find the Installation ID

1. App settings → **Install App** → install on the target org, scoped to the
   selected repo.
2. The **Installation ID** is the trailing number in the post-install URL:
   `https://github.com/organizations/<org>/settings/installations/<INSTALLATION_ID>`.

   Or list installations with the App JWT:

   ```
   gh api -H "Accept: application/vnd.github+json" /app/installations
   # → each object's "id" is an installation id
   ```

## e. Run the hosted webhook service

Flags override the env (`--gh-app-id`, `--gh-app-installation-id`,
`--gh-app-key-file`); `--github-app` forces App auth on.

```
go run ./cmd/kitsoki gh-agent serve \
  --repo "$KITSOKI_GH_AGENT_REPO" \
  --db /var/lib/kitsoki-gh-agent/gh-jobs.sqlite \
  --addr 127.0.0.1:8787 \
  --public-base-url "$KITSOKI_GH_AGENT_PUBLIC_BASE_URL" \
  --poll-interval 0 \
  --github-app
```

This mints an installation token, sets `GH_TOKEN`, serves `/healthz`,
`/gh-agent/webhook`, `/run/<job-id>`, and `/api/run/<job-id>`, and dispatches
accepted `@kitsoki` issue/PR comments to the configured no-LLM story route.

For local single-shot testing, `gh-agent poll` still exists and uses the same
dispatcher path:

```
go run ./cmd/kitsoki gh-agent poll \
  --repo "$KITSOKI_GH_AGENT_REPO" \
  --public-base-url "$KITSOKI_GH_AGENT_PUBLIC_BASE_URL" \
  --github-app
```

## f. Deploy the test VM service

From the repo root, run the dry-run first:

```
scripts/deploy-gh-agent.sh
```

When the commands look right, deploy and restart the service:

```
scripts/deploy-gh-agent.sh --yes
```

The script performs:

```
GOOS=linux GOARCH=amd64 GOCACHE=/private/tmp/kitsoki-gocache \
  go build -o /private/tmp/kitsoki-ghagent ./cmd/kitsoki
scp /private/tmp/kitsoki-ghagent "$KITSOKI_GH_AGENT_REMOTE:/tmp/kitsoki-ghagent.<pid>"
ssh "$KITSOKI_GH_AGENT_REMOTE" "sha256sum /tmp/kitsoki-ghagent.<pid> | awk '{print \$1}'"
ssh "$KITSOKI_GH_AGENT_REMOTE" "install -m 755 /tmp/kitsoki-ghagent.<pid> /usr/local/bin/kitsoki && rm -f /tmp/kitsoki-ghagent.<pid>"
git archive --format=tar HEAD | ssh "$KITSOKI_GH_AGENT_REMOTE" "mkdir -p /opt/kitsoki && tar -x -C /opt/kitsoki"
ssh "$KITSOKI_GH_AGENT_REMOTE" "sha256sum /usr/local/bin/kitsoki | awk '{print \$1}'"
ssh "$KITSOKI_GH_AGENT_REMOTE" 'chmod 755 /usr/local/bin/kitsoki && systemctl restart kitsoki-gh-agent'
curl -fsS "$KITSOKI_GH_AGENT_PUBLIC_BASE_URL/healthz"
```

The deploy helper uploads to a temporary path, compares the local linux/amd64
build sha256 with the remote upload sha256, installs it into `/usr/local/bin`,
syncs the tracked checkout to `/opt/kitsoki`, then verifies the installed binary
sha256 before restarting the service. The live POC can prove the VM is running
the binary and story/fixture files just built from the current checkout without
relying on direct overwrite of the running executable.

Useful read-only smoke checks:

```
curl -fsS "$KITSOKI_GH_AGENT_PUBLIC_BASE_URL/healthz"
ssh "$KITSOKI_GH_AGENT_REMOTE" 'systemctl is-active kitsoki-gh-agent && systemctl is-active caddy'
ssh "$KITSOKI_GH_AGENT_REMOTE" 'python3 - <<'"'"'PY'"'"'
import sqlite3
conn = sqlite3.connect("file:/var/lib/kitsoki-gh-agent/gh-jobs.sqlite?mode=ro", uri=True)
for row in conn.execute("select job_id, origin_ref, story, state, run_url, comment_id, err_msg from gh_jobs order by created_at desc limit 10"):
    print(row)
PY'
```

After a live issue or PR mention has a job id, collect reviewable markdown
evidence under `.context`:

```
scripts/collect-gh-agent-poc-evidence.sh \
  --case bug-issue \
  --job-id <job-id> \
  --source-url <issue-or-pr-url> \
  --mention-url <mention-comment-url> \
  --comment-url <kitsoki-comment-url> \
  --remote-db
```

For demo capture, turn that evidence note into the gated Playwright capture plan:

```
scripts/build-gh-agent-capture-plan.mjs --case bug-issue
KITSOKI_GH_AGENT_LIVE_CAPTURE=1 \
KITSOKI_GH_AGENT_LIVE_CAPTURE_PLAN=.artifacts/github-agent-live/capture-plan-bug-issue.json \
pnpm -C tools/runstatus exec playwright test github-agent-live-capture --project=chromium
```

To run the full four-case POC sequence from one guarded command, start with the
dry run:

```
scripts/run-gh-agent-live-poc.sh --pr-url <throwaway-pr-url>
```

After reviewing the printed VM/GitHub mutations, run the live sequence only with
explicit approval:

```
scripts/run-gh-agent-live-poc.sh \
  --yes-live-mutations \
  --pr-url <throwaway-pr-url> \
  --capture \
  --developer-arc-media <path-to-slidey-developer-arc-mp4-or-rrweb>
```

The script deploys current code unless `--skip-deploy` is set, creates the bug,
feature, and guidance issues, comments on the supplied PR, waits for the VM
`gh_jobs` rows, writes `.context/live-poc-*.md`, builds capture plans, optionally
records rrweb clips, builds the source Slidey deck, and verifies the live deck
when `--developer-arc-media` is supplied.

The ambiguous guidance case still writes a public run URL before posting its
guidance comment. The GitHub thread should show both the request for direction
and a `$KITSOKI_GH_AGENT_PUBLIC_BASE_URL/run/<job-id>` link, while the job state
parks at `awaiting_guidance`.
The live runner waits for each row to reach its expected proof state with both
`run_url` and `comment_id` populated before collecting evidence, so capture plans
point at the App response rather than the requester mention.

Live mode fails before any deploy or GitHub mutation if the PR URL is missing,
is not a pull request URL for the selected repo, if `--developer-arc-media` points
at a missing file, or if `--capture` is requested without `pnpm` on `PATH`.
It also writes a run-level audit summary at
`.context/live-poc-run-<stamp>.md` tying together the created GitHub URLs, job
ids, evidence notes, capture plans, and final review commands. For isolated
smoke runs, set `KITSOKI_GH_AGENT_EVIDENCE_DIR`,
`KITSOKI_GH_AGENT_MEDIA_ROOT`, `KITSOKI_GH_AGENT_DECK_JSON`,
`KITSOKI_GH_AGENT_DECK_VIDEO`, and `KITSOKI_GH_AGENT_LIVE_SUMMARY`.

After all four live case clips and the developer-arc media exist, build the
Slidey deck scaffold from the evidence and media:

```
scripts/build-gh-agent-live-deck.mjs \
  --developer-arc-media <path-to-slidey-developer-arc-mp4-or-rrweb>
```

The command is strict by default: it fails if required evidence notes, live URLs,
case clips, or developer-arc media are missing. The primary output is
`.artifacts/github-agent-live/live-github-agent.slidey.json`. MP4 and HTML
exports are optional review artifacts only; they are not produced by the default
POC flow.

If an MP4 review export is explicitly requested, render the same deck:

```
scripts/render-gh-agent-live-deck-video.sh
```

Finally, verify the whole proof bundle before sharing the deck source:

```
scripts/verify-gh-agent-live-poc.mjs \
  --developer-arc-media <path-to-slidey-developer-arc-mp4-or-rrweb>
pnpm -C tools/runstatus exec playwright test github-agent-live-zoom-qa --project=chromium
```

The verifier checks the evidence/deck contract. The Playwright zoom QA replays
the captured rrweb logs and samples the readable-zoom frames themselves, so it
catches source-color or label-only zoom regressions that marker metadata cannot.

The verifier is read-only and strict by default. It checks the four `.context`
evidence notes, `/api/run` JSON, read-only `gh_jobs` rows, capture plans, rrweb
clips, developer-arc media, and generated Slidey deck. HTML and MP4 exports are
verified only when those optional paths are supplied.
The evidence notes must show `/healthz` returned `ok`, `/run/<job-id>` returned
HTTP 2xx, `/api/run/<job-id>` returned JSON, and the VM `gh_jobs` row was fetched
successfully with `created_at` / `updated_at` timestamps.

Prepare the gated `kitsoki-ui-qa` feature/scenario files:

```
scripts/write-gh-agent-live-qa-plan.mjs
```

The script writes `.context/qa-gh-agent-live-feature.md` and
`.context/qa-gh-agent-live-scenarios.yaml`, then prints the `qa.sh` command for
an explicitly rendered MP4 or labeled frame set. Do not wire that QA command into
automated tests; it uses the local vision reviewer and must stay operator-gated.

## g. Production

Identical under the **`constructorfabric`** org: create/install the same App
there, point the env at that App's ID / installation / key, and run with the
production `--repo`.
