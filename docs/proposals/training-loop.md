# Runtime: the training loop

**Status:** Draft v1. Nothing implemented yet.
**Kind:**   runtime (+ story)
**Epic:**   [`stories-as-trainable-models.md`](stories-as-trainable-models.md) (slice 3)

## Why

Slices 1 and 2 give the two missing pieces of the training loop — the **loss**
(a scored `reward` event) and the **gradient** (a ranked `attribution` pointing
at which decision point, and which kind of weight, to change). What remains is the
**optimizer step and its validation gate**: take the attribution, draft a
candidate edit to the named weight, run it against the no-LLM flow-fixture suite,
and accept it only if reward improves without regressing anything else. That is
the whole loop, and it is the part [`concept.md` §4](../architecture/concept.md)
already describes by hand — "convert prompt to flow," "widen the deterministic
surface" — but performs as a manual operator ritual, one decision point at a time,
with the operator personally judging whether the conversion helped.

This slice automates the *mechanism* of that ritual while keeping the operator (or
a validation gate) as the acceptance authority. It also draws the
[boundary the epic fixes](stories-as-trainable-models.md#relationship-to-the-4-layer-model)
into code: of the [4-layer self-improvement model](../competitive-analysis/market-research.md)
(§4.5), Layers 1–2 (validate+nudge within a step; recycle to a prior step) are the
**adaptive forward pass** and are *not* this slice — they run during a session and
leave the failure→success deltas slice 2 reads. This slice is the **training
half**: the per-step optimizer is **Layer 3** (self-patch the workflow under
budget); the batched epoch is **Layer 4** (cross-run mining), and that epoch is
not built from scratch — it rides the existing
[`tools/session-mining/`](../../tools/session-mining/README.md) kit, whose
L0→L4 progressive-determinism ladder and `aggregate`/`report` stages already
encode "record decisions at a gate → fit a default → climb a rung." After this
slice, kitsoki has one named training loop and the competitive-analysis §4.5 is
rewritten to point at it rather than describing a parallel roadmap.

## What changes

A new **training loop** orchestrator runs the cycle
*forward pass → reward → attribution → candidate edit → validate → accept/discard*,
and a thin **`train` process story** (in the dogfood instance, beside the
[`dev-story` design pipeline](../../stories/dev-story/README.md)) drives it
interactively: pick a failing scenario, see the attribution, review the proposed
edit, watch it run against the flow fixtures, accept or refine. *The story's
trainable surface is adjusted by a gated, validated, budgeted optimizer step —
never an autonomous one.*

## Impact

- **Code seams:** new `internal/training/` orchestrator (loop driver + acceptance
  gate, DI over slice-1 reward and slice-2 attribution); a new `train` story under
  `.kitsoki/stories/kitsoki-dev/` (rooms + an `agent.task` candidate-edit author); reuses
  the existing flow-fixture runner ([`testing.md`](../tracing/testing.md)) as the
  validation harness.
- **Vocabulary:** no new effects/host calls in the engine proper — the loop is
  orchestration over slices 1–2 plus the existing `agent.task` (to draft the
  edit) and `host.run`/flow-runner (to validate). The `train` story is ordinary
  YAML.
- **Stories affected:** none change behavior; the loop *operates on* a target
  story's files (its weights), it does not change the engine other stories run on.
- **Backward compat:** additive; the `train` story is a new instance surface.
- **Docs on ship:** `docs/architecture/trainable-model.md` (the loop closes the
  thesis the epic opens), with the competitive 4-layer section rewritten to
  reference it.

## The model

```
   pick failing scenario (low reward, slice 1)
              │
              ▼
   attribution (slice 2) ──▶ {decision_ref, weight_kind, blame}
              │
              ▼
   candidate edit  ── agent.task drafts a change to the named weight ──┐
   (the optimizer step;                                                 │
    budget = learning rate)                                             │
              │                                                         │
              ▼                                                         │
   VALIDATE: run the no-LLM flow-fixture suite on the edited story      │
              │                                                         │
       ┌──────┴───────┐                                                 │
   reward improved &           reward worse or a                        │
   no regression?              fixture regressed?                       │
       │                            │                                   │
       ▼                            ▼                                   │
   ACCEPT (commit the edit)    DISCARD ◀──── refine / try next ranked ──┘
```

- **The optimizer step is `propose → validate → keep-or-discard`** (the epic's
  break-point #2: this is RL/evolutionary, not gradient descent). The
  `candidate edit` is exactly a progressive-determinism conversion from
  [`concept.md` §4](../architecture/concept.md) — prompt→flow, a slot template, a
  `.star` script, a widened intent vocabulary — chosen by the `weight_kind` the
  attribution named.
- **The validation set is the flow-fixture suite** (the epic's shared decision
  #4, the overfitting guard). An edit is accepted only if (a) the failing
  scenario's reward improves on replay, and (b) no existing flow fixture
  regresses. This is what separates "training" from "tweak-and-pray."
- **Budget is the learning rate** (4-layer Layer-3 precedent): each step is
  bounded — one weight, a capped edit size, a capped number of `ranked` candidates
  tried — so the loop can't run away rewriting the story.
- **Moat regularization:** the candidate edit is rejected at the gate if it blurs
  interpretation vs. execution or trips a load-time invariant — an edit that, say,
  hands the LLM a wider action surface than the intent alphabet allows is
  discarded regardless of reward (the epic's shared decision #3).

### Two application modes — and the distinctive one

Once an edit is accepted, *when* it takes effect is a real choice, and it is where
this model departs from a conventional trainer (the epic's "training during
inference" property):

- **Stage-for-next-run (safe default).** The accepted edit lands as a staged diff
  / PR against the story; *future* sessions load the improved weights. This is the
  conventional train-then-deploy posture.
- **Hot-apply mid-session (the distinctive mode, gated).** Because the weights are
  live-editable artifacts, an accepted edit — **any weight kind, including
  structural graph/`.star` changes** — can be reloaded into the **running**
  session, so the same run improves from its next turn onward: genuine online
  fine-tuning of a model while it is in use. Strictly gated: only after the full
  validation gate passes, only with operator opt-in, and only at a **turn
  boundary** (never mid-turn). This is the capability the epic calls out as the
  most distinctive; v1 may ship stage-for-next-run first and add hot-apply behind
  a flag.

#### Replay determinism for hot-apply — special-cased, edges deferred

Hot-applying *structural* weights breaks the engine's current assumption that one
session runs against one static story for its whole life. We resolve this at a
deliberately coarse altitude now and leave the finer points to a future iteration:

- **The model:** a session's trace is a sequence of turns, and **each turn is
  pinned to a weight-version hash** (the content hash of the whole weight set in
  effect). Today every turn shares one hash; hot-apply lets the hash *change
  between turns*. At the boundary the engine emits a `weights.reload` decision
  carrying the new hash; a replay reconstructs the version active at each turn from
  these markers and loads it before replaying that turn. Because edits land **only
  at turn boundaries**, each turn is still internally deterministic against a
  single frozen version — replay stays exact, turn by turn.
- **Why this is enough for v1:** the weight-version snapshot covers *all* kinds
  uniformly (a `.star` or graph change is just a different content hash), so no
  special per-kind replay logic is needed — the coarse "version per turn" rule
  subsumes structural edits.
- **Deferred to a future us (called out, not solved):** a structural edit that
  invalidates the *live* world — e.g. removes or renames the room the session is
  currently **in**, or retypes a `world:` key the session already populated — and
  edits that span an in-flight parallel region or a pending background job across
  the boundary. v1's gate **rejects** a hot-apply whose diff touches the current
  room or a populated world key (fail safe → fall back to stage-for-next-run);
  lifting that restriction (live state migration/remap) is the future work. This
  keeps the determinism story sound today without blocking on the hard cases.

## Decision recording

Each loop turn lands recorded decisions (the moat): the `reward` (slice 1), the
`attribution` (slice 2), the **candidate-edit** `agent.task` (prompt, world
snapshot, the diff it produced), the **validation** result (which fixtures ran,
the reward delta), and the **accept/discard** verdict (decider: human via the
operator bridge, or `default` when a flow gates it headlessly). A training session
is therefore itself a fully replayable trace — you can audit *why the story
changed*, which is the whole point the epic makes (auditable improvement, not
prompt-and-pray).

## Engine seams & invariants

- The orchestrator is **pure orchestration** over existing primitives — it adds no
  new engine concept. It injects (DI) the slice-1 scorer, the slice-2 analyzer,
  the `agent.task` runner, and the flow-fixture runner; tests supply
  deterministic fakes/cassettes for all four.
- **The `train` story operates in a sandbox.** Candidate edits are applied to a
  worktree/copy of the target story (reuse the existing task FS-sandbox pattern),
  validated there, and only committed on accept — never edited in place mid-loop.
- **Load-time invariant:** a candidate edit that fails the target story's load
  (the same invariants every story is held to) is auto-discarded before it even
  reaches the reward replay — a malformed optimizer step costs one load, not a
  broken story.

## Backward compatibility / migration

Additive. The loop and the `train` story are new surfaces; no existing story,
flow, cassette, or engine path changes. The competitive-analysis 4-layer section
is *rewritten* (not deleted) to reference the model — a docs reconciliation, not a
behavior change.

## Tasks

```
## 1. Engine
- [ ] 1.1 `internal/training/`: loop orchestrator (DI over reward + attribution + task + flow-runner)
- [ ] 1.2 Acceptance gate: reward-improved AND no-fixture-regression AND load-invariants-hold
- [ ] 1.3 Per-step budget (learning rate): one weight, capped edit size, capped candidates tried
- [ ] 1.4 Sandbox: apply candidate to a worktree/copy; commit only on accept; record every decision
- [ ] 1.5 Application mode: stage-for-next-run (default). Hot-apply (mid-session reload, any weight kind incl. structural) behind a flag, gated by the full validation gate + operator opt-in, turn-boundary-only
- [ ] 1.6 Hot-apply replay: per-turn weight-version pinning + `weights.reload` decision marker; replayer loads the version in effect per turn. Gate rejects a hot-apply touching the current room or a populated world key (defer live-state migration)

## 2. Story
- [ ] 2.1 `train` process story under .kitsoki/stories/kitsoki-dev/: pick-scenario → show-attribution → draft-edit (agent.task) → validate → accept/refine
- [ ] 2.2 The candidate-edit task: scoped by weight_kind to one progressive-determinism conversion

## 3. Verification
- [ ] 3.1 Flow fixture: a seeded failure→success scenario drives the full loop to ACCEPT with a deterministic candidate (cassette) and the reward improves on replay
- [ ] 3.2 Flow fixture: a regressing candidate is DISCARDED (a fixture goes red) — verified to fail without the gate
- [ ] 3.3 A malformed candidate is auto-discarded at load
- [ ] 3.4 Hot-apply replay: a fixture that reloads weights mid-session (incl. a structural edit) replays byte-identically via per-turn version pinning; a candidate touching the current room is rejected to stage-for-next-run

## 4. Adopt + document
- [ ] 4.1 Run the loop once end-to-end on a real dogfood story scenario (no-LLM via cassette)
- [ ] 4.2 Write docs/architecture/trainable-model.md (close the epic's thesis); rewrite the competitive 4-layer section to reference it; delete the epic + children
```

## Verification

No-LLM throughout. Seed a failure→success scenario, supply a cassette for the
candidate-edit `agent.task` (a fixed diff) and for any `decide` reward/attribution
judge, and drive the loop via a flow fixture: assert it reaches ACCEPT and the
replayed reward rises. A second fixture supplies a regressing candidate and
asserts DISCARD — and per CLAUDE.md is verified to *fail without the acceptance
gate*. A live-LLM end-to-end run is gated and only on explicit request (never in
CI).

## Open questions

1. **Who authors the candidate edit — operator or `agent.task`?** *Lean:
   `agent.task` drafts, operator/flow accepts (the epic's open question #2). The
   loop never both proposes and accepts unsupervised.*
2. **Single step or epoch?** v1 is one scenario → one edit → accept (Layer 3).
   The batched epoch (Layer 4: mine *many* kitsoki traces for the recurring
   failure, fix it once) is the natural follow and reuses
   [`session-mining`](../../tools/session-mining/README.md) — a kitsoki `distill`
   emitting its report shape, then its existing `aggregate`/`report`/ladder
   stages. *Lean: ship single-step; leave the `distill`-shaped seam for the epoch.
   Per the epic's open question #3, the epoch reuses the kit rather than forking.*
3. **Does accept auto-commit, or stage a proposed diff for human review?** *Lean:
   stage a diff (a `docs/proposals/`-style artifact or a PR) — training a story is
   a code change and should go through the same review as any other, especially
   for the graph/`.star` weight kinds. See the two application modes above.*
4. **For hot-apply, how far does the live-state-migration restriction bind?**
   Replay determinism is settled (per-turn version pinning + `weights.reload`
   marker, "Replay determinism" above), and v1 fails safe by **rejecting** a
   hot-apply that touches the current room or a populated world key. *Open: how
   often that restriction actually bites in practice (does a useful structural
   edit usually avoid the live room?), and whether a narrow remap — e.g. an
   unchanged-room rename alias — is worth doing before the full migration story.
   Deferred to a future iteration; not a v1 blocker.*

## Non-goals

- **Autonomous, unsupervised self-modification.** Every step is budgeted,
  validation-gated, and acceptance-gated. The 4-layer Layer-3 runaway-patching
  risk is heeded by construction.
- **Tuning the LLM.** The optimizer adjusts the story's weights (prompts, slot
  templates, `.star`, graph, deciders); the agent behind each decision point is
  frozen.
- **Batched epochs / cross-run pattern mining (Layer 4 "analyze").** Seam left
  (a `distill` that feeds the existing `session-mining` kit); out of scope for v1.
- **Re-implementing Layers 1–2.** The adaptive forward pass (validate+nudge,
  recycle-to-prior-step) is runtime self-correction, not training — this slice
  consumes its trace, it does not build or change it.
- **A metrics surface for training progress over time.** Downstream runstatus, not
  this slice.
