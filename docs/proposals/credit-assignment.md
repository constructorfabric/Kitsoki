# Tracing: failure→success credit assignment

**Status:** Draft v1. Nothing implemented yet.
**Kind:**   tracing
**Epic:**   [`stories-as-trainable-models.md`](stories-as-trainable-models.md) (slice 2)

## Why

Slice 1 gives a low-reward episode a *score*. That tells you the story did badly;
it does not tell you **which decision point to change** — and a story's
forward pass touches many: a routing call, a slot template, an `agent.decide`
gate, a `.star` script, a host invocation, a transition the author declared. A
neural net answers "which weight" with a gradient. Kitsoki has no gradient, but
it has something a black-box net does not: a complete, replayable trace of every
decision the run made ([`overview.md:450`](../architecture/overview.md)). The
"gradient" we can compute is **trace-attributed credit assignment** — walking
that trace backward from the scored outcome to the decision points most likely
responsible for the loss.

The signal that makes this tractable is the one the epic names as the key datum:
the **failure → success pair**. When the same (or an adjacent) input scored low
under one story surface and high under another — a later run, a candidate edit, a
human-corrected replay — the *delta between the two traces* localizes the
responsible decision far more sharply than a single failed trace ever could. This
slice records that attribution as a trace annotation that slice 3's optimizer
consumes.

