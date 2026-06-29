# Epic: The ad-hoc workbench — free-form dev-story that mines itself into a project story

**Status:** Draft v2. Nothing implemented yet; depends on two in-flight
worktrees landing first (see *Substrate*).
**Kind:**   epic (runtime + story + tui)
**Slices:** 5 (0/5 shipped; Slice 0 is two existing worktrees)

## Why

Kitsoki is excellent *once a story exists*. It has no good **on-ramp** for
"I just want to start working ad hoc — like Claude Code — and let the
structure emerge." Today you either author a story up front (high friction
for a workflow you haven't discovered yet) or you run Claude Code with no
tracing, no recorded decisions, and no path from "what I just did" to "a
reusable kitsoki intent."

The thesis already says this should be possible.
[`concept.md` §4](../architecture/concept.md) frames the whole product as a
loop: *prove the workflow with free prompts → read the trace → convert the
recurring decision points into deterministic rooms/intents/gates.* And the
architecture for *growing a story* already exists: `kitsoki-dev` is a
per-project **root instance** that `imports: dev-story` under alias `core`,
binds providers, and can add project-specific rooms/intents/world. That is
exactly the shape ideas.md calls "extensible stories — reusable dev-story w/
company and project-specific aspects."

What's missing is that the loop is **manual, offline, and front-loaded**:

- The root instance must be **authored up front** with its rooms, bindings,
  and intents — there is no "start almost empty and grow."
- Mining (`tools/session-mining`) runs as a **separate CLI ritual**; you read
  a `BRIEF.md` and hand-author YAML. [`dev-story-mining`](../../stories/dev-story-mining/README.md)
  made it *runnable*, but as a **story you launch**, not something ambient.
- Nothing mines a project's **existing** Claude transcript history. Nothing
  starts on first launch. Nothing surfaces accept/refine proposals *inline*.
- dev-story lands you in a **menu hub**, not a free-form workspace — the
  deterministic surface is the front door, when for an unexplored project it
  should be the *floor*.

This epic closes all four: every project gets a root story that **starts
almost empty and lands in free-form mode**, and an **always-on miner** —
seeded from the project's existing transcripts — proposes the parts that
specialize dev-story for this project, which you accept or refine. The story
*builds itself up* as you work.

## What changes

**One sentence:** a project's root story (an instance of dev-story, like
`kitsoki-dev`) lands you in a **free-form workspace you drive like Claude
Code**, while an **always-on miner** — seeded by the project's existing Claude
transcripts and fed by every live session — surfaces "capture this as an
intent / room / host-binding / gate?" proposals you accept or refine, each
accept applied as a **live edit to the root instance** (reusing meta-mode's
edit-and-reload), so the project's specialization of dev-story **accretes from
a blank start** instead of being authored up front.

Three coupled moves, separable but better together:

1. **Free-form by default** — dev-story (and its `kitsoki-dev` instance) land
   in a free-form room, not the menu hub. The deterministic hub/pipelines
   become the *floor and the grown structure*, one intent away.
2. **The per-project root is the artifact that grows — and it's progressive
   too.** Not a separate `workbench` story. It starts as an *implicit* import
   of dev-story declared in `.kitsoki.yaml` (zero files), grows by accreting
   small inline overrides there, and *graduates* to a full story under
   `.kitsoki/stories/<project>/` (the convention) only when its specialization outgrows
   inline config. `kitsoki-dev` is the fully-graduated example (and the dogfood
   proof).
3. **Ambient mining is the engine** — starts on first launch for **any**
   instance, seeds from existing transcripts, and proposes the specializations
   (including wiring up dev-story's stub rooms and, occasionally, enriching
   dev-story itself).

**The root story's three rungs** — progressive determinism applied to the story
*definition* itself, not just its decision points:

| Rung | Form | When |
|---|---|---|
| 0 — implicit | no file; `.kitsoki.yaml` defaults to `import: dev-story` | brand-new repo; just run `kitsoki` |
| 1 — inline | `.kitsoki.yaml` `root:` import + a few `overrides:` (bindings, world, synonyms) | small project-specific config |
| 2 — full story | `.kitsoki/stories/<project>/app.yaml` imports dev-story (like `kitsoki-dev`) | real rooms/intents; the conventional home |

