# GitHub App setup — the `@kitsoki` agent

This runbook stands up the GitHub App that authenticates the `@kitsoki` agent
against **live** GitHub. The auth seam lives in
[`internal/ghagent/githubapp`](../../internal/ghagent/githubapp/githubapp.go):
it mints a short-lived **App JWT** (RS256, stdlib crypto), exchanges it for an
**installation access token**, and exports that token as `GH_TOKEN` so the
existing `gh`/`git` CLI path (the `host.cliExec` seam) authenticates as the
installation — no per-user PATs.

Test install: the **`bsacrobatix`** org. Production is identical under
**`constructorfabric`** later. See
[`docs/proposals/kitsoki-github-agent.md`](../proposals/kitsoki-github-agent.md)
shared decision #1 for the auth/permissions decision.

The live POC runs the hosted webhook service on the test VM:

- Public base URL: `https://kitsoki-test.slothattax.me`
- Webhook URL: `https://kitsoki-test.slothattax.me/gh-agent/webhook`
- Systemd service: `kitsoki-gh-agent.service`
- Durable job DB: `/var/lib/kitsoki-gh-agent/gh-jobs.sqlite`

## a. Create the App (under `bsacrobatix`)

`bsacrobatix` org → **Settings → Developer settings → GitHub Apps → New GitHub
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

- **Webhook URL:** `https://kitsoki-test.slothattax.me/gh-agent/webhook`
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

1. App settings → **Install App** → install on `bsacrobatix`, scoped to a
   throwaway test repo (e.g. `bsacrobatix/kitsoki-sandbox`).
2. The **Installation ID** is the trailing number in the post-install URL:
   `https://github.com/organizations/bsacrobatix/settings/installations/<INSTALLATION_ID>`.

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
  --repo bsacrobatix/Kitsoki \
  --db /var/lib/kitsoki-gh-agent/gh-jobs.sqlite \
  --addr 127.0.0.1:8787 \
  --public-base-url https://kitsoki-test.slothattax.me \
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
  --repo bsacrobatix/Kitsoki \
  --public-base-url https://kitsoki-test.slothattax.me \
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
scp /private/tmp/kitsoki-ghagent root@206.189.84.218:/usr/local/bin/kitsoki
ssh root@206.189.84.218 'chmod 755 /usr/local/bin/kitsoki && systemctl restart kitsoki-gh-agent'
curl -fsS https://kitsoki-test.slothattax.me/healthz
```

Useful read-only smoke checks:

```
curl -fsS https://kitsoki-test.slothattax.me/healthz
ssh root@206.189.84.218 'systemctl is-active kitsoki-gh-agent && systemctl is-active caddy'
ssh root@206.189.84.218 'sqlite3 /var/lib/kitsoki-gh-agent/gh-jobs.sqlite "select job_id, origin_ref, story, state, run_url, comment_id, err_msg from gh_jobs order by created_at desc limit 10;"'
```

## g. Production

Identical under the **`constructorfabric`** org: create/install the same App
there, point the env at that App's ID / installation / key, and run with the
production `--repo`.
