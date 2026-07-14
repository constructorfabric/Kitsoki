# Epic: Usable Kitsoki — free-form productivity on the WS substrate

**Status:** All six slices shipped. S1 (`docs/architecture/room-workbench.md`),
S3 (`docs/architecture/hosts.md`#cache-usage-visibility-and-the-pre-dispatch-budget-gate,
`docs/stories/state-machine.md` §8), S4 (`docs/tracing/scenario-foundry.md`),
S5 (`docs/architecture/github-agent.md`), and S6
(`docs/tracing/usable-kitsoki-gate.md`) all have shipped-record child
proposals migrated to `docs/` (child files for S1/S3 deleted per lifecycle
guidance; S4's `scenario-foundry.md` kept as a historical design record).
**One item stays honestly open** — this epic's own release-readiness bar
(shared decision 8) is not fully closed: the epic-finalization live-gate run
(2026-07-05, `run_live_gate.py --live-gate`, real `opus` dispatches, MCP
surface) confirmed `stories/dev-story` live-green (2/2 real cells, zero
silent bounce, zero misroute-adjacent) but captured **zero turn signal** on
`pets-dev`/`slidey-dev` (a reproducible "session opens, no turn ever drives"
anomaly, ruled out as stale-binary/app-load, not yet root-caused — see
[`live-run-summary.md`](../../tools/arena/tests/fixtures/usable-kitsoki-gate/live-run-summary.md)).
This file stays open, trimmed to that one remaining item, until a re-run
(now with the hardened orchestrator-log capture) either confirms or
root-causes those two targets — see Shared decision 8 below.
**Kind:**   epic
**Slices:** 6 (6/6 shipped; epic-level live sign-off partial — see Status)

## Why

Generalized-usage delivered the *substrate* the product thesis needs —
an enforced effect taxonomy, one toolbox-enforcement policy for all
agent verbs, a supervised sandbox, and offline conformance lint (all
landed; see `docs/proposals/agent-capability-model.md`) — but
deliberately did not build the free-form driver on top of it. Today the
engine's only general escape hatch, `maybeOffRamp`
(`internal/orchestrator/offpath.go:337-422`), is **read-only by
construction**: it fires one `host.agent.converse` turn that does not
advance state, call hosts, or mutate world
(`internal/orchestrator/offpath.go:352-354`). The moment a user's input
isn't an authored intent, kitsoki degrades from "agent that does the
work" to "chatbot that talks about the work." Compounding that: prose
routinely misroutes to adjacent commands (five fix branches in one
week culminated in semantic routing shipping **off by default**,
`8a1f42b1`), and 1,132 `on_error:` arcs across stories silently bounce
failures back to `idle`/`landing` with no explanation — a whole skill
(`kitsoki-debugging`) exists purely to recover what the engine
swallowed. On the GitHub-agent surface the same honesty problem shows
up in public: `@kitsoki` on an issue runs `stories/bugfix` against a
beat fixture that stubs every agent call
(`internal/ghagent/testdata/bugfix.beat.yaml:26-33`) and reports
"Done" having made no change. None of this is a hidden defect — an
evaluator hits R1–R5 in the first session, and three of them are
honesty violations of kitsoki's own moat.

The one place the free-form-agent-on-a-room pattern already works is
dev-story's landing room (`stories/dev-story/rooms/landing.yaml`), but
it's hand-built into the flagship story rather than generalized into
an engine primitive, and every test harness that could validate a
generalized version simulates only clean, pre-authored interactions —
none of them can express the corrections, retries, and abandonments
that are exactly where the UX fails. Building the free-form workbench
without first being able to test it against realistic usage repeats
the pattern that produced today's gap (R8: flows can green-mask live
failures).

## What changes