Mined proposals (Slice 3) climb the same ladder: a small delta lands as a
rung-1 override; a structural one (or enough accreted overrides) triggers
materialization to rung 2.

## Where this fits (existing state)

This epic is the **always-on runtime** that several drafted proposals already
assume happens by hand. It reuses, rather than re-invents:

| Existing piece | Role here | Status |
|---|---|---|
| [`agent-off-ramp.md`](agent-off-ramp.md) | free-text floor: no-match → `converse`, no advance | **worktree** `review/agent-off-ramp` (shipped, read-only v1); already declared on `dev-story/main` |
| [`story-conformance-mining.md`](story-conformance-mining.md) | outcome + satisfaction capture (did the user undo it?) | **worktree** `feat/story-conformance-mining` (Phase 1) |
| dev-story **imports/instance** model (`stories/dev-story/app.yaml` `imports:`, `.kitsoki/stories/kitsoki-dev/`) | the project root story that extends dev-story and binds providers | landed; ideas.md "extensible stories" |
| `tools/session-mining/` + [`session-pattern-mining/`](session-pattern-mining/) | the stateless analyzer (distill → ground → score → emit) | landed (CLI, batch) |
| [`dev-story-mining`](../../stories/dev-story-mining/) | the mine→map→decide→author→record gate pipeline | landed (manual, in-story) |
| meta-mode ([`controller.go`](../../internal/metamode/controller.go)) | live YAML edit → tree-snapshot → **auto-reload, world preserved** | landed |
| [`reload.go`](../../internal/orchestrator/reload.go) | `Reload` + `RerunOnEnter` — swap AppDef, keep journey | landed |
| [`execution-modes-and-gate-deciders.md`](execution-modes-and-gate-deciders.md) | captured intent → gate resolved by `default \| llm \| human` decider | partially landed |
| [`training-loop.md`](training-loop.md) / [`stories-as-trainable-models.md`](stories-as-trainable-models.md) | the validation gate: an accepted edit must keep the no-LLM flow suite green | draft |

Distinct from the neighbours: [CDD](conversation-driven-development.md) is the
*methodology* and the watch/QA machinery; *stories-as-trainable-models* is the
*formalism* (reward → attribution → optimizer step). **This epic is the
plumbing that makes the discovery-and-propose front of that loop run with no
operator ritual, against the per-project root instance** — seeded from history,
surfaced inline, applied via reload, validated by the flow suite.

## The model

```
  EXISTING transcripts                LIVE session (free-form landing, any instance)
  ~/.claude/projects/<slug>/*.jsonl         agent/agent turns → CC transcripts
            │                                          │
            └──────────────┬───────────────────────────┘
                           ▼
                 ambient miner service  (Slice 2 — background, debounced)
                 reuses tools/session-mining (stateless, single-session)
                 + conformance outcomes/satisfaction
                           │
                           ▼  recipes above determinism_priority threshold,
                              deduped vs the instance's current inventory
                 proposer  (Slice 3 — dev-story-mining map→author personas)
                 drafts a delta to THE PROJECT ROOT (.kitsoki.yaml override → .kitsoki/stories/ file):
                   host-binding | world override | new intent/room |
                   wire a dev-story stub room | gate  (occasionally: enrich dev-story)
                           │
                           ▼
                 proposal card  ──accept──▶ apply edit + live RELOAD ──▶ flow suite green?
                 (Slice 4)      │            (meta-mode path, world kept)   │ yes → keep
                                │                                           │ no  → revert + hold
                                ├─refine──▶ meta-mode, draft preloaded
                                └─reject──▶ recorded negative signal
                           │
                           ▼
                 every accept/refine/reject is a RECORDED decision (the moat)
                 conformance loop: do later sessions route through the captured
                 structure, or keep falling back to free-form?  → next proposal
```

- **Deterministic:** the miner pipeline grounding/scoring (already no-LLM
  except its one agent pass), the decision to surface a proposal (a
  threshold × a dedup against already-captured structure), applying an
  accepted edit, the reload, and the flow-suite gate.
- **Interpretive (recorded):** the miner's one agent segmentation pass, the
  proposer's YAML draft, and the operator's accept/refine/reject verdict.
