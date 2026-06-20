# Tracing: scores & annotation — turn the trace into a labeled dataset

**Status:** Draft v1. Nothing implemented yet.
**Kind:**   tracing
**Epic:**   ../trace-introspection.md

## Why

kitsoki's core architectural commitment is "every interpretive decision is a
labeled datapoint → self-improvement"
(`feedback_kitsoki_moat_is_architecture`). The trace already records the
decisions (`machine.gate_decided`, agent calls, routing). What's missing is
the **label**: there is no human-annotation, score, or feedback event
anywhere in the system — a repo-wide search finds none
(`internal/store/event.go` has no `*Score`/`*Annotation` kind; "annotation"
appears only in a code comment). An operator who reviews a run in
`runstatus` and judges "this `refine` decision was wrong" has nowhere to put
that judgment.

This is the one capability the Langfuse survey
(`.context/langfuse-trace-viewer-comparison.md`, idea #4) flags as
*genuinely missing* and *strategically highest value*: Langfuse's Annotate +
Scores flow lets a human attach a score/comment to a trace/observation. For
us it's not a copy — it closes our own loop: a scored gate is a labeled
training/eval datapoint, the raw material the moat promises to learn from.

## What changes

One sentence: **add a read-only `trace.annotation` event kind — an operator
attaches a `{target_call_id|target_turn, score?, label?, comment?}` to a
gate/turn/agent-call from the runstatus viewer, recorded as operator
metadata in a trace-adjacent annotation stream, never mutating the story
trace or advancing the machine.**

Annotation is *metadata about* the trace, not part of the run. It is written
to its own append-only stream (a sidecar JSONL keyed by the run's
session/call ids), so the deterministic story trace stays byte-identical and
replayable, and meta-mode's read-only invariant
(`feedback_meta_mode_readonly`) holds.

## Impact

- **Producers:** a new write path — `runstatus`'s server gains a
  `runstatus.annotation.add` JSON-RPC method (the SPA is read-only over the
  *story* trace but may write *annotations* to the sidecar). Annotations are
  appended to `<run>.annotations.jsonl` (or a per-session store), not the
  story `*.jsonl`.
- **Consumers:** the SPA renders existing annotations inline on the target
  row/detail (a score badge + comment) and offers an "Annotate" affordance;
  the snapshot projection (`internal/runstatus/snapshot.go:22`) gains an
  `Annotations []Annotation` field merged in at load.
- **Format:** new event kind `trace.annotation` in a separate stream;
  references the target by the deterministic `call_id`
  (`internal/host/callid.go`) or `(session_id, turn)`.
- **Backward compat:** total — a run with no annotations sidecar renders
  exactly as today (empty `Annotations`). The story trace format is
  unchanged.
- **Docs on ship:** `docs/tracing/trace-format.md` (the `trace.annotation`
  kind + the sidecar stream), `docs/tracing/run-status-ui.md` (annotate
  flow).

## Event / format model

```jsonc
// <run>.annotations.jsonl — separate from the story trace
{ "msg": "trace.annotation", "ts": "2026-06-09T18:22:04Z",
  "attrs": { "session_id": "…", "target_call_id": "2d8e4fbb0a78646d",
             "score": 0, "label": "wrong-intent",
             "comment": "should have chosen refine, not accept",
             "annotator": "brad", "schema_version": 1 } }
```

| Event | When emitted | Key fields |
|---|---|---|
| `trace.annotation` | operator submits an annotation from the viewer | `target_call_id` **or** `(session_id, turn)`; optional `score` (numeric), `label` (string/enum), `comment`; `annotator`; `ts` |

A target is identified the same way the viewer already pairs events — by
`call_id` for an agent call/gate, or `(session_id, turn)` for a whole turn.
Multiple annotations per target are allowed (append-only); the viewer shows
the latest or all, per Open question 2.

## Determinism

The **story trace stays deterministic and byte-identical** because
annotations live in a separate stream — replaying a cassette produces the
same story `*.jsonl` whether or not an annotations sidecar exists (the
Layer-7 byte-equality guard in `snapshot.go` `FromSink` is unaffected). The
annotation stream itself is *not* part of replay — it carries a wall-clock
`ts` and an operator id, both inherently non-deterministic, which is exactly
why it must not live in the replayable trace. Targets are bound by the
deterministic `call_id`, so an annotation made on one replay still resolves
on the next.

## Producers & consumers

- **Producer:** the runstatus server's new `runstatus.annotation.add` method
  appends to the sidecar. This is the first *write* in the otherwise
  read-only runstatus surface — scoped strictly to annotation metadata, with
  no path to mutate the story trace, world, or machine (epic Shared decision
  2). Offline artifact mode has no server, so it's read-only over baked-in
  annotations (Open question 1).
- **Consumer:** `snapshot.go` merges the sidecar into the `Snapshot` at load
  (`Annotations []Annotation`); the SPA renders a score/label badge + comment
  on the annotated row and in the detail pane, and exposes the Annotate
  affordance. Downstream (out of scope): a job that reads many annotation
  streams into an eval/training dataset — the payoff of the whole loop.

## Backward compatibility

Pure addition in a separate stream. Runs without a `.annotations.jsonl`
load and render exactly as today (`Annotations` is empty). No story-trace
field changes; no cassette regeneration; every existing fixture/artifact is
unaffected. The annotation event carries `schema_version` so the shape can
evolve without breaking older sidecars.

## Fixtures / golden traces

- A new checked-in `*.annotations.jsonl` fixture paired with the bugfix
  snapshot: a few annotations targeting a gate `call_id` and a turn. The
  Playwright suite asserts the badges/comments render on the right rows and
  that a run *without* the sidecar renders unchanged (the compat case).
- A server unit test: `runstatus.annotation.add` appends a well-formed line
  and rejects a target `call_id` not present in the run (no dangling
  annotations).

## Tasks

```
## 1. Emit / consume
- [ ] 1.1 Define the trace.annotation event + Annotation struct; sidecar stream path convention
- [ ] 1.2 runstatus server: runstatus.annotation.add (validate target exists; append-only); reject unknown call_id/turn
- [ ] 1.3 snapshot.go: load + merge the sidecar into Snapshot.Annotations
- [ ] 1.4 SPA: render score/label/comment badge on the target row + detail; Annotate affordance (live mode)

## 2. Prove
- [ ] 2.1 Server unit: add appends; unknown target rejected; story trace replay still byte-identical (Layer-7)
- [ ] 2.2 Playwright: annotations render on the right rows; no-sidecar run renders unchanged

## 3. Document
- [ ] 3.1 docs/tracing/trace-format.md (trace.annotation + sidecar) + docs/tracing/run-status-ui.md (annotate flow); trim/delete this slice
```

## Open questions

1. **Annotation in offline artifact mode.** A `file://` artifact has no
   server to POST to. *Lean: v1 — live mode writes annotations; the artifact
   export bakes in annotations already recorded and is read-only over them
   (the export already inlines the snapshot, so it can inline the
   annotations too). Editing in the artifact is a follow-on, if ever.*
2. **One annotation per target, or a thread?** *Lean: append-only thread
   (multiple allowed); the viewer shows the latest score as the badge and
   all comments on expand — preserves history, matches the append-only
   trace philosophy.*
3. **Score type — free numeric, or a configured scale/enum?** Langfuse
   supports numeric, categorical, and boolean scores. *Lean: start with an
   optional numeric `score` + a free `label` string; a configured score
   schema (named scales, enums) is a follow-on once we know what evals
   want.*

## Non-goals

- **Auto-scoring / LLM-as-judge evaluators.** This slice is *human*
  annotation. Programmatic evaluators that score traces automatically are a
  separate capability (and overlap `agent-contract-eval.md`'s Layer-2
  correctness eval) — out of scope here.
- **Cross-run aggregation / an eval dashboard.** Turning the annotation
  streams into a training/eval dataset is the downstream payoff, a separate
  consumer of this data.
- **Mutating the story.** Annotations never advance the machine, write
  world, or edit the trace — they are operator metadata in a separate
  stream (epic Shared decision 2; `feedback_meta_mode_readonly`).
