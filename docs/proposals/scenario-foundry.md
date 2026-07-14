# Tracing: Scenario Foundry

**Status:** Shipped. Tasks 1-3 and 5 are done; 4.1 (paraphrase tier)
shipped, 4.2 (a live local paraphraser) is deliberately not built — no
evidence the pre-recorded pool is insufficient. The IR shape and
compiler contracts now live in
[`docs/tracing/scenario-foundry.md`](../tracing/scenario-foundry.md)
(the authoritative reference); this file is kept short as the
historical design record (Why / Tasks / Non-goals).
**Kind:**   tracing
**Epic:**   usable-kitsoki.md

## Why

Every existing harness simulates *clean, pre-authored* interactions.
`kitsoki test flows` accepts free text only as exact-match recording
entries (`internal/harness/replay.go`); swarm tier 1 clicks explicit
intents; swarm tier 2's free text must match a per-run recording
verbatim; product-journey's `scenarios.json`/`personas.json` are
hand-authored (`tools/product-journey/scenarios.json`,
`tools/product-journey/personas.json`). None of it can express the
richest real signal — **corrections, retries, and abandonment** — the
dirty, meandering sessions where the usable-kitsoki off-ramp/workbench
work (S1–S3) actually gets judged.

The corpus to mine already exists and is large: ~3,192 Claude Code
sessions (~1.9 GB) under `~/.claude/projects/`, already read by
`tools/session-mining/prep.py` (`tools/session-mining/README.md:38-63`),
plus ~1,141 codex rollouts under `~/.codex/sessions/` that are
**currently invisible** — grepping `tools/session-mining/` for `codex`
turns up nothing; the README's ingestion path only resolves
`~/.claude/projects/<slug>` (`tools/session-mining/README.md:38`).
Session mining already computes most of what a scenario needs per
turn: verbatim `user_text`, a grounded tool recipe, the real `outcome`,
and — with `emit.py --outcomes` — a per-instance **`satisfaction`**
flag recording whether a later turn corrected the result
(`tools/session-mining/emit.py:27-29,155`, `tools/session-mining/README.md:145`).
What's missing is narrow: (a) a codex adapter, (b) a compiler from
mined intents+outcomes into a runnable multi-turn scenario, and (c)
wiring from that scenario into the harnesses that already exist but
can't consume it yet — flow fixtures, swarm tier 2, and product-journey.
Without this, the S6 release gate (`docs/proposals/usable-kitsoki.md`)
has nothing realistic to run, and S1–S3 (the workbench, never-silent
runtime, context floor) get proven only against hand-authored happy
paths — repeating the exact pattern (R8) that green-masked live
failures before.

`conversation-driven-development.md` sketched slice 1 (`kind:
conversation` cases compiled from mining) as the intended home for this
shape; this proposal absorbs that slice and ships it against the
concrete mining/harness plumbing above rather than the feature-catalog
codegen CDD's other three slices still own (see Impact).

## What changes

A **Scenario Foundry**: a codex rollout parser feeding the existing
session-mining pipeline, a **scenario IR** (`kind: conversation` —
persona, goal, ordered turns including correction turns and
abandonments, expected effects) compiled from mined intents + outcomes
+ satisfaction, and three compilers from the IR into the harnesses that
already exist but currently can't be fed realistic multi-turn input:
flow fixtures + recordings, swarm tier-2 scripts, and product-journey
`scenarios.json`/`personas.json` entries. A **paraphrase tier (2.5)**
rephrases the mined utterance via semantic-match recordings so
free-text realism isn't bottlenecked on exact-match strings. First
deliverable, inside a week: the codex parser plus 20 mined Kitsoki
scenarios in the IR, hand-checked, as the calibration set everything
else is built against.

## Impact

- **Producers:** `tools/session-mining/` — a new codex source adapter
  alongside the existing Claude Code ingestion
  (`tools/session-mining/README.md:38-63`); the scenario compiler reads
  `emit.py --outcomes` output (verbatim `user_text`, `outcome`,
  `satisfaction.corrected`/`corrective_ops`/`followup_text_head`,
  `tools/session-mining/emit.py:155-266`).
- **Consumers:** `internal/harness/replay.go` (flow fixtures gain a
  paraphrase-tier input path), `tools/swarm/tiers/tier2.ts` and the
  arena swarm plugin's unread `SWARM_PERSONA_MIX`/`SWARM_FIXTURE` env
  vars (`tools/arena/arena/plugins/swarm.py:26,129-132`), and
  `tools/product-journey/scenarios.json` / `personas.json`
  (`tools/product-journey/README.md:454-455`). Downstream, the S6
  release gate (`usable-kitsoki.md`) is the primary consumer of the
  compiled scenario corpus at swarm concurrency.
