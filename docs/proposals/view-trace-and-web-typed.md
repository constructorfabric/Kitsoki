# Tracing: Trace + web render the typed view

**Status:** Draft v1. Nothing implemented yet.
**Kind:**   tracing
**Epic:**   [view-rendering-readability.md](view-rendering-readability.md) (slice 3)

## Why

The trace and the web viewer both consume a width-80 string fossil today
instead of the semantic view. `tools/runstatus/CLAUDE.md` is explicit:
"the trace itself must always be correct… never use a UI hack to cover
for a problem in the trace." A width-80 pre-rendered string *is* a UI
projection masquerading as the record.

Concretely, when `TypedView == nil` (most views, until slice 1), the web
falls to `renderView(entry.text)` (`ChatTranscript.vue:31`) and shows the
80-col string as `white-space: pre-wrap` monospace (`.chat-view`,
`:211`). Its own comment (`:25`) concedes it "must NOT re-flow that
layout — doing so collapses lists into run-on prose." The result is an
80-column terminal screenshot frozen inside a fluid browser column:
`kv` columns line up only at exactly 80, lists wrap raggedly, and the
`chat-row--agent { max-width: 98% }` rule (`:133`) is a width hack to
hold the fossil.

Once slice 1 makes `TypedView` always-populated, the trace can record the
tree and the web can render it through `ViewElement` for *every* turn.

## What changes

**One sentence:** the view-render trace event records the typed element
tree + env snapshot (not the width-80 string), and the web viewer renders
every agent turn through `ViewElement`, deleting the 80-col fossil
fallback.

This is a **consumer** change (web reads what slice 1 already produces)
plus a **producer** change (the trace records the tree). Both ride the
always-populated `TypedView` from slice 1.

## Impact

- **Producers:** the view-render event emitter (the `view.rendered`
  journal entry, see `transcript.go:1109` `KindViewRendered` consumer for
  today's shape) records `typed_view` + env.
- **Consumers:** `internal/runstatus/server/driver.go` (already
  serializes `TypedView` at `:108`,`:151` — slice 1 makes it non-nil for
  every shape), `tools/runstatus/src/components/ChatTranscript.vue`,
  `tools/runstatus/src/stores/run.ts`.
- **Format:** `view.rendered` body carries `typed_view` (the element
  tree) alongside the existing `view_text` (kept, deprecated, for old-
  trace replay).
- **Backward compat:** old traces/cassettes have only `view_text`; the
  web keeps a *read-time* fallback for them (render `view_text` as today)
  but new traces always carry `typed_view`. Replay of a pre-change
  cassette is unaffected.
- **Docs on ship:** `docs/tracing/`.

## Event / format model

```jsonc
{
  "kind": "view.rendered",
  "turn": 7,
  "body": {
    "user_input": "describe it free-form",
    "typed_view": { "Elements": [ { "Kind": "prose", "Source": "…" }, … ] },
    "view_text": "…"   // deprecated: width-80 projection, kept for old-trace replay
  }
}
```

| Event | When emitted | Key fields |
|---|---|---|
| `view.rendered` | a turn renders a room view | `typed_view` (canonical tree), `view_text` (deprecated projection), `user_input` |

The tree is recorded at render time from the turn's env snapshot, not
reconstructed from mutable story files later (memory
`narration-belongs-in-the-trace`).

## Determinism

The typed tree is derived deterministically from the (pinned) view
definition + the turn's env snapshot — both already captured. Recording
the tree adds no nondeterminism; element order and `Source` strings are
stable. Replay of a new cassette reproduces a byte-identical
`typed_view`. The existing deterministic-id / ordering guarantees are
untouched (this slice adds a field, changes no id).

## Producers & consumers

- **Producer:** the single view-render site records `typed_view` from
  `TurnOutcome.TypedView` (always-populated post slice 1).
- **Consumer (web):** `ChatTranscript.vue` renders
  `entry.typedView.Elements` through `ViewElement` for every agent turn
  (`hasElements` always true for new traces). Delete the
  `v-html="renderView(entry.text)"` branch (`:31`–`:35`), the
  `renderView`/`renderInline`/`escapeHtml` helpers (`:62`–`:90`), the
  `.chat-view` CSS block (`:211`–`:233`), and the `max-width: 98%` hack
  (`:133`). Keep a minimal `view_text` fallback **only** for traces that
  predate `typed_view`.
- **Consumer (TUI):** unchanged — it already reads `TypedView` via the
  outcome; slice 2 owns its render chain.

## Backward compatibility

Pre-change traces and cassettes carry only `view_text`; the web's
read-time fallback renders them exactly as today, so historical traces
still display. New traces always carry `typed_view`. The `view_text`
field stays on the event (deprecated) until the fallback is removed in a
later cleanup (Epic shared decision 3).

## Fixtures / golden traces

- A golden trace with a legacy-scalar room turn now carries a populated
  `typed_view`; a reviewer regenerates it by re-recording the room and
  diffing — the `typed_view` appears, `view_text` is unchanged.
- A web component snapshot (Vitest) renders a turn from the shared
  element fixture corpus (slice 4) through `ChatTranscript` and asserts
  no `.chat-view` monospace fossil node is produced.
- A pre-change cassette (only `view_text`) still loads and the web
  fallback renders it — the back-compat contract.

## Tasks

```
## 1. Emit / consume
- [ ] 1.1 Record typed_view + env on view.rendered (producer)
- [ ] 1.2 Web: render every agent turn via ViewElement; delete the 80-col fossil branch + helpers + CSS
- [ ] 1.3 Keep a read-time view_text fallback for pre-typed_view traces only

## 2. Prove
- [ ] 2.1 Golden trace: legacy-scalar room turn carries populated typed_view; replay stable
- [ ] 2.2 Web snapshot: no .chat-view monospace node for a typed turn (shared corpus)
- [ ] 2.3 Pre-change cassette still loads + renders via fallback

## 3. Document
- [ ] 3.1 Update docs/tracing/; trim/delete this proposal; update the epic slice row
```

## Open questions

1. **Drop `view_text` from new traces entirely, or keep it as a derived
   convenience?** *Lean: keep it (deprecated) through this epic* — some
   trace tooling still greps it; removal is the Epic-shared-decision-3
   cleanup.

## Non-goals

- Rich markdown in the web viewer — the element contract stays the seven
  kinds (Epic non-goal).
- The TUI render chain (slice 2).
- Deleting `view_text` / `TurnOutcome.View` (separate cleanup).
