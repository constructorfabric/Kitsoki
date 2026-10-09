# Plan — Stabilizing Kitsoki for Generalized Usage

**The referenceable plan for the `stabilize/generalized-usage` line of work.**
Worked by the `/goal` goal-seeker (`.agents/skills/goal/`, design in
`.context/goal-command-design.md`). Full audit + wall map:
`.context/generalized-usage-decomposition-overview.md`.

- **Target:** [`GOAL.md`](GOAL.md) — six criteria G1–G6 for a *stranger*.
- **Changes + gates:** [`decomposition.yaml`](decomposition.yaml) — this slice = WM + W1.
- **Integration base:** worktree `.worktrees/generalized-usage` on branch
  `stabilize/generalized-usage` (off `main`). Every change lands here via its own
  `.worktrees/gu-<id>` worktree, RED→GREEN, integrated under a merge lock; the base
  merges to `main` a wall at a time.

## How it's worked (dogfood — critical)

The goal-seeker is a **kitsoki story** (`stories/goal-seeker/`, change WM.0) — the
maximally-dogfooded form of `/goal`, not an out-of-engine script. It **composes
what already exists**: `punch-list` (load→drive→verify→report loop, deterministic
steps in `host.starlark.run`, live driving via `host.agent.task`) + `ship-it`
(lost-work-safe integrate→verify→cleanup in an isolated worktree) + `fleet`
(multi-worker dispatch), and adds an **`evaluate`** `host.agent.decide` gate that
reads a **bounded** preamble (`relevant_world` + a `host.starlark.run` projection
with detail-pointers) and emits `{verdict, summary, next_instruction}`, looping
until `done`. Bounded context is native (per-gate `relevant_world`); the session
**world + trace** are the ledger + log; workers are `kitsoki-mcp-driver` / limited
codex in disjoint worktrees; integration carries the **no-lost-work guarantee**.

`/goal` (Claude Code) is a **thin driver** that advances the goal-seeker *session*
one turn (`kitsoki turn` / MCP `session.drive`). The Python `goal.py` is a
transitional **reference oracle** — the Starlark port is tested against its golden
outputs, then it's deleted (WM.0 acceptance).

**Running status — a deterministic PM deck hierarchy (WM.3).** Reporting is the
project-management view, built with no LLM from ledger+log+trace and navigable by
clicking down:

```
Executive summary deck  (one stable bundle, refreshed each cycle — the PM top view:
   │                     G1–G6 status · per-wall progress · per-change state·gate·verdict)
   ├─▶ per-change deck + focused summary doc   (every integrated unit of work emits one)
   │        └─▶ real artifacts   (trace · git show <sha> · gate cmd · worktree · log line)
   └─▶ retrospective deck  (closeout — see Z.0)
```

Every unit of work produces a focused summary doc + its own deck; those roll up into
the exec summary; the user clicks links to navigate — nothing is inlined (same
summary+pointer discipline as the evaluator preamble).

**Closeout retrospective (Z.0, tracked).** When the goal reaches `done`, the
goal-seeker runs a `retrospective` room producing a **case-study review**
(`docs/case-studies/generalized-usage.md`, the genre of `bug-fix.md`) + a deck
linked from the exec summary: per-wall what-changed, DORA-style outcomes + cost,
pipeline strengths/weaknesses, friction findings, and the story hardening shipped —
each claim linking to its change deck/trace. It is a tracked change so it can't be
skipped.

The kitsoki fixes the story needs to run on a foreign repo / headless / parallel
(the **WM wall**: MCP driving `host.starlark.run` stories #37, headless profiles
#30, live prompt_path #32, foreign-repo targeting; plus WM.1 bounded-preamble
projection and WM.2 cross-provider decide model) are themselves generalized-usage
work — *fixing kitsoki to dogfood the goal advances the goal.* That recursion is
why WM goes first, and **any limitation the build hits is appended as WM.x, never
worked around outside the engine.**

## Execution model — many cheap parallel workers (WM.5, critical)