- **Format:** a new scenario IR schema (`kind: conversation`) — not a
  trace event type; it is compiled *from* mined intents/outcomes, one
  layer above the JSONL trace format itself.
- **Backward compat:** additive only. Existing flow fixtures, host
  cassettes, and hand-authored `scenarios.json`/`personas.json` entries
  are untouched; mined scenarios land alongside them, distinguished by
  provenance (`source: mined` vs hand-authored).
- **Tooling spillover:** most of the surface here is CLI/tooling
  (`tools/session-mining/`, `tools/swarm/`, `tools/product-journey/`),
  not the trace-event/runstatus surfaces a typical tracing proposal
  touches. It is scoped as tracing because its whole premise is reading
  what traces/session logs already capture and re-projecting it as
  fixtures — no new engine trace events are added.
- **Docs on ship:** `docs/tracing/scenario-foundry.md` (the IR shape +
  compiler contracts); a short note in `tools/session-mining/README.md`
  for the codex adapter.

## Scenario IR, determinism, producers & consumers

Shipped as designed here, with two corrections learned during
implementation (both from the codex adapter and flow-fixture compiler
respectively): codex's real per-line shape splits `type` across both
the outer record and `payload`, and `trace to-flow`'s shape could not
be reused directly because it needs an already-resolved
`intent`/`slots` per turn, which a mined scenario doesn't have (the
flow-fixture compiler instead targets a purpose-built harness story).
The IR schema, worked example, determinism contract, and every
producer/compiler are documented in full — with source references —
in [`docs/tracing/scenario-foundry.md`](../tracing/scenario-foundry.md);
this section is intentionally not duplicated here.

## Backward compatibility

Existing `tools/session-mining/` outputs, flow fixtures, and
`tools/product-journey/{scenarios,personas}.json` entries are read-only
inputs/siblings — nothing here changes their schema or existing
consumers. A story or test that never opts into a mined scenario is
unaffected.

## Fixtures / golden traces

The calibration set itself is the regression contract: 20 hand-checked
mined scenarios (Phase 1 below) become a golden fixture set — each
scenario's compiled flow fixture must replay deterministically
(`kitsoki test flows`), and a reviewer regenerates the set by rerunning
the compiler against the same (redacted) mining output and diffing.

## Tasks

