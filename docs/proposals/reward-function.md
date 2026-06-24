# Runtime: the reward function

**Status:** Draft v1. Nothing implemented yet.
**Kind:**   runtime
**Epic:**   [`stories-as-trainable-models.md`](stories-as-trainable-models.md) (slice 1)

## Why

A model can't be trained without a loss. Kitsoki already produces the *training
set* — every decision is a labeled datapoint in the event log
([`overview.md:450`](../architecture/overview.md)) — and the *forward pass*
(running a session). What it can't do is say, mechanically, **how good a finished
episode was**. Today that judgment lives in an operator's head as they read a
trace, or it is implicit in scattered places — a flow assertion that passed, an
`agent.decide` that accepted, a terminal room that was (or wasn't) reached, a
`host.run` exit code. The signal is *there*, in the trace, but it is never
collected into one scalar the rest of the training loop (the epic's slices 2–3)
can optimize against.

This slice adds that scorer: a declarative, pluggable **reward function** that
scores a completed episode and records the score as a decision. It is the loss
in the [epic's mapping table](stories-as-trainable-models.md#the-mapping-shared-decision--see-shared-decisions),
and it is independently useful before any loop consumes it — a per-run quality
metric an operator can sort and filter on.

## What changes

A story may declare a `reward:` block: an ordered list of **signal sources**, each
resolved by a **scorer** (the decider analog), combined into one scalar in
`[0,1]` plus a discrete `label` (`success` / `failure` / `partial`). When an
episode reaches a terminal room (or is explicitly closed), the engine evaluates
the block and emits one `reward` event. *Every story room/phase ends a run; every
finished run gets exactly one scored reward.*

## Impact

- **Code seams:** new `internal/reward/` (scorer registry + episode scorer),
  invoked from the turn loop where a terminal room is detected (alongside the
  existing terminal handling in the orchestrator); `reward:` parsed in the app
  loader next to `world:` declarations.
- **Vocabulary:** a new top-level `reward:` block; a new `reward` trace event; no
  new effects or host calls (reward is computed by the engine over world + trace,
  not fired by a transition).
- **Stories affected:** none change behavior — `reward:` is opt-in and absent by
  default. A story with no block emits no `reward` event (back-compat).
- **Backward compat:** fully additive; existing stories, flows, and cassettes are
  unchanged and untouched.
- **Docs on ship:** `docs/stories/state-machine.md` (the `reward:` block),
  `docs/tracing/` (the `reward` event).

## Vocabulary changes

| Kind | Name | Shape | Notes |
|---|---|---|---|
| block | `reward:` | `{ sources: [...], combine: <strategy> }` | top-level, opt-in |
| scorer | `terminal` | room name → `{score, label}` | deterministic: which terminal room was reached |
| scorer | `flow` | assertion outcome → `{score, label}` | deterministic: did the run satisfy declared assertions |
| scorer | `host` | exit code / parsed output → `{score, label}` | deterministic: e.g. a test command's exit code |
| scorer | `decide` | reuses `agent.decide` accept/reject | the existing gate verdict as reward |
| scorer | `effort` | L1 retry / L2 recycle counts → `{score, label}` | deterministic: in-run self-correction is a cost/quality signal (a step that needed 3 retries scores below a first-try success) |
| scorer | `operator` | human accept/refine rating | deferred reward; resolved when the operator rates |
| gate / decider | `combine` | `default \| llm \| human` | how multiple source scores fold into one scalar |

## The model

The reward block mirrors the existing gate/decider shape
([`docs/stories/architecture.md:501`](../stories/architecture.md)) deliberately —
a reward is just a *gate whose choices are scores*, resolved by a scorer instead
of a router. Reuse the decider taxonomy: a `default` scorer is deterministic
(read world/trace, return a fixed score); an `llm` scorer is an `agent.decide`
judge ("rate this finished run 0–1 with a reason"); a `human` scorer is an
operator rating surfaced via the operator bridge.

```
{terminal room reached} ──▶ [reward block]
    sources: terminal + flow + host  ──each scorer(default|llm|human)──▶ {score, label}
    combine: default(min|mean|weighted)  ──▶  reward ∈ [0,1], label ∈ {success|failure|partial}
                                              ──▶  emit `reward` event
```

- **Interpretive vs deterministic** is explicit per scorer: `terminal`/`flow`/
  `host` are deterministic and replay byte-identically; `decide`/`operator` are
  interpretive and recorded as such (the moat). A story can have an entirely
  deterministic reward (the preferred form — re-runnable, like the script-vs-object
  preference in [`concept.md` §3](../architecture/concept.md)) or an LLM/human one
  when the outcome genuinely needs judgment.
- **Sparse and deferred reward is first-class.** An `operator` source may not
  resolve at episode end; the `reward` event is emitted with `label: pending` and
  amended (or a second event appended) when the rating arrives. This matches the
  epic's "reward is sparse/delayed" break-point.

## Decision recording

The reward is a recorded, labeled datapoint — the moat
([`concept.md` §6](../architecture/concept.md)). One `reward` event per finished
episode:

```jsonc
{ "type": "reward", "ts": "…", "episode_id": "…",
  "score": 0.0, "label": "failure",
  "sources": [ { "scorer": "terminal", "kind": "default", "score": 0.0, "room": "abandoned" },
               { "scorer": "host", "kind": "default", "score": 0.0, "detail": "exit=1" } ],
  "combine": "min", "interpretive": false }
```

For an `llm`/`human` source, the entry carries the prompt/world snapshot id and
rationale so a reviewer can audit *this* score (same contract as any
`agent.decide`). This event is what slice 2's credit assignment reads to know
which episodes are failures and which are successes.

## Engine seams & invariants

- The scorer runs **once**, when the orchestrator detects a terminal room (or an
  explicit `close-episode` effect). Hook beside the existing terminal handling;
  do not score mid-run (mid-run scoring is the per-decision-reward option the epic
  deferred).
- **Load-time invariants** (fail-fast at load, not at runtime): every `source`
  names a registered scorer; `terminal`/`host` sources reference rooms/hosts that
  exist; `combine` is a known strategy; an `llm` scorer names a real `agent.decide`
  schema. A malformed `reward:` block fails the load with a clear message, like
  every other story invariant.
- **DI:** the scorer registry is injected (not a package global), so tests supply
  a deterministic scorer and the operator scorer is a no-op when no operator is
  attached (mirrors the operator-ask bridge in CLAUDE.md).

## Backward compatibility / migration

Default-off and additive. No `reward:` block → no `reward` event → identical
behavior and identical traces/cassettes. No migration. A story adopts reward by
adding the block; nothing else changes.

## Tasks

```
## 1. Engine
- [ ] 1.1 `internal/reward/`: scorer registry (DI) + episode scorer + combine strategies
- [ ] 1.2 Parse `reward:` in the loader; load-time invariants + clear errors
- [ ] 1.3 Invoke at terminal-room detection; emit the `reward` event (incl. `pending`/deferred path)

## 2. Verification
- [ ] 2.1 Stateless unit: a deterministic scorer over a hand-built world/trace returns the expected {score,label}
- [ ] 2.2 Flow fixture: a story with a `reward:` block emits the expected `reward` event on the failure and the success path
- [ ] 2.3 No-op operator scorer when no operator attached (headless/cassette)

## 3. Adopt + document
- [ ] 3.1 Add a deterministic `reward:` block to one real story (e.g. a bugfix terminal: merged=success / abandoned=failure)
- [ ] 3.2 Document the block in state-machine.md + the event in docs/tracing/; trim this slice from the epic
```

## Verification

A reviewer confirms without an LLM: build a world+trace fixture for a finished
run, run the deterministic scorer, assert the `{score, label}`; then run a flow
fixture that drives a story to its failure terminal and to its success terminal
and assert the two `reward` events. The only LLM-needing case is the `decide`
scorer, which is exercised via a cassette (recorded judge), never a live call
(CLAUDE.md).

## Open questions

1. **Score range / type** — scalar `[0,1]`, or a richer struct (score + label +
   confidence)? *Lean: scalar + label now; confidence only if slice 2 needs it.*
2. **One event or amend-in-place for deferred operator reward?** *Lean: append a
   second `reward` event (append-only trace is cleaner than mutation); slice 2
   reads the latest.*

## Non-goals

- **Per-decision (dense) reward.** Deferred to the epic; this slice scores whole
  episodes only.
- **Dashboards/aggregation over reward.** A downstream runstatus concern.
- **Choosing the edit that improves reward.** That's slices 2 (where) and 3 (what)
  — this slice only measures.
