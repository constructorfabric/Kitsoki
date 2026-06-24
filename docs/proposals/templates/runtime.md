# Runtime: {Title}

**Status:** Draft v1. Nothing implemented yet.
**Kind:**   runtime
**Epic:**   — standalone   <!-- or ../{epic}.md -->

<!--
  A "runtime" proposal changes engine behavior: the state machine, gates
  and deciders, the effect/host vocabulary, world semantics, or load-time
  invariants. The bar is higher than a story proposal because this is the
  shared substrate every story runs on.

  Hold the line on the moat (see memory): separate interpretive decisions
  from deterministic execution; make each decision point pluggable; record
  every decision. A runtime change that blurs that boundary needs to say so
  loudly under Open questions.

  References: docs/stories/state-machine.md, docs/architecture/, the
  execution-modes-and-gate-deciders and auto-advance-states proposals.
-->

## Why

<!-- What can't be expressed today, or what's expressed by hand-rolled
     duplication across stories that this would generalize. Name the
     stories that currently work around the gap. -->

## What changes

<!-- The mechanism, in one screen. What concept is added or changed at the
     engine level, and the one sentence that captures it
     ("every room/phase ends in an intent gate resolved by a decider"). -->

## Impact

- **Code seams:** {package / file:line where this lands}
- **Vocabulary:** {new effects / host calls / world keys / gate or decider types — table below}
- **Stories affected:** {which existing stories change behavior, if any}
- **Backward compat:** {existing stories keep working? default off? migration needed?}
- **Docs on ship:** `docs/stories/state-machine.md`, `docs/architecture/{…}.md`

## Vocabulary changes

<!-- The concrete surface authors and the engine will see. Drop rows/cols
     that don't apply. -->

| Kind | Name | Shape | Notes |
|---|---|---|---|
| effect | `{effect}` | `{args}` | {when it fires} |
| host call | `host.{ns}.{verb}` | `{in → out}` | {side effects} |
| world key | `{key}` | `{type}` | {who writes / reads} |
| gate / decider | `{name}` | `{default \| llm \| human}` | {what it resolves} |

## The model

<!-- The mechanism in detail. Diagram the gate/decider/state flow if it
     helps. Be explicit about what is INTERPRETIVE (LLM/human, recorded)
     vs. DETERMINISTIC (engine, replayable). -->

```
{room/phase} ──▶ [gate] ──decider(default|llm|human)──▶ intent ──▶ {transition}
```

## Decision recording

<!-- How does this change show up in the trace? Every interpretive decision
     must land as a recorded, labeled datapoint (the moat). Name the event
     type(s) emitted and the fields that make the decision reconstructable.
     If it adds a new event, that's likely also a tracing.md concern — link it. -->

## Engine seams & invariants

<!-- Where this hooks into the loop, and the load-time invariants that
     guard it (what a malformed story should fail-fast on at load, not at
     runtime). Cite file:line. -->

## Backward compatibility / migration

<!-- Do existing stories and cassettes keep working unchanged? Is the new
     behavior default-on or opt-in? If stories must migrate, what's the
     mechanical change and is there a one-shot to apply it? -->

## Tasks

```
## 1. Engine
- [ ] 1.1 {add the seam / type}
- [ ] 1.2 Load-time invariant + clear error message
- [ ] 1.3 Decision recording wired into the trace

## 2. Verification
- [ ] 2.1 Stateless unit: `kitsoki turn` exercises the new gate/effect
- [ ] 2.2 Flow fixture(s) cover the new path (and the legacy path still passes)
- [ ] 2.3 Multi-system test if I/O or concurrency is involved (see CLAUDE.md)

## 3. Adopt + document
- [ ] 3.1 Migrate one real story onto the mechanism
- [ ] 3.2 Update state-machine.md / architecture docs; trim/delete this proposal
```

## Verification

<!-- How a reviewer confirms it works without an LLM. Prefer stateless
     `kitsoki turn --state … --intent … --world @w.json` probes and
     intent-only flow fixtures. Note any test that DOES need an LLM and
     why (LLM tests are not run by default — see memory). -->

## Open questions

1. {Question} — {options}. *Lean: {x}.*

## Non-goals

- {Adjacent engine change this explicitly defers.}
