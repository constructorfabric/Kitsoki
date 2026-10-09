# tools/swarm — the swarm mix model (tiers 1-3)

Puts concurrent Playwright browser contexts on **ONE shared `kitsoki web`
server**, to soak the multi-session registry (`cmd/kitsoki/registry.go`) the
way real concurrent users would — something nothing in the repo did before
tier 1 (see `docs/goals/ui-qa-scale/plan.md` and `decomposition.yaml`'s
`swarm-tier1`/`swarm-tiers23` entries for the full design rationale). Three
tiers, increasing realism and cost:

| Tier | Realism | LLM spend | Gate |
| --- | --- | --- | --- |
| 1 — scripted replay users | explicit intents only, no free text | zero | `swarm-replay-users.spec.ts` (standing CI) |
| 2 — cassette-agent users | real free text, routed by a recorded `--harness replay --recording ...` session | zero | `swarm-cassette-users.spec.ts` (standing CI) |
| 3 — live persona explorers | autonomous LLM-driven browsing | REAL, every run | manual only, gated behind `--live-explorers` (contract stub-tested in CI) |

## Tier 1 — scripted replay users

## What's here

- `personas.ts` — loads the shared `tools/product-journey/personas.json`
  catalogue and derives the one behavioral lens tier 1 uses per persona
  (`watchesTrace`).
- `journey.ts` — `openUserSession` (mint + open, phase 1) and
  `driveUserJourney` (the scripted clicks, phase 2) drive one user's page
  through the PRD story's `happy_path` flow fixture, calling an injected
  `audit` callback once per distinct FSM state reached. `assertIsolated`
  is the per-user isolation check.
- `isolation.ts` — the strict, trace-based isolation assertion + marker
  helper (see its doc comment for why the session TRACE, not the rendered
  page, is the ground truth here).
- `audit.ts` — thin policy layer over `tools/runstatus`'s `ui-audit.ts`
  (severity gating + a `SharedAxeGate` so axe's expensive pass runs once per
  distinct page state, not once per user per state).
- `rss.ts` — informational server-RSS watcher (no cap yet — that's the
  separate `swarm-session-cap` change; this only records a baseline).
- `retry.ts` — bounded retry-with-backoff for RPC calls that hit a known
  transient store-busy error (see "Known findings" below).
- `concurrency.ts` — a tiny in-process concurrency limiter used to throttle
  how many users' INTERACTIVE steps run at once (see "Known findings").
- `results.ts` — the per-run results JSON schema + writer
  (`.artifacts/swarm/results-<run_id>.json`).

The actual Playwright spec that wires these together lives at
`tools/runstatus/tests/playwright/swarm-replay-users.spec.ts` (Playwright specs
must live under `tools/runstatus/tests/playwright/`; the driver library lives
here so it can be reused by later tiers/changes without duplicating it into
that directory).

## Why no dependencies (just a `{"type": "module"}` marker) here

`package.json` here is a **one-line ESM marker only** (`{"type": "module"}`) —
without it, Node's ESM loader walks up looking for the nearest `package.json`
to decide whether a `.js` file is CommonJS or ESM, and this repo has none at
the root, so it would default to CommonJS and reject `tools/swarm`'s named
exports. It carries no `dependencies`/`devDependencies` and there is no
`node_modules` here — the package is TypeScript-only, imported via relative
paths from
`tools/runstatus/tests/playwright/swarm-replay-users.spec.ts`, which is where
`npx playwright test` actually runs (`cd tools/runstatus && npx playwright
test tests/playwright/swarm-replay-users.spec.ts`). Node resolves each
module's bare-specifier imports (`@playwright/test`, `@axe-core/playwright`,
etc.) relative to **that module's own location**, not the entry file's — so a
file physically outside `tools/runstatus/` can still safely `import` a
`tools/runstatus` helper (e.g. `_helpers/server.ts`) at runtime, because that
helper's own imports resolve from its own directory chain, which DOES have
`tools/runstatus/node_modules`.

The one thing this repo would NOT let `tools/swarm/` do safely is import an
npm package directly (e.g. `@axe-core/playwright`) — `tools/swarm/` has no
`node_modules` of its own and isn't an ancestor of `tools/runstatus/node_modules`.
That's why the axe-core (`AxeBuilder`) and Playwright-runtime calls stay in the
spec file itself, while `tools/swarm/audit.ts` only takes a **type-only**
import of `ui-audit.ts`'s `RawFinding`/`Severity` (erased at build time, never
resolved at runtime) and accepts the actual finding arrays as plain data.

