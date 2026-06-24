---
# triage-marathon: FIXED 45276b88 — run.ts refetches currentView on background_completion SSE turn (live dogfood drive)
id: 2026-06-12T022703Z-web-transport-hides-background-completion-failure
title: "Web transport doesn't surface a background_completion turn's failure (last_error / say), so a failed background job looks hung"
target: kitsoki
filed_at: 2026-06-12T02:27:03Z
status: fixed
severity: P2
component: web
kitsoki_rev: 0c9a0ff
trace_ref: "~/.kitsoki/sessions/bugfix/94c6daa4-web-0391e58b-f236-4261-a74b-ca107674f5aa.jsonl"
external: {}
assignee: ""
url: "issues/bugs/2026-06-12T022703Z-web-transport-hides-background-completion-failure.md"
---

## Body

When a background job fails, the engine fires a `background_completion` turn
that sets `last_error` and emits a `machine.say` describing the failure. In
the **web** transport this turn's output does not reach the browser — the user
keeps seeing the stale `…executing` ("running…") view and the session "looks
hung" rather than showing the error.

Split out from
`2026-06-12T022551Z-background-job-killed-by-caller-ctx-cancel.md`. That bug
(now resolved) was the root cause of the specific hang observed; this one is
the independent rendering gap that made the failure **invisible** even though
the engine reported it. Fixing the ctx-cancel bug means the happy path no
longer hits this, but any genuine background-job failure still won't surface
in the web UI.

### Trace evidence

From `94c6daa4-web-…jsonl`, turn 5 (`background_completion`) emitted exactly
the diagnostics the UI should have shown:

```
turn 5  world.update  last_error="Context Extraction job ended with status: failed"
        machine.say   "Context Extraction job ended with status `failed`. Type `quit` to abort."
        scheduler.completed status=failed
                       error="host.agent.decide: claude exec failed: context canceled"
        turn.end      outcome=background_completion
```

The operator saw none of this in the browser.

### Expected vs actual

- **Expected:** when a `background_completion` turn lands, the web UI
  re-renders the destination view (and any `say`/`last_error`) so the operator
  sees the job's terminal status.
- **Actual:** the web UI stays on the pre-completion `…executing` view; the
  failure `say` and `last_error` are dropped.

### Investigation hints

- Compare how the web SSE/render push handles a normal user-driven turn vs a
  `background_completion` turn (no inbound request drives it — the scheduler's
  completion event does). The push that re-renders the view likely only fires
  on the request path.
- `cmd/kitsoki/web.go` hosts live orchestrators; check where job-completion
  events are fanned out to connected browser clients vs how TUI consumes them
  (the TUI does re-render on completion — this is web-specific).
- Relates to the `on_error: idle` anti-pattern note in the kitsoki-debugging
  skill: a destination view that doesn't surface `world.last_error` is a silent
  failure; here it's surfaced in the engine but lost in transport.

## Body notes

P2 (not P0/P1): no data loss or wrong result — purely a visibility gap, and
the most common trigger (the ctx-cancel bug) is now fixed. Still worth fixing
because any future background-job failure mode will reproduce the "looks hung"
report.

---

## Triage verdict (dogfood marathon, live triage-only mode) — STILL-LIVE (conf 0.8)

Resolved from UNCLEAR by driving `stories/bugfix` in the new **triage-only mode** (live, claude-native, $1.80).

**Root cause:** the web run store `tools/runstatus/src/stores/run.ts` SSE subscription
(`hydrate()` L286–295) only does `events.push(e); applyStatePath(e)` — it never refreshes
`currentView` (L116). `currentView` is written solely by `applyTurnResult` (L418, RPC-driven),
`loadInitialView` (L457, once), `rehydrate` (L396, meta-mode), `resetSessionState` (L364). A
scheduler-driven `background_completion` turn fires none of these → operator stays on the stale
`…executing` view, so `world.last_error` never surfaces. The server relay
`notifications.go OnBackgroundTurn` (L151–196, wired `web.go:241`) only feeds the cross-session
inbox badge, NOT a destination-view re-render; `turn_stream.go handleTurnStream` emits the `done`
view frame only on the request path. No regression test pins a view refresh on background completion.

**Suggested action:** refetch `source.view()` in the run store when a `turn.end` with
`outcome=background_completion` / new landed state arrives over SSE (mirror the TUI's
re-render-on-completion); add a regression test asserting `currentView` updates without an inbound RPC turn.