- **Inviolate:** ambient mining **never** mutates the running story on its
  own. It only ever produces a *proposal*. Structure changes exactly along the
  meta-mode edit-and-reload path that already exists, and only on an explicit
  accept. The free-form floor is always there next turn.

---

## Slice 0 — Substrate (land the two worktrees first)

Not new work; the epic is dead without them.

- **`review/agent-off-ramp`** is the free-form floor's mechanism: free text
  the router can't map becomes a `converse` answer instead of "didn't catch
  that" (`internal/orchestrator/offpath.go` `maybeOffRamp`,
  `internal/app/types.go` `State.AgentOffRamp`). Already declared on
  `dev-story/main`. Shipped read-only v1 — no advance, no host calls. That
  ceiling matters: see Slice 1's open question on whether the free-form
  landing needs *more* than converse.
- **`feat/story-conformance-mining`** gives mining its **outcomes** layer:
  per-tool `{is_error, stdout_head}` joined back to recipes + a satisfaction
  flag (did the user then `git reset`/`--amend`/say "no, undo"?). That signal
  is what lets the proposer prefer recipes that *worked* and the conformance
  loop detect captured structure the user keeps bypassing.

**Action:** review + merge both before cutting Slices 1–4.

## Slice 1 — Free-form by default + the blank project root (story)

Two deliverables: make dev-story land free-form, and make starting a new
project root trivial.

**1a — Free-form landing in dev-story.** dev-story gains a free-form root room
(the "workbench" landing) that behaves like Claude Code — a broad-toolbox
agent (`cwd` = the repo) with the `agent_off_ramp` floor — and becomes the
`root:`. The existing menu hub (`main`) and every pipeline (bf/pr/impl/cyp/…)
stay exactly one intent away and are the **progressively-determinized layer**:
as proposals land, more of what you'd do free-form becomes a named intent off
this room. Adapt **kitsoki-dev** the same way (it inherits the landing through
the `core` import; the dogfood instance should *demonstrate* the loop).

**1b — The implicit project root + graduation.** No scaffolded story file by
default. `.kitsoki.yaml` carries a `root:` block — `import: dev-story` plus a
small `overrides:` map (bindings, world keys, synonyms) — and the loader
synthesizes the root instance from it (rung 1). With no `.kitsoki.yaml` at all,
the default is `import: dev-story` with default bindings (rung 0): run
`kitsoki` in any repo and you get free-form dev-story out of the box.
`kitsoki materialize` graduates the implicit root to a full story under
`.kitsoki/stories/<project>/` (rung 2) — the conventional home for real stories —
folding the accumulated overrides into a normal dev-story instance like
`kitsoki-dev`.

- **Designed to grow:** overrides accrete in `.kitsoki.yaml` until they warrant
  a real file; the free-form agent's share of the work shrinks as the
  deterministic surface grows — the visible payoff of progressive determinism.
- **First-start banner** (Slice 4) fires from the free-form landing's
  `on_enter` `once:`.

**Open question (load-bearing):** what is the free-form landing concretely —
a `mode: conversational` room, a full-tool `host.agent.task` room (edits/runs,
like the bugfix/implementation task rooms), or just `main` + off-ramp? *Lean:
a full-tool task room.* "Like Claude Code" means it does work; the off-ramp's
read-only converse is the Q&A floor, not the work surface. This is the one
place the epic may ask for engine latitude beyond the shipped off-ramp — and
it's precedented by the meta-mode agent and the bugfix/implementation task
rooms, which run full-tool agents inside a checkpointed room.

## Slice 2 — The ambient miner service (runtime)

A session-scoped background service, started by `kitsoki run` / `kitsoki web`,
for **any** instance (not just a blank root).