If a later tier needs real npm dependencies of its own, promote this to a real
workspace with its own `package.json` at that point — don't add one
speculatively now.

## What tier 1 asserts, per user

1. **Completion** — the scripted journey reaches "drafting", the last state
   this flow fixture can genuinely reach live (see "Known findings" —
   the flow's own `accept` step has a pre-existing gap, not a swarm concern).
2. **Strict session isolation** — a unique per-user marker (sent in the first
   free-text message) must never appear in another session's TRACE. See
   `isolation.ts` for why the trace, not the rendered page, is the ground
   truth.
3. **Zero console errors** — no `console.error` / uncaught page errors during
   the user's journey.
4. **UI audit clean** — `ui-audit.ts`'s `geometryProbe` (per user, per
   distinct state) plus a shared axe-core pass (once per distinct state,
   across the whole run — see `audit.ts`'s `SharedAxeGate` doc comment) report
   zero `error`-severity findings.

Server RSS is watched across the whole run (`rss.ts`) and recorded in the
results JSON — informational only; there is no cap or RSS assertion in this
tier (a future change, `swarm-session-cap`, adds the actual bound).

## Negative control

`swarm-replay-users.spec.ts` includes a dedicated test that seeds a real
cross-talk fault: two browser pages are deliberately pointed at the SAME
minted session id (simulating the exact registry bug this tier's isolation
gate exists to catch), and asserts `isolation.ts`'s `checkIsolation` correctly
flags the leak (found in the shared session's own trace). This proves the
gate can go red for the right reason without making the standing gate flaky —
the fault is seeded, not incidental.

## Known findings (out of this change's scope to fix — tools/swarm/** and the
spec file only; recorded here so they aren't lost)

1. **PRD `accept` never reaches `@exit:done` under `kitsoki web --flow`.**
   The `drafting` room's `on_enter` writes `world.prd_artifact` via the
   STUBBED `host.agent.task` (canned metadata only — no real file lands on
   disk), but the `accept` arc's real (unstubbed) `host.starlark.run`
   "publish" call reads that file back off disk and fails with `no draft at
   .artifacts/prd/example-cli/004-prd.md`. Reproduced with a bare RPC driver
   (no browser, no concurrency at all — a single `runstatus.session.submit`
   call), so it is not a swarm-scale artifact; the pre-existing
   `multi-story.spec.ts` drives this exact same `accept` step and hits the
   identical failure on this branch. `kitsoki test flows` passes the same
   fixture because `test_kind: flow`'s `starlark_inspect_cassette` masks the
   consequence in a way `kitsoki web --flow` does not. Tier 1's journey ends
   at "drafting" (the last state genuinely reachable live) rather than
   papering over this with a fake completion.
2. **Per-session `store.Open()` under a mint burst.** `cmd/kitsoki`'s runtime
   opens a fresh `*sql.DB` per minted session against the SAME shared SQLite
   file rather than sharing one connection/pool across the registry. A true
   burst of concurrent `session.new` calls can hit a transient
   `SQLITE_BUSY`/"database is locked" on that connection's very first pragma
   before its own `busy_timeout` is in effect. `retry.ts` absorbs this with a
   bounded client-side retry (realistic client behavior under overload), but
   the underlying design — one connection per session against one file — is
   a real scalability limitation worth its own fix.
3. **Turn-processing throughput under a true 24-way burst.** With all 24
   sessions minted and live, letting every user's remaining scripted clicks
   fire with no throttling causes some users' turns to queue for well over a
   minute; a live snapshot during a run showed the `kitsoki` server process
   near-idle on CPU while wall-clock latency still ballooned — consistent
   with SQLite single-writer lock contention (threads blocking, not
   spinning) rather than a CPU bottleneck. `concurrency.ts`'s
   `INTERACTIVE_CONCURRENCY` (default 3, override with
   `SWARM_INTERACTIVE_CONCURRENCY`) throttles the harness's OWN request
   concurrency to what this environment's turn-processing path can sustain —
   every session is still minted and its page kept open for the whole run,
   so "24 concurrently live" is never compromised, only how many are
   simultaneously mid-turn.

## Running it

```
cd tools/runstatus
npx playwright test tests/playwright/swarm-replay-users.spec.ts
```

Requires `make web && make embed-stories` once beforehand (the go:embed'd SPA
+ story catalogue) and a Playwright chromium install
(`npx playwright install chromium`). No LLM, no docker, no network egress.

Override the user count with `SWARM_USERS` (defaults to 24; the gate requires
>= 24) and the interactive-turn throttle with `SWARM_INTERACTIVE_CONCURRENCY`
(defaults to 3 — see "Known findings" #3 for why this isn't higher by
default).

## Tier 2 — cassette-agent users

Adds FREE-TEXT REALISM while staying zero-LLM: instead of clicking a
pre-selected intent, a tier-2 user types a real sentence, and a
`--harness replay --recording ...` session (an interpreter harness backed by
a hand-authorable recording — NOT tier 1's nil-harness `--flow` fixture)
routes it deterministically, with a `--host-cassette` answering whatever
`host.*` call the routed path fires.

**Fixture reuse, not a new story.** Rather than hand-authoring a new
story/recording/cassette trio, tier 2 reuses `stories/off-ramp-demo` verbatim
— it already exercises exactly this posture end-to-end
(`off-ramp-video.spec.ts` proved it live first) via its "agent off-ramp"
feature: an off-menu question is answered IN PLACE (no state change) through
`host.agent.converse`, stubbed by `assets/converse-cassette.yaml`
(`match_on: [handler]`, so the SAME canned answer backs every user's question
regardless of its exact text). The one derived artifact tier 2 generates at
test time is a SMALL per-run recording (`tools/swarm/tiers/tier2.ts`'s
`buildTier2Recording`) — one entry per swarm user, mapping that user's own
marker-bearing question to the shipped fixture's exact clarify response.
This is necessary, not decorative: `internal/harness/replay.go`'s
`ReplayHarness` matches recording entries by EXACT (case-insensitive,
trimmed) input text, so tier 1's "prepend a unique isolation marker" trick
would silently break the shipped recording's one hardcoded entry. Deriving N
near-identical entries from the real fixture keeps every user's utterance
resolving deterministically AND keeps a real per-user isolation marker in
play, without hand-authoring a giant new recording file.

**Coexisting with tier 1 on ONE server** (the acceptance criterion): tier 1's
own driver (`journey.ts`) is PRD-story-specific and can't run against
off-ramp-demo, and `kitsoki web`'s harness posture (nil vs. live) is
session-INVARIANT — one `runtimeBase` per server process, not one per
session (see `cmd/kitsoki/web.go`'s doc comments on `recordingPath` /
`flowPath` for the flow+live-harness coexistence path that DOES exist,
seeding-only). So `swarm-cassette-users.spec.ts` proves coexistence with a
tier-1-STYLE user expressed against the SAME off-ramp-demo story instead:
`tools/swarm/tiers/scriptedMixUser.ts` drives the explicit `browse` menu
intent — no free text, no interpreter routing, no recording entry needed at
all (see `stories/off-ramp-demo/assets/recording.yaml`'s own doc comment) —
which is tier 1's defining property (scripted, zero LLM), just against a
story that can share one live-harness server process with tier 2's
cassette-agent users.

What's here (`tools/swarm/tiers/`):
- `tier2.ts` — `buildTier2Recording` / `makeTier2ScratchDir` (the derived
  per-run recording), `openCassetteUserSession` / `driveCassetteUserJourney`
  (mint + free-text drive, mirroring `journey.ts`'s two-phase split),
  `assertCassetteIsolated` (thin re-export of `isolation.ts`'s trace-based
  check — story-agnostic, so tier 2 doesn't need tier 1's PRD-specific code).
- `scriptedMixUser.ts` — the tier-1-style scripted coexistence user
  described above.

Run:

```
cd tools/runstatus
npx playwright test tests/playwright/swarm-cassette-users.spec.ts
```

Same prerequisites as tier 1 (`make web && make embed-stories`, a Playwright
chromium install). No LLM, no docker, no network egress.

## Tier 3 — live persona explorers (gated, real LLM spend)

A small number (**hard-capped at 3**) of headless, LIVE persona agents, each
holding its own browser context against the shared swarm server, exploring
autonomously and journaling findings — genuine LLM spend on every run. This
tier is **OFF by default and structurally cannot start by accident**:

1. `tools/swarm/tiers/explorer.ts`'s `dispatchExplorers` is the ONLY way to
   run explorers, and it calls `assertLiveExplorersAllowed` FIRST — before
   touching personas, the runner, or the budget. That function requires
   `liveExplorers === true` with no default anywhere in the module.
2. The only place that boolean is ever constructed from something external
   is `tools/swarm/tiers/liveExplorerCli.ts`, and it is threaded from a
   literal `--live-explorers` flag in `process.argv` — never an env var,
   never a config default, never inherited from anything.
3. `resolveExplorerBudget` hard-clamps the explorer count to
   `MAX_LIVE_EXPLORERS` (3) regardless of what is requested — there is no
   code path that can ask for more than 3 concurrent live agents.
4. The standing CI gate (`swarm-cassette-users.spec.ts`'s "tier 3 — stubbed
   live-explorer dispatch contract" describe block) exercises
   `dispatchExplorers` with an injected STUB `runExplorer` — no browser, no
   subprocess, no LLM call — proving the refusal, the clamp, and the
   findings/blockers/cost aggregation contract stay correct without ever
   constructing a real explorer. `liveExplorerCli.ts` (the real wiring) is
   NEVER imported by any test.

**What a real explorer does** (`liveExplorerCli.ts`'s `runLiveExplorer`,
wired as `dispatchExplorers`'s `runExplorer`):
1. Mints its own session on the shared server and opens a headless
   Playwright browser context on it.
2. Spawns a real agent process (default `claude -p`, override with
   `--agent-cmd`) briefed with the persona's description and a lens mirroring
   `tools/product-journey/run.py`'s `persona_lens()` (starting surface, first
   question, evidence emphasis, escalation trigger, finding bias — see
   `docs/architecture/operator-ask.md` for why the agent proceeds solo
   instead of asking: `AskUserQuestion` is hard-denied headless). The agent
   uses kitsoki's MCP tool surface to explore and calls
   `python3 tools/product-journey/run.py --record-finding` /
   `--record-blocker` directly against `--run-dir` whenever it finds
   something.
3. After the agent exits, the Node/Playwright side (which owns the live
   `Page`) calls `tools/swarm/capture`'s `recordFinding` once, so every
   explorer leaves an rrweb+console+HAR evidence bundle under
   `.artifacts/swarm/findings/` regardless of what the agent chose to
   journal narratively — the same capture loop tier 1/2's per-user gate
   failures already use.

`liveExplorerCli.ts` resolves `@playwright/test`'s `chromium` lazily via
`createRequire` rooted at `tools/runstatus/package.json`, so it can launch a
real browser without `tools/swarm/` needing its own `node_modules` (see "Why
no dependencies" above) — that resolution only ever runs on the
`--live-explorers` code path; the CI stub test never reaches it.

### Manual live-acceptance procedure (real LLM spend — never run this in CI)

1. Create a product-journey run bundle to journal into:
   ```
   python3 tools/product-journey/run.py --project <id> --persona <persona-id> --emit-run
   ```
   Note the printed `.artifacts/product-journey/<run-id>` directory.
2. Start a shared `kitsoki web` server (tier 1/2's posture, or your own):
   ```
   go run ./cmd/kitsoki web --stories-dir stories --addr 127.0.0.1:7799 \
     --harness replay --recording stories/off-ramp-demo/assets/recording.yaml \
     --host-cassette stories/off-ramp-demo/assets/converse-cassette.yaml
   ```
3. Dispatch the explorers:
   ```
   node --loader tsx tools/swarm/tiers/liveExplorerCli.ts \
     --live-explorers \
     --server http://127.0.0.1:7799 \
     --story-path stories/off-ramp-demo/app.yaml \
     --run-dir .artifacts/product-journey/<run-id> \
     --max-explorers 3
   ```
4. Review `.artifacts/product-journey/<run-id>/findings.json` (narrative
   findings the agents journaled) and `.artifacts/swarm/findings/` (the
   rrweb/console/HAR evidence bundles the runner captured per explorer).

Budget a real dollar amount before running this — every invocation spawns up
to 3 live agent processes. There is no automatic spend cap beyond the
explorer-count clamp; treat `--max-explorers` and your own agent-backend
quota/rate limits as the actual budget control.
