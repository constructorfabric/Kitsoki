# Bugfix Summary: grid-cards narration drift (slidey-128)

> Status: **PR-ready** · Branch: `fix/cards-item-timing-drift` · Mode: one-shot, unsupervised

## Ticket

Grid `cards` scenes with **more than 6 items desync narration** — the spoken
narration runs ahead of the on-screen cards because the per-item timing estimate
diverges from what the renderer actually holds beyond the sixth card.

## Reproduction

- Fixture: a `cards` scene with 8 items + per-item narration.
- Observed: from item 7 onward, the narration audio is ~0.5s ahead of the card
  reveal; by the last item the drift is visible and audible.
- A regression test (`test/timing.test.js`) pins the estimate for a >6-item grid
  against the renderer's real hold, and **failed before the fix**.

## Root Cause

`estimateScene()` fell back to a default per-item hold for cards beyond
`cards_item_5`, but the renderer uses a *different* fallback for those same items.
The estimate table and the render loop disagreed past the sixth item, so the
narration scheduler (which trusts the estimate) drifted.

## Fix

Align the grid-cards estimate with the renderer's actual fallback beyond
`cards_item_5` — one source of truth for the per-item hold. The estimate now
matches the render for grids of any length.

```diff
- const perItem = TIMING.cards_item ?? 30;      // estimate-only fallback
+ const perItem = TIMING.cards_item ?? 20;      // matches the renderer fallback
```

## Validation

- `node --test test/timing.test.js` — the regression test now **passes**: the
  estimate equals the renderer hold for a 6-, 8-, and 12-item grid.
- Full suite green; no other timing snapshot moved.
- Re-ran the 8-item reproduction deck: narration and cards stay in lock-step
  through the last item.

## Self-Review Notes

- The change is confined to the estimate table; the render path is untouched.
- Considered widening the fallback table per-index — rejected as overkill; the
  single aligned constant is the minimal correct fix.
- Worktree isolated (`.worktrees/bf-slidey-128`); no changes outside slidey timing.

## Ready For

A pull request against `slidey` from `fix/cards-item-timing-drift`, carrying this
summary plus the reproduction and validation evidence.