- **Seed from history (the user's explicit ask):** on the *first* launch for a
  project, resolve `~/.claude/projects/<slug>` from the repo path and kick off
  a bounded first-pass mine over **existing** transcripts (recency/size sample
  — `prep.py` already supports this), not only sessions born in free-form mode.
  Emit a system message: *"Mining your last N sessions for reusable patterns —
  I'll suggest structure as I find it. `/mine` to control."*
- **Feed from live work:** thereafter, mine new transcripts on a **debounced**
  cadence — the free-form agent's own turns and any dispatched
  `host.agent.task` turns produce Claude Code transcripts the same pipeline
  consumes.
- **Reuse, don't rebuild:** the `tools/session-mining` pipeline is stateless
  and single-session-capable (`prep.py --job` → the one agent pass →
  `ground.py`/`tag_score.py`/`emit.py`). Wrap it as a skill or an internal
  runner invoked via the existing **local background-jobs** infra so it
  survives across turns and never blocks input.
- **Applies to any instance.** Ambient mining enriches whatever you're running
  — a mature `kitsoki-dev` ambiently proposes new dev-story gates; a blank root
  proposes its first rooms. The blank root is the limiting case, not the only
  client.
- **Config** lives in machine-global `.kitsoki.yaml` beside `story_dirs` /
  `harness_profiles` (`docs/architecture/harness-profiles.md`): `enabled`,
  `cadence`, `first_pass_sample`, `priority_threshold`, `transcript_dirs`,
  per-project `mined_through` watermark so we never re-mine the same session.

**Cost guardrail.** Mining's one agent pass costs real LLM at runtime (fine —
this is not a test; the no-LLM rule is for CI). The first-pass over a long
history is **sampled and budgeted**, runs in the background, and is fully
pausable via `/mine pause`. Per [CLAUDE.md](../../AGENTS.md), nothing in the
*test* path may call a live LLM — ambient mining is gated out of flow fixtures
and exercised with cassettes.

## Slice 3 — Mine → propose → apply, against the root instance (the loop)

The ambient version of `dev-story-mining`'s mine→map→decide→author, targeting
the **active root instance** instead of being a story you launch.

- **Draft:** a recipe over `priority_threshold`, deduped against the instance's
  current intent/room/binding inventory (the `mapper` persona's "regenerate the
  inventory, never from memory" rule), is handed to the dev-story-mining
  `author` / `story-author` persona, which drafts a concrete delta to the root
  instance. The delta kinds are exactly the levers dev-story already exposes:
  - **host binding** — bind an `iface.*` default to a concrete provider (the
    kitsoki-dev pattern: `iface.ticket` → `host.local_files.ticket`).
  - **world override** — set a dev-story knob for this project (`judge_mode`,
    the doc-profile keys, `base_branch`, …).
  - **new intent / slot-template** — a named, deterministically-routed action
    for something done free-form repeatedly.
  - **wire a stub room** — dev-story ships `deploy` / `observability` /
    `incident` / `docs` as route-back-to-main stubs; a recurring deploy recipe
    becomes the `deploy` room's real content.
  - **gate** — a `decider` checkpoint (`default | llm | human`) at a recurring
    judgment fork.
  - **occasionally: enrich dev-story itself** — when the pattern is generic, the
    delta lands in the base story, not the project root (flagged as such).
- **Lightest rung:** the proposer expresses each delta at the lowest rung that
  fits — a binding/world/synonym becomes a `.kitsoki.yaml` `overrides:` entry
  (rung 1); a new room or gate, or a root whose overrides have grown past a
  threshold, triggers materialization to `.kitsoki/stories/<project>/` (rung 2). Most
  early proposals are one-line config edits; real structure arrives only when
  earned.
- **Surface:** the draft becomes a **proposal card** (reuse the proposal-review
  surface). Non-blocking — it queues; a badge shows the count.
- **Accept:** apply the edit and **live-reload** via the meta-mode path
  (`controller.go` tree-snapshot → `SendResult.ReloadRequested` →
  `orchestrator.Reload` + `RerunOnEnter`, world preserved) — a `.kitsoki.yaml`
  override reloads the synthesized root, a `stories/` edit reloads the file
  tree, same `Reload` path. The accept is
  **gated on the no-LLM flow suite staying green** (the
  [`training-loop`](training-loop.md) validation step): if the edit regresses a
  fixture, revert and hold the proposal with the failure attached.
- **Refine:** drop into meta-mode with the draft preloaded — the operator edits
  the root instance conversationally, normal reload applies.
- **Reject:** recorded as a negative signal so the same recipe doesn't nag.
- **Close the loop (conformance):** once structure is captured, later sessions
  either route through it (it earns its keep → recorded decisions let its
  decider climb the L0→L4 ladder per
  [`execution-modes-and-gate-deciders.md`](execution-modes-and-gate-deciders.md))
  or keep falling back to free-form (the structure was wrong → the
  reverse-direction signal from [`concept.md` §4](../architecture/concept.md),
  surfaced as a "this intent isn't being used — widen or drop it?" proposal).

## Slice 4 — `/mine` control surface + first-run UX (tui + web)

- **First-run system message** (Slice 2) on first launch per project.
- **`/mine` command:** `status` (watermark, queue depth, last run),
  `pause` / `resume`, `now` (force a pass), `scope <dir>` (add/remove
  transcript dirs), `queue` (list pending proposals), `accept <id>` /
  `dismiss <id>`. Mirrors the existing `/meta`, `/provider`, `/model`
  operator-command pattern.
- **Surfacing:** a proposal badge in the TUI status line and the web header;
  proposals open in the same review surface meta-mode/checkpoints use. Never
  steals focus mid-turn.

---

## Vocabulary changes

| Kind | Name | Shape | Notes |
|---|---|---|---|
| config block | `root:` (in `.kitsoki.yaml`) | `{ import: dev-story, overrides: {bindings, world, synonyms} }` | implicit project root (rung 1); absent ⇒ default `import: dev-story` (rung 0) |
| CLI | `kitsoki materialize` | `[--name <slug>]` | graduate the implicit root to a full story under `.kitsoki/stories/<project>/` (rung 2) |
| story room | dev-story free-form landing | `root:` room, full-tool agent + `agent_off_ramp` | the "workbench"; menu hub one intent away |
| operator cmd | `/mine` | `status\|pause\|resume\|now\|scope\|queue\|accept\|dismiss` | controls the ambient miner |
| config block | `mining:` (in `.kitsoki.yaml`) | `{ enabled, cadence, first_pass_sample, priority_threshold, transcript_dirs }` | machine-global, like `harness_profiles:` |
| config key | `mining.mined_through` | per-project `map[slug]watermark` | de-dup; never re-mine a session |
| world key | `captured` | `int` | proposals accepted this session (drives the "free-form shrinking" view) |
| event | `MiningProposalRaised` | `{recipe_id, kind, target, priority, draft_path}` | one per surfaced proposal; `target` = root-instance \| dev-story |
| event | `MiningProposalDecided` | `{recipe_id, verdict, by, flows_green}` | accept/refine/reject — **the recorded decision** |

## Decision recording

The moat requirement: every interpretive decision lands as a labeled,
reconstructable datapoint.

- The miner's agent segmentation pass already records grounded recipes
  (`analysis.json`, instance-first, `agent_gates` named).
- **New:** `MiningProposalRaised` / `MiningProposalDecided` (table above) put
  the *surface-and-verdict* decision in the kitsoki trace — so for any captured
  structure you can answer "which mined recipe proposed it, what was the
  priority, did the flow suite pass on accept, who accepted it, did it target
  the root or the base." A rejected proposal is equally recorded (the negative
  is signal).
