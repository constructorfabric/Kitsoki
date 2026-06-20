# Epic: stories as trainable models

**Status:** Draft v1. No slices implemented yet.
**Kind:**   epic
**Slices:** 3 (0/3 shipped)

## Why

Kitsoki already *improves* stories over time, but it describes that improvement
in two separate, informal vocabularies and never names the thing they have in
common. [`concept.md` §4 "Progressive determinism"](../architecture/concept.md)
frames improvement as "turning recurring LLM decisions into deterministic
edges — one decision point at a time, each conversion auditable, each backed by
trace evidence." The [4-layer self-improvement
model](../competitive-analysis/market-research.md) (§4.5) frames it as an
error-loop → cross-phase-feedback → self-patching → knowledge-extraction
roadmap, "learns without training data," knowledge in git not weights. Both are
describing **training a model** — they just don't say so.

This epic adopts that frame explicitly: **a kitsoki story is a quasi-deterministic
model of a domain, and it is trainable by the same loop a neural network is —
forward pass, loss, gradient, update — except the "weights" being adjusted are
the story's scripts, prompts, and workflow graph, not a tensor.** The payoff is
not metaphor for its own sake: naming the loop tells us exactly what is missing
to *close* it. A neural net has a loss function, a gradient, and an optimizer
step; kitsoki today has the forward pass (running a session), the training set
(the event log — every decision is already a labeled datapoint,
[`overview.md:450`](../architecture/overview.md)), and the update mechanism
(a progressive-determinism edit). It is missing a **reward function** (slice 1),
a **credit-assignment** signal that points at *which* decision point to change
(slice 2), and the **loop** that ties scoring → attribution → candidate edit →
validation → accept (slice 3). This epic builds those three.

