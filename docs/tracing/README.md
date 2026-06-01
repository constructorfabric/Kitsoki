# Tracing & replay

**The session trace is the authoritative state.** Everything kitsoki
shows you — the current room, the world, the transcript, the routing
decision that got you here — is a projection of an append-only event
log. Nothing is "lost" between turns; it is replayed. That single fact
is what makes kitsoki testable, debuggable, and auditable, and it is
what this section is about.

*Audience: anyone testing, debugging, or developing a story — authors
and contributors alike.*

---

## How the pieces fit

```
        record                 replay / project
  turn ───────▶  trace (JSONL)  ───────▶  state · world · transcript
                    │
                    ├── flow tests assert on the projection      (testing.md)
                    ├── cassettes stand in for host/LLM calls    (cassettes.md)
                    ├── `kitsoki turn` drives one turn against it (turn.md)
                    └── `kitsoki replay-routing` re-runs routing  (../architecture/semantic-routing.md)
```

- **[`trace-format.md`](trace-format.md)** — the JSONL schema: the
  event vocabulary (`oracle.call.start` / `.complete` / `.error`,
  patches, checkpoints), the `EventSink` contract, how `call_id` is
  derived, and the replay determinism guarantees. Read this to
  understand what "the trace is the state" actually means.

## Testing a story

- **[`testing.md`](testing.md)** — the two test modes:
  - **Mode 1 (intent pass-rate)** exercises LLM routing — does the
    model resolve phrasings to the right intent? Costs tokens; run
    selectively.
  - **Mode 2 (deterministic flow)** drives the state machine with
    explicit `intent:` turns and asserts on the resulting state, world,
    view, and inbox. No tokens, fast, the workhorse for locking
    behaviour. This is the test you write for almost every bug.
- **[`cassettes.md`](cassettes.md)** — host cassettes: VCR-style
  recorded host/oracle call sequences with episode matching,
  `!include`, record mode, and CI safety. Use them when a flow test
  needs a multi-call host/LLM sequence to be deterministic.

> **Multi-system bugs don't show up in unit tests.** Bugs that involve
> concurrent I/O, slog + TUI rendering, or file writes racing API calls
> hide in isolated function tests. Capture the *combined* output that
> reaches the user, introduce the real concurrency, and confirm the
> test FAILS without the fix. See the project's testing rule in
> `CLAUDE.md` and the `rendering-tests` skill.

## Driving and debugging a session

- **[`turn.md`](turn.md)** — the `kitsoki turn` probe: drive a single
  turn either against a persistent trace (stateful) or as a stateless
  one-shot. The scriptable way to reproduce a turn outside the TUI.
- **`kitsoki replay-routing`** — re-run the routing stack over recorded
  turns to see which tier resolved each intent and where synonyms are
  missing. Documented with the routing stack in
  [`../architecture/semantic-routing.md`](../architecture/semantic-routing.md).
- **The `kitsoki-debugging` skill** — for "went back to idle", "silent
  bounce", "stuck at <state>" complaints. It drives the same state
  machine the TUI uses against the real on-disk repo state and surfaces
  the host-call errors that the TUI's `on_error:` arcs swallow. Invoke
  it before guessing.

## Inspecting a run in a browser

- **[`run-status-ui.md`](run-status-ui.md)** — the run-status web UI: an
  interactive state diagram, a filterable trace timeline, and a detail drawer,
  bundled into the `kitsoki` binary. Export a self-contained `.html` artifact
  with `kitsoki export-status … -o run.html`, or serve a live, updating view
  with `kitsoki status serve … --trace run.jsonl`. Built from the trace, so it
  shows exactly what the trace records.

## See also

- **[`../tui/rendering-tests.md`](../tui/rendering-tests.md)** —
  regression tests for the TUI's *rendered output* (layout, overlaps),
  a sibling discipline to flow tests.
- **[`../stories/background-jobs/testing.md`](../stories/background-jobs/testing.md)**
  — testing async handlers with flow fixtures and a virtual clock.
