# Pull Request: fix(timing) align grid-cards estimate with renderer fallback

> Repo: `slidey` · Head: `fix/cards-item-timing-drift` → Base: `main` · Closes: slidey-128

## What & Why

Grid `cards` scenes with more than six items desynced their narration: the
per-item timing **estimate** diverged from the renderer's actual hold beyond
`cards_item_5`, so the narration scheduler drifted ahead of the card reveals.
This aligns the estimate with the renderer's fallback — one source of truth for
the per-item hold — so narration and cards stay in lock-step for grids of any
length.

## Changes

- `src/timing.js` — grid-cards estimate fallback now matches the renderer
  (`?? 30` → `?? 20`) beyond the sixth item.
- `test/timing.test.js` — regression test pinning the estimate to the renderer
  hold for 6-, 8-, and 12-item grids.

## Validation

- `node --test test/timing.test.js` — **passing** (was failing before the fix).
- Full test suite green; no unrelated timing snapshot moved.
- 8-item reproduction deck re-rendered: narration tracks the cards through the
  final item (see the attached reproduction + validation clip).

## Reviewer Notes

- Scope is the estimate table only; the render loop is untouched.
- Minimal correct fix — a single aligned constant rather than a per-index table.
- Deterministic: same spec in → same timing out; the export stays byte-stable.

## Checklist

- [x] Regression test added and passing
- [x] Full suite green
- [x] No changes outside slidey timing
- [x] Reproduction + validation evidence attached
- [x] Changelog entry

## CI

```
✓ build           (12s)
✓ unit tests      (8s)   — 1 new (timing regression)
✓ lint            (3s)
```

Ready to merge.