The richest source of those pairs is **already produced by the runtime**: the
4-layer model's Layer 2 (cross-phase feedback) recycles a failed step back to an
earlier step with corrective context and then succeeds — a failure→success delta
*within a single episode*
([epic §Relationship to the 4-layer model](stories-as-trainable-models.md#relationship-to-the-4-layer-model)).
That in-run recycle is the cleanest possible pair (same input, same run, one
corrective nudge between the two outcomes), and the corrective context it carried
(`cycle`, `retry_phase`, `reasoning`, `instructions`) is a near-explicit label for
*what the weights were missing*. Slice 2 treats an L2 recycle as a first-class
pairing source, not only cross-run pairs.

## What changes

Mostly a **consumer** of what's already traced (decisions + slice-1 `reward`
events), plus one new annotation. Given a low-reward episode — and, when
available, a paired high-reward episode on the same input key — the engine
produces a ranked `attribution`: the decision points whose presence/value most
plausibly explains the reward delta, each tagged with *which kind of weight* it
is (prompt / slot template / `.star` / graph edge / decider). Failure→success
pairs become first-class, queryable trace artifacts rather than two unrelated
runs an operator happens to eyeball side by side.

## Impact

- **Producers:** one new `attribution` annotation, emitted by a new
  `internal/attribution/` analyzer run over a finished trace (offline/on-demand,
  not in the hot turn loop). No change to existing decision producers.
- **Consumers:** slice 3's training loop (the primary consumer); the trace
  viewer / runstatus can render attributions as highlighted rows on the timeline.
- **Format:** new `attribution` annotation type; an episode-pairing key carried
  on/derived from existing events (no change to decision events themselves).
- **Backward compat:** purely additive — old traces and cassettes load and replay
  unchanged; attribution is computed *over* them, never required *by* them.
- **Docs on ship:** `docs/tracing/` (the `attribution` annotation + pairing).

## Event / format model

Attribution is an **annotation** — derived, written alongside a trace, never
mutating the recorded decisions it points at (replay determinism, below). It
references decisions by their existing ids.

```jsonc
{ "type": "attribution", "ts": "…",
  "episode_id": "run-A",            // the low-reward episode
  "paired_with": "run-B",           // the high-reward episode on the same input_key, or null
  "input_key": "scenario:scale-frontend",
  "reward_delta": 0.0,              // A.score; or B.score - A.score when paired
  "ranked": [
    { "decision_ref": "evt:agent.decide#3", "weight_kind": "decider",
      "delta": "A routed `clarify`, B routed `scale`", "blame": 0.71 },
    { "decision_ref": "evt:slot.match#1",    "weight_kind": "slot_template",
      "delta": "template missed `replicas` in A", "blame": 0.22 }
  ],
  "method": "pairwise-trace-diff" }
```

| Event | When emitted | Key fields |
|---|---|---|
| `attribution` | On-demand, after an episode is scored (slice 3 requests it, or an operator does) | `episode_id`, `paired_with`, `input_key`, `reward_delta`, `ranked[]` (`decision_ref` + `weight_kind` + `blame`), `method` |

The `weight_kind` is what makes the attribution actionable: it tells slice 3
*what category of edit* to draft, mapping straight onto the
[epic's weight definition](stories-as-trainable-models.md#the-mapping-shared-decision--see-shared-decisions).

## Determinism

Attribution must be **reproducible from the same inputs** so a replay lines up:

- It is a **pure function of the two traces + the slice-1 reward events** — no
  fresh I/O, no live LLM (a `decide`-based blame heuristic, if used, runs against
  a cassette). The same trace pair → byte-identical `ranked` ordering.
- `decision_ref`s reuse existing **deterministic** event ids; attribution adds no
  new ids to the decisions themselves.
- The annotation is written to a **sidecar** keyed by `episode_id` (the same
  pointer-not-inline pattern used by
  [`transcript_ref`](../tracing/trace-format.md#agent-action-transcript-sidecar)),
  not inlined into the trace — so a pre-change trace plus its attribution is
  exactly the old trace with a separate file beside it.

## Producers & consumers

- **Producer:** `internal/attribution/` — a `Pair(low, high) → Attribution`
  analyzer. Two methods, escalating: (a) **pairwise-trace-diff** (deterministic,
  default) — align the two traces by room/decision position, find the first
  divergent decision, weight blame by proximity to the scored outcome; (b)
  **decide-ranked** (optional, cassette-gated) — an `agent.decide` judge ranks
  candidate decisions when the structural diff is ambiguous. Method (a) is the
  re-runnable, script-form preference from [`concept.md` §3](../architecture/concept.md);
  (b) is the judgment fallback, recorded as interpretive.
- **Intra-run (L2) pairing:** when a trace contains a Layer-2 recycle (a step
  failed, looped to an earlier step with corrective context, then succeeded), the
  two passes over the recycled step *are* the pair — `Pair(failed-pass,
  succeeded-pass)` over one episode, with the recycle's corrective payload joined
  in as a pre-labeled hint. Strongest signal, no cross-run matching needed.
- **Intra-step (L1) signal:** a Layer-1 retry burst (a step that erred and
  re-prompted N times before succeeding) is a localized blame beacon on *that*
  step's prompt/decider, scaled by N — even with no terminal failure. It feeds
  blame weighting directly (no pairing needed), turning the in-run self-correction
  the runtime already does into attribution input.
- **Single-trace fallback:** with no pair (`paired_with: null`), blame the
  decision points on the path to the failure terminal, weighted by reward-source
  proximity (which scorer fired the failure). Weaker, honestly flagged via
  `method`, and the epic's reason for preferring pairs.
- **Consumers:** slice 3 reads `ranked[0].weight_kind` + `decision_ref` to scope
  the candidate edit; the trace timeline highlights blamed rows (a small
  runstatus follow-up, not in this slice).

## Backward compatibility

Old traces and cassettes are untouched and still load/replay byte-identically —
attribution is computed over them and stored in a sidecar. The `attribution`
annotation is optional everywhere; nothing in the engine requires it to run a
session. No fixture regen for existing flows.

## Fixtures / golden traces

- A **paired golden:** two checked-in traces for one `input_key` — a failure run
  and a success run that differ at exactly one decision — and the expected
  `attribution` sidecar. The regression contract: `Pair(low, high)` over the two
  goldens reproduces the `ranked` ordering byte-for-byte. Regenerated by a
  documented `go test -run Attribution -update` (or equivalent) command.
- A **single-trace golden** exercising the no-pair fallback and asserting `method`
  is flagged as the weaker single-trace form.

## Tasks

```
## 1. Emit / consume
- [ ] 1.1 `internal/attribution/`: pairwise-trace-diff analyzer over two scored traces
- [ ] 1.2 Single-trace fallback (path-to-failure blame), `method` honestly flagged
- [ ] 1.3 Write the `attribution` sidecar keyed by episode_id; deterministic `ranked` ordering

## 2. Prove
- [ ] 2.1 Paired golden (failure + success differing at one decision) → expected attribution, byte-stable
- [ ] 2.2 Single-trace golden exercises the fallback; old traces/cassettes still replay unchanged

## 3. Document
- [ ] 3.1 Document the `attribution` annotation + failure→success pairing in docs/tracing/; trim from the epic
```

## Open questions

1. **Episode pairing key** — explicit `input_key` declared by the story, or
   inferred (hash of the opening user turn / scenario id)? *Lean: explicit when
   the story declares scenarios (flows already have ids), inferred fallback
   otherwise.*
2. **Blame weighting** — pure structural (first divergence + proximity) for v1, or
   bring the reward *source* into the weight (blame the path that fed the failing
   scorer)? *Lean: structural v1; source-aware weighting is a fast follow once
   slice 1's source breakdown is in the `reward` event.*
3. **How many candidates does `ranked` carry?** *Lean: top-k (k=3) — slice 3
   tries them in order; more is noise.*

## Non-goals

- **Computing a real gradient.** This is a heuristic estimate over a discrete
  trace (the epic's break-point #1); attributions are ranked hypotheses, not
  derivatives.
- **Making the edit.** Slice 3 drafts and validates the change; this slice only
  says *where* and *what kind*.
- **A trace-viewer UI for attributions.** Highlighting blamed rows in runstatus
  is a separate, small follow-up.
