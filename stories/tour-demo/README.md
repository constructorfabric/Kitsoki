# tour-demo — on-demand tour serving loop (P5)

Demonstrates the "ask" half of the browser-MCP + click-through-tour brief's
P5 serving loop
(`.context/2026-07-12-browser-mcp-tour-implementation-brief.md`):
**ask → plan → validate**, fully real and no-LLM.

## What's implemented and verified here

- `host.tour.plan` (`internal/host/tour_plan.go`): given a `feature_id`,
  loads `features/<feature_id>.yaml`'s `tour:` block and converts it to
  tour format v2 via P1's `tour.ConvertToV2`. Pure, deterministic, zero
  network/LLM. Unit-tested against the real feature catalog
  (`TestTourPlanHandler_PlansFromRealFeatureCatalog`).
- `host.tour.validate`: structurally validates a v2 document
  (`TourManifestV2.Validate`) and proves it's renderable by converting it
  back to v1 (P3's `ConvertV2ToV1`) — the format the *existing* player
  already knows how to execute. Unit-tested, including the "unrenderable
  act step" rejection path.
- The `ask` intent (`rooms/dispatch.yaml`): resolves a feature, plans,
  validates, and lands in `planned` with both `world.tour_v2` (the v2
  document) and `world.tour_v1_steps` (ready for the existing overlay) —
  or `not_found` with a clear reason when planning fails. Routing follows
  the established post-bind pattern from `stories/git-ops/rooms/
  cleanup.yaml`: self-target, `on_enter` guard reads the bound world, an
  internal `emit_intent` does the actual navigation.
- Two flow fixtures (`flows/*.yaml`), no LLM: the happy path and the
  not-found path. `host.tour.plan`/`host.tour.validate` are stubbed via
  `host_handlers` — the standard convention for every host call in this
  repo's flow tests (see `stories/git-ops/flows/cleanup_by_slot.yaml`'s
  identical stubbing of the deterministic `host.run`) — so these fixtures
  prove the STORY's routing/binding/on_error wiring; the handler LOGIC
  itself is proven separately by the Go unit tests against the real
  on-disk feature catalog.

Run: `go run ./cmd/kitsoki test flows stories/tour-demo/app.yaml --v`

## What's deliberately NOT implemented here: push → play

The brief's remaining two steps — pushing `world.tour_v1_steps` to the
live browser session and having the existing tour overlay
(`tools/runstatus/src/components/tour/TourOverlay.vue` /
`stores/tour.ts`'s `startWithSteps()`) pick it up automatically — are
**designed but not wired**, and this was verified LIVE (not guessed):
`go run ./cmd/kitsoki web --stories-dir stories/tour-demo --flow
stories/tour-demo/flows/ask_agent_actions.yaml`, driven with
`mcp__claude-in-chrome__*`, confirms the story runs correctly end to end
in a real browser (the `ask` turn lands in `planned` and renders "Here's
how — the tour is ready and pushed to your session."). But inspecting the
session's actual live data surface — `runstatus.session.trace` (the SSE
event backing) and `runstatus.session.view`'s `TurnResult` — found the
mechanism this README originally assumed does NOT exist:

- `runstatus.session.trace` only carries state-machine lifecycle events
  (`session.story`, `machine.transition`, `machine.state_exited`,
  `machine.state_entered`) — **no** `world.update`/bind-level event at
  all. The `{kind: "world.update", payload: {set: {...}}}` shape this
  README originally pointed at only exists in the flow-test CLI's OWN
  internal debug trace (`--trace-out`/`--json`), a completely different
  code path that never reaches the browser.
- `TurnResult` (what `runstatus.session.view`/`.submit` actually return)
  has no raw-world field either — `relevant_world` only reaches the
  browser baked into the rendered `view`/`typed_view` template string. A
  room's `world.tour_v1_steps` object is invisible to the frontend as
  structured data; only what the Pongo template chooses to interpolate as
  text is visible.

So the real gap is bigger than "wire an existing event" — kitsoki's
web serving layer has **no channel for arbitrary structured JSON payloads
today**, only rendered view strings and state-lifecycle events. Landing
push→play needs one of:

1. A new `View`/block type a room can populate with an opaque JSON blob
   (e.g. `data: '{{ world.tour_v1_steps }}'`), which the frontend renders
   into `window.__startTourWithSteps(...)` when present — the
   lowest-risk option, reusing the existing view-rendering pipeline
   rather than adding a new transport.
2. A dedicated RPC (`runstatus.session.tour`, mirroring
   `runstatus.session.world` that this investigation confirmed does NOT
   exist) the frontend polls or the story's `on_enter` triggers.

This is real, scoped follow-up work — not a "small wiring" — and belongs
in its own reviewed slice with its own live verification, which is why it
stops here rather than landing a guessed transport. The proven, working
part of this story (ask → plan → validate, `world.tour_v2` /
`world.tour_v1_steps` populated and visible in the room's own rendered
view right now) is real progress toward it: the "generate" and "validate"
halves of the loop are done; only "push" needs a new, small transport.
