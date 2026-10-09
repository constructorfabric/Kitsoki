---
name: product-journey-qa
description: Run or refine the Kitsoki product-journey QA pipeline: generate 10-GitHub-repo natural-use matrices, create persona/scenario run bundles, drive or hand off visual/Kitsoki MCP evidence capture, run no-LLM replay/dogfood gates, review/validate bundles, and produce Slidey decks with playback evidence. Use when asked to dogfood onboarding, bugfix, PRD/design, feature implementation, product-discovery, product-bug, personas, or the reusable product-journey QA agent/story.
---

# Product Journey QA

Use this skill when the task is to evaluate Kitsoki as a product through
repeatable persona journeys, not when merely editing one unrelated story room.
The durable surfaces are:

- Runner: `tools/product-journey/run.py`
- Story wrapper: `stories/product-journey-qa/app.yaml`
- Driver agent: `.agents/agents/product-journey-qa-driver.md`
- Catalogs: `tools/product-journey/personas.json`,
  `tools/product-journey/scenarios.json`,
  `tools/product-journey/github-targets.json`
- Review output: `.artifacts/product-journey/**/deck.slidey.json`,
  `review.json`, `media-manifest.json`, `scenario-outcomes.md`,
  `driver-journal.md`, `driver-handoff.md`

## Operating Rules

- Automated tests and repeatable gates must not call a real LLM. Use flow
  fixtures, cassettes, demo evidence, or replay artifacts.
- Live/model work is only for explicitly authorized exploratory runs. If a
  scenario needs live authorization and none was given, record a blocker instead
  of faking evidence.
- Proof evidence sources are `local`, `retained`, `external`, and `cassette`.
  `demo` evidence exercises aggregation only; it is not product proof.
- Preserve the persona lens. A core maintainer, dependency debugger,
  docs-minded contributor, IDE-first engineer, and hobbyist contributor should
  start from different surfaces, ask different first questions, and weigh
  evidence differently.
- Every scenario attempt needs a driver journal event, even when it ends in a
  blocker.
- A bundle is not discussion-ready until the story-owned final gates have run:
  `autonomous_watchdog`, `autonomous_fix` when credible issue findings exist,
  then `review` and `validate`. The deck must have playback media or an
  explicit playback blocker.

## No-LLM Gates

Start with the cheap gates before live capture:

```sh
python3 tools/product-journey/run.py --validate-corpus --json-output
python3 tools/product-journey/run.py --capture-preflight --json-output
python3 tools/product-journey/run.py --driver-replay-sweep --seed demo --json-output
python3 tools/product-journey/run.py --gate ghagent,autonomous-fix,persona-autofix,autonomous-marathon --json-output
python3 tools/product-journey/run.py --gate autonomous-marathon \
  --autonomous-marathon-smoke-repeats 2 --json-output
python3 tools/product-journey/run.py --gate marathon-ledger \
  --marathon-smoke-ledger .artifacts/product-journey/marathon-smokes/<id>/autonomous-marathon-smoke.json \
  --min-marathon-smoke-cycles 2 --json-output
GOCACHE=/private/tmp/kitsoki-gocache go run ./cmd/kitsoki test flows stories/product-journey-qa/app.yaml
```

`--capture-preflight` must stay no-LLM and fail closed before live persona
spend: it checks webshot capture, Studio MCP `studio.ping`, and provider-quota
state for malformed JSON or active cooldown windows.
The story rejects live-budgeted pending `autonomous_marathon` creation unless
`capture_preflight` has passed; replay mode stays no-LLM and does not require
this live-capture preflight.
Live-budgeted pending marathons also require `ticket_repo` and
`gh_agent_public_base_url` before driver handoff, so the story cannot spend
capture budget on a run that cannot later file findings, drain gh-agent fixes,
or expose reviewable run links. The runner checks
`<gh_agent_public_base_url>/healthz` and refuses `ready_for_driver` unless it
returns HTTP 200 with body `ok`, then checks
`<gh_agent_public_base_url>/api/ready` and refuses handoff unless the hosted
agent reports `status=ready`, the same `ticket_repo`, and an enabled drain loop.

Use `--gate driver-replay --smoke-scenario <scenario-id>` when narrowing a
single scenario. Use `--gate dogfood` when checking matrix-to-rollup artifact
composition. See `tools/product-journey/README.md#gates` for the full gate
list and the shared `gate_status`/`readiness_status` JSON contract; the
`--*-smoke` flag names above are deprecated aliases that still work.

`--autonomous-marathon` writes `autonomous-marathon-control.json` and
`autonomous-marathon-control.md` beside the run. Those artifacts record cadence,
per-scenario live budget, human role, heartbeat/watchdog timing, and final gates
so the standing loop can be reviewed without relying on operator glue.
`--autonomous-marathon-watchdog --run-dir <run>` checks that control file against
`driver-journal.json` and writes `autonomous-marathon-watchdog.json/md`; stale
heartbeats fail closed before spend. Story callers should include
`--report-blocked-autonomous-watchdog` so the blocked report binds into the
Kitsoki view instead of disappearing as a host error.
`--autonomous-marathon --run-dir <run>` also enforces the same watchdog before
native issue filing, gh-agent drain, close-out, or stats work; a stale heartbeat
returns `autonomous_marathon_invalid` with the watchdog report attached.