Work is spread across **many disjoint workers in parallel**, each assigned a model
by a **cheap-first policy**: **`synthetic.new` by default** (≈free — used wherever a
step tolerates it), then **claude-native / codex at low effort**, and frontier only
when a step provably needs it. Load is **spread across multiple provider quotas**;
on **quota exhaustion** a worker **rotates** to another profile/quota, and if none is
free the change **parks-with-ref gracefully** (never a hard fail, never lost work).
An **ongoing model-selection evaluation** records `(change × model × outcome × cost)`
and steers future assignments to the cheapest model that still solves each archetype —
maximizing the subscription limits (this feeds, and is fed by, the WB benchmark's H2).

## Demonstrable outcomes — tour-driven videos in the decks (WM.4, critical)

Each **video-demonstrable** change (marked with a `demo_video:` field in
`decomposition.yaml`) produces a **tour-driven demo video** via the **no-LLM rrweb
pipeline**, **embedded in that change's deck** (WM.3) and surfaced in the exec
summary. The authoritative list is `video-catalog.md` (WM.4). Flagship videos:
the stranger **install** flow (0.1), the **goal-seeker loop** running (WM.0), and
the **cost proof** — a paired kitsoki-vs-agentic run + the confirmation scorecard
(WB.5). Engine/lint/docs-only changes are gate-proven, not video-demoed.

The **exec summary opens with a short compendium tour video** — a highlight reel
assembled deterministically from the flagship clips, showcasing the highest-impact
work at a glance (brief, overview-level), refreshed as flagships land.

## Cost-efficiency benchmark (WB, critical)

The proof of G4/G5 is a **research-grade study** (`.context/cost-efficiency-benchmark-plan.md`,
promoted to `docs/research/` at M0): pre-registered hypotheses **H1–H4**, a frozen
task corpus with **hidden deterministic oracles**, a **strong baseline**
(`single-briefed`), a **training loop** where failures become generic process
patches, and a **held-out confirmation run** that alone yields the headline numbers.
Milestones M0–M5 are changes WB.0–WB.5. Its case-study + deck roll into the exec
summary and feed the profit scorecard.

## Agent sandboxing / capability enforcement (WS, clears G3)

`docs/proposals/agent-capability-model.md` was design-only (epic, 0/3 slices
shipped) — the deferred item behind G3's "agents can't exceed declared
capabilities." It is now decomposed as a strict chain: **WS.1** effect taxonomy
(`pure|read|write|external` + `deterministic`, replacing the overloaded
`external_side_effect` boolean) → **WS.2** named `toolboxes:` + one
`enforceToolbox` policy collapsing the three ad-hoc mechanisms (`ask`/`decide`'s
`mutationTools` deny, `converse`'s read-only branch, `task`'s unrestricted
`bypassPermissions`) → **WS.3** the secure agent runtime (`sandbox:`, the
`supervised` backend floor) → **WS.4** an offline conformance lint (with a RED
fixture) proving a trace's recorded tool use never exceeded its declared
toolbox/effect. Each gate is a no-LLM Go test, per the proposals' own
"Verification: No LLM" sections. **WS.3 dogfoods on the goal-seeker's own
worker dispatch** (WM.5) — a real risky write-agent call site adopts
`sandbox:` + `effect: write`, and the flagship demo (WM.4) shows an agent's
out-of-box write refused with the trace proving the box held.

## Every unit of work ships the complete package, not just code (WP)

A change is not "done" when its code gate goes green — it is done when it
ships its **narrative docs**, its **demo** (if `demo_video`-flagged), and a
truthful **feature-site** update, per `decomposition.yaml`'s
`definition_of_done:` block. Three gated changes give this teeth: **WP.1**
lints that a shipped, proposal-driven change trims/deletes its source
`docs/proposals/*.md` (repo CLAUDE.md's rule) and lands a narrative doc;
**WP.2** lints that the feature catalog (`features/*.yaml`) + pitch deck
truthfully and *positively* reflect the shipped package (extends the W5
site-truthfulness idea from honesty-only to coverage); **WP.3** lints that
`video-catalog.md` (WM.4) enumerates every `demo_video`-flagged change,
including WS.3's sandbox demo, with a produced clip. The goal-seeker's
`record`/`integrate` step checks this whole package, not just the code gate.

## The plan is a living, change-managed graph (WM.6 → WM.7)

`decomposition.yaml` is not a document to hand-edit — it is a **versioned
graph** carrying the same guarantees the goal-seeker enforces on code: a
change lands only RED→GREEN, reviewer-only-green, no lost work. **WM.6** pins
the shared schema (the change-node shape both `goal.py`'s lint and
`stories/deliver`/`decompose` validate against) and names the two-graph model
(acyclic `depends_on` DAG + cyclic process graph), deferring the unified
runtime and impact-analysis. **WM.7** makes *evolving* that graph mid-flight —
folding in a reviewed design, capturing a surfaced WM.x limitation,
re-scoping, deferring, or pulling a change forward — itself a **change-managed
transaction**: named trigger+provenance, validation against the WM.6 schema
plus the full structural lint (reject on cycle / scope overlap / missing
RED-first gate / dangling dep), an adversarial feasibility review, graph
versioning with no in-flight change ever orphaned, and an observable
plan-evolution event in the WM.3 PM decks. The **golden delta corpus is this
very session**: the WS + WP fold-in, done by hand-editing this file, is exactly
the unmanaged path WM.7 replaces — its flow replays "add the WS wall" as the
accepted case and a corrupted variant (injected cycle/scope-overlap/missing-
gate) as the rejected case. See `.context/plan-as-graph-decision.md` and
`.context/managed-inflight-plan-update.md`.

## Walls (full map in the overview; first slice detailed in decomposition.yaml)

| Wall | Criterion | What it clears |
|------|-----------|----------------|
| **WM** — meta / dogfood-enablement | G2, G6 | the goal-seeker (WM.0) + make the plan drivable through kitsoki on a foreign repo |
| **W1** — Install | G1 | release embed+smoke, portable `.mcp.json`, first-screen honesty, doctor/init, onboarding deps |
| **W2** — Run on their stack | G2 | cross-provider/headless/MCP portability (#28/#31/#33/#35), continue-mode |
| **W3** — Trust | G3 | error banner, git-ops truthfulness, stall watchdog, router, capability enforcement |
| **W4** — Afford | G4 | MCP token reduction, context floor, budget gate, non-Anthropic cost |
| **W5** — Evaluate | G5 | binary-first docs, site truthfulness, scored external bake-off, README honesty |
| **WB** — Cost-Efficiency Benchmark | G4, G5 | *critical* — research-grade proof of the cost thesis (M0–M5, hypotheses H1–H4, held-out confirmation); from `.context/cost-efficiency-benchmark-plan.md` |
| **WS** — Sandbox / capability enforcement | G3 | the deferred 6.4, now decomposed: effect taxonomy → toolboxes+enforcement → FS/runtime sandbox → conformance lint; dogfooded by confining the goal-seeker's own workers |
| **WP** — Complete Package | G5, G6 | every shipped change ships its narrative docs, its demo (if `demo_video`-flagged), and a truthful feature-site/pitch update — not just green code |
| **WH** — Hygiene | de-risk | prd-demo pollution, proposal cleanup, park the ambition layer, flow backfill |
| **WZ** — Closeout | G5, G6 | end-of-goal retrospective case-study review + deck (Z.0, tracked) |

## Sequencing

- **Wave 0 (now):** WM.0 (the goal-seeker) + WM fixes + all W1 Tier-0. `0.1` first (highest ROI).
- **Wave 1:** finish W3 trust + W2 portability + project-init + MCP token PR + WS.1→WS.4 (sandbox chain) + WM.6→WM.7 (decomposition-graph schema + managed-update transaction).
- **Wave 2:** W1 remainder + W5 docs/site + WH hygiene + park decision + WP.1→WP.3 (complete-package lints, applied retroactively to everything shipped so far).
- **Wave 3:** context floor, scored external bake-off, continue-mode.

## Run it

**Target (WM.0):** `/goal` drives the `stories/goal-seeker/` session; flow-tested
with `kitsoki test flows stories/goal-seeker/app.yaml` (no LLM).

**Interim (reference oracle, until WM.0 lands):** the same deterministic logic runs
via `goal.py`, which the Starlark port is validated against:
```
python3 .agents/skills/goal/goal.py init     --goal-dir docs/goals/generalized-usage   # once
python3 .agents/skills/goal/goal.py ledger   --goal-dir docs/goals/generalized-usage   # state
python3 .agents/skills/goal/goal.py preamble --goal-dir docs/goals/generalized-usage   # bounded preamble
```
Open decisions: design §10 (evaluator model, K, dispatch mode) + the WM.0 build fork
(new `stories/goal-seeker/` importing punch-list+ship-it — recommended — vs extending
punch-list in place).
