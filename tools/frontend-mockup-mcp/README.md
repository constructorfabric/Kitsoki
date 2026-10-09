# Frontend Mockup MCP

`tools/frontend-mockup-mcp` is a focused stdio MCP server for browser-based
mockup, wireframe, and visual QA work. It deliberately avoids frontend
debugging surfaces such as console logs, network traces, source maps, and
backend tracing.

The server exposes:

- `mockup_navigate` to open a URL at a review viewport.
- `mockup_visual_qa` to return a screenshot plus compact design context.
- `mockup_dom` to return a token-efficient DOM/layout/accessibility summary.
- `mockup_stagehand_observe`, `mockup_stagehand_extract`, and
  `mockup_stagehand_act` for Stagehand-assisted page understanding and actions.
- `mockup_tour_start`, `mockup_tour_step`, and `mockup_tour_export` to
  interactively author a deterministic, source-first tour that can be replayed
  later to render a demo video.
- `mockup_demo_doctor` and `mockup_record_tour` (below) to validate and
  refresh a mockup → slidey deck demo packet, driven by a demo manifest.
- `mockup_create` (below) to generate a self-contained mockup from a scenario
  spec instead of hand-editing HTML.
- `mockup_close` to end the local browser session.

Stagehand LLM calls are routed through `kitsoki agent ask` using
`KITSOKI_MOCKUP_AGENT` and are only used by the explicit Stagehand tools. The
visual QA and DOM tools are deterministic browser reads.

Tour export writes a JSON source file, a Playwright replay spec, and a small
HTML storyboard preview. Commit the reviewed source JSON/spec only when the tour
is intended to become durable; keep intermediate renders under `.artifacts/`.

## demo-doctor / record-tour

These two commands (also exposed as MCP tools `mockup_demo_doctor` and
`mockup_record_tour`) implement the mockup → rrweb → slidey demo tooling
contract. Neither needs an active browser session; they only shell out to
`node`, `fs`, and the target `slidey` checkout. The frozen interface contract
they implement lives in `~/code/POG/.context/mockup-demo-tooling-contract.md`
(§4 manifest, §5 demo-doctor, §6 record-tour) — that document is the source
of truth for the schema; this README is a pointer, not a copy.

### Demo manifest (`*.demo.json`, version 1)

All relative paths resolve against the manifest file's directory:

```json
{
  "version": 1,
  "mockup": "portal-mockup.html",
  "deck": "portal-demo.slidey.json",
  "deckClipsRoot": "demo-clips",
  "slidey": "~/code/slidey",
  "airSec": 1.5,
  "target": { "launch": "python3 -m http.server 7791 --bind 127.0.0.1", "addr": "127.0.0.1:7791", "readyPath": "/portal-mockup.html", "cwd": ".", "timeoutMs": 30000 },
  "viewport": { "width": 1600, "height": 900 },
  "postersDir": "../.artifacts/demo/posters",
  "tours": [
    { "tour": "portal-tour-orientation.json", "out": "../.artifacts/demo/portal-orientation.rrweb.json" }
  ]
}
```

- `slidey` resolution order: the manifest's `slidey` field (`~` expanded,
  resolved relative to the manifest dir if not absolute) → `SLIDEY_SRC` env →
  `~/code/slidey`.
- Tour → deck-scene mapping is **derived**, not declared: a deck scene
  matches a tour when the scene's `rrweb` path (resolved via the deck's
  folder, following symlinks) realpaths to the tour's `out`. Tours with no
  matching scene simply don't participate in the chapters check or in
  auto-dwell sizing.
- The mockup must declare its states as `const states = { key: {...}, ... }`
  — demo-doctor's states check parses this pragmatically (bracket/string
  depth tracking, not a real JS parser), so keep it a flat top-level object
  literal.

### `demo-doctor`

```
node scripts/demo-doctor.mjs <manifest.demo.json> [--json]
```

Exits 0 iff all five checks pass; prints one `ok`/`FAIL` line per check
(`--json` emits a machine report: `{ ok, manifest, checks: [{name, ok,
detail}] }`).

1. **states** — every `setStep('X')` in every tour's `before` evals names a
   states key.
2. **deck paths** — every deck scene's `rrweb` path stays inside the deck's
   folder once normalized (symlink traversal allowed; a literal `../` escape
   fails).
3. **freshness** — each tour's clip exists and is newer than both its tour
   file and the mockup.
4. **chapters** — for each matched scene/tour pair, the clip's
   `.chapters.json` sidecar ids (in order) match the deck scene's narration
   `chapter` ids (in order).
5. **estimate** — `node <slidey>/src/index.js <deck> --estimate --json`
   reports zero flags, top-level and per-scene. If that invocation errors,
   exits non-zero, or doesn't parse as the documented JSON shape (including
   the case where `--json` isn't implemented yet and slidey silently falls
   back to its human-readable table), this check FAILs with a message
   starting `slidey --estimate --json unavailable: ...` instead of crashing.

### `record-tour`

```
node scripts/record-tour.mjs <manifest.demo.json>
```