**This is not a parallel concept — it subsumes the right half of the existing
roadmap.** The [4-layer model](../competitive-analysis/market-research.md) splits
cleanly down the middle (see [§Relationship to the 4-layer model](#relationship-to-the-4-layer-model)):
Layers 1–2 (validate-and-nudge *within* a step, and recycle-to-a-previous-step
with corrective context) are the **adaptive forward pass** — the running model
self-correcting *within one episode*, no weights changed. Layers 3–4 (self-patch
the workflow, extract knowledge across runs) are the **trainable model** — they
change the weights, and they are what this epic formalizes. The training signal
itself is largely *produced* by Layer 2: when a step fails and the run recycles to
a prior step with corrective info and then succeeds, that in-run **failure→success
delta** is a labeled gradient datum slice 2 reads directly. And the Layer-4
substrate already partly exists: the
[`tools/session-mining/`](../../tools/session-mining/README.md) kit mines
transcripts for recurring procedures and ranks them on an explicit
progressive-determinism ladder (L0→L4) where "you climb a rung by *recording* the
decisions made at a gate" — that *is* the weight update, and its kitsoki consumer
([`session-pattern-mining/`](session-pattern-mining/)) is the natural home for the
batched-epoch end of this loop.

The single most important training datum is the one the request names: the
**failure → success pair** — the same (or adjacent) input that failed under the
old story surface and succeeds under a candidate edit (or, per above, succeeds
after a Layer-2 in-run recycle). That pair *is* the gradient signal, and the
no-LLM flow-fixture suite ([`testing.md`](../tracing/testing.md)) *is* the
validation set that gates whether an edit actually reduced loss without
overfitting.

## What changes

Once every slice ships, kitsoki can answer "is this story getting better, and
why" mechanically rather than by an operator's read of the trace:

- **The model is named.** A story's *trainable surface* (the "weights") is
  defined precisely: prompt bodies, synonym/slot templates, `.star` glue, the
  room/intent/transition graph, and decider configs. Everything else — the
  engine, the moat (control inversion), the load-time invariants — is the fixed
  **architecture**, never trained. This mapping lands as a new
  `docs/architecture/trainable-model.md` thesis section that `concept.md` §4 and
  the competitive 4-layer model both link to as the formal account.
- **A reward function exists** (slice 1). A declarative, pluggable `reward:`
  scorer assigns a scalar (and a label) to a completed episode, sourced from
  terminal-room outcome, flow assertions, `agent.decide` accept/reject,
  operator accept/refine, or a host exit code. It records a `reward` decision in
  the trace — a recorded, labeled datapoint like every other decision (the moat).
- **Credit assignment exists** (slice 2). Given a low-reward episode and,
  ideally, a paired high-reward episode on the same input, the engine attributes
  the loss to specific decision points by walking the trace backward from the
  scored outcome, and records an `attribution` annotation. Failure→success pairs
  are first-class trace artifacts.
- **The training loop exists** (slice 3). An orchestrated cycle —
  *forward pass → reward → credit assignment → candidate edit → validate against
  flow fixtures → accept/reject* — drivable interactively as a `train` process
  story. The candidate edit is a progressive-determinism conversion (the update
  rule); the flow-fixture suite is the validation gate (no overfitting); the
  per-edit budget is the learning rate (bounded, from the 4-layer Layer-3
  precedent). This is **Layer 3** (self-patch) plus the **Layer 4** epoch
  (cross-run mining on the `session-mining` substrate); Layers 1–2 stay as the
  adaptive forward pass, untouched. An accepted edit either stages for the next
  run (safe default) or — gated, for any weight kind including **structural**
  (graph/`.star`) — **hot-applies mid-session** so the live run improves from its
  next turn: online fine-tuning of a model while it is in use (the distinctive
  property above). Replay determinism is special-cased by pinning each turn to the
  weight-version hash in effect; the hard edge cases are deferred (slice 3).

## Impact

- **Spans:** runtime (slices 1, 3), tracing (slice 2), with a story surface on
  slice 3 (the interactive `train` driver) and a docs/thesis reframe owned here.
- **Net surface:** one new `reward:` block + outcome-scorer seam and `reward`
  event (slice 1); one `attribution` trace annotation + a failure→success
  pairing consumer (slice 2); one training-loop orchestrator + `train` process
  story + the flow-fixture validation gate (slice 3); one new
  `docs/architecture/trainable-model.md` plus back-links from `concept.md` §4 and
  the competitive 4-layer section.
- **Docs on ship:** `docs/architecture/trainable-model.md` (the thesis +
  mapping), `docs/stories/state-machine.md` (the `reward:` block), and
  `docs/tracing/` (the `reward` event + `attribution` annotation).

## The mapping (shared decision — see §Shared decisions)

The rigorous correspondence is the spine of the whole epic; every slice defers
to it. Honest about where it holds and where it breaks:

| Neural-net concept | Kitsoki analog | Slice |
|---|---|---|
| Weights θ (trained) | The story's mutable surface: prompt bodies, synonym/slot templates, `.star` scripts, room/intent/transition graph, decider configs | — (defined here) |
| Architecture (fixed) | The engine + the moat (control inversion, [`concept.md` §2](../architecture/concept.md)) + load-time invariants. Never trained. | — |
| Forward pass (**adaptive**) | Run a session/turn: LLM resolves the narrow decision points ([`concept.md` §3](../architecture/concept.md)), deterministic edges execute — *self-correcting within the episode via 4-layer L1 (validate+nudge) and L2 (recycle-to-prior-step)*. **Inference-time, not training.** | (exists) |
| Input x / prediction ŷ | A user turn or scenario / the outcome: artifact produced, transitions taken, intent routed | (exists) |
| Label y | The desired outcome: operator accept, test pass, downstream success | 1 |
| **Loss / reward** | A scorer over a finished episode — **failure→success is the key signal** | **1** |
| Training set | The event log — every recorded decision is already a labeled datapoint ([`overview.md:450`](../architecture/overview.md)) | (exists) |
| Validation set | The no-LLM flow-fixture + cassette suite ([`testing.md`](../tracing/testing.md)) | 3 (gate) |
| **Gradient** | Trace-attributed credit assignment: which decision point caused the loss | **2** |
| Backprop | Walking the trace backward from the scored outcome to the responsible edge / prompt / host call | 2 |
| **Optimizer step (update)** | A **progressive-determinism edit** ([`concept.md` §4](../architecture/concept.md)): prompt→flow, slot template, `.star`, widened vocab — **= 4-layer L3 (self-patch the workflow)** | **3** |
| Learning rate / step size | The scope of one edit, bounded by a per-edit budget (4-layer L3 budget control) | 3 |
| Epoch / batch | Cross-run pattern mining — **= 4-layer L4 ("analyze")**, on the existing [`session-mining`](../../tools/session-mining/README.md) ladder substrate | 3 (future) |
| Regularization | The moat invariants — an edit may not blur interpretation vs. execution; load-time fail-fast | 3 (gate) |
| Inference | Deterministic replay | (exists) |
| Overfitting | A story tuned to its trace corpus that fails on new inputs — caught by the flow-fixture validation gate | 3 |

**Where the analogy breaks (stated loudly, because it shapes the design):**

1. **No differentiability.** There is no true gradient. "Credit assignment"
   (slice 2) is a *heuristic estimate* over a discrete trace, closer to RL
   advantage attribution than to backprop. The design must treat attributions as
   ranked hypotheses, not derivatives.
2. **The update is discrete and authored.** An "optimizer step" is a human- or
   LLM-authored edit to structured artifacts (graph + prose + code), not a
   continuous nudge to a flat tensor. This is **evolutionary / RL**, not gradient
   descent — candidate edit, validate, keep-or-discard.
3. **Reward is sparse and delayed.** Downstream success may not be known until
   long after the episode. Slice 1 must support both immediate scorers (flow
   assertion, exit code) and deferred/operator reward.
4. **Heterogeneous weights.** The "weights" are not interchangeable scalars;
   editing the room graph is categorically different from editing a prompt. The
   credit-assignment output must name *which kind* of weight to touch.

These four are *why* the loop is gated, budgeted, and validation-checked rather
than run as an autonomous optimizer.

**Where the analogy runs the *other* way — training during inference.** A neural
net is **frozen while it runs**: inference can adapt the *activations* (prompting,
retrieval, the L1–L2 self-correction below) but never the *weights*, and improving
the weights means an offline training cycle whose benefit only ever reaches
*future* inferences. Kitsoki's weights are not a frozen tensor — they are scripts,
prompts, and workflow YAML, **editable while the session is live**. So an
optimizer step (slice 3) can in principle land *mid-run*, and the *same* session
picks it up on its next turn. The conventional wall between "inference = this run"
and "training = future runs" is therefore **porous** here: L1–L2 improve the run
at hand *without* changing weights; an L3 edit can improve the run at hand *by*
changing them. This continual/online property — the model fine-tuned while
actively in use — is the most distinctive thing the frame buys, and it is exactly
*why* the validation gate and budget above are non-negotiable: a live weight edit
to a running session is as dangerous as it is powerful, so it is the gated,
opt-in mode (slice 3), with stage-for-next-run as the safe default.

## Relationship to the 4-layer model

The [4-layer self-improvement model](../competitive-analysis/market-research.md)
(§4.5) and this epic are **the same theory at two altitudes**, and the seam
between them is exact: the 4 layers split into an *inference* half and a *training*
half, and **the trainable model is only the training half (Layers 3–4)**.

| Layer | What it does | In this model | Owner |
|---|---|---|---|
| **L1** — phase-internal error loop | Within one step: capture the error, re-prompt with it, retry (bounded) | **Adaptive forward pass** — the model self-corrects *inside* a decision point. Weights unchanged. | runtime (mostly shipped: the harness retry envelope) — **not this epic** |
| **L2** — cross-phase feedback | A later step rejects an earlier step's output and recycles back with structured corrective context | **Adaptive forward pass** — the episode revisits a prior step. Weights unchanged. **But the failure→(recycle)→success delta it leaves in the trace is the richest training datum** slice 2 reads. | runtime — **not this epic**, but its trace is slice 2's input |
| **L3** — pipeline self-patching | Patch the offending prompt/script/workflow itself, syntax-check, commit, restart | **The optimizer step** — a progressive-determinism edit to the weights, under budget. | **this epic, slice 3** |
| **L4** — cross-run knowledge extraction | Extract patterns across completed runs into a knowledge base; inject; analyze batch-level | **The epoch** — batched update over many episodes, on the existing [`session-mining`](../../tools/session-mining/README.md) ladder substrate. | **this epic, slice 3 (seam) + [`session-pattern-mining/`](session-pattern-mining/)** |

So the line is: **L1–L2 make a single run smarter (no weights change); L3–L4
make the story smarter for every future run — and, because the weights are
live-editable, potentially the current run too (weights change).** Kitsoki keeps
both — this epic does not absorb or replace L1–L2; instead it **mines their trace
output as signal**: an L1 retry burst (a step that needed three attempts) and an
L2 recycle (a failure→success delta within one episode) are exactly the labeled
data the loss (slice 1) and gradient (slice 2) feed on. The conventional
constraint "you can't mine the run you're in to improve it" is relaxed here — the
signal *and* the weight edit can both land live. The
`session-mining` kit is the half-built L4: it already mines transcripts and ranks
patterns on the L0→L4 progressive-determinism ladder ("climb a rung by recording
the decisions at a gate"); turning *kitsoki's own traces* into that input, and
feeding the result back as candidate edits, is the batched-epoch end of slice 3.

## Slices

| # | Slice | Kind | Scope (one line) | Depends on | Status | File |
|---|---|---|---|---|---|---|
| 1 | Reward function | runtime | Declarative pluggable `reward:` scorer over a finished episode; records a `reward` decision event | — | Draft | [`reward-function.md`](reward-function.md) |
| 2 | Credit assignment | tracing | Attribute a low-reward episode (paired with a high-reward one) to specific decision points; emit an `attribution` annotation | 1 | Draft | [`credit-assignment.md`](credit-assignment.md) |
| 3 | The training loop | runtime + story | Orchestrate score → attribute → candidate edit → validate (flow fixtures) → accept; drivable as a `train` process story | 1, 2 | Draft | [`training-loop.md`](training-loop.md) |

## Sequencing

```
#1 (reward function) ──▶ #2 (credit assignment) ──▶ #3 (training loop)
        └── the loss            └── the gradient          └── the optimizer + validation gate
```

Strictly serial by dependency: there is no gradient without a loss, and no
optimizer step without a gradient. Slice 1 is self-contained engine work and is
independently useful (a `reward` signal is valuable on its own as a run-quality
metric, even before any loop consumes it). Slice 3 is the only one that *closes*
the loop and is also where the existing 4-layer roadmap (Layers 2–4) gets
reconciled into the model.

## Shared decisions

1. **The mapping table above is canonical.** No child re-derives "what is a
   weight" or "what is the gradient"; each cites this section. The mutable
   surface = prompts, slot/synonym templates, `.star`, the room/intent/transition
   graph, decider configs. Everything else is fixed architecture.
2. **This is RL/evolutionary, not gradient descent.** Every slice treats the
   update as *propose-candidate → validate → keep-or-discard*, never as an
   autonomous continuous step. The four break-points above are the reason.
3. **Reward, attribution, and edits are all recorded decisions.** The moat
   ([`concept.md` §6](../architecture/concept.md)) holds: each new interpretive
   step (the reward judge, the attribution heuristic, the candidate-edit author)
   lands in the trace as a labeled datapoint, replayable and auditable.
4. **The flow-fixture suite is the validation set.** An edit is accepted only if
   it does not regress the no-LLM flow fixtures (and ideally improves the failing
   one). This is the overfitting guard and it is non-negotiable — it is what
   keeps "training" from becoming "prompt-tweak-and-pray" (the thing
   [`concept.md` §4](../architecture/concept.md) explicitly contrasts against).
5. **The model owns 4-layer L3–L4 only; L1–L2 stay as the adaptive forward
   pass.** Decided — see [§Relationship to the 4-layer model](#relationship-to-the-4-layer-model).
   No child re-litigates the boundary; slice 3 rewrites the competitive-analysis
   §4.5 to draw this line rather than presenting two separate roadmaps. The
   [`session-mining`](../../tools/session-mining/README.md) kit is the designated
   L4 substrate.

## Cross-cutting open questions

1. **Is reward per-episode or per-decision?** A scalar over the whole run is
   simplest and matches the failure→success-pair framing; per-decision reward
   (closer to dense RL) would make credit assignment trivial but is far harder to
   source. *Lean: per-episode reward (slice 1), heuristic per-decision
   attribution (slice 2) — i.e. sparse reward + estimated gradient.*
2. **Who authors the candidate edit in slice 3 — operator, or an LLM `task`?**
   The progressive-determinism conversions in `concept.md` §4 are author-driven
   today. *Lean: LLM-proposed, operator/flow-gated — the loop drafts the edit,
   the validation set + operator accept it. Decided in slice 3's child.*
3. **Does the L4 epoch reuse `session-mining` as-is, or fork a kitsoki-trace
   variant?** The kit mines Claude Code `*.jsonl` transcripts; kitsoki episodes
   are richer (typed decisions + `reward`/`attribution` events). *Lean: a kitsoki
   `distill` that emits the same report shape so the existing `aggregate`/`report`
   stages and the ladder vocabulary are reused. Decided in slice 3's child.*

## Non-goals

- **Actually fine-tuning or training an LLM.** The "model" here is the *story*;
  the LLM behind each decision point is an unchanged, frozen agent. We adjust
  the scripts/prompts/workflows around it, never its weights.
- **An autonomous self-modifying agent.** Every optimizer step is gated by the
  validation set and (per slice 3) operator acceptance and a budget. No unbounded
  self-patching — the 4-layer model's own Layer-3 risk note is heeded.
- **A new metrics/analytics surface.** Reward is a trace event; building
  dashboards over it is a downstream runstatus concern, not this epic.
- **Real-LLM training runs by default.** Every loop and validation pass is no-LLM
  (flow fixtures + cassettes) unless a real run is explicitly requested
  (CLAUDE.md).