```
## 1. Codex adapter
- [x] 1.1 Parse `~/.codex/sessions/*.jsonl` rollout format into the
      shared intermediate turn/tool-call shape `prep.py` already
      produces for Claude Code sessions. Implemented as
      `codex_adapter.py`/`codex_prep.py`; the real per-line shape splits
      `type` across the outer record and `payload` (not a single flat
      `payload.type` stream as first assumed) and codex's headless
      dispatched-agent filter keys on `payload.originator`, not
      `entrypoint`.
- [x] 1.2 Route codex sessions through the existing outcome/satisfaction
      join (`emit.py --outcomes`) and redaction ladder (`redact.py`)
      unchanged. `codex_outcomes.py` emits the same `outcomes.json`
      shape, id-keyed on codex's real `call_id` (an easier, exact join
      than Claude Code's positional one).
- [x] 1.3 Calibration: parse a small sample of real codex rollouts,
      hand-verify turn/outcome extraction against the raw JSONL. See
      `tests/test_codex_adapter.py` and the codex portion of
      `tools/session-mining/calibration/MANIFEST.md` (surfaced a real
      filtering gap: codex approval-sidecar sessions share the human
      `codex-tui` originator tag and must be excluded separately).

## 2. Scenario IR + first calibration set
- [x] 2.1 Define the `kind: conversation` IR schema (persona, goal,
      turns incl. correction/abandonment fields, expected_effects,
      provenance)
- [x] 2.2 Scenario compiler: mined intents + outcomes + satisfaction →
      IR documents, scoped to goal-bounded spans
- [x] 2.3 Redaction gate: every mined scenario passes `redact.py`'s
      ladder before it can be committed; unredacted IR stays local/
      gitignored (shared decision 9). Implemented as `redact.py
      --scenario` (genericizes goal/turns/corrective_ops/
      followup_text_head/expected_effects, then re-runs the HIGH-RISK
      `scan()` gate and fails closed — exit 1, empty stdout — if
      anything secret-shaped survives). Unit-tested in
      `tests/test_redact_scenario.py`.
- [x] 2.4 Ship 20 hand-checked Kitsoki scenarios as the calibration set
      (first-week deliverable). Ran the real pipeline over local
      claude-code + codex corpora; 20 scenarios compiled and passed the
      redaction gate, 18 survived hand-check (2 excluded for
      insufficient standalone signal after required path redaction, not
      padded back to 20). See `tools/session-mining/calibration/` +
      `MANIFEST.md` for the set, provenance, and hand-check notes
      (including two real pipeline findings surfaced along the way: a
      codex approval-sidecar-session filtering gap, and an `emit.py`
      corrective-op false-positive).

## 3. Downstream compilers
- [x] 3.1 Flow-fixture + recording compiler. Shipped as
      `flow_fixture_compiler.py`, targeting a purpose-built
      `stories/scenario-foundry-harness/` rather than `trace to-flow`'s
      shape directly — that converter needs an already-resolved
      `intent`/`slots` per turn, which a mined scenario doesn't carry.
- [x] 3.2 Swarm tier-2 compiler, wiring `SWARM_PERSONA_MIX`/
      `SWARM_FIXTURE` (`tools/arena/arena/plugins/swarm.py:129-132`).
      `tools/swarm/tiers/tier2.ts` now reads both env vars, falling
      back to the prior hardcoded fixture when unset.
- [x] 3.3 Product-journey compiler (IR → `scenarios.json`/`personas.json`
      entries, mined personas from intent clusters). Shipped as
      `product_journey_compiler.py`; mined entries are tagged
      `"source": "mined"` with `stage: "mined_from_sessions"` so live
      dispatch never auto-selects them.

## 4. Paraphrase tier 2.5
- [x] 4.1 Semantic-match recording path: pre-recorded paraphrase pool,
      no live spend in CI, reuses the replay harness's match discipline.
      Implemented as an optional `paraphrases:` list on a recording entry
      (`internal/harness/replay.go`) — every pool member is indexed under
      the same exact/case-insensitive lookup as `input:`, so replay stays
      a pure pre-recorded lookup (no embedding/live-model call at replay
      time or in CI); an utterance outside the pool still misses with
      `ErrRecordingMiss`. Documented in
      `docs/tracing/testing.md#paraphrase-tier-25`. Tested in
      `internal/harness/harness_test.go`
      (`TestReplayHarness_ParaphrasePool`,
      `TestReplayHarness_ParaphraseEmptyRejected`).
- [ ] 4.2 (only if 4.1 proves insufficient) small local paraphraser,
      gated behind explicit invocation per the repo's no-LLM-in-CI rule
      — deferred; not built (no evidence yet that the pre-recorded pool
      is insufficient).

## 5. Document
- [x] 5.1 Write `docs/tracing/scenario-foundry.md`; update
      `tools/session-mining/README.md` with the codex adapter note
- [x] 5.2 Update `conversation-driven-development.md`'s slice 1 row to
      point at this proposal (done as part of landing this file)
- [x] 5.3 Migrate shipped content to docs/ and trim this proposal (kept,
      not deleted, as the historical design record — Why/Tasks/Non-goals)
```

## Open questions

1. **Paraphrase tier shape** — semantic-match recordings (embedding
   threshold, deterministic given the model) vs. a small local
   paraphraser. *Lean: semantic-match first — it reuses the replay
   harness rather than adding a new dependency (shared decision 7).*

## Non-goals

- **The S6 release gate itself.** This proposal produces the scenario
  corpus and compilers S6 (`usable-kitsoki.md`) runs at swarm
  concurrency; the gate's pass/fail thresholds and arena job type are
  S6's design, not this one's.
- **CDD slices 2–4.** The mockup demo binding, the corpus expansion
  loop, and the build handoff in `conversation-driven-development.md`
  keep their own scope; only slice 1 (conversation cases from mining)
  is absorbed here, because this proposal ships it against concrete
  mining/harness plumbing rather than the feature-catalog codegen.
- **Hand-authored scenario authoring UX.** Hand-authored scenarios
  remain valid for gap-filling (shared decision 4); this proposal does
  not change how they're written, only adds the mined path alongside.
- **Swarm axis design.** `SWARM_PERSONA_MIX`/`SWARM_FIXTURE` already
  exist as arena-threaded env vars; this proposal wires tier 2 to read
  them, it doesn't redesign the axes.