The closed loop (contract §6): run `--estimate --json` once to get per-cue
`audioSec`, compute `dwellMs = ceil((audioSec + airSec) * 1000)` for every
tour step whose id matches a chapter of its matched scene (steps without a
matching cue keep their authored dwell); write a generated tour-set JSON
(slidey's `capture --tours` schema) into `<clips dir>/tour-set.generated.json`
(the parent of `postersDir`, or the manifest dir if unset) and run
`slidey capture --tours <tour-set>`; re-run `--estimate --json` and fail
loudly on any flag; finish by running demo-doctor and propagating its
failure. Exits 0 with a JSON summary (`{ ok, tourSet, dwellOverrides,
doctor }`) on success.

The `slidey capture --tours` step can run for minutes (browser launch plus
one capture per tour). Its child stdout/stderr are streamed straight through
to `record-tour.mjs`'s own **stderr** as they're written, so a healthy run and
a hung one are distinguishable by watching stderr live; the process's stdout
stays reserved for the final JSON summary, so piping/parsing stdout (e.g. from
`mockup_record_tour`, or a subprocess caller) is unaffected.

The tour<->scene matching and slidey shellout helpers are shared between both
scripts (and the MCP tools) in `lib/demo-manifest.mjs`.

## `create-mockup`

```
node scripts/create-mockup.mjs <scenario.mockup.json> <out.html> [--manifest] [--renderer <renderer.js>]
```

(also exposed as MCP tool `mockup_create`) — contract §7 item 2. Generates a
fully self-contained mockup HTML from a **scenario spec**, killing the
"mockup states are hand-edited code" friction: five stable zones (rail /
intake / graph / inspector / timeline) with stable `data-testid`s, a
`const states = {...}` data block wrapped in refreshable
`<!-- mockup:states:begin/end -->` markers, and the
`window.storyboard.setStep(id)` contract the rest of this tool (and
demo-doctor's states check) already relies on.

### Scenario spec (`*.mockup.json`, version 1)

```json
{
  "version": 1,
  "title": "Portal control room",
  "tagline": "optional header subtitle",
  "zones": {
    "rail": { "heading": "Scenarios", "typeHeading": "Types", "scenarios": [["Name", "Sub"]], "types": ["segment", "persona"] },
    "intake": { "heading": "Intake", "fields": ["Decision", "Claim", "Constraint"] },
    "graph": { "projection": "graph-projection.json" },
    "inspector": { "heading": "Inspector", "evidenceHeading": "Evidence" },
    "timeline": { "heading": "Timeline" }
  },
  "states": {
    "open": {
      "group": "compliance",
      "rail": { "active": 0, "decision": "...", "detail": "..." },
      "intake": ["value 1", "value 2", "value 3"],
      "graph": { "title": "...", "sub": "...", "state": "<graph-projection state/graph id>" },
      "inspector": {
        "headline": "...", "meaning": "...",
        "metrics": [["label", "value"]],
        "evidence": [["green|red|", "text", "meta"]],
        "action": "..."
      },
      "timeline": { "steps": ["Step 1", "Step 2"], "done": 0 }
    }
  }
}
```

- `zones` declares each zone's static shape (headings, the rail's scenario/type
  lists, intake field labels, an optional `graph.projection` path resolved
  against the scenario file's directory). `states.<id>` declares each state's
  per-zone content -- the generated `render(key)` only ever touches
  `document.getElementById(id).textContent`/`.innerHTML` and
  `document.body.setAttribute(...)`, so it stays exercisable by a tiny
  in-memory DOM stub in tests without a real browser.
- `states.<id>.group` (optional) buckets states for `--manifest`'s one-tour
  -per-group output; defaults to the rail scenario name at `rail.active`, or
  `"tour"` if no rail is configured.
- Graph wiring: if `zones.graph.projection` is set, either pass `--renderer
  <renderer.js>` (embeds that file directly -- no slidey dependency; what
  tests use) or let it resolve a slidey checkout via the SAME chain as the
  demo manifest's `slidey` field (scenario's own `slidey` field → `SLIDEY_SRC`
  env → `~/code/slidey`) and shell out to that checkout's own
  `tools/graph-projection-sync.js` to inject the renderer + data blocks --
  keeping the renderer's source of truth in slidey, not duplicated here.
  Omitting `zones.graph.projection` entirely skips graph wiring; the mockup's
  `drawGraph()` becomes a no-op.

### `--manifest`

Co-emits a starter `*.demo.json` manifest (schema above), one starter tour
JSON per state group (each step's `id` is the state key, so
`window.storyboard.setStep('<key>')` before-evals and deck narration
`chapter` ids line up automatically), and a starter slidey deck -- with small
default dwells (2500ms; override with `--dwell-ms`) so a first capture is
fast. This makes `create-mockup` → `record-tour` → `demo-doctor` a closed
loop with no hand-editing: `demo-doctor`'s states/deck-paths checks pass the
moment `--manifest` is emitted, and freshness/chapters/estimate pass once
`record-tour.mjs` has captured clips against the real target.

Run `npm test` for the hermetic (no real slidey, no browser, no LLM) fixture
suite in `test/`.