Once every slice ships: every room may declare a `workbench:` — a
full-tool `host.agent.task` loop bound to *that room's* WS toolbox,
sandbox, and effect class — and unmatched free text falls into the
workbench (which can do real work) instead of the read-only off-ramp.
Every `on_error:` traversal renders the error at least once before
resting, and near-miss routing goes to the workbench rather than an
adjacent command. Dispatch is cheap: the stable story prefix is
cache-friendly, the double journey read is gone, and a workbench turn
costs a small marginal-token budget instead of a ~68k-token floor. A
Scenario Foundry compiles real Claude Code and Codex conversations —
personas, corrections, abandonments included — into flow fixtures,
swarm scripts, and product-journey scenarios, so the workbench (and
everything else) is judged against real usage, not hand-authored happy
paths. `@kitsoki` on a GitHub issue either runs the real pipeline in a
per-job worktree or honestly reports "acknowledged — pipeline not yet
enabled," never fakes "Done." And a standing release gate runs mined
scenarios at swarm scale and blocks release on silent bounces,
misroutes, or a workbench that doesn't complete a minimum share of
what the mined session's real agent completed.

## Impact

- **Spans:** runtime (orchestrator routing/off-ramp/error paths, host
  agent dispatch, ghagent), story (`workbench:` block; dev-story
  landing becomes the primitive's first consumer), tracing (scenario
  IR consumes traces; the parity metric consumes both mined corpora),
  tooling (session-mining, swarm, product-journey, arena), TUI/web
  (workbench turns render as ordinary agent turns — thin surface work).
- **Net surface:** one `workbench:` YAML block + loader validation, a
  single routing/off-ramp decision point replacing four call sites, an
  error-rendering invariant with a G-FLOW gate, a prompt-cache +
  dispatch-shape change in `internal/host`, a codex session-mining
  adapter + scenario IR + compilers in `tools/session-mining`, a
  per-job-worktree ghagent dispatch path, and one new arena job type
  (`usable-kitsoki-gate`).