- The captured intent's *own* gate then records decisions per
  `execution-modes-and-gate-deciders.md` (`GateDecided`), which is what lets it
  later drop a determinism rung. The chain: recipe → proposal → intent → gate
  decisions → ladder move.

## Impact

- **Code seams:**
  - Free-form landing: a new room + `root:` in `stories/dev-story/`; inherited
    by `.kitsoki/stories/kitsoki-dev/`.
  - Implicit root: the `.kitsoki.yaml` loader synthesizes a dev-story instance
    from the `root:` import + `overrides:` (the same loader that reads
    `story_dirs`); `kitsoki materialize` (new `cmd/kitsoki/materialize.go`)
    writes the rung-2 story tree.
  - Ambient miner: a new `internal/mining/` service started from the run/web
    session lifecycle; invokes `tools/session-mining` via the background-jobs
    runner; `/mine` handled beside the other operator commands in
    `internal/tui/` and the web command surface.
  - Apply path: **reuses** `internal/metamode/controller.go` (snapshot +
    `ReloadRequested`) and `internal/orchestrator/reload.go` (`Reload`,
    `RerunOnEnter`) unchanged.
  - Config: `.kitsoki.yaml` loader (the same one that reads `story_dirs` /
    `default_profile`).
- **Stories affected:** **dev-story + kitsoki-dev change landing behavior**
  (free-form root; the menu hub becomes a child room). Their flow fixtures get
  one extra hop at the front (free-form landing → `go_main`). All other stories
  unchanged. Ambient mining is opt-in via config.
