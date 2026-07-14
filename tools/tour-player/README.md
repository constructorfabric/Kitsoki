# kitsoki-tour-player

Framework-free playback for tour format v2 — extracted from
`tools/runstatus/src/components/tour/TourOverlay.vue` /
`src/stores/tour.ts` (the Vue reference implementation). Zero runtime
dependencies, ~4.8 kB gzipped as a single IIFE, works in any page:
kitsoki-family apps and arbitrary third-party SPAs alike (verified live
against `pog-portal`, a completely separate Vue app in a different repo —
see `scripts/verify-pog-portal.mjs`).

Authoring half of the browser-MCP + click-through-tour work
(`.context/2026-07-12-browser-mcp-tour-implementation-brief.md`, P4).
`tools/browser-mcp`'s `tour_*` tools (P3) produce the tour JSON this class
consumes; the P5 serving loop pushes it into a live session.

## Usage

```html
<script src="dist/tour-player.iife.js"></script>
<script>
  const tour = { version: 2, id: "how-to-save", steps: [ /* ... */ ] };
  const player = new KitsokiTourPlayer.TourPlayer(tour, {
    onHeal: (heal) => console.log("healed", heal),
    onEvent: (event) => console.log(event)
  });
  player.start();
</script>
```

Or as an ES module: `import { TourPlayer } from "./dist/tour-player.esm.js"`.

## What it does

- Executes v2 steps: spotlight ring + popover (`highlight`), `advanceOn`
  listeners on the REAL target element (`gate`), consent-gated DOM actions
  (`act` — `confirm` renders a Confirm button, `watch`/`auto` perform
  immediately), route holds (`navigate`).
- Anchor resolution + healing exactly as tour format v2 specifies: ranked
  bundle (role+name → testid → text → css → ancestor), first strategy that
  resolves to exactly one element wins, `onHeal` fires a
  `{stepId, failedAnchor, matchedAnchor, confidence}` event whenever the
  primary strategy missed but a secondary one resolved — never silent.
- SPA resilience: a `MutationObserver` on `document.body` re-anchors when
  the target re-mounts (Reactour-style), plus a 200ms poll backstop and an
  anchor-grace watchdog that skips an un-anchorable step rather than
  blocking the tour forever.
- `TourRegistry` (`webmcp.ts`): the WebMCP-shaped in-page control surface —
  `start_guided_tour`/`abort_tour`/`list_tour_anchors` on
  `navigator.modelContext` when present, else a postMessage transport
  compatible with the `@mcp-b/webmcp-polyfill` shape. `registerAnchor` also
  builds the anchor catalog a tour generator (P5) can plan against.

## A real, load-bearing bug this extraction found and fixed

The player's own rendering (inserting/updating the spotlight ring and
popover) mutates `document.body` — exactly what the SPA-resilience
`MutationObserver` watches. An earlier version called `refresh()`
unconditionally from the observer callback, which meant the player's own
DOM writes re-triggered the observer, which called `refresh()` again, which
wrote to the DOM again — an infinite, synchronous-feeling mutation loop.
It didn't show up in the jsdom unit tests (their assertions run
immediately, before the loop has time to spiral), but running the real
built bundle against a live headless-Chromium `pog-portal` page hung
solid: the loop starved the browser's CDP round trip and `page.evaluate()`
never returned.

Fixed two ways, both needed:

1. The `MutationObserver` callback now goes through a `requestAnimationFrame`-coalesced
   `scheduleRefresh()` instead of calling `refresh()` directly, so a burst
   of mutations collapses into at most one refresh per frame.
2. `renderRing`/`renderPopover`/`positionPopover` are now idempotent — each
   tracks the last geometry/step it wrote and skips the DOM write entirely
   when nothing changed, which is what actually breaks the mutation
   feedback loop at steady state rather than merely throttling it.

This is exactly the kind of thing `scripts/verify-pog-portal.mjs` exists to
catch: jsdom tests alone would never have surfaced it.

## Scripts

- `npm run build` — esbuild to `dist/tour-player.{esm,iife}.js` + `tsc
  --emitDeclarationOnly` for `dist/*.d.ts`.
- `npm test` — jsdom unit tests (anchor resolution/healing, state machine,
  advance-on gating, act policy, WebMCP postMessage fallback). No browser,
  no LLM.
- `npm run verify:pog-portal` — the real-browser, real-third-party-app
  proof described above. Requires `~/code/pog/portal` (or `POG_PORTAL_DIR`)
  and a cached Playwright chromium; SKIPs cleanly when either is absent
  (e.g. CI). No LLM.

## Non-goals (P4 scope)

Migrating `tools/runstatus`'s `TourOverlay.vue` to consume this package
(making the Vue component "a thin wrapper", per the brief) is deferred —
that's a live-product refactor of kitsoki web's onboarding tour and
deserves its own dedicated QA pass rather than riding along with this
extraction. `TourOverlay.vue`/`stores/tour.ts` are untouched by this
change; this package is net-new and additive. The native
`navigator.modelContext` path is wired but unexercised (no browser ships it
unflagged yet) — only the postMessage fallback is tested.
