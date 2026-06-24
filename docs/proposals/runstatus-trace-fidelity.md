# Tracing: bugfix trace fidelity & a faithful runstatus

**Status:** Producer (§1) and consumer (§2) shipped; fixtures migrated
(§3.1) and the bugfix exemplar fully proven (§3.2/§3.3: `bugfix.spec.ts`
9/9 green, all six fidelity invariants verified by an independent render
check — each meaningful aspect renders **exactly once** across the two
parallel columns, with prompt + response in every agent row). The
canonical trace events (`agent.call.*` with verb in attrs, `machine.say`,
`turn.input`/`machine.intent_accepted` on the flow path) are emitted and
documented in [`docs/tracing/trace-format.md`](../tracing/trace-format.md).
**One item remains (§3.4):** the sibling-fixture specs
(`artifact.spec.ts` ×5, `agent-prompts.spec.ts` ×1) still assert the
old `.detail-drawer` UX and a now-dropped `machine.transition` row — they
were broken by the intended §2 consumer rewrite (inline row-body
expansion), not by a trace regression, and need migrating to the new
model. Keep this proposal until §3.4 lands, then delete.
**Kind:**   tracing
**Epic:**   — standalone

## Why

The bugfix story is the gold-standard demo of the moat: a full
interpret-then-execute pipeline where every interpretive decision lands in the
JSONL trace. `runstatus` is how we *show* that trace — two parallel columns, a
state-diagram on the left and an event timeline on the right, each meant to
present every meaningful aspect of the run **exactly once**.

It doesn't. Rendering `.artifacts/bugfix.html` (driven from
`stories/bugfix/flows/happy_human.yaml` through the real orchestrator) shows:

- **Every LLM call is rendered twice.** Each agent call appears as a separate
  `agent.call.start` *and* `agent.call.complete` row in both columns instead
  of one merged row — duplication in the same column, the exact thing the two
  columns are supposed to avoid.
- **The rich agent detail never renders.** Expanding an agent row shows a
  generic fallback with `Response → [object Object]`; no verb badge, no decide
  choices, no prompt/response panes. The proposer/implementer reasoning — the
  single most valuable thing in the trace — is lost.
- **Every phase header is duplicated.** The right column visits
  `reproducing, proposing, reproducing, implementing, proposing, testing, …` —
  each phase appears twice, non-adjacent, with its agent call severed from its
  host/world work.
