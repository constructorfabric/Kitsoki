# Spatial capture — click a frame, resolve the element, ask the oracle

In the [web UI](web-ui.md), an operator can **point at a pixel in a frame** —
a paused `<video>`, an `<img>`, or an rrweb replay — and the read-only oracle
answers about *what they pointed at*: *"the `intent-btn-run` you clicked is
disabled because `world.ready` is false."* The click resolves, against the
rendered DOM, to a real element (`{selector, role, text, bbox}`), and that
element + the still frame + the click point ride on the off-path chat message as
a **visual ambient bundle** (see
[`docs/architecture/visual-ambient.md`](../architecture/visual-ambient.md)).

This is the web capture surface. The terminal gets parity by handing off to a
focused window — see [`spatial-handoff.md`](spatial-handoff.md). The recorded
trace shape is in
[`docs/tracing/trace-format.md`](../tracing/trace-format.md#inputvisual--the-spatial-attachment).

In normal live-session use, the most discoverable entry point is the media
artifact itself: click `Pin` to put the artifact in the
[session media workbench](../web/session-media-workbench.md), then click
`Annotate` in the pinned pane. That flow uses the same spatial substrate, but
normalises the pick into the newer
[annotation anchor](../architecture/artifact-annotation.md) union so a story can
route it to read-only discussion or to a `Send & refine` turn. Use `/review`
when you specifically need to flag a video time range and ask the read-only
oracle about that flagged frame.

## Mental model

A **magnifying glass over a frame**: click anywhere and it tells you *"that's
`intent-btn-run`, a button labeled Run,"* then your next chat message carries
that — and the frame — to the oracle. The operator never sees an
`elementFromPoint` call or a selector heuristic; they see *"I pointed here, and
the assistant knows what I meant."* It joins two halves that already existed in
the web UI — frame display + still-grab ([`/review`](video-review.md)) and the
rrweb-reconstructed DOM (the bug-report capture) — that had never been bound.

## Layout

```
┌────────────────────────────────────────┬─────────────────────────┐
│  ▶ [ frame @ 0:14 ]            ✛(1180,540)│  pointing at:            │
│        ┌───────────┐                      │  ▢ intent-btn-run        │
│        │  ╲ Run  ◄──┼─ box annotation     │    button · "Run"        │
│        └───────────┘                      │  ┌─────────────────────┐ │
│                                           │  │ > why is this       │ │
│  [pick] [box] [clear]                     │  │   disabled here?    │ │
│                                           │  └─────────────────────┘ │
│                                           │  [ Ask ]                 │
└────────────────────────────────────────┴─────────────────────────┘
```

## Components

| Component | Source | Renders / does |
|---|---|---|
| `SpatialPicker.vue` | overlay over the displayed frame | crosshair at the click point; drag → box annotation; maps the click from the rendered frame back into the root's pixel space and emits the resolved bundle |
| `ReplayFrame.vue` | an rrweb `Replayer` + a `SpatialPicker` rooted at its iframe | mounts the recorded session as a paused still of the **real reconstructed UI** and overlays the picker (the "second root") so a click resolves a real app element, not the opaque `<video>` |
| `lib/resolveElement.ts` | a DOM root + (x,y) | `elementFromPoint(root, x, y)` → `{selector, role, text, bbox}`; **pure**, no global `document` |
| element chip | `resolveElement` output | the `selector` + `role` + truncated visible `text` |
| chat attachment | the bundle + a thumbnail | the still + the chip on the sent message |

The capture is wired into [`ReviewPage.vue`](video-review.md), and a flag in
`FlagDetail.vue` carries the optional `point` + `element` — so an existing
`/review` flag *becomes* a spatial attachment rather than a parallel type
(epic shared decision 5).

## One resolver, two roots

`elementFromPoint(root, x, y)` is the same pure function over either DOM:

- **Live page DOM** (`document`) on the run surface.
- **rrweb `Replayer` iframe** (`iframe.contentDocument`) in a recorded/review
  context — the reconstructed-DOM substrate the bug-report capture already
  builds.

The review surface mounts both. `ReplayFrame.vue` wraps an rrweb `Replayer`
(the same lazy-`import('rrweb')` + `new Replayer(events, { root })` setup
`BugReportModal.vue` uses), pauses on the last frame, and overlays a
`SpatialPicker` rooted at the Replayer iframe's `contentDocument`. When the
reviewed media carries recorded rrweb events (`DataSource.videoEvents` returns
≥ 2 events), [`ReviewPage.vue`](video-review.md) renders the `ReplayFrame` —
the **real reconstructed UI**, so a click resolves a real app control
(`intent-btn-run`, role `button`) — instead of the opaque `<video>`; without a
recorded session it keeps the live-`<video>` + transparent-overlay path. The
recording's intrinsic viewport (the rrweb `Meta` width/height) is the iframe's
own pixel space, into which `SpatialPicker` maps the click before resolving;
the live page rect only scales the *rendered* overlay (`scaleReplayToFit`).

Element identity prefers a `data-testid` ancestor — the app testids everything
(`intent-btn-*`, `chat-row-*`, `composer-*`), so a testid is the most stable
handle — and falls back to a structural `:nth-of-type` path from the body. The
`bbox` is always recorded so a downstream consumer (and the oracle) can see
*where*, not just *what*.

## Wire path

"Ask" calls `DataSource.offpath(sessionId, input, visual?)` — the existing
read-only off-path `converse` RPC (`runstatus.session.offpath`) extended with an
**optional** `visual` param. `LiveDataSource.visualParams` maps the
`VisualBundle` into the exact wire shape `host.VisualAmbient` expects (flattening
`element.bbox` into a positional `[x, y, w, h]`); the server lifts it onto the
turn ctx with `host.WithVisualAmbient` and records it as `input.visual`. No new
route; the param is optional, so a chat with no point is byte-identical to
today.

## What it costs, honestly

- **Resolution rides on the reconstructed DOM.** `elementFromPoint` returns the
  *topmost* node; an absolutely-positioned overlay can resolve to itself. The
  recorded `bbox` + the `data-testid` ancestor chain let a reviewer (and the
  oracle) see the ambiguity.
- **rrweb masking redacts text.** Masked nodes resolve fine but their `text` is
  the masked form — correct for privacy; the selector + role still carry signal.
- **Frame ↔ DOM scaling.** The click is in *rendered-frame* pixels; the picker
  maps it through the frame's natural size + rendered rect before resolving.

## Tests

Web surface — Vitest + Playwright, never the Go `CapturedIO` harness, oracle
**stubbed** (no LLM, per CLAUDE.md):

- `tests/unit/resolveElement.test.ts` — a point over a `data-testid` node
  resolves to that selector + role + text + bbox; a nested point prefers the
  nearest `data-testid` ancestor; a bare node falls back to a structural path.
- `tests/unit/SpatialPicker.test.ts` — click emits the right `point`; the bundle
  carries the resolved element.
- `tests/unit/converse-visual-attachment.test.ts` — the off-path call ships the
  `visual` bundle.
- `tests/playwright/spatial-capture.spec.ts` — drive `kitsoki web` against a
  fixture frame (the live-`<video>` path), click a known element, **Ask**,
  assert `session.offpath` fired with the `visual` bundle.
- `tests/playwright/spatial-replay-resolve.spec.ts` — the **replay-frame**
  path: `videoEvents` returns the checked-in rrweb fixture
  (`tests/fixtures/spatial-replay.rrweb.json`, recorded at 1280×720),
  `ReviewPage` mounts `ReplayFrame`, a click over the reconstructed Run button
  resolves `[data-testid="intent-btn-run"]` (role `button`) — proving
  resolution against the reconstructed DOM, not the `<video>`.

## Non-goals

- **Unresolved external media semantics** — generic HTML/rrweb/image artifacts
  can now carry semantic sidecars and selector-resolved fields, but Kitsoki still
  does not infer producer-specific object schemas on its own. A producer names
  the fields it wants addressable; otherwise the picker falls back to DOM nodes
  or pixel regions.
- **A web-tier write path** — the chat is the read-only off-path `converse`;
  guidance never edits source from a click (shared decision 1).