## Matrix Workflow

For the 10 popular GitHub repositories with at least 100 open bugs:

```sh
python3 tools/product-journey/run.py --refresh-github-targets --seed <seed>
python3 tools/product-journey/run.py --emit-matrix --seed <seed> \
  --target-proof-file .artifacts/product-journey/target-proofs/<proof-id>
python3 tools/product-journey/run.py --validate-matrix \
  --matrix-dir .artifacts/product-journey/matrices/<matrix-id> \
  --strict-target-proof
```

Use `--matrix-personas all` when each target should be paired with every
persona. Use normal `--validate-matrix` for draft matrices; use
`--strict-target-proof` before a live scored sweep so missing refreshed GitHub
bug/star/license proof is an error. The matrix is an assignment plan, not
evidence that Kitsoki worked.

## Run Bundle Workflow

Create one bundle from a matrix assignment or direct target:

```sh
python3 tools/product-journey/run.py --emit-run \
  --project <target-id> --persona <persona-id> --seed <seed> \
  [--scenarios <scenario-id,...>] [--live-budget-minutes <n>]
```

Then hand it to the reusable driver:

1. Read `agent-brief.md`, `driver-plan.md`, and `driver-handoff.md`.
2. Use `.agents/agents/product-journey-qa-driver.md` for live/cassette MCP
   capture.
3. Attach evidence with `--attach-evidence` or the story `attach` intent;
   loaded runs also expose `last_result.next_driver_attach_command`.
   Use `last_result.next_driver_capture_route` as the deterministic setup and
   recording entrypoint for the first missing proof slot. If the route is
   absent or unusable, record a blocker instead of inventing a setup path.
4. Record findings with `--record-finding`; record honest blockers with
   `--record-blocker` or `last_result.next_driver_blocker_command`.
5. Record each attempt with `--record-driver-event` or the story `driver_event`
   intent.
6. For the full issue-to-fix loop, submit the story-owned issue-to-fix gate:
   `autonomous_fix ticket_repo=<owner/repo> gh_agent_public_base_url=<url>`.
   This files credible `issue` findings with uploaded evidence, enqueues and
   drains gh-agent fixes, refreshes the deck/review artifacts, and validates the
   bundle without agent oversight. The story calls the native
   `kitsoki gitops autonomous-fix` facade. Before filing anything, that facade
   runs the autonomous watchdog check and fails closed on stale or missing
   standing-loop control, then checks `<gh_agent_public_base_url>/healthz` and
   `/api/ready`; readiness must report `status=ready`, the same `ticket_repo`,
   and an enabled drain loop.
   A mismatch returns `autonomous_fix_invalid` with filing still `not_run`;
   successful runs carry watchdog and health/readiness summaries into the story
   view and `autonomous-fix-report.md`.
   The facade then drives `kitsoki bug
   file-findings` (host.GitHubFileFindings) and native gh-agent queue/drain
   surfaces behind the story boundary: native GitHub API filing uses `GH_TOKEN` /
   `GITHUB_TOKEN`, open issues are searched for a strong title match before
   creating anything, duplicates receive a related-finding comment instead of a
   new issue, evidence uploads as release assets for newly-filed issues, the
   issue gets an `## Artifacts` section + kitsoki metadata block, and the issue
   URL is written back into `findings.json` (`item.github_issue`) so re-runs
   are idempotent. If the pre-file search fails, the gate fails closed rather
   than creating a possible duplicate. Before draining a queued repair,
   `kitsoki gitops autonomous-fix` posts a `kitsoki-autofix-claim` comment
   through `host.gh.ticket` and records the claim URL in `findings.json`; this
   makes in-flight autonomous work visible to parallel agents and reviewers.
   Completed gh-agent jobs must publish a `triage-verdict.md` preflight asset
   plus an `independent-verify.md` asset from the story run; a fix report or
   patch alone does not satisfy the autonomous gate. Once the filing, gh-agent,
   review, and validation gates pass,
   `kitsoki gitops autonomous-fix` posts a `kitsoki-fixed-in` close-out comment
   through `host.gh.ticket`, closes the GitHub issue, and records
   `findings.issue_closeout` plus closed issue state back into `findings.json`
   so stats can be derived mechanically. Never file, claim, or close these
   findings with raw `gh
   issue create` or text-only `issue_create` — that drops the evidence.
   For cross-run stats that need fresh GitHub state, use the story intent
   `stats refresh_issue_state=true ticket_repo=<owner/repo>` or the runner
   `--stats --refresh-issue-state`; both refresh through native
   `kitsoki gitops issue-state-cache` / `host.gh.ticket`, not raw `gh`.
   Open `weakness` findings are routed to the PRD/design path instead of the
   bugfix queue: review regenerates `weakness-routes.json`,
   `weakness-routes.md`, `prd-design-intake.json`, `prd-design-intake.md`,
   and a `PRD/design routes` deck scene pointing each observed weakness at
   `stories/prd` with a `start` intent, persona lens, scenario, and evidence
   context attached.
   For a deterministic no-operator marathon, submit
   `autonomous_marathon scenarios=<ids> autonomous_driver_mode=replay
   ticket_repo=<owner/repo> gh_agent_public_base_url=<url>`. Replay mode is
   still story-owned: it creates the run, attaches cassette-backed local proof
   artifacts, records the driver journal and findings, runs native gitops
   filing/fixing/close-out, refreshes review artifacts, validates, and derives
   stats in one call. Missing `ticket_repo` or `gh_agent_public_base_url` must
   fail closed in the story view with `autonomous_marathon_invalid` instead of
   disappearing behind a host error. Use default
   `autonomous_driver_mode=pending` when a live budgeted driver still needs to
   be handed off manually; use `autonomous_driver_mode=replay` for the no-LLM
   fully autonomous proof. Explicit `record` or `live` marathon driver modes
   create the control/report bundle, auto-queue the story-owned
   `dispatch_driver` intent, run the reusable driver through
   `host.agent.task`, and then queue the native autonomous finalizer instead of
   relying on operator glue. Record/live modes must also provide
   `autonomous_driver_live_profile=<profile>` so a replay miss cannot silently
   fall through to an ambient backend; missing profile fails closed in the story
   view before runner or agent dispatch. Live-budgeted pending mode must still provide
   `ticket_repo` and `gh_agent_public_base_url` up front because those values
   are required for the autonomous file/fix/close-out gates after capture. The
   hosted gh-agent health/readiness checks must pass before the story returns a
   driver handoff for live capture; readiness must identify the same ticket repo
   and a draining worker.
   For a standing cadence tick, submit `autonomous_marathon_advance_due`. The
   story scans local marathon control artifacts, picks the next due active
   control, and advances it through the same native `autonomous_marathon` path
   as a fresh cycle. If nothing is due, or due controls are blocked, the story
   returns an explicit no-op/blocked status instead of asking an operator or
   agent to copy a command.
