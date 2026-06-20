# Tracing: observation kinds — a semantic taxonomy over EventKind

**Status:** Draft v1. Nothing implemented yet.
**Kind:**   tracing
**Epic:**   ../trace-introspection.md

## Why

`runstatus` renders each event by ad-hoc per-component matching: the
timeline enumerates subsystem prefixes by hand for its filter chips
(`TraceTimeline.vue:243`), `EventDetail.vue` dispatches to a specific detail
component per kind, and the agent/host detail components each re-derive
"what am I" from the raw `msg`. There is no single answer to "what *kind of
thing* is this event" — so badging, coloring, collapse-by-category, and a
future Graph-view layout-by-kind are each reinvented per surface.

Langfuse gets its rendering richness for free because the observation
*type* is semantic — a Generation renders one way, a Retriever another
(`.context/langfuse-trace-viewer-comparison.md`, idea #2). We already have
the equivalent raw signal: every `EventKind` is a dotted string with a
subsystem prefix (`turn.*`, `agent.*`, `machine.*`, `world.*`, `harness.*`)
and a clear semantic role. What's missing is *naming the small set of
semantic categories once* so every consumer agrees.

## What changes

One sentence: **define a canonical, closed set of observation kinds —
`decision | agent-call | host-call | narration | world-mutation | routing |
lifecycle` — as a pure function of the existing `Event.Kind` string, exposed
once on the Go side (and mirrored in the SPA), so consumers badge, color,
collapse, and lay out by category instead of re-matching `msg`.**

This is a *consumer-substrate* change, not a producer change: **no new
per-event field, no new event, no trace-format wire change.** The category
is derived, never stored — consistent with "the UI is a projection of the
immutable event stream" (epic Shared decision 1). The deliverable is a
documented mapping table + the one place that owns it.

## Impact

- **Producers:** none. `internal/store/event.go` is unchanged.
- **Consumers:** a new canonical map (Go: `internal/store/observation.go` →
  `ObservationKind(kind string) Kind`; SPA: `src/lib/observation.ts`
  mirroring it). Refactor `TraceTimeline.vue:243` (filter chips),
  `EventDetail.vue` (detail dispatch), and the agent/host detail routing to
  read the category instead of re-matching prefixes.
- **Format:** no wire change. The taxonomy is documented in
  `docs/tracing/trace-format.md` as the canonical reading of `EventKind`.
- **Backward compat:** total. Old traces/cassettes carry the same
  `EventKind` strings; the category is computed at read time, so every
  existing fixture and artifact keeps rendering (better, not differently).
- **Docs on ship:** `docs/tracing/trace-format.md` (the kind→category table).

## Event / format model

No new event. The taxonomy is a projection of the constants in
`internal/store/event.go:20-208`:

| Observation kind | `EventKind`(s) it covers | Why grouped |
|---|---|---|
| `decision` | `machine.gate_decided` (`:141`), `agent.off_path.{question,answer}` (`:67,:70`) | An interpretive choice with available/chosen/confidence — the moat; rendered decision-first (slice #2) |
| `routing` | `turn.start` (`:22`, carries tier/match/confidence), `machine.intent_accepted` (`:95`) | What advanced the turn and how it was routed |
| `agent-call` | `agent.call.{start,complete,error}` (`:148,:153,:158`), `agent.tool_call` (`:29`) | An LLM/operator call with prompt/response/cost/latency |
| `host-call` | `harness.{called,dispatched,returned,error}` (`:50,:57,:59,:118`) | Deterministic side-effecting execution |
| `narration` | `machine.say` (`:45`), `turn.end` (`:89`, carries the rendered `view`) | Operator-facing text |
| `world-mutation` | `world.update` (`:39`) | A `set:` world write |
| `lifecycle` | `machine.{state_exited,state_entered,transition,validation_failed,guard_rejected,timeout,error}`, `scheduler.*`, `session.story`, `story.changed`, `ide.context_captured` | Structural/bookkeeping events |

```jsonc
// No new event. ObservationKind("machine.gate_decided") == "decision"
// computed at read time; the on-disk line is unchanged.
{ "msg": "machine.gate_decided", "attrs": { "decider": "llm", "chosen_intent": "accept", "confidence": 0.92 } }
```

## Determinism

The mapping is a **pure total function** of the `Kind` string — same input,
same category, no I/O, no clock. It cannot affect replay because it touches
no producer and adds no field. The closed-set guarantee is enforced by a Go
test that asserts every declared `EventKind` constant maps to exactly one
non-empty category (fail-fast if a future kind is added without
classifying it).

## Producers & consumers

- **Producer:** unchanged — this slice adds none.
- **Canonical owner:** `internal/store/observation.go` holds the one
  `switch` (or table) over `EventKind`. `docs/tracing/trace-format.md`
  documents it; the test guards it against drift.
- **Consumers read the category, never the prefix.** The SPA mirror
  (`src/lib/observation.ts`) is the single source for chip grouping, row
  badge/color, and collapse-by-category. A unit test asserts the Go and TS
  tables agree (golden JSON dumped from the Go side, asserted in Vitest).

## Backward compatibility

Pure addition of a derived view. Every recorded trace, cassette, and
checked-in `*.snapshot.json` fixture loads and renders unchanged — the
category is computed from the `Kind` they already carry. No fixture
regeneration required.

## Fixtures / golden traces

- A Go golden test (`observation_test.go`) dumps the full
  `EventKind → ObservationKind` table to JSON; Vitest asserts the TS mirror
  matches it byte-for-byte — the cross-language drift guard.
- The existing `tools/runstatus/fixtures/bugfix.snapshot.json` is the render
  regression contract: after the consumer refactor, `bugfix.spec.ts` must
  still pass (rows now grouped/badged by category, same content).

## Tasks

```
## 1. Emit / consume
- [ ] 1.1 internal/store/observation.go: ObservationKind(kind) closed-set map + exported Kind type
- [ ] 1.2 Go test: every EventKind constant maps to exactly one non-empty category (drift guard)
- [ ] 1.3 src/lib/observation.ts: mirror; Vitest asserts it equals the Go golden dump
- [ ] 1.4 Refactor TraceTimeline.vue:243 chips, EventDetail.vue dispatch, agent/host routing to read the category

## 2. Prove
- [ ] 2.1 bugfix.spec.ts still green; rows badge/group by category
- [ ] 2.2 Go + Vitest taxonomy tables proven equal

## 3. Document
- [ ] 3.1 Add the kind→category table to docs/tracing/trace-format.md; trim/delete this slice
```

## Open questions

1. **Should `decision` include `off_path.{question,answer}`?** Off-path is a
   read-only meta turn, not a gate. *Lean: yes — it's still an interpretive
   choice the operator should be able to filter on; slice #2 can style it
   distinctly within the category.*
2. **Mirror by codegen or by hand?** A hand-mirrored TS table is simplest
   but can drift; the golden-equality test (1.3) catches drift either way.
   *Lean: hand-mirror + golden test; codegen is overkill for ~30 constants.*

## Non-goals

- **Recording a category on the event.** It's derived, not stored (epic
  Shared decision 1). If a future need forces it onto the wire, that's a new
  proposal.
- **The decision-first detail rendering itself** — that's slice #2; this
  slice only gives it the `decision` category to key on.
- **A Graph view** — slice #3 may lay out by category, but the layout is its
  concern; this slice only supplies the categories.
