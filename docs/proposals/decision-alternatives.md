# Runtime: decide alternatives — record the runner-ups, not just the winner

**Status:** Draft v1. Nothing implemented yet.
**Kind:**   runtime
**Epic:**   ../trace-introspection.md

## Why

The `agent.decide` verb resolves a gate by returning a single chosen intent
plus a confidence — `judges.Verdict{Verdict, Intent, Reason, Confidence}`
(`internal/judges/judges.go:19-34`), with the schema declaring
`additionalProperties: false` so any richer structure the model produces is
**silently dropped** (`agent_decide.go` ~`:215`). The decider then records
`chosen_intent` + `confidence` into `machine.gate_decided`
(`decider.go:260-273`) and discards everything else.

So a confidence of `0.92` has nothing to be relative to except its
threshold: we never learn that `refine` scored `0.61` and `cancel` `0.05`.
That's our own gap, not a Langfuse one
(`.context/langfuse-trace-viewer-comparison.md`, idea #5): capturing the
ranked alternatives makes slice #2's confidence bar a legible *ladder*, and
gives the kind of decision-introspection no generic tracer has — every gate
becomes a small ranked dataset, feeding the decision-as-labeled-datapoint
self-improvement loop (`feedback_kitsoki_moat_is_architecture`).

## What changes

One sentence: **the decide verdict gains an optional ranked `alternatives`
list (`[{intent, score, reason?}]`) over the gate's `available_intents`; the
decider records it into `machine.gate_decided`; nothing about the *chosen*
intent or the deterministic transition changes.**

The winner is still `Intent` at the top of the ranking; `alternatives` is
additive and optional, so a decider (or a local-model backend) that returns
only the winner still works.

## Impact

- **Code seams:**
  - `internal/judges/judges.go:19-34` — add `Alternatives []IntentScore` to
    `Verdict`; relax the schema to allow the array (still
    `additionalProperties: false` on each entry).
  - `internal/host/agent_decide.go` ~`:215` — parse alternatives when
    present; the decide prompt/schema asks for a score per available intent.
  - `internal/orchestrator/decider.go:260-273` — add `alternatives` to the
    `gate_decided` payload.
- **Vocabulary:** the decide schema gains a ranked-scores field; the
  `gate_decided` event gains an `alternatives` attr (also a tracing concern —
  see Decision recording).
- **Stories affected:** none behaviorally. Every story that uses a decide
  gate gains richer trace data; the chosen intent and transition are
  unchanged, so all cassettes/flows still resolve identically.
- **Backward compat:** additive + optional. Old cassettes whose recorded
  decide responses carry no `alternatives` replay byte-identically; the
  field is absent, not empty-required.
- **Docs on ship:** `docs/stories/state-machine.md` (decide gate emits
  ranked alternatives), `docs/tracing/trace-format.md` (the
  `gate_decided.alternatives` attr).

## Vocabulary changes

| Kind | Name | Shape | Notes |
|---|---|---|---|
| schema field | `Verdict.alternatives` | `[{intent: string, score: float [0,1], reason?: string}]` | one entry per `available_intent`, ranked desc; winner == `intent` |
| gate / decider | `gate_decided.alternatives` | same array, recorded | additive attr; absent on legacy traces |

## The model

```
gate ──▶ agent.decide(available_intents, threshold)
            │  INTERPRETIVE (recorded): ranks ALL available intents,
            │  returns winner + confidence + ranked alternatives
            ▼
        decider.resolveAutoGate
            │  DETERMINISTIC: chosen = Verdict.Intent (unchanged);
            │  confidence vs Threshold (unchanged) decides bail-to-human
            ▼
        machine.gate_decided{ chosen_intent, confidence, alternatives, … }
```

The boundary the moat cares about is untouched: the *ranking* is interpretive
(the LLM/operator's judgment, now fully recorded); the *selection* of the top
intent and the threshold comparison stay deterministic engine code
(`decider.go`). This slice only widens what the interpretive step is asked to
report — it does not move the decision boundary.

## Decision recording

`machine.gate_decided` is the moat event. Today it records the winner +
confidence; this slice makes it record the **full ranked judgment** the
decider saw:

```jsonc
{ "msg": "machine.gate_decided", "state_path": "proposing",
  "attrs": { "decider": "llm", "chosen_intent": "accept", "confidence": 0.92,
             "threshold": 0.80, "bailed_to_human": false,
             "alternatives": [ {"intent":"accept","score":0.92},
                               {"intent":"refine","score":0.61},
                               {"intent":"cancel","score":0.05} ] } }
```

This is the field slice #2 renders as a ranked confidence ladder. Because it
lands on an existing event as an optional attr, it's a tracing change too —
documented in `trace-format.md` alongside slice #1.

## Engine seams & invariants

- **Where it hooks:** the verdict parse in `agent_decide.go` (~`:215`) and
  the payload build in `decider.go:recordGate` (`:260-273`).
- **Load-time invariant:** none new — the schema relaxation is validated by
  the existing agent-schema conformance path. A defensive runtime check:
  if `alternatives` is present, every entry's `intent` must be a member of
  the gate's `available_intents` (drop unknowns with a warning rather than
  fail the turn — a hallucinated extra intent must never become selectable).
- **Selection unchanged:** the engine still selects `Verdict.Intent`; it
  does **not** re-rank or pick from `alternatives`. Alternatives are
  record-only. This is the invariant that keeps the change safe.

## Backward compatibility / migration

- **Default behavior:** the decide prompt/schema asks for alternatives, but a
  response without them is valid (optional field). No story migration.
- **Cassettes:** existing recorded decide responses have no `alternatives`;
  they replay unchanged and `gate_decided` simply omits the attr. New
  recordings capture it going forward. The Layer-7 byte-equality replay
  guard (`internal/runstatus/snapshot.go` `FromSink`) confirms legacy
  cassettes stay byte-identical.
- **Local-model backend** (`local-model-agent.md`): grammar-forced output
  can include the ranked array, but small models may rank poorly — the field
  being optional means a backend can omit it without breaking gates.

## Tasks

```
## 1. Engine
- [ ] 1.1 Add Verdict.alternatives ([{intent,score,reason?}]); relax the decide schema (still additionalProperties:false per entry)
- [ ] 1.2 Parse alternatives in agent_decide.go; runtime check that each intent ∈ available_intents (drop+warn unknowns)
- [ ] 1.3 Record alternatives (+ threshold) into machine.gate_decided (decider.go recordGate)

## 2. Verification
- [ ] 2.1 Stateless unit: a decide gate flow fixture whose stubbed response carries ranked alternatives → gate_decided.attrs.alternatives present, chosen unchanged
- [ ] 2.2 Legacy flow fixture (no alternatives) still resolves the same intent; replay byte-identical (Layer-7)
- [ ] 2.3 Unknown-intent alternative is dropped, not selected

## 3. Adopt + document
- [ ] 3.1 Update one real decide gate's prompt/schema to ask for ranked scores (e.g. bugfix judge)
- [ ] 3.2 Update docs/stories/state-machine.md + docs/tracing/trace-format.md; trim/delete this slice
```

## Verification

No LLM needed: a flow fixture stubs the decide agent's response (per
`feedback_agent_stub_by_id`) to return a verdict *with* a ranked
`alternatives` array, and a stateless `kitsoki turn` (or the flow runner)
asserts the `gate_decided` event carries it while the chosen intent and
transition match the no-alternatives baseline. A mutation check — stub an
alternative ranked *above* the chosen intent — must confirm the engine still
selects `Verdict.Intent` (record-only invariant), proving alternatives can't
hijack selection. No real-LLM test is added.

## Open questions

1. **Score semantics — calibrated probability or relative rank?** If the LLM
   emits arbitrary scores, the bar is ordinal not metric. *Lean: treat as
   relative rank for display (slice #2 shows the ladder, not absolute
   percentages); `confidence` for the *winner* keeps its existing
   threshold-comparison meaning.*
2. **Must alternatives cover every available intent, or just the top-k?**
   *Lean: ask for all (gates are small), but accept a partial list — the UI
   shows "(others unscored)" rather than fabricating zeros.*

## Non-goals

- **Re-ranking or engine selection from alternatives.** The engine selects
  the chosen intent exactly as today; this slice is record-only.
- **The confidence-ladder UI** — slice #2 renders it; this slice only emits
  the data.
- **Routing alternatives.** This is about decide gates. Capturing runner-up
  routes for `turn.start` semantic routing is a separate, larger change to
  `internal/orchestrator/semantic.go` — out of scope.
