### Trace Companion — an ambient pet for long runs

# PRD: Trace Companion — an ambient pet for long runs

**Status:** Draft · **Owner:** PM · **Date:** 2026-06-27
**Workspace:** `.artifacts/prd/long-trace-runs-are-visually-monotonous`

---

## 1. Problem & context

When a kitsoki run takes a long time, the trace column in the web UI is a wall of
events that scrolls slowly and otherwise sits still. There is nothing in the
interface that conveys, at an emotional or ambient level, *"work is happening,
you're not alone, hang tight."* The wait reads as dead air. Users get a precise,
truthful picture of run state from the trace timeline, the state diagram, and the
`<state> live` badge — but nothing that adds **warmth** or a low-effort glanceable
sense of activity.

The trace column is, and must remain, a **read-only projection of the trace**:
"The UI never mutates state; it only projects the trace"
([run-status-ui.md](../../../docs/tracing/run-status-ui.md), intro). Any new
element we add to that column cannot touch trace data, state, or functionality —
it can only *react to* what the trace already says.

This PRD proposes a **Trace Companion**: a small, decorative, animated pet pinned
at the bottom of the trace column. It perks up and animates while a run is active
and rests when the run is idle, complete, or has not started. It is purely
cosmetic — a bit of ambient companionship layered on top of the existing,
unchanged trace surface.

### Where it lives

The pet pins to the **bottom of the trace / right column** of the live web UI
(`kitsoki web`), beneath the `TraceTimeline` and `StateDiagram` components
([web/README.md](../../../docs/web/README.md), "Layout (Drive)" and
"Trace + diagram (right)", lines ~168–189). It is a sibling overlay of those
components, never a participant in them.

## 2. Target users

- **People watching a live run.** Anyone driving or observing a session in
  `kitsoki web` while a long-running agent works — the primary audience, because
  the pet's whole purpose is to fill the wait.
- **Developers / operators dogfooding kitsoki.** Frequent, repeat watchers of long
  traces who benefit most from ambient warmth and who are most sensitive to
  anything that distracts from the trace itself.

Non-audience: consumers of the **static** self-contained HTML artifact
(`export-status`) and read-only `status serve` viewers — see Non-goals.

## 3. Goals & non-goals

### Goals

- **G1 — Companionship.** Give a long wait a sense of presence and warmth.
- **G2 — Ambient activity signal.** Glanceable, peripheral-vision sense of whether
  a run is active, without reading the timeline.
- **G3 — Zero functional footprint.** No change to trace data, layout behaviour, or
  existing run-status functionality.
- **G4 — Calm by default.** Quiet, mostly-still presence that never competes with
  the trace.

### Non-goals

- Not a status source of truth; not a notification/alerting system; **no sound**
  (v1); not in the static HTML artifact or read-only viewer (v1); no game
  mechanics; no new trace events.

## 4. Run states the pet reacts to

Mood derives **entirely from existing run state** (live/current/terminal model;
`<state> live` badge; terminal "Session complete" composer). No new state.

| Run state | Source signal (existing) | Pet behaviour |
|---|---|---|
| **No run / not started** | empty run, no current state | **Resting** — calm, minimal motion. |
| **Active** | `<state> live` badge; trace appending over SSE | **Perked up** — alert, gentle "working" loop. |
| **Idle, not terminal** | live session awaiting input | **Resting / attentive** — settles back; occasional idle motion. |
| **Complete (terminal)** | "Session complete" composer note | **Resting + content** — brief one-shot "done" beat, then calm. |
| **Failed / errored (terminal)** | terminal error state | **Resting + subdued** — muted, understated; no alarm. |

Behaviour is a pure function of on-screen state; transitions are soft; terminal
beats are brief one-shots, then a calm resting loop.

## 5. Requirements

### 5.1 Functional
- **FR1 Placement** — pinned bottom of trace/right column; no reflow/occlusion;
  yields when space is tight.
- **FR2 State-driven mood** — reads existing run store; no new RPCs/trace fields.
- **FR3 Read-only** — never writes trace/state, never drives intents.
- **FR4 Single fixed character (v1)** — no picker, no naming.
- **FR5 Opt-out** — persistent per-browser off switch; when off, renders nothing.
- **FR6 Non-blocking** — no effect on timeline, diagram, drawers, composer.
- **FR7 Graceful at boundaries** — defaults to resting on reconnect/empty/switch;
  no mood flicker.

### 5.2 Non-functional
- **NFR1 Reduced motion** — static/near-static under `prefers-reduced-motion`.
- **NFR2 Performance under load** — off main render path, pause on hidden tab,
  negligible CPU, never competes with SSE ingestion.
- **NFR3 Calm presence** — low default liveliness.
- **NFR4 On-brand palette** — kitsoki palette only (Gold/Clay/Adobe/Rust/Mesa
  shadow/Sand/Paper, optional Turquoise); correct on tuned backgrounds
  ([logo.md](../../../docs/branding/logo.md)).
- **NFR5 Cultural restraint (hard)** — no kachina/ceremonial/sacred imagery
  (logo.md "Don't"); blocking review gate.
- **NFR6 Accessibility** — `aria-hidden`; never the sole conveyor of state.
- **NFR7 No layout regression** — existing trace-column overflow/layout gates stay
  green.

## 6. Success metrics

Warmth without intrusion, not throughput:
- **Low opt-out** (primary) · **No functional/distraction complaints, no layout
  regressions** (primary) · **Positive sentiment** (secondary) · **No perf
  regression** and **reduced-motion compliance** (guardrails). Targets are
  placeholders pending an instrumentation decision (see Open Questions).

## 7. Constraints (hard)

Purely cosmetic/read-only; must not distract; reduced-motion respected; no perf
regression under heavy runs; palette-only + no sacred imagery; opt-out available.

## 8. Open questions

The Round 1 clarification answers were left blank; the PRD adopts conservative
defaults but flags: (1) exact reaction set & subdued-error treatment; (2) confirm
single fixed character + opt-out only; (3) confirm quiet liveliness + no sound;
(4) whether to instrument success at all and the opt-out threshold; (5) confirm
live-`kitsoki web`-only scope; (6) who designs the creature and what it is, within
the brand/cultural gates.

---

*Full document written to `.artifacts/prd/long-trace-runs-are-visually-monotonous/004-prd.md`.*
