# TUI: multi-view + waterfall — co-equal projections of one trace

**Status:** Draft v1. Nothing implemented yet.
**Kind:**   tui
**Epic:**   ../trace-introspection.md

<!-- "TUI" = the runstatus web SPA inspection surface
     (docs/tracing/run-status-ui.md). Rendering-through-typed-components,
     data-in only; the rendering-test guard here is Playwright/Vitest. -->

## Why

`runstatus` shows a run as a state diagram (left) + an event timeline
(right). The timeline is a single projection — a vertical list grouped by
phase/turn. We record `duration_ms` on every agent and host call
(`agent_dispatch.go:420`; rendered today only as inline text via `fmtMs()`
at `TraceTimeline.vue:123-125`), but there is **no latency waterfall**, so
"this turn took 8s and 6s of it was one `host.agent.task`" is invisible
without reading numbers row by row.

Langfuse's lesson (`.context/langfuse-trace-viewer-comparison.md`, idea #1 —
rated the strongest gap) is *same data, multiple co-equal projections*:
Tree / Timeline / Graph kept at parity. We already have two of the three
(the grouped timeline ≈ Tree; the Mermaid diagram ≈ Graph) but they live in
fixed panels, not as switchable views, and we lack the Timeline/waterfall.
Separately, the Home session list (idea #8) shows Story / Session / State /
Activity with **no sort or filter** (`HomeView.vue:73-108`), so triaging
many dogfood runs to the expensive or bailed-to-human ones means eyeballing.

## What changes

One sentence: **make the right pane a set of co-equal view modes over the
one immutable event stream — Tree (today's grouped timeline), Timeline (a
duration-keyed waterfall), and Graph (the existing Mermaid diagram promoted
to a switchable tab) — and turn the Home session list into a
sortable/filterable triage table.**

All three views are pure projections of the same `Snapshot.Events`
(`internal/runstatus/snapshot.go:22`); switching modes never refetches or
mutates. Color/collapse/badge in every mode keys off the observation
category from slice #1.

## Impact

- **Code:** a new `ViewModeTabs.vue` host that swaps between
  `TraceTimeline.vue` (Tree, existing), a new `TraceWaterfall.vue`
  (Timeline), and `StateDiagram.vue` (Graph, existing — moved behind the
  tab); `RunView.vue` layout change to give the tabs the right pane; a
  sort/filter header on `HomeView.vue`'s session table + supporting computed
  state in `src/stores/run.ts` (or a `home` store).
- **Rendering:** new `TraceWaterfall` typed component (bars bound to
  `duration_ms`, positioned by start time within a turn) — data-in, no
  hand-built SVG strings; reuse the category colors from slice #1.
- **Input:** a view-mode toggle (tabs / keyboard); sort + filter controls on
  Home. Read-only otherwise.
- **Docs on ship:** `docs/tracing/run-status-ui.md` (view modes, triage
  table).

## Mental model

One trace, three lenses you flip between like map/satellite/terrain: Tree to
read the sequence, Timeline to *see* where the wall-clock went, Graph to see
the shape of the state machine. The Home table is the index that gets you to
the right run in the first place.

## Layout

```
RunView right pane:                  Home session table:
┌─[Tree][Timeline][Graph]──────────┐ ┌ Story ▲ │ State │ Turns │ Cost │ Dur │ ⚑ ┐
│ Timeline (waterfall):            │ │ bugfix  │ done  │   7   │ $.04 │ 88s │   │
│ turn 2 proposing                 │ │ feature │ await │  12   │ $.11 │142s │ ⚑ │  ← bailed
│  agent.decide ▓▓▓▓▓▓▓▓ 88.9s    │ │ dev     │ idle  │   3   │  $0  │  4s │   │
│  host.run      ▓ 0.4s            │ └ (click a header to sort; filter chips) ┘
│ turn 3 implementing              │
│  agent.task   ▓▓▓▓ 41s          │
└──────────────────────────────────┘
```

## Rendering changes

- **`TraceWaterfall.vue`** (new) — one row per agent/host call, a bar whose
  **length ∝ `duration_ms`** and whose offset ∝ start time within its turn,
  so parallelism and bottlenecks are visible at a glance. Bars colored by
  observation category (slice #1). Non-timed events (`world.update`,
  `machine.say`) render as zero-width ticks, not bars. Clicking a bar opens
  the same detail pane the Tree uses (including slice #2's decision-first
  detail for decide rows).
- **`ViewModeTabs.vue`** (new) — a thin tab host; persists the active mode
  in the URL (consistent with the runstatus Phase 2 "URL-persisted state"
  agenda in `runstatus-proposal.md`). Tree is `TraceTimeline.vue` unchanged;
  Graph is `StateDiagram.vue` moved behind the tab (the cross-panel "click a
  node → scroll the timeline" linking is preserved within the Graph tab).
- **Home triage table** — make the existing columns sortable
  (`HomeView.vue:73-108`) and add derived columns the survey calls for:
  **turn count**, **total cost** (sum of `cost_usd` across agent calls),
  **total duration**, **terminal/active**, **bailed-to-human?** (any
  `gate_decided.bailed_to_human`). Filter chips: active/terminal,
  has-bailed. These are computed from the snapshot the session already
  loads — no new RPC if the list endpoint returns enough; otherwise a small
  `runstatus.sessions.list` field addition (Open question 2).

## Input & commands

| Command / key | Does | Notes |
|---|---|---|
| view-mode tabs (Tree/Timeline/Graph) | swap the right-pane projection | active mode persisted in URL |
| `1`/`2`/`3` (optional) | keyboard view-mode switch | nice-to-have |
| click a column header (Home) | sort the session table | toggle asc/desc |
| filter chips (Home) | narrow to active/terminal/bailed | clear restores |

## Rendering tests

Runstatus SPA — guarded by Playwright + Vitest, not the chat-TUI combined-I/O
harness.

- `view-modes.spec.ts` (Playwright, artifact mode) — load the bugfix
  fixture: assert all three tabs render the same event set (Tree row count ==
  Waterfall bar+tick count for timed/untimed events), switching modes
  doesn't refetch, and the URL reflects the active mode.
- `TraceWaterfall` Vitest — bar length/offset as a pure function of
  `(duration_ms, start_ts, turn_window)`; zero-width tick for non-timed
  events. Verified to fail (no waterfall exists today).
- `HomeView` Vitest — sort comparators and filter predicates as pure
  functions over the session list; cost/duration/bailed derivation from a
  fixture snapshot.

## Migration plan

The Graph (Mermaid) view moves from a fixed left panel into the tab host.
During the change the two-panel `RunView` becomes diagram-behind-a-tab; the
existing node-click → timeline-scroll linking must keep working *within* the
Graph tab. No data migration — every mode reads the same `Snapshot`.

## Tasks

```
## 1. Render
- [ ] 1.1 ViewModeTabs host; move StateDiagram behind the Graph tab; URL-persist active mode
- [ ] 1.2 TraceWaterfall: bars ∝ duration_ms, offset ∝ start; category colors (slice #1); ticks for untimed events
- [ ] 1.3 HomeView: sortable columns + derived turns/cost/duration/terminal/bailed + filter chips

## 2. Drive
- [ ] 2.1 View-mode toggle (tabs + optional 1/2/3 keys); Home sort/filter controls

## 3. Prove + document
- [ ] 3.1 view-modes.spec.ts (Playwright) — three modes, same event set, no refetch, URL state; verified to fail without the change
- [ ] 3.2 TraceWaterfall + HomeView Vitest (bar geometry, sort/filter purity)
- [ ] 3.3 Re-render the bugfix artifact; eyeball the waterfall surfaces the long task call
- [ ] 3.4 Update docs/tracing/run-status-ui.md; trim/delete this slice
```

## What we lose, honestly

Making the Graph a tab means the diagram and timeline are no longer
side-by-side — you lose the simultaneous "where am I on the map *and* in the
log" glance that the current two-panel layout gives. Mitigation: keep
within-tab cross-linking, and consider a "pin Graph" split for wide
viewports as a follow-on (out of scope). The waterfall also makes very long
calls (an 88s decide) dominate the bar scale; we clamp/log-scale rather than
let one bar flatten the rest (Open question 1).

## Open questions

1. **Waterfall time scale — linear or log?** A single 88s call dwarfs sub-second
   host calls on a linear scale. *Lean: linear within a turn (turns are the
   natural window and rarely span two orders of magnitude), with a per-turn
   max so bars are relative to their turn, not the whole run.*
2. **Do triage columns need a new RPC field?** Cost/duration/bailed can be
   computed client-side if the list endpoint returns per-session events, but
   that's heavy for a list. *Lean: add precomputed `turn_count`,
   `total_cost_usd`, `total_duration_ms`, `bailed` to
   `runstatus.sessions.list`'s row shape — cheap server-side rollup, keeps
   the list endpoint light.*

## Non-goals

- **A node-link agent Graph** beyond the existing Mermaid state diagram.
  Langfuse's force-directed agent graph is out of scope; we promote the
  diagram we already emit to a co-equal tab, no new graph library (the
  `runstatus-proposal.md` Phase 4 Vue-Flow swap is a separate track).
- **Decision-first detail** — slice #2; this slice routes waterfall-bar
  clicks into whatever detail pane #2 provides.
- **Cross-run aggregation** of the triage columns into an eval dashboard —
  downstream of slice #5, not here.