- **Backward compat:** the free-form-landing change is the one behavioral shift
  — gated so `main` stays reachable and every existing pipeline is unchanged;
  fixtures get a mechanical one-line prefix. Ambient mining off by default (or
  first-run consent). New trace events are additive → replay-compatible.
- **Docs on ship:** `docs/architecture/concept.md` §4 (the loop becomes
  *ambient*, against the root instance), a new
  `docs/architecture/ambient-mining.md`, `docs/stories/imports.md` (the blank
  root that grows), the dev-story README (free-form landing), and
  `docs/stories/meta-mode.md` (the shared apply path).

## Backward compatibility / migration

The free-form landing is the only behavioral change: dev-story's `root:` moves
from `main` to the free-form room, with `main` reachable via one intent.
Mechanical fixture migration = prefix the affected flows with the landing →
`go_main` hop (scriptable). Ambient mining adds proposals only if
`mining.enabled`. New projects get an implicit dev-story root from
`.kitsoki.yaml` (or no config at all); existing instances (`kitsoki-dev`,
`gears-rust`) keep their rung-2 story files and are unaffected except for the
landing hop. New trace events are optional fields → older cassettes replay
unchanged.

## Verification

Everything except the live agent passes is testable with no LLM:

- **Free-form landing (Slice 1):** flow fixture — boot lands in the free-form
  room; free text off-ramps to a stubbed `converse` (stub-by-id); `go_main`
  reaches the existing hub; accepting a seeded proposal adds an intent and the
  room now routes it deterministically. The existing dev-story suite passes
  with the one-hop prefix.
- **Implicit root (Slice 1b):** unit test — a `.kitsoki.yaml` `root:` import +
  `overrides:` synthesizes a loadable dev-story instance that lands free-form;
  `kitsoki materialize` writes a loadable rung-2 story tree.
- **Ambient miner (Slice 2):** unit-test the watermark/dedup and the
  history-seed resolver against a fixture transcript dir; the mining pipeline
  itself already has the no-LLM `test_outcomes.py` and grounding tests. The one
  agent pass is cassette-backed.
- **Loop (Slice 3):** flow fixture — a seeded mined recipe → proposer draft
  (stubbed) → accept → reload → assert the new intent/binding is live and the
  flow suite is green; a second fixture where the drafted edit *breaks* a
  fixture → assert revert + hold.
- **`/mine` (Slice 4):** command-dispatch unit tests (status/pause/resume),
  mirroring existing operator-command tests.

The only genuinely-LLM step — "is the mined recipe a *good* capture, and is the
drafted YAML correct" — is exercised by hand in a real dogfood run (run
`kitsoki-dev`, capture ≥1 real specialization end-to-end), never in CI.

## Tasks

```
## 0. Substrate (existing worktrees)
- [ ] 0.1 Review + merge review/agent-off-ramp
- [ ] 0.2 Review + merge feat/story-conformance-mining (Phase 1 outcomes)

## 1. Free-form landing + blank root
- [ ] 1.1 dev-story free-form landing room (full-tool agent + agent_off_ramp); root: → it; main one intent away
- [ ] 1.2 Adapt kitsoki-dev to the free-form landing (inherits via core import); prefix affected dev-story/kitsoki-dev flows with the landing hop
- [ ] 1.3 Implicit root: .kitsoki.yaml root: (import dev-story + overrides) synthesized by the loader; default-to-dev-story when absent; kitsoki materialize graduates to .kitsoki/stories/<project>/
- [ ] 1.4 First-start banner via on_enter once:
- [ ] 1.5 Flow fixture: boot → free-form landing; off-ramp; go_main; seeded proposal adds a live intent

## 2. Ambient miner
- [ ] 2.1 internal/mining/ service started from run/web lifecycle
- [ ] 2.2 History-seed resolver (repo → ~/.claude/projects/<slug>) + bounded first pass + system message
- [ ] 2.3 Debounced live-session mining over the background-jobs runner
- [ ] 2.4 mining: config block in .kitsoki.yaml + per-project mined_through watermark
- [ ] 2.5 Cassette-back the single agent pass; gate ambient mining out of flow fixtures

## 3. Mine → propose → apply loop
- [ ] 3.1 Proposer: recipe (over threshold, deduped vs instance inventory) → delta draft (binding | world | intent/room | stub-wire | gate)
- [ ] 3.2 Proposal card on the review surface; MiningProposalRaised event (target = root | dev-story)
- [ ] 3.3 Accept → meta-mode edit+reload of the root instance; gate on no-LLM flow suite green; MiningProposalDecided event
- [ ] 3.4 Refine → meta-mode preloaded; Reject → recorded negative
- [ ] 3.5 Conformance loop: flag captured structure that isn't being used (still falling back to free-form)

## 4. Control surface + UX
- [ ] 4.1 /mine status|pause|resume|now|scope|queue|accept|dismiss
- [ ] 4.2 Proposal badge (TUI status line + web header), non-focus-stealing
- [ ] 4.3 First-run consent (if default-on)

## 5. Adopt + document
- [ ] 5.1 Dogfood: run kitsoki-dev free-form against this repo; capture ≥1 real specialization end-to-end
- [ ] 5.2 docs/architecture/ambient-mining.md; concept.md §4 "ambient" note; imports.md "blank root that grows"; trim this epic
```