- **Operator narration is dropped.** `say:` lines (e.g. *"Fix applied —
  TKT-001 …"*) render as bare, textless `world.update` rows.
- **What advanced each turn is invisible.** The flow-driven trace carries no
  `turn.input` / `machine.intent_accepted` events, so the timeline shows
  transitions happening unprompted.
- **3 tests are already red.** `tools/runstatus/tests/playwright/bugfix.spec.ts`
  fails its three agent-detail assertions today (`6 passed, 3 failed`).

The root is a **split trace contract**: the real orchestrator emits agent
events as `agent.call.start/complete` with the verb in `attrs.verb`
(`internal/store/event.go:109,114`), but the runstatus consumer — and the five
hand-authored fixtures it was built against — assume `agent.<verb>.start`
(`tools/runstatus/src/components/TraceTimeline.vue:212`). The bugfix fixture is
the only one produced by the real engine, and it's the one that breaks.

This proposal makes the trace canonical and the runstatus surface a faithful,
deduplicated mirror of it.

## What changes

One sentence: **`agent.call.*` (verb-in-attrs) becomes the single canonical
agent trace shape; the trace stops conflating narration with world mutation
and records the intent that drives each turn; and runstatus is rewired to that
canonical trace so each meaningful aspect renders once per column.**

Concretely, in two halves:

- **Producer (perfect trace).** Stamp agent on-enter events with the real
  foreground turn (deleting the load-time `repairAgentTurns` hack); split
  `say:` narration into its own event kind instead of an `EffectApplied` with
  no `set`; emit `turn.input` / `machine.intent_accepted` on the flow path so
  flow-driven traces match live sessions; retire the dead `agent.ask.*`
  naming and migrate the five synthetic fixtures to `agent.call.*`.
- **Consumer (faithful representation).** Match `agent.call.start/complete`
  and read the verb from `attrs.verb` (restores merge, suppression, and
  `AgentDetail`); group the right column by **phase** so it stays parallel to
  the left; render narration rows; surface bundled prompt sidecars in artifact
  mode.

## Impact

- **Producers:**
  - `internal/host/agent_dispatch.go` (`appendAgentCalledEventWithEpisode` ~296, `appendAgentReturnedEvent` ~360) — turn stamping + always-present prompt ref.
  - `internal/machine/machine.go:~1500` (`say` → distinct kind) and the flow runner / testrunner (`internal/testrunner/flows.go`) — emit intent events.
  - `internal/store/event.go:27` — retire dead `LLMCalled = "agent.ask.start"`.
- **Consumers:** `tools/runstatus/src/components/{TraceTimeline,StateDiagram,EventDetail}.vue`, `src/components/agent/*`, `src/data/snapshot-source.ts` (delete `repairAgentTurns`), `tools/runstatus/scripts/build-artifact.mjs` (bundle sidecars).
- **Format:** new `machine.say` event kind; agent events gain a guaranteed prompt reference; no field removed.
- **Backward compat:** no *real* recorded trace uses `agent.<verb>.*` — the engine never emitted it. Only the five synthetic fixtures do; they are migrated, not supported in perpetuity. `repairAgentTurns` deletion is safe once agent turns are stamped correctly upstream.
- **Docs on ship:** `docs/tracing/trace-format.md` (agent + say + intent events), `docs/tracing/turn.md`.

## Event / format model

Canonical agent events (already what the engine emits — this proposal makes it
the *only* shape and fixes the consumer to it):

```jsonc
// agent.call.start  — verb lives in attrs.verb, not the msg
{ "msg": "agent.call.start", "turn": 2, "state_path": "proposing",
  "attrs": { "verb": "decide", "agent": "proposer", "model": "claude-sonnet-4-6",
             "call_id": "2d8e4fbb0a78646d", "prompt_file": "agent-prompts/8bf4401a34847271.txt" } }
// agent.call.complete
{ "msg": "agent.call.complete", "turn": 2, "state_path": "proposing",
  "attrs": { "verb": "decide", "duration_ms": 88858,
             "response": { "intent": { … }, "text": "…" } } }
```

| Event | When emitted | Key fields / fix |
|---|---|---|
| `agent.call.start` | on-enter agent dispatch | stamp `turn` = foreground turn (not 0); always carry a prompt ref (inline `prompt` if small, else `prompt_file`) |
| `agent.call.complete` | agent returns | `turn` matches its start; `verb` + `response` + `duration_ms` |
| `machine.say` *(new)* | a `say:` effect resolves | `{ "text": "Fix applied — …" }` — split out of `EffectApplied` so `world.update` means *only* a world mutation |
| `world.update` | `set:` effect applies | unchanged; consumer groups `attrs.set` per phase, ignores non-`set` (now impossible — say is its own kind) |
| `turn.input` | each flow/live turn | `{ "input": "accept" }` — flow runner must emit, like live sessions |
| `machine.intent_accepted` | intent passes `Validate` | records *what* advanced the turn |

## Determinism

- `call_id` derivation is already deterministic (`internal/host/callid.go:36`),
  so start/complete pairing survives replay. Keep it.
- **Turn stamping must keep replay byte-identical.** The agent events currently
  land at `turn=0` *before* their triggering `turn.start`; stamping them with the
  foreground turn changes a field but must not change ordering or the raw bytes
  for any *non-agent* event. The Layer-7 byte-equality check
  (`internal/runstatus/snapshot.go` `FromSink`) is the guard — regenerate the
  cassette-backed fixture and confirm replay is stable.
- Deleting `repairAgentTurns` (`snapshot-source.ts:75`) removes a *load-time
  mutation* of the trace; afterwards `snapshot.events` equals the on-disk trace,
  which is the property a "perfect trace" wants.

## Producers & consumers

**Producer — why the turn is wrong today.** On-enter agent events are written
during `RunInitialOnEnter` and stamped `turn=0`; the host/world effects of the
same transition are stamped with the *pre-transition* `state_path`. The
consumer's `repairAgentTurns` tries to patch the turn back but computes
`nearestTurn(time ≤ agent_time)`, which resolves to the **previous** turn for
all but the first call (verified: reproducing→1, proposing→1, implementing→2,
testing→3, validating→5, done→6 — all but the first are off by one). Fixing the
stamp at the source is the clean fix.

**Consumer — why the right column should group by phase.** A bugfix checkpoint
spans two turns: *entering* a phase (on-enter agent + artifact bind) happens in
turn N, while *accepting* out of it happens in turn N+1. Grouping the right
column by `(state_path, turn)` therefore splits every phase. The **left** column
already groups by phase (`StateDiagram.vue`) and shows each once; making the
right column group by phase too makes the two columns genuinely parallel and
kills the duplicate headers — independent of the turn-stamp fix.

**Consumer — the agent match.** Replace the verb-enumerated regexes
(`TraceTimeline.vue:212-213`, `EventDetail.vue` ~174, `StateDiagram.vue` ~199)
with a match on `agent.call.start` / `agent.call.complete`, reading
`attrs.verb` for routing to `agent/DecideDetail.vue`, `TaskDetail.vue`, etc.
This single change restores: start+complete merge into one row (no same-column
dup), the already-correct `host.agent.*` suppression
(`TraceTimeline.vue:410,480`), and `AgentDetail` rendering with verb badge and
prompt/response panes.

## Backward compatibility

- **Synthetic fixtures** (`completed`, `edge-cases`, `in-progress`,
  `agent-rich`, `agent-with-separate-prompts`) encode `agent.<verb>.*`, a
  shape the engine never produced. They are migrated to `agent.call.*` (and
  `agent-rich` keeps exercising all five verbs via `attrs.verb`). No dual-scheme
  support is added — the fiction is removed.
- **Real traces/cassettes** already emit `agent.call.*`; nothing on disk needs a
  compat shim.
- `machine.say` is additive; old traces simply have no `machine.say` events and
  their `say` text stays where it was (an `EffectApplied`). The consumer can keep
  a one-release fallback that renders a non-`set` `world.update` as narration, or
  we regenerate the fixtures and drop it — see Open question 2.

## Fixtures / golden traces

- `tools/runstatus/fixtures/bugfix.snapshot.json` is the regression contract;
  regenerate via `make -C tools/runstatus/fixtures bugfix` after the producer
  fixes, then rebuild `.artifacts/bugfix.html` with
  `kitsoki export-status --from-snapshot tools/runstatus/fixtures/bugfix.snapshot.json -o .artifacts/bugfix.html`
  (or `make -C tools/runstatus/fixtures artifacts` for all fixtures).
- Bundle the `agent-prompts/*.txt` sidecars into the artifact (or inline them
  into the snapshot) so `usePromptLoader` resolves them under `file://`.
- `bugfix.spec.ts`: the 3 agent assertions go green; add invariants — (a) each
  agent call renders **exactly one** merged row, (b) each phase header appears
  **exactly once**, (c) `machine.say` rows show their text, (d) no textless
  `world.update` rows, (e) `turn.input` rows present for each accept.

## Tasks

```
## 1. Producer — perfect trace
- [x] 1.1 Stamp agent.call.start/complete with the foreground turn; verify state_path is the destination phase
- [x] 1.2 Split `say:` into a `machine.say` event kind (event.go + machine.go); world.update becomes set-only
- [x] 1.3 Emit turn.input + machine.intent_accepted on the flow path to match live sessions
- [x] 1.4 Guarantee a prompt reference on every agent.call.start (inline or prompt_file); retire dead LLMCalled="agent.ask.start". NB: the *cassette* emitter (`internal/testrunner/cassette.go` `writeCassetteAgentEvents`), not the live handlers, emits the start event during fixture replay — it was only offloading large prompts to sidecars and dropping small inline ones. Fixed to mirror the live dispatch path.
- [x] 1.5 Replay stays byte-identical for non-agent events (Layer-7 check); regenerate bugfix fixture

## 2. Consumer — faithful representation
- [x] 2.1 Match agent.call.start/complete, verb from attrs.verb (TraceTimeline, EventDetail, StateDiagram, agent/*). Merged row stitches start's prompt + complete's response into one logical call (TraceTimeline `AgentMerge.merged`).
- [x] 2.2 Delete repairAgentTurns (snapshot-source.ts); snapshot == on-disk trace
- [x] 2.3 Group the right column by phase (parallel to the left); one header per phase
- [x] 2.4 Render machine.say as a narration row; stop rendering non-set effects as world.update
- [x] 2.5 Bundle agent-prompts sidecars into the artifact (build-artifact.mjs inlines prompt_file → prompt); usePromptLoader resolves under file://
- [x] 2.6 TaskDetail renders `response.text` for task-verb responses (was an empty Overview tab)

## 3. Prove
- [x] 3.1 Migrate the 5 synthetic fixtures to agent.call.* (agent-rich keeps all 5 verbs)
- [x] 3.2 bugfix.spec.ts: 9/9 green (the 3 agent tests fixed); all six fidelity invariants verified by an independent render check (each aspect once, no same-column dup, prompt+response in every agent row)
- [x] 3.3 Re-render bugfix artifact and eyeball both columns: each aspect once, no dup
- [ ] 3.4 REMAINING: update the *sibling-fixture* specs to the new inline-expansion model. `artifact.spec.ts` (×5: in-progress/completed/edge-cases) and `agent-prompts.spec.ts` (×1) still assert the removed `.detail-drawer` UX and a now-dropped `machine.transition` row. These are obsolete-UX assertions broken by the §2 consumer rewrite — not trace-fidelity regressions — and need migrating to `.trace-timeline__row-body` inline expansion.

## 4. Document
- [x] 4.1 Update docs/tracing/trace-format.md (agent.call.*, machine.say, intent events)
- [ ] 4.2 After 3.4 lands: trim/delete this proposal
```

## Open questions

1. **Right-column grouping key** — group strictly by phase, or by turn with a
   per-turn resolved phase label? *Lean: by phase, to stay parallel with the
   left column and guarantee one header per phase.*
2. **`machine.say` compat window** — regenerate all fixtures and drop the
   non-`set`-`world.update`-as-narration fallback immediately, or keep the
   fallback for one release? *Lean: regenerate and drop — these are test
   fixtures, not field data.*
3. **Prompt delivery in artifacts** — inline prompts/responses into the snapshot
   JSON (self-contained, larger file) or bundle the sidecar `.txt`/`.json` files
   alongside? *Lean: inline small, bundle large, matching the existing >1KB
   offload threshold in `agent_event_sink.go`.*

## Non-goals

- **Token/cost breakdown UI.** Surfacing `prompt_tokens` / `cost_usd` richer than
  the current header is a runstatus enhancement, not part of fidelity here.
- **New agent verbs or schema changes** to the agent payloads themselves.
- **The other stories' traces.** This proposal fixes the canonical contract and
  the bugfix exemplar; other stories inherit the fix but aren't audited here.
