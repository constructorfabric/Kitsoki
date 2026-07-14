# kitsoki-browser-mcp

The converged, keyless browser-automation MCP server: deterministic
primitives (no LLM) plus Stagehand-grounded `observe`/`act` routed through
the kitsoki harness. Authoring half of the browser-MCP + click-through-tour
work (`.context/2026-07-12-browser-mcp-tour-implementation-brief.md`, P2).

Converges `tools/stagehand-mcp` (the harness `LLMClient` bridge and
headless-LOCAL Stagehand launch) and the browser-automation surface of
`tools/frontend-mockup-mcp`, on the official `@modelcontextprotocol/sdk`
instead of a hand-rolled JSON-RPC loop.

## Tool surface

Deterministic primitives (no LLM call):

- `browser_navigate(url)`
- `browser_click(anchor)` / `browser_fill(anchor, value)`
- `browser_scroll(anchor | deltaX/deltaY)` / `browser_press(key, anchor?)`
- `browser_snapshot(cap?, within?)` — capped, interactive-only, ref-based
- `browser_find(query)` — searches the last snapshot; never the full page
- `browser_batch(ops[])` — N primitive ops in one call
- `browser_close()`

LLM-grounded (via the harness):

- `observe(instruction?)` → serializable `Action[]`
- `act(instruction | action)` — a string instruction is LLM-grounded; an
  `Action` object from a prior `observe()` replays with **zero** further LLM
  calls

An `anchor` is a ranked multi-anchor bundle — `{role, name, testid, text,
css, ancestor}`, or `{ref}` from a prior snapshot — shaped identically to
tour format v2's `TargetBundle` (`internal/tour/manifest_v2.go`,
`tools/runstatus/src/tour/types-v2.ts`), so a resolved anchor drops straight
into a `tour_step` call once P3 lands. Resolution order: role+name ->
testid -> text -> css -> ancestor fallback. The first strategy that resolves
to **exactly one** element wins; anything else is a loud
`AnchorResolutionError` carrying every attempt — never a silent "first
match" pick.

## Implementation notes / gotchas

- **Stagehand wraps Playwright's `Page`/`Locator` with a reduced API.** No
  `Locator.all()`, `.evaluate()`, `.press()`, or `.scrollIntoViewIfNeeded()`;
  `page.click(selector)`/`page.type(selector, ...)` throw a `-32602 invalid
  parameters` transport error (the same landmine
  `tools/frontend-mockup-mcp`'s `mockup_click` hit). Anchor resolution and
  snapshotting therefore run as a single `page.evaluate()` over the real
  DOM that tags the winning element with a one-shot marker attribute
  (`data-kitsoki-anchor-id` / `data-kitsoki-ref`); actions go through
  `page.locator('[data-kitsoki-...="..."]').click()/.fill()`, the methods
  the wrapper does keep. `browser_press` with an anchor clicks (which
  focuses) then calls the page-level `page.keyPress(key)`.
- **Stagehand version pinned at `3.6.0`** (unchanged from `tools/
  stagehand-mcp`). The brief's P2 risk list floats bumping it; that bump is
  deferred to when it can be exercised against a live LLM to catch any
  Stagehand API surface drift — bumping blind isn't safe to land.
- **No live LLM in automated gates.** `npm run smoke` drives every
  deterministic primitive through a real headless Stagehand/Playwright
  browser (a real browser launches; the LLM client is never called).
  `npm test` cassette-tests the harness `LLMClient` bridge itself — the
  exact code path `observe`/`act` route every call through — against a
  canned stub `kitsoki agent ask` replacement, not a live model.

## Env vars

- `KITSOKI_REPO` — repo root passed to `kitsoki agent ask` (default: cwd)
- `KITSOKI_AGENT_CMD` — the kitsoki binary/command (default: `kitsoki`)
- `KITSOKI_BROWSER_MCP_AGENT` — which agent profile grounds `observe`/`act`
  (default: `codex-native`)
- `KITSOKI_BROWSER_MCP_HEADLESS` — set to `0` for a headed browser (default:
  headless)
- `KITSOKI_BROWSER_MCP_ORIGIN_ALLOWLIST` — comma-separated exact-match
  origins `browser_navigate` may open; empty means unrestricted (local
  fixture / dev use)

## Non-goals (P2 scope)

Tour authoring/replay tools (`tour_start`/`tour_step`/`tour_replay`/
`tour_export`) land in P3. `tools/frontend-mockup-mcp`'s design-review
tooling is unaffected here; folding its browser/tour code onto this package
is a P6 hardening step.
