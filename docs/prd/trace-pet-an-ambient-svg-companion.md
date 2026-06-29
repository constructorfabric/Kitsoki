### Trace Pet — an ambient SVG companion for the kitsoki trace column

# PRD: Trace Pet — an ambient SVG companion for the kitsoki trace column

A small SVG "pet" anchored at the bottom of the trace column that **rests** when nothing runs, **animates** while a turn is live, and gives a **brief pass/fail reaction** on landing — an ambient, glanceable signal of run state that never obstructs or competes with the timeline.

## Problem
The trace timeline is precise but static between events; an operator with split attention has no peripheral cue that work is in flight, just passed, or just failed. The signal exists in the trace but must be read row-by-row. The pet adds the missing ambient channel.

## Key design law
The pet is a **pure projection of the trace** — it only ever surfaces state the trace records, never implies one it doesn't (`run-status-ui.md`). No new event kinds, no engine changes; read-side only.

## State model (pinned to declared event kinds, `trace-format.md` §4)
| Pet state | Trace source |
|---|---|
| Resting | no open agent call, no turn in flight |
| Active | `agent.call.start` unpaired by `call_id` / `turn.start` w/o `turn.end` |
| Pass (brief) | `machine.transition` / accepted decide-verify |
| Fail (brief) | `agent.call.error`, `machine.validation_failed`, `machine.guard_rejected`, rejected decide |
Pass/Fail are transient one-shots that decay to Resting; in-flight outcomes are never pre-empted. State is a pure function of trace events, so live ≡ replay (determinism preserved).

## Requirements (highlights)
- Fixed reserved slot at the bottom of the trace column; never overlaps/steals pointer events from the timeline.
- Render-at-source SVG (not a CSS hack over the timeline) per `web/README.md` render-fidelity rule.
- On-brand: small-glyph discipline + kitsoki palette only, no sacred imagery (`branding/logo.md`).
- Deterministic: state transitions trace-driven only → `--flow`/cassette/replay-video byte-reproducible.
- Accessibility (hard): respect `prefers-reduced-motion`; **never the sole signal** of a state; pass/fail distinct beyond colour.
- Testable under the no-LLM harness with a Playwright no-obstruction/state assertion.

## Goals / Non-goals
Ambient glanceable signal, faithful projection, non-obstructive, on-brand, replay-safe. **Not** a state-of-record, **not** interactive, **not** a multi-run dashboard widget, **not** TUI in v1, **no** engine changes, **not** a stateful tamagotchi.

## v1 surface
Web run-status SPA (Drive + Observe + read-only UI share one component). TUI and home-screen multi-run list deferred.

## Status note
Round-1 clarifications came back **blank**, so audience, success measure, extra states, the off-switch/a11y bar, and the surface set are stated as assumptions and raised as open questions for another clarification round. Full document at `.artifacts/prd/add-a-tiny-svg-pet-anchored/004-prd.md`.