## Open questions

1. **Free-form landing shape** — `mode: conversational` room vs full-tool
   `host.agent.task` room (edits/runs, like Claude Code) vs just `main` +
   off-ramp. *Lean: full-tool task room.* The shipped off-ramp is read-only by
   design; the landing needs the task-room shape bugfix/implementation use.
2. **Does `main` stay a separate room, or does the free-form landing replace
   it** (main's navigation folded in)? *Lean: keep `main` as a child room,*
   free-form landing as the new `root:` — least disruptive, fixtures get a
   one-line prefix.
3. **Mining cadence/trigger** — background-debounced after an activity
   threshold vs session-end vs on-demand-only. *Lean: background-debounced,
   with `/mine now` and `/mine pause`.* "No extra effort" rules out
   on-demand-only.
4. **Default on or first-run consent** — ambient mining reads transcript
   history and spends LLM. *Lean: first-run consent banner, then on*, recorded
   in `.kitsoki.yaml`.
5. **What mining reads for the kitsoki layer** — the dispatched-agent CC
   transcripts (rich tool detail, what `session-mining` already eats) vs the
   kitsoki trace event log (intent/routing/world boundaries). *Lean: CC
   transcripts for the recipe, kitsoki trace for "what's already
   deterministic" so the proposer dedups correctly.*
6. **Apply mechanism** — reuse meta-mode file-edit + reload (lean) vs build the
   ideas.md "pure in-memory mutable app + export-to-yaml" path. *Lean: reuse
   meta-mode* — it exists, preserves world, and keeps edits as reviewable files
   on disk.
7. **Root vs base targeting** — when does a delta land in the project root vs
   enrich dev-story itself? *Lean: project root by default;* promote to
   dev-story only when the `mapper` flags the pattern as generic (and as a
   separate, explicit proposal).
8. **Graduation threshold** — when does an accreting `.kitsoki.yaml` root
   move from rung 1 to a rung-2 `stories/` file: a count of overrides, the
   first structural (room/gate) delta, or only on explicit `kitsoki
   materialize`? *Lean: explicit + a nudge* — the proposer suggests
   materializing once a structural delta appears, but never rewrites the tree
   without an accept.

## Non-goals

- **A separate `workbench` story.** The workbench *is* dev-story's free-form
  landing; the artifact that grows is the per-project root instance. Folding it
  into dev-story (vs a standalone story) is deliberate.
- **In-memory mutable apps / export-to-YAML.** Tempting (ideas.md) but a
  separate proposal; the apply path here is on-disk edit + reload of the root
  instance.
- **Auto-applying mined structure without a human accept.** The miner only ever
  proposes. Structure changes only on explicit accept, only along the existing
  meta-mode path.
- **Replacing Claude Code.** The free-form landing *embeds* a Claude-Code-like
  agent and adds tracing + emergent structure; it is not a new coding agent.
- **The full training-loop optimizer** (`training-loop.md`). This epic is the
  *discovery + propose + apply-with-validation* front; the reward/attribution
  formalism stays in that proposal and can plug into the same proposal surface
  later.
- **Sharing mined patterns across users.** Local-only here; the redaction +
  share-gate path stays in `session-pattern-mining/`.
