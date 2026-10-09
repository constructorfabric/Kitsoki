# Browser automation MCP and click-through tours

LLM browser control (`tools/browser-mcp`) and data-driven click-through
tours (tour format v2, `tools/tour-player`) share one format so the same
tour authored headlessly by an agent plays back live in a user's browser —
kitsoki-family apps and arbitrary third-party pages alike.

## Tour format v2

An app-agnostic, portable tour: `{version: 2, id, origin?, steps: []}`.
Each step carries a ranked, multi-anchor `target` bundle (`role+name` →
`testid` → `text` → `css` → an `ancestor`-scoped fallback — never a single
selector), a `kind` (`highlight` | `gate` | `act` | `navigate`), and
narration (`popover`). `gate` is the default click-through kind: the user
performs the real action and the tour reacts; `act` is the consent-gated
agentic tier (`policy: watch | confirm | auto`, defaulting `act` steps to
`confirm`).

Anchor resolution always tries the ranked bundle in order and accepts only
a strategy that resolves to exactly one element. When the primary
strategy misses but a secondary one resolves uniquely, that's a **heal** —
`{stepId, failedAnchor, matchedAnchor, confidence}` — always reported,
never applied silently.

Three hand-written mirrors are kept in lockstep by doc comment (schema
validation, not shared code, since each targets a different runtime):
`schemas/tour-v2.schema.json`, `internal/tour/manifest_v2.go`, and
`tools/runstatus/src/tour/types-v2.ts` (plus a fourth, `tools/tour-
player/src/types.ts`, so the player stays independently publishable with
no sibling-tool coupling). A step field with no counterpart in one of
these needs a reason, not just an edit.

Legacy v1 tours (`internal/tour`'s pre-existing `kitsoki tour` renderer,
predating this work) convert losslessly both directions:
`ConvertToV2`/`ConvertV2ToV1` (`internal/tour/convert_v2.go`,
`convert_v2_to_v1.go`). `LoadTourManifest` auto-detects `version: 2` and
downconverts at load time, so the existing chromedp renderer plays v2
tours unchanged. v1's `Target` is always resolved as a bare
`data-testid` selector, so only a v2 bundle's `testid` field survives that
direction; a v1→v2→v1 round trip is lossless via each step's `data`
field (opaque per-step payload, used exactly for this).

## tools/browser-mcp — the authoring half

A keyless MCP server (official `@modelcontextprotocol/sdk`, stdio
transport) that drives headless Chromium via Stagehand, routing every LLM
call through the `kitsoki agent ask` harness (`lib/kitsoki-llm-client.mjs`
— cassette-recorded, harness-billed, no direct provider key).

Two tool tiers:

- **Deterministic primitives** (`browser_navigate/click/fill/scroll/
  press/snapshot/find/batch`) never call an LLM. `browser_snapshot` is
  capped, interactive-only, and ref-based; a stale ref fails loudly.
  Anchor resolution and snapshotting run as a single `page.evaluate()`
  over the real DOM, tagging the winning element with a one-shot marker
  attribute — **not** `page.locator(selector).click()`/`page.click(selector)`,
  because Stagehand wraps Playwright's `Page`/`Locator` with a reduced
  API (no `Locator.all()`/`.evaluate()`/`.press()`, and
  `page.click(selector)` throws a `-32602` transport error). Every
  primitive here goes through the wrapper's `.click()`/`.fill()` (the
  methods it does keep) or `page.evaluate()` for anything else.
- **LLM-grounded** (`observe`/`act`): `observe(instruction?)` returns
  serializable `Action[]` candidates; `act(instruction | action)` replays
  a prior `Action` with **zero** further LLM calls, or grounds a fresh
  natural-language instruction through the harness.
- **Tour authoring/replay** (`tour_start`/`tour_step`/`tour_export`/
  `tour_replay`): `tour_step` validates a step's target against the live
  DOM and enriches whatever fields the author left blank from the
  resolved element, never overwriting an explicit choice. `tour_replay`
  deterministically re-resolves every step (with healing) and, for `act`
  steps, performs them — no LLM call.

Origin allowlisting (`KITSOKI_BROWSER_MCP_ORIGIN_ALLOWLIST`) is
exact-match; empty means unrestricted (local fixture / dev use only).

### Destructive-action taxonomy

`act` steps are the only place browser-mcp or the player perform a
mutating DOM action. Three tiers, matching tour format v2's `policy`
field:

