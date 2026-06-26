# Slidey annotate continuity QA

Date: 2026-06-26

## Bug

Opening Annotate on a rendered Slidey deck mounted a fresh annotation iframe at
the deck default position. If the operator was viewing a later slide or reveal
transition, the annotation overlay appeared on slide 1 instead of the exact
visual state they were reviewing.

After the edit completed, the re-rendered main deck iframe had the same problem:
the new content-addressed deck handle booted at slide 1, so the requested change
was not immediately visible at the slide/transition the operator just edited.

## Fix

- Kitsoki now stores `embed:view.step` alongside the existing opaque
  `embed:view.scope`.
- Kitsoki sends `{ type: "embed:annotate", enabled: true, scope, step }` to the
  live annotation iframe.
- Slidey now publishes `step` with every `embed:view` message.
- Slidey now honors `scope + step` on `embed:annotate` by navigating the fresh
  annotation iframe before drawing pick markers.
- Kitsoki now appends `?scene=<scope>&step=<step>` to the main slideshow iframe
  URL when it has a last viewed deck position, so a newly rendered handle opens
  where the operator was looking.
- Slidey now consumes `scene + step` query params on initial load for bundled,
  `?spec=`, and CLI workspace viewer paths.

## Regression Coverage

- `tools/runstatus/tests/unit/embed-view.test.ts`
  - parses `embed:view.step`
  - sends `scope + step` in `embed:annotate`
- `tools/runstatus/tests/unit/artifact-annotator-embed.test.ts`
  - verifies the live deck iframe receives the continuity payload
- `tools/runstatus/tests/unit/view-element-scene-dispatch.test.ts`
  - verifies the media card preserves scene + transition when opening Annotate
  - verifies a re-rendered deck handle opens at the last viewed scene + step
- `/Users/brad/code/slidey/test/embed-annotate.test.js`
  - verifies Slidey restores requested scene + transition before drawing markers
- `/Users/brad/code/slidey/test/useDeck-embed-scene.test.js`
  - verifies normal embedded navigation publishes both scene and step
  - verifies the initial-view query parser preserves scene + step on boot

## Visual MCP QA

Commands run:

```sh
GOCACHE=/private/tmp/kitsoki-go-cache go run ./cmd/kitsoki mcp-test \
  --stories-dir ./stories \
  --timeout 20s \
  --server-arg mcp \
  --server-arg --stories-dir --server-arg ./stories \
  --server-arg --db --server-arg .artifacts/visual-mcp-smoke/sessions.db
```

```sh
GOCACHE=/private/tmp/kitsoki-go-cache go run ./cmd/kitsoki mcp-test \
  --stories-dir ./stories \
  --timeout 20s \
  --server-arg mcp \
  --server-arg --stories-dir --server-arg ./stories \
  --server-arg --db --server-arg .artifacts/visual-mcp-smoke/sessions.db \
  --calls '[{"tool":"session.new","args":{"story_path":"testdata/apps/cloak/app.yaml","harness":"replay","cassette":"testdata/apps/cloak/recording.yaml","trace":".artifacts/visual-mcp-smoke/cloak-trace-escalated.jsonl","key":"cloak2"}},{"tool":"visual.open","args":{"kind":"web","handle":"cloak2","viewport":{"width":1280,"height":800}},"save":{"visual_handle":"structuredContent.visual_handle"}},{"tool":"visual.observe","args":{"visual_handle":"${visual_handle}","include_semantic":true}}]'
```

Results:

- MCP server initialized over stdio with visual tools registered:
  `visual.open`, `visual.observe`, `visual.snapshot`, `visual.act`,
  `visual.diff`, `visual.git_diff`, `visual.record`.
- Replay/no-LLM session opened successfully.
- `visual.open` returned a web visual handle.
- `visual.observe(include_semantic:true)` returned a real webshot-backed semantic
  observation with action handles and bounding boxes after rerunning with
  loopback/bind permissions.

## Tooling Gaps Found

- Sandboxed MCP visual semantic observation cannot bind its loopback webshot
  server (`listen tcp 127.0.0.1:0: bind: operation not permitted`). The tool
  returns JSON state but semantic/pixel QA requires escalation in this
  environment.
- `mcp-test` defaulted to the shared sessions DB, which was read-only here. A
  repo-local DB under `.artifacts/visual-mcp-smoke/` was required.
- The generic MCP smoke did not directly prove Slidey deck annotation
  continuity; it can prove visual tool availability and webshot semantics over a
  replay session, but a first-class no-LLM fixture for a live Slidey deck
  annotation continuity path would be better.
- Happy DOM component tests still try to fetch iframe URLs in the background,
  producing noisy EPERM stderr even when assertions pass. The tests should use a
  fixture/stub iframe loader or isolate the postMessage contract without
  triggering network fetch.
