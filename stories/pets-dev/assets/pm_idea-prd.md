# PRD: Trace-Column Pet

> Status: **Published** · Owner: Product · Source idea: "give the trace column a little life"

## Problem & Context

Watching a long kitsoki session is a lonely wait at the **trace column** — the
right-hand observer panel in the web drive view that just ticks rows by while the
agent thinks, tests run, and a fix lands. There is nothing alive on the screen,
and a long agent loop can feel like staring at a log. A small ambient companion
can add warmth and life to that wait **without taking screen space**.

## Target Users

- **Developers** watching a long kitsoki session's trace column in the web UI.
- **Demo viewers** following a recorded run who want the surface to feel friendly.
- **Operators** sitting with a session, glancing at the corner between turns.

## Goals

- A single small animated **SVG pet** (VS-Code-Pets / tamagotchi style) anchored
  at the **bottom of the trace column** that idles (bob/blink) while the session
  runs.
- **Opt-in and never in the way** — off by default, and it must never block trace
  content or displace trace rows.
- **Zero weight** — pure inline SVG + CSS keyframes, no images, no dependencies,
  no added network requests.

## Non-Goals

- A full multi-state companion overlay or its own panel (this is one small sprite).
- A topbar widget or anything outside the trace column.
- Image/sprite-sheet assets, sound, or any third-party animation library.

## Requirements

### Functional

- An inline-SVG sprite rendered at the bottom of the trace column, absolutely
  positioned so trace rows are never displaced.
- A CSS keyframe **idle animation** (a gentle bob and an occasional blink).
- *(Optional)* a subtle reaction when a turn is running (e.g. a slightly faster
  bob) — driven by state the trace column already has.
- An **opt-in toggle**: the pet only mounts when the setting is enabled.

### Non-functional

- **Headline metric**: opt-in-pet-enabled-sessions.
- Pure inline SVG + CSS — no images, no new dependencies, no network.
- No measurable impact on trace rendering or scroll performance.

## Acceptance Criteria

- With the setting enabled, a small animated pet appears at the bottom of the
  trace column and idles continuously.
- With the setting disabled (the default), no pet is mounted and nothing changes.
- The pet never overlaps or pushes trace rows; the column scrolls normally.
- No new asset files, dependencies, or network requests are introduced.

## Edge Cases

- Very short trace column → the pet stays anchored to the column bottom, not the
  last row.
- Reduced-motion preference → the idle animation is dampened or paused.
- Narrow viewport → the pet scales down and never clips the scrollbar.

## Rollout

1. Land the `TracePet.vue` sprite + dock seam behind the opt-in toggle (off).
2. Document the setting in the web UI reference.
3. Announce in the changelog; purely additive, no migration.

## Open Questions

- Should the pet react when a turn is running, or only idle? *(Leaning: a subtle
  idle-only first cut, with reaction deferred.)*
- One fixed pet, or a small chooser later? Deferred.