- **Docs on ship (as landed):** `docs/architecture/overview.md`
  (routing/off-ramp section), `docs/stories/state-machine.md`
  (`workbench:` schema + the turn-loop double-read fix),
  `docs/architecture/room-workbench.md` (the primitive's narrative home),
  `docs/architecture/hosts.md` (cache-usage/budget-gate + ghagent honesty
  contracts), `docs/architecture/github-agent.md`, `docs/tracing/scenario-foundry.md`,
  and `docs/tracing/usable-kitsoki-gate.md` (the release gate's narrative
  home).

## Slices

| # | Slice | Kind | Scope (one line) | Depends on | Status | File |
|---|---|---|---|---|---|---|
| S1 | Room workbench primitive | runtime + story | Generalize dev-story's landing into an engine primitive: any room may declare `workbench:`, a full-tool `host.agent.task` loop bound to the room's WS toolbox/sandbox/effect class and constructed context, with deterministic seams (explicit `initial_world` injection) under any free text it accepts. Unmatched free text falls into the workbench, not the read-only off-ramp. | GU base reconciled onto main; WS (landed) | Shipped (child proposal deleted per lifecycle guidance) | [`../architecture/room-workbench.md`](../architecture/room-workbench.md) |
| S2 | Never-silent runtime | runtime | Every `on_error:` traversal renders the error at least once before resting, gated by a G-FLOW invariant across all stories; near-miss routing goes to the workbench, never an adjacent command; one routing decision point instead of four call sites. | — | Shipped | [`semantic-routing.md`](../architecture/semantic-routing.md#13-synonym-templates), [`state-machine.md`](../stories/state-machine.md#on_error-redirects-and-the-recursion-cap), [`overview.md`](../architecture/overview.md#3-the-journey-of-one-turn) |
| S3 | Dispatch context floor | runtime | Prompt-cache the stable story prefix, stop re-sending the full serialized story per dispatch, remove the double journey read, add a budget/early-escalation gate. Target: <15k marginal tokens per workbench turn. | — | Shipped (child proposal deleted per lifecycle guidance) | [`../architecture/hosts.md`](../architecture/hosts.md#cache-usage-visibility-and-the-pre-dispatch-budget-gate), [`../stories/state-machine.md`](../stories/state-machine.md#8-the-turn-loop-state-machine-of-the-orchestrator) |
| S4 | Scenario Foundry | tracing + tooling | Codex rollout parser; a scenario IR (persona, goal, ordered turns incl. correction turns and abandonments, expected effects) compiled from mined intents/outcomes/satisfaction; compilers from the IR to flow fixtures+recordings, swarm tier-2 scripts, and product-journey scenarios/personas; a paraphrase tier so free-text realism isn't bottlenecked on exact-match recordings. | — (parallel with S1–S3) | Shipped | [`../tracing/scenario-foundry.md`](../tracing/scenario-foundry.md) (child proposal `scenario-foundry.md` kept as historical design record) |
| S5 | Honest gh-agent issues | runtime (ghagent) | Issue routes drive the real bugfix pipeline in a per-job worktree with a live-or-replay harness defaulting to replay (fixing the `KITSOKI_APP_DIR` process-global race scoped to the load span); `stories/dev-story` and PR routes still honestly report "acknowledged — pipeline not yet enabled," never "Done," pending their own recorded cassettes. | S1, S3 | **Shipped** | [`github-agent.md`](../architecture/github-agent.md) |
| S6 | Release gate: the realism bar | tooling | An arena job type (`usable-kitsoki-gate`): mined scenarios (S4) × personas × surfaces run at swarm concurrency, gated on zero silent bounces, zero misroute-to-adjacent-command, and a parity metric (workbench completes ≥X% of what the mined session's real agent completed), with evidence bundles on every failure. Its parity metric is defined day one so S1 develops against it. | S1–S5 | Shipped (`usable-kitsoki-release-gate.md` deleted per lifecycle guidance; the epic-level LIVE gate run over dev-story + a second story is the epic's own definition-of-done, executed at finalization, not part of this slice) | [`usable-kitsoki-gate.md`](../tracing/usable-kitsoki-gate.md), [`arena/README.md`](../../tools/arena/README.md) |

Full evaluation and evidence trail: `.context/2026-07-05-usable-kitsoki-evaluation-and-proposal.md`
(gitignored; not the durable record — this file and its children are).

## Sequencing

```
S2, S3, S4 ──▶ start immediately, in parallel (S2/S3 engine-local, S4 tooling-local)
GU base reconciled onto main (existing top GU action, not a slice here)
       └──▶ S1 (needs WS + worker-brief + ladder landed)
                  └──▶ S5 (needs S1's loop to run a job, and S3 for affordability)
S1 + S2 + S3 + S4 + S5 ──▶ S6 (assembles last; parity metric spec fixed day one)
```

WB.4/WB.5 (cost-benchmark training/held-out rounds), goal-seeker
elegance item 4 (headless run-to-done), and R10 (cost-meter accuracy)
continue in the existing GU goal and P0 cost-meter work — they are
prerequisites for *marketing* claims, not for this epic's slices.

## Shared decisions

1. **Unmatched free text falls to a work-capable governed workbench by
   default.** The read-only off-ramp remains only for rooms that
   explicitly opt out of work.
2. **Structured dispatch uses deterministic seams** — explicit
   `initial_world` injection — never LLM-mediated seeding. This is a
   hard GU cycle-8 lesson (three independent live failures at the same
   seam): an LLM operator won't reliably pass `initial_world`.
3. **WS effect/toolbox declarations are the only permission
   mechanism** the workbench honors. No new permission system.
4. **Realism fixtures come from mined conversations** via the scenario
   IR (S4); hand-authored scenarios are gap-filling only.
5. **No surface says "Done" without a verifiable effect** — applies to
   gh-agent status comments, flows-vs-live gaps, and any workbench
   turn alike.
6. **S6's parity metric is binary completion + evidence bundles**, not
   a scored rubric. Adopted from the draft's recommendation (open
   question below); overridable by Brad before S6 is cut.
7. **S4's paraphrase tier starts with semantic-match recordings**
   (reuses the existing replay harness), with a local paraphraser as a
   fallback only if semantic-match proves insufficient. Adopted from
   the draft's recommendation; overridable by Brad before S4 is cut.
8. **S1 rolls out behind a per-story opt-in flag** until the S6 gate is
   green on dev-story plus two other stories, then the default flips
   repo-wide. Adopted from the draft's recommendation; overridable by
   Brad before S1 is cut. **The `workbench:` block on a room IS the
   opt-in** (not a separate flag) — a room carries it, or it doesn't.
   Per-story status as of the live-gate epic-finalization run (one real
   `run_live_gate.py --live-gate` pass, 2 calibration scenarios x all 3
   real `workbench:` targets, `mcp` surface; full detail:
   [`live-run-summary.md`](../../tools/arena/tests/fixtures/usable-kitsoki-gate/live-run-summary.md)):
   - **`stories/dev-story`** — carries `workbench:` (the hand-authored
     primary, `rooms/landing.yaml`). **Live-green**: 2/2 real cells, zero
     silent bounce, zero misroute-adjacent, real `opus` dispatches that
     correctly stayed read-only on a mutating ask. Per open question 1's
     "a story that's green shouldn't wait on siblings" lean, `dev-story`'s
     opt-in is confirmed live on its own.
   - **`stories/pets-dev`** / **`stories/slidey-dev`** — both carry
     `workbench:` too (inherited unmodified from `dev-story` via
     `@kitsoki/dev-story` import-folding; confirmed present and wired by
     `internal/orchestrator/conversational_default_intent_test.go`), so
     the opt-in is structurally in place. **Not yet live-confirmed**: this
     run captured zero turn signal for either (a reproducible "session
     opens, no turn ever drives" anomaly — ruled out as a stale-binary or
     app-load issue, not yet root-caused; see live-run-summary.md). Their
     live sign-off stays open pending a re-run (the harness was hardened
     this run to persist the orchestrator's own log for exactly this
     case) — this is a live-gate-harness gap being investigated, not an
     observed workbench defect on either story.
9. **Every mined fixture passes session-mining's `redact.py` ladder**
   before it is committed; the unredacted scenario IR stays
   local/gitignored, consistent with the licensing quarantine
   (strategic-plan P0.5).

## Cross-cutting open questions

1. **Interaction between S1's opt-in flag (decision 8) and S6's gate.**
   Does the flag flip per-story as each passes S6, or only once after
   all three pilot stories pass together? *Lean: per-story — a story
   that's green shouldn't wait on siblings still hardening.*
2. **Where does the parity metric's baseline agent come from** — the
   real Claude Code / Codex session recorded in the mined transcript,
   or a fresh baseline run captured at S6 build time? *Lean: the mined
   transcript's own outcome, since that's what "productive like the
   free-form agent it competes with" is measured against; S6's author
   should confirm this doesn't require re-running the original agent.*

## Non-goals

- LINE, the generic feedback SDK, and trainable stories — parked per
  the existing GU goal, untouched here.
- Multi-tenant gh-agent install and viewer auth (slice 5 of the
  existing `kitsoki-github-agent.md` epic) — this epic only fixes
  issue-dispatch honesty (S5), not tenancy or auth.
- WB.4/WB.5 execution (cost-benchmark training/held-out rounds).
- The object-graph lane.
- Fixing the cost meters themselves (existing P0 work; R10 stays there).
- `ad-hoc-workbench.md` was **retired when S1's proposal (`room-workbench.md`)
  was cut** — its Slices 2-4 content is not lost; a future proposal can
  retarget the miner at `workbench:` rooms once S1 ships.
  `conversation-driven-development.md` slice 1 is **absorbed by S4** —
  S4's author retires that slice (and trims or retires the parent
  proposal) once the scenario IR lands. This epic does not edit
  `conversation-driven-development.md` directly.

<!--
  Lifecycle: as each slice ships, update its row's Status and migrate its
  detail into docs/ per that child's own plan, then delete the child file.
  When every slice has shipped, the epic is just an empty index — delete it
  too. Git history preserves the decomposition.

  Epic-finalization checkpoint (2026-07-05): all six slices' child work has
  shipped and migrated (S1/S3 child proposals deleted; S4's kept as a
  historical record; S2/S5/S6 already had no child file to delete). This
  file is NOT yet an empty index because the epic's own definition-of-done
  (shared decision 8: S6 green on dev-story + two other stories) is only
  PARTIALLY met — see the Status line and decision 8 for the live-gate
  anomaly on `pets-dev`/`slidey-dev`. Delete this file once a re-run either
  confirms both (decision 8 flips repo-wide) or root-causes and fixes the
  anomaly; until then it is the one remaining honest record of epic-level
  open work.
-->
