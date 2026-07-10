# Session Media Workbench

The session media workbench is the browser chat layout for working on a specific
artifact. When a room emits a media `ViewElement` such as an HTML mockup, Slidey
deck, video, image, or markdown-derived artifact, the operator can pin that
artifact beside the conversation instead of leaving every update embedded only in
the transcript.

## Behavior

- Each media element exposes a `Pin` affordance.
- Pinning opens a stable media pane in the live session view.
- The chat pane gets a persistent `Working on` context strip naming the pinned
  artifact, so the operator can always tell what the conversation is acting on
  even after the transcript scrolls.
- The selected media handle is suppressed from the transcript while pinned and
  replaced with a compact receipt, so the conversation keeps chronology without
  mounting duplicate embeds.
- The chat remains live beside the media pane: composer, streaming agent
  activity, routing chips, media annotations, and follow-up turns all continue to
  use the same session and trace.
- The observability pane behaves like devtools. It can show Graph or Trace, dock
  right, dock bottom, float in the window, or pop out to a separate browser
  window through the existing `?surface=graph` / `?surface=trace` entrypoints.
- The layout can switch between vertical and horizontal arrangements. Explicit
  splitter controls resize media/chat, chat/devtools, and bottom-docked devtools
  panes with pointer drag or keyboard arrows.
- Workbench layout preferences are saved in browser `localStorage`: orientation,
  devtools dock, active devtools tab, and splitter sizes.

When the trace/devtools pane is hidden, the workbench removes the devtools split
entirely. The pinned media pane and chat pane are the only remaining tracks, so
the chat expands into the space that Graph/Trace occupied.

The workbench is browser-only. The VS Code integration keeps its existing
single-surface decomposition: chat, trace, and graph remain separate dockable
webviews selected by `window.__KITSOKI_SURFACE`.

## Using Pinned Annotation

The common spatial-feedback flow is:

1. Open a live browser session (`/s/<session>/chat`) that has produced a media
   artifact.
2. Click `Pin` on the media toolbar. The artifact moves into the stable media
   pane and the transcript keeps a compact `Pinned in the workbench` receipt.
3. Click `Annotate` in the pinned pane.
4. Point at the artifact. For images and video frames, draw or click a region;
   for HTML and rrweb-backed frames, click a DOM element; for Slidey decks,
   click one of the producer-reported element markers.
5. Type the instruction for that spot and click `Send` or `Send & refine`.

`Send` records an anchored off-path note for read-only discussion. `Send &
refine` appears when the media element declares an `AnnotateIntent`; in that
case the picked anchor and the instruction dispatch the story's refine intent,
so the next turn can edit exactly the element you pointed at.

Video artifacts also expose `Open in review — flag a scene or moment →`. Use
that when you want the older `/review` workflow: flag a time range, click the
frame, then ask the read-only spatial oracle about that flagged spot.

## Implementation Notes

`ViewElement.vue` remains the one artifact renderer. The pin button dispatches a
`kitsoki:pin-media` browser event with the media handle; `InteractiveView.vue`
selects the matching media element from `store.chatEntries` and renders that same
`ViewElement` in the media pane.

The pinned pane renders the same `ViewElement` with its own `Pin` button hidden,
so all media affordances still work there: `Annotate`, video `Open in review`,
Slidey deck open links, and artifact-specific playback or iframe controls. The
annotation machinery is intentionally not tied to pinning; pinning only gives the
artifact a stable workspace beside the ongoing chat.

Video media uses the artifact poster endpoint when available. This keeps pinned
videos legible in screenshots, tours, and QA frames before the browser has played
or seeked the media.

The transcript suppression is intentionally handle-based. It only hides the
currently pinned artifact handle, and only in workbench mode. This preserves
other typed view elements and keeps older or alternate media visible unless the
operator selects them.

The splitter sizes are stored as percentages rather than pixel widths. That
keeps the workbench usable across window sizes while preserving the operator's
intent: media gets more room, chat gets more room, or devtools gets more room.

## Verification

Automated tests must stay no-LLM. The focused coverage lives in:

- `tools/runstatus/tests/unit/interactive-view-embed.test.ts`
- `tools/runstatus/tests/unit/view-element.test.ts`
- `tools/runstatus/tests/unit/chat-transcript.test.ts`

The feature demo is cataloged as `session-media-workbench` and should be recorded
with the deterministic mockup-video flow. Vision QA for the rendered MP4 should
use the feature plan and scenarios under `.context` or the feature catalog text
as the review target; do not add the vision QA gate to automated tests.
