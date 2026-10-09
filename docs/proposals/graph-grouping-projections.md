# Graph grouping projections — persona views over areas and initiatives

**Status:** draft. Follow-on to the landed grouping taxonomy
([`../architecture/graph-grouping-taxonomy.md`](../architecture/graph-grouping-taxonomy.md)):
the `area`/`initiative` types, `feature.in_area`, the lints, and the viewer's
group-by-area mode shipped 2026-07-05; this proposal covers the remaining
computed projections and their surfaces. No new stored views — every surface
below is a query over existing anchors and edges.

## Why

The taxonomy exists so persona questions become cheap graph queries. The
portfolio grouping (PM view) shipped as the viewer's group-by-area mode; the
other persona views have anchors but no surface yet:

| Persona | Question | Status |
|---|---|---|
| PM / portfolio | state + roadmap delta per area | partially shipped (viewer group-by-area; no roadmap-delta rollup) |
| Project manager | is my initiative on track, what's blocked | **not built** |
| Lead dev owning an area | which projects touch my area right now | **not built** |
| QA engineer / QA lead | what must I verify / area evidence coverage | **not built** |
| Site visitor | features browsable by category | **not built** |

## Slices

1. **Initiative detail view** (tui/web). One `initiative` node → its
   `includes` fan-out with per-change status/assignee, plus derived
   `targets` chips linking into each affected area. The project manager's
   and project-QA's home surface.
2. **Area inbox** (tui/web). The reverse index: for area X, every open
   change/initiative whose `implements` targets land in features `in_area`
   X — regardless of who owns the initiative — plus the
   requirement → `verified_by` evidence-coverage table. This is the view
   that makes cross-cutting initiatives safe for a lead: they see
   everything touching their scope without owning any of it.
3. **Site category navigation** (runtime, G3 follow-on). Areas become the
   public category structure: let `site-page.presents` target an `area`
   (a category landing page) as well as a `feature`, and replace
   `promo.order`-only flat ordering with order-within-area. This is also
   what activates the **area-visibility lint**: once site-pages render
   areas, the existing `lintVisibilityLeaks` renders-edge walk must cover
   the `area → feature` hop so a public area cannot expose only-internal
   features.
4. **Portfolio roadmap rollup** (runtime). Group the computed roadmap delta
   (`graph diff`, G4) by primary area, rolled up the `part_of` DAG —
   completes the PM view beyond static grouping.
5. **CLI wiring** (small). `kitsoki graph lint` gains a flag enabling the
   advisory `initiative-scope` check (today it is opt-in via `LintOptions`
   and only exercised by tests).

## Non-goals

`component` stays deferred; no `goal`/`wall`/`epic` types; no stored views
or per-persona configuration — if a view needs authored state, the design
is wrong.