| Policy | When to author it | What happens |
|---|---|---|
| `confirm` (default) | Any action with a real-world consequence outside the page itself — form submission, a delete/remove control, anything that sends data | The player renders a Confirm affordance; nothing happens until the user clicks it. |
| `watch` | A visible, reversible, same-page action the user should see happen but doesn't need to gate on (e.g. opening a panel the rest of the tour depends on) | Performed immediately, with a visible pulse/cursor so the action isn't invisible. |
| `auto` | Same-origin, trusted, fully reversible, no user-visible side effect worth narrating | Performed immediately and silently. Reserve for kitsoki-family apps or contexts where the tour author IS the app owner. |

Authoring a step whose `act.kind` is `click` against a control whose
accessible name or nearby text suggests deletion/removal/irreversible
mutation should default to `confirm` even if the author didn't set
`policy` explicitly — `tour_step`/`tour_replay` do not currently enforce
this heuristically; it is an authoring discipline today, not a runtime
guard. `browser-mcp`'s own primitives (`browser_click`, etc.) have no
policy tier at all — they're the agent's direct hands, not tour playback,
so the calling agent is responsible for its own confirmation UX.

## tools/tour-player — the playback half

Framework-free (~4.8 kB gzipped IIFE, zero runtime deps), extracted from
`tools/runstatus`'s Vue reference implementation
(`TourOverlay.vue`/`stores/tour.ts`, which are unmigrated — see below).
Executes v2 steps directly against the page it's loaded into: spotlight
ring + popover, real-click gates, consent-gated `act`, route holds, a
`MutationObserver` + poll backstop for SPA re-anchoring, and an
anchor-grace watchdog that skips (never blocks on) an un-anchorable step.

Verified standalone against `pog-portal` — an unrelated Vue app in a
different repo, no anchor catalog, anchors discovered live from the real
page — proving the format and player generalize past kitsoki-family apps
(`tools/tour-player/scripts/verify-pog-portal.mjs`).

A `TourRegistry` (`src/webmcp.ts`) exposes `start_guided_tour`/
`abort_tour`/`list_tour_anchors` as WebMCP-shaped tools on
`navigator.modelContext` when present, else a postMessage transport
compatible with `@mcp-b/webmcp-polyfill`. Only the postMessage fallback
is exercised today (no browser ships native WebMCP unflagged yet).

**A real bug this extraction found**: the player's own DOM writes
(spotlight ring, popover) mutate `document.body` — exactly what the
SPA-resilience `MutationObserver` watches. An early version called
`refresh()` unconditionally from the observer callback, creating an
infinite mutation feedback loop invisible to synchronous jsdom
assertions but fatal against a real browser (it starved the page's CDP
round trip solid). Fixed by `requestAnimationFrame`-coalescing the
observer callback and making every render function
(`renderRing`/`renderPopover`/`positionPopover`) idempotent — skip the
DOM write when nothing changed, which is what actually breaks the
feedback loop rather than merely throttling it.

## The on-demand serving loop — partially wired

`internal/host/tour_plan.go`'s `host.tour.plan`/`host.tour.validate` are
pure, deterministic Go host verbs (no LLM, no network): given a
`feature_id`, `plan` loads that feature's existing `tour:` block
(`features/*.yaml`) and converts it to v2; `validate` checks the document
and proves it renders by round-tripping to v1. `stories/tour-demo`
demonstrates the `ask → plan → validate` half end to end, with flow
fixtures and a live-browser check via `kitsoki web --flow`.

Pushing a validated tour to the live browser session and having the
existing overlay pick it up automatically is **not wired**. Live
investigation (not guesswork) found kitsoki's web serving layer has no
channel for arbitrary structured JSON today —
`runstatus.session.trace`'s SSE events are state-machine lifecycle only
(no `world.update`/bind event reaches the browser), and `TurnResult`
carries no raw-world field, only the rendered view template string.
Landing "push" needs a small new transport (a JSON-carrying view block a
room can populate, most likely) — see `stories/tour-demo/README.md` for
the concrete next step.

## What's deliberately not done

- **`tools/runstatus`'s `TourOverlay.vue`/`stores/tour.ts` are not
  migrated** onto `tools/tour-player`. They remain the live product's
  onboarding-tour implementation; migrating a live surface deserves its
  own reviewed slice with live QA, not a ride-along on this extraction.
- **`tools/frontend-mockup-mcp`'s browser-driving primitives are not
  folded onto `tools/browser-mcp`** — only its duplicated harness
  LLM-call bridge was retired (it now imports
  `tools/browser-mcp/lib/kitsoki-llm-client.mjs` instead of carrying its
  own copy). The design-review/mockup tooling (`mockup_tour_*`,
  `mockup_click`, etc.) is untouched.
- **The push transport** described above.
