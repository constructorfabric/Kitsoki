# Tracing: {Title}

**Status:** Draft v1. Nothing implemented yet.
**Kind:**   tracing
**Epic:**   — standalone   <!-- or ../{epic}.md -->

<!--
  A "tracing" proposal changes the observability substrate: trace event
  types and fields, cassette fidelity, the run-status surfaces that consume
  traces, or golden-trace fixtures.

  This kind is where the moat becomes visible: every interpretive decision
  the engine makes should already land in the JSONL trace as a labeled
  datapoint. A tracing proposal usually either (a) records something that
  isn't captured yet, or (b) builds a consumer that reads what's already
  there. Be clear which.

  References: docs/tracing/trace-format.md; docs/tracing/cassettes.md;
  the runstatus proposals; existing cassettes under stories/*/cassettes/
  (or wherever they live).
-->

## Why

<!-- What can't be seen, reproduced, or attributed today. Who needs it —
     an operator supervising a run, a developer debugging, a reviewer
     replaying a decision? -->

## What changes

<!-- One screen. New/changed events vs. a new consumer of existing events.
     The one sentence that frames it ("cassettes must carry agent metadata
     so runstatus snapshots show model + token usage per step"). -->

## Impact

- **Producers:** {where events are emitted — file:line}
- **Consumers:** {cassettes, runstatus, `kitsoki render`, export-status}
- **Format:** {new event types / fields; schema change}
- **Backward compat:** {do existing traces / cassettes still load & replay?}
- **Docs on ship:** `docs/tracing/{…}.md`

## Event / format model

<!-- The concrete shape. New event types and the fields that make a
     decision reconstructable. Show a sample JSONL line. -->

```jsonc
{ "type": "{event.type}", "ts": "…", "{field}": "…", "meta": { … } }
```

| Event | When emitted | Key fields |
|---|---|---|
| `{ns.event}` | {trigger} | {fields that carry the decision} |

## Determinism

<!-- The thing that makes tracing trustworthy. Are ids (e.g. call_id)
     deterministic so replays line up? Does a cassette replay produce a
     byte-identical trace? Spell out the invariant and how it's enforced. -->

## Producers & consumers

<!-- Where the data originates and who reads it. If this adds a consumer
     (e.g. a runstatus surface), sketch how it reads the trace. If it adds
     a producer, name every call site that must emit. -->

## Backward compatibility

<!-- Old traces and cassettes are on disk and in fixtures. Do they still
     load? Is the new field optional? What happens on replay of a
     pre-change cassette? -->

## Fixtures / golden traces

<!-- The regression contract. Which golden trace or cassette proves the
     new field is emitted / consumed correctly, and how a reviewer
     regenerates it. -->

## Tasks

```
## 1. Emit / consume
- [ ] 1.1 {add event field at producer | add consumer}
- [ ] 1.2 Deterministic id / ordering preserved
- [ ] 1.3 Backward-compat path for old traces/cassettes

## 2. Prove
- [ ] 2.1 Golden trace / cassette fixture updated; replay is stable
- [ ] 2.2 Consumer renders the new data (snapshot or fixture)

## 3. Document
- [ ] 3.1 Update docs/tracing/; trim/delete this proposal
```

## Open questions

1. {Question} — {options}. *Lean: {x}.*

## Non-goals

- {Adjacent observability work this defers — e.g. "token breakdown is runstatus, not here."}
