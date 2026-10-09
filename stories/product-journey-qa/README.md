# Product Journey QA Story

This story wraps the deterministic product-journey runner so operators can drive
the standard persona/scenario evidence workflow inside Kitsoki.

```sh
kitsoki run @kitsoki/product-journey-qa
```

For the narrow "check one scenario across transports" case, use
[`stories/scenario-qa`](../scenario-qa/README.md) (`kitsoki run
@kitsoki/scenario-qa`) instead — this story is the broader persona x scenario x
10-repo matrix, marathon, and autonomous-fix surface. See [Naming](#naming)
below.

## Naming

One product, four names:

| Name | What it is |
|---|---|
| **Persona QA** | The product ([`docs/persona-qa.md`](../../docs/persona-qa.md)). |
| **`scenario-qa`** | The narrow one-scenario x N-transports story ([`../scenario-qa/README.md`](../scenario-qa/README.md), alias `@kitsoki/persona-qa`). |
| **`product-journey-qa`** | This story — the broader persona x scenario x 10-repo matrix, marathon, and autonomous-fix surface. |
| **`product-journey`** | The deterministic runner backend this story drives ([`../../tools/product-journey/README.md`](../../tools/product-journey/README.md)). |
| **`persona_qa`** | A shared support kit, not an operator surface. |

It is intentionally no-LLM:

- `validate_corpus` calls `tools/product-journey/run.py --validate-corpus
  --json-output` to preflight personas, scenarios, quality gates, evidence
  hints, and the 10-repo GitHub target catalog before a sweep.
- `refresh_targets` calls `tools/product-journey/run.py
  --refresh-github-targets --json-output` to write a GitHub target-proof
  artifact for the 100-open-bug and popularity matrix contract.
- `load` calls `tools/product-journey/run.py --summarize-run --json-output` to
  load an existing run bundle into the story. This is the MCP-only driver entry
  point when the reusable driver receives a `run_dir` and needs to attach
  evidence through Kitsoki instead of reading files directly. After `load`, the
  story world `last_result` contains `driver_scenarios`,
  `next_driver_capture_route`, `missing_proof_evidence`, and
  `driver_final_gates` for the driver to inspect through MCP. The route is the
  deterministic setup/recording entrypoint for the first missing proof slot.
- `matrix` calls `tools/product-journey/run.py --emit-matrix --json-output` to
  create the 10-repo GitHub assignment plan. Pass `target_proof_file=...` after
  `refresh_targets` when the matrix should embed current GitHub proof.
- `dogfood_smoke` calls `tools/product-journey/run.py --gate dogfood
  --json-output` to prove the no-LLM artifact loop from matrix creation through
  assignment run, seeded evidence, review, validation, rollup, and Slidey decks.
- `driver_replay_smoke` calls `tools/product-journey/run.py
  --gate driver-replay --json-output` to prove one reusable-driver scenario
  with cassette-backed proof evidence, linked driver journal refs, media
  manifest coverage, review, validation, and a compact Slidey smoke deck. Pass
  `scenario=project-onboarding`, `scenario=prd-design`, or another scenario id
  to exercise a specific journey path.
- `driver_replay_sweep` calls `tools/product-journey/run.py
  --driver-replay-sweep --json-output` to run the same replay proof for every
  product-journey scenario and summarize playback/validation coverage.
- `persona_autofix_smoke` calls `tools/product-journey/run.py
  --gate persona-autofix --json-output` to prove that a persona replay bundle
  with an observed issue finding enters the native `kitsoki gitops
  autonomous-fix` gate and produces filed issue, gh-agent run, fix evidence,
  and `independent-verify.md` artifacts without live GitHub or LLM work.
- `autonomous_marathon_smoke` calls `tools/product-journey/run.py
  --gate autonomous-marathon --json-output` to prove the standing-loop shell
  across the core `core-use-cases` scope (`project-onboarding`, `prd-design`,
  and `bugfix`) for every active curated persona: scoped persona runs, replayed
  driver proof, target/persona/lens preservation in the driver and brief
  contracts, credible issue filing, gh-agent fixes, independent verification,
  integration landing proof, retained JSON/Markdown ledger artifacts,
  human-review artifacts, and mechanically derived found/filed/fixed stats. Use
  `autonomous_marathon_smoke repeats=<n>` when the no-LLM gate should prove
  multiple full active-persona cycles instead of a single sweep.
  Re-check a retained ledger through the story with
  `validate_marathon_smoke_ledger`, which defaults to the last smoke ledger, or
  pass `marathon_smoke_ledger=<path>`. Use
  `validate_marathon_smoke_ledger min_cycles=<n>` when a proof must show many
  completed cycles, not just a single retained sweep. The underlying CLI
  validator is
  `tools/product-journey/run.py --gate marathon-ledger
  --marathon-smoke-ledger <path> --min-marathon-smoke-cycles <n>
  --json-output`. Every gate's JSON gains uniform `gate`/`gate_status`/
  `readiness_status` keys on top of its existing fields; see
  `tools/product-journey/README.md#gates`.
- `rollup` calls `tools/product-journey/run.py --rollup-matrix --json-output`
  to create or refresh the matrix-level Slidey deck from reviewed run bundles.
- `validate_matrix` calls `tools/product-journey/run.py --validate-matrix
  --json-output` to check the matrix and rollup artifact contract without
  rewriting files.
- `start` calls `tools/product-journey/run.py --emit-run --json-output`.
- `campaign_plan` is the product-facing standing-campaign entrypoint. It plans
  a reusable run from the shared persona/scenario catalog, defaulting to the
  universal campaign scenarios for docs-to-MCP first run, agent launch,
  remote-worker readiness, and campaign rollup review. It delegates to `start`
  so the generated run bundle, driver handoff, capture routes, and Slidey deck
  stay in the normal product-journey artifact shape.
- `attach` calls `tools/product-journey/run.py --attach-evidence --json-output`;
  include `source` when the driver knows whether the artifact is `retained`,
  `external`, `local`, or `cassette`.
- `campaign_attach` is the product-facing evidence writeback action. It calls
  the same attach runner as `attach`, then marks the campaign summary/deck as
  refreshed so operators can use one vocabulary for retained browser, TUI,
  MCP/session, rrweb, and worker evidence.
- `campaign_worker` calls `tools/product-journey/run.py --campaign-worker
  --json-output` to record a local, arena, or VM worker readiness receipt in
  the current run bundle. The receipt writes `campaign-worker-receipt.json/md`,
  names the backend, worker id, scenario scope, budget, readiness status,
  receipt source, and imported artifacts, then binds those fields back into the
  run page. This is the stable artifact seam for a future real VM/arena
  dispatcher; a blocked worker receipt stays visible and conservative instead
  of implying evidence capture happened.
- `record` calls `tools/product-journey/run.py --record-finding --json-output`
  for strengths, weaknesses, issues, and fixes.
- `blocker` calls `tools/product-journey/run.py --record-blocker --json-output`
  when a scenario was attempted but cannot honestly proceed without live
  authorization, a missing cassette, unavailable repo state, or another
  external prerequisite.
- `driver_event` calls `tools/product-journey/run.py --record-driver-event
  --json-output` to append what the reusable driver actually attempted to
  `driver-journal.md/json`.
- `autonomous_fix` calls the native `kitsoki gitops autonomous-fix` facade to
  file credible issue findings, claim each queued GitHub issue with a
  `kitsoki-autofix-claim` comment, drain gh-agent fixes, refresh review artifacts,
  require each completed fix run to publish `independent-verify.md`, post a
  `kitsoki-fixed-in` close-out comment, close the GitHub issue, write
  `autonomous-fix-report.md`, and validate the bundle without exposing raw
  `gh` or gh-agent plumbing as the operator contract. The native gate runs
  `autonomous_watchdog` before filing anything and returns a reviewable
  `autonomous_fix_invalid` result when the standing-loop control is missing or
  stale.
- `campaign_issue_fix` is the general issue/fix product verb, gated by a
  declared `finding_sink` policy (`finding_sink=local-artifact` by default, or
  explicit `finding_sink=github`):
  - `local-artifact` (default, no slots required) calls
    `tools/product-journey/run.py --file-local-findings` for every credible
    `issue` finding, which shells to `kitsoki bug create --sink local-artifact
    --target kitsoki` per finding and records the returned path as
    `item.local_ticket` in `findings.json`. This matches the repo-wide local
    developer/dogfood bug-loop convention (AGENTS.md): findings stay local
    under `.artifacts/issues/bugs/` and no GitHub filing or gh-agent repair is
    touched. Review a filed local ticket pile directly with `kitsoki bug list
    --sink local-artifact --target kitsoki` / `kitsoki bug show <id>`.
  - `github` (explicit opt-in; requires `ticket_repo` and
    `gh_agent_public_base_url`) delegates to `autonomous_fix` after binding the
    campaign ticket repo and hosted gh-agent URL into story state, running the
    full evidence-backed filing + gh-agent repair + independent verification +
    close-out gate.
  Both sinks satisfy the `findings-filed` review/validate gate for the
  findings they resolve. The GitHub-only gate chain (gh-agent drain, fix/
  triage/verify evidence, run URLs, integration landing, issue close-out,
  autonomous-fix report) only applies to credible findings that are still
  unresolved by the local sink, so a campaign run that only ever used the
  default local sink is never forced through GitHub filing/fixing it never
  requested. `campaign_worker` and `record`/`blocker` are unaffected by this
  policy; it only governs where credible `issue` findings get filed.
- `campaign_stats` delegates to `stats` for filed/fixed/reopened/similar issue
  counts across retained campaign artifacts.
- `campaign_deck_refresh` delegates to `review` to regenerate the Slidey deck
  and readiness summary from the current run. The deck remains conservative
  whenever evidence, playback, validation, issue, or worker gates are red.
- `campaign_tick` advances the next due standing campaign control artifact via
  the existing autonomous-marathon due path. It is the cadence verb a local or
  VM worker can call without copying runner commands together.
- `autonomous_marathon scenarios=core-use-cases autonomous_driver_mode=replay
  ...` creates a scoped run for onboarding, PRD/design, and bugfix, attaches
  cassette-backed local proof artifacts, records the driver journal and
  credible findings, then runs the same native gitops autonomous fix, review,
  validation, PRD weakness routing, close-out, and stats gates in one
  story-owned call. Missing `ticket_repo` or `gh_agent_public_base_url` fails
  closed in the story view as `autonomous_marathon_invalid` so the operator can
  review the invalid gate state without reading host errors. Omit the mode, or
  use `pending`, when a live driver should capture evidence before finalization;
  use `replay` for the no-LLM autonomous proof. Explicit `record` or `live`
  driver modes create the same control/report bundle, auto-queue the
  story-owned `dispatch_driver` intent, launch the reusable product-journey QA
  driver through `host.agent.task`, and then queue the native autonomous
  finalizer. They require `autonomous_driver_live_profile=<profile>` up front,
  so replay misses cannot silently fall through to an ambient live backend.
  GitHub filing/fixing stays inside the story-owned gitops/gh-agent gates; the
  driver must not call `gh` directly.
  Live pending mode also requires the hosted gh-agent `/healthz` and
  `/api/ready` checks to pass for the same ticket repo before returning a driver
  handoff.
  The direct `autonomous_fix` gate repeats those hosted gh-agent checks before
  filing anything, so loaded or separately finalized runs fail closed with
  `filing_status=not_run` when the agent is unhealthy or points at another repo.
  Each marathon run also writes `autonomous-marathon-control.json/md` with the
  cadence, per-scenario live budget, human role, heartbeat/watchdog timing, and
  final gates, and the story view surfaces that control state for review.
  Open weakness findings are also materialized as `prd-design-intake.json/md`:
  each item includes the `stories/prd` `start` handoff, upstream evidence paths,
  and the persona lens so usability gaps can enter the PRD/design path instead
  of being mistaken for bugfix queue work.
- `autonomous_watchdog` calls `tools/product-journey/run.py
  --autonomous-marathon-watchdog --json-output` to compare the latest
  `driver-journal.json` heartbeat with the marathon control policy. Stale runs
  write `autonomous-marathon-watchdog.json/md` and bind
  `autonomous_watchdog_blocked` into the story so the loop stops before spend
  with a human-reviewable blocker artifact.
  `autonomous_marathon` finalization enforces that same check before filing
  issues, draining gh-agent fixes, closing tickets, or deriving stats.
- `seed_demo` calls `tools/product-journey/run.py --seed-demo-evidence
  --json-output` to populate a no-LLM review bundle.
- `handoff` calls `tools/product-journey/run.py --driver-handoff --json-output`
  to refresh `driver-handoff.md/json` for the reusable QA driver without
  launching live LLM work.
- `review` calls `tools/product-journey/run.py --review-run --json-output` to
  write `review.json` and score whether the bundle is ready for human review.
- `validate` calls `tools/product-journey/run.py --validate-run --json-output`
  to check required files, metrics freshness, media coverage, scenario
  outcomes, review statuses, and Slidey scenes without rewriting files.
- Flow fixtures stub `host.product_journey.run`, so automated tests never call a live model or
  external service.

The generated run bundle under `.artifacts/product-journey/<run-id>/` is the
handoff point for visual MCP, Studio MCP, oracle results, and Slidey review.
Use `agent-brief.md` inside that bundle to drive the live persona session, then
use `driver-plan.md` for the scenario harness/visual-surface/action contract and
`execution-plan.md` to copy the generated `--attach-evidence` and
`--record-blocker` commands.
Use `driver-handoff.md` when handing the bundle to
`.agents/agents/product-journey-qa-driver.md`; it names the run directory,
driver inputs, dispatch modes, missing evidence, and final watchdog,
autonomous-fix, review, and validation commands without spending live model
calls.
Loaded runs expose `last_result.next_driver_capture`,
`last_result.next_driver_capture_route`, `last_result.next_driver_attach_command`,
and `last_result.next_driver_blocker_command` so the reusable driver can begin
with the first missing proof slot directly from story state. The route names
the primary story session, resolved tools, artifact path template, and writeback
commands; if that route is missing or unusable, the driver records an honest
blocker instead of improvising setup or faking evidence.
Use `driver-journal.md` to review the driver's attempted actions, MCP tools,
evidence references, blockers, and per-scenario status before judging whether a
run reflects natural product usage.
Captured media is indexed in `media-manifest.json` so the generated Slidey deck
can expose playback-ready videos and screenshots without scraping prose.
Scenario-level evidence and finding summaries are written to
`scenario-outcomes.md` for review and matrix rollups.
Run `review` before `validate`; review refreshes derived artifacts and only
marks a run ready when every scenario has evidence or an explicit blocker, while
validation deliberately catches stale or inconsistent bundles. The story view
surfaces review pass/total/fail/warn counts so operators can tell whether a run
is progressing against the full review gate set without opening `review.json`.
Validation views also show a compact issue summary, so the operator can see the
first failing or warning check IDs before opening the raw validator output.
`accept` is validation-gated in run, matrix, and dogfood rooms; if validation has
not reported `valid`, the story stays put and sets `status=accept_needs_validation`.