8. If there are no credible issue findings, or after `autonomous_fix` reports
   the bundle valid, submit `review` and `validate` through the story. Use
   `file_findings` or the CLI `--file-findings`/`--review-run`/`--validate-run`
   commands only as fallback/debug surfaces when a story session is unavailable,
   not as the canonical autonomous path.

Review `deck.slidey.json` for the narrative, `Playback evidence` scenes,
`Proof gates`, `Persona lens`, `Driver contract`, and `PRD/design routes`
scenes.

## Story Surface

When driving through Kitsoki itself, open `stories/product-journey-qa/app.yaml`.
Useful intents:

- `validate_corpus`
- `capture_preflight`
- `matrix seed=... matrix_personas=primary|all`
- `driver_replay_smoke scenario=... persona=... seed=...`
- `driver_replay_sweep persona=... seed=...`
- `native_ghagent_smoke`
- `autonomous_fix_smoke`
- `persona_autofix_smoke`
- `autonomous_marathon_due`
- `autonomous_marathon_advance_due`
- `autonomous_marathon_smoke repeats=<n>`
- `validate_marathon_smoke_ledger min_cycles=<n>`
- `start project=... persona=... seed=...`
- `load run_dir=...`
- `handoff`
- `attach`
- `record`
- `blocker`
- `file_findings ticket_repo=owner/repo mode=dry-run`
- `dispatch_driver`
- `autonomous_watchdog`
- `autonomous_fix ticket_repo=owner/repo gh_agent_public_base_url=<url>`
- `driver_event`
- `validate_matrix_strict`
- `review`
- `validate`

Prefer the story as the write surface when an operator session is attached.
Use CLI fallback when the story session is unavailable.
When the goal is the full issue-to-fix loop, prefer `autonomous_fix`: it files
credible findings with evidence, enqueues and drains gh-agent fixes, refreshes
review artifacts, and validates the bundle in one story-owned reliability gate.
The underlying fallback command is `kitsoki gitops autonomous-fix`; do not ask
operators or agents to run raw `gh` commands or direct gh-agent plumbing for the
product-journey loop.
The story stores gh-agent queue state for `autonomous_fix` at
`<run_dir>/gh-agent-jobs.sqlite` by default; pass `gh_agent_db=<sqlite>` only to
override that run-local path. `file_findings` is filing-only and must not queue
or drain gh-agent fixes.
The CLI exits nonzero for an invalid autonomous loop by default; the story uses
`--report-invalid-autonomous-fix` so failed gate details bind into world state
for review instead of disappearing behind a host error.

## Improvement Loop

When refining the pipeline:

1. Identify the missing proof from `review.json`, `validation` output,
   `driver-handoff.md`, or a failed flow.
2. Patch the smallest durable surface: catalog, runner, story, driver agent, or
   this skill.
3. Add or update a deterministic flow/cassette/replay check.
4. Re-run `--validate-corpus`, `--driver-replay-sweep`,
   `--gate ghagent,autonomous-fix,persona-autofix,autonomous-marathon`, and
   product-journey story flows.
5. Commit only the product-journey slice, leaving unrelated workspace dirt
   untouched.
