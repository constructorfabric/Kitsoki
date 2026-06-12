# The VS Code extension

The `tools/vscode-kitsoki/` extension puts kitsoki's **web UI inside the editor**:
chat in the activity-bar sidebar, the live trace + state diagram in a bottom
panel, themed to match VS Code, bound to the workspace's `stories/` and
`.kitsoki/`. It is a **third head** on the orchestrator body — the same Vue SPA
the [browser web UI](web-ui.md) serves, relayed into a webview instead of a
browser tab, driving the same `kitsoki web` backend over the same JSON-RPC/SSE
protocol. The orchestrator, its method set (`internal/runstatus/server/server.go`),
and the SSE contract are **unchanged**: every interpretive decision still lands
in the trace byte-for-byte, because it is the same process the browser talks to.

> This is **not** the inverse [`/ide`](README.md#editor-awareness-ide) work
> ([`hosts.md`](../architecture/hosts.md#hostide--editor-awareness)), where the
> terminal kitsoki dials *out* to a running editor to rent its capabilities. Here
> the editor *hosts* kitsoki's own UI. The two are complementary and share no code.

*Audience: operators running kitsoki from inside VS Code, and contributors on the
embed/relay/demo plumbing. The shared SPA, JSON-RPC method set, and trace
rendering are documented once in [`web-ui.md`](web-ui.md) — this page covers only
what is unique to the editor embed.*

## Layout

```
VS Code window
┌──────────┬───────────────────────────────────────────────┐
│ Activity │  editor: your story YAML / code                │
│   bar    │                                                │
│  [≈]     ├───────────────────────────────────────────────┤
│ kitsoki  │  ╭ Kitsoki › Chat (sidebar WebviewView) ─────╮ │
│          │  │  story library · chat transcript · room    │ │
│          │  │  view · trace + state diagram (one SPA)     │ │
│          │  ╰────────────────────────────────────────────╯ │
├──────────┴───────────────────────────────────────────────┤
│ Panel  [ Kitsoki Trace ]   a second SPA instance          │
└───────────────────────────────────────────────────────────┘
```

- **Sidebar chat** — a `WebviewView` (`kitsoki.chat`) in a custom activity-bar
  `viewsContainer` (`kitsoki`). It loads the full SPA, so the story library, chat,
  room view, and the per-session trace/diagram all render in it.
- **Bottom Trace panel** — a second `WebviewView` (`kitsoki.trace`) in a `panel`
  `viewsContainer` (`kitsokiPanel`). It loads the *same* bundle and shares the one
  backend (and therefore the session store).

Both views are registered by one `KitsokiViewProvider`
(`tools/vscode-kitsoki/src/webview.ts`) and declared under `contributes` in
`tools/vscode-kitsoki/package.json`. Commands `Kitsoki: Open Chat`,
`Open Trace`, and `Restart Backend` are contributed in the `Kitsoki` category
(`tools/vscode-kitsoki/src/extension.ts`).

## The transport seam — where the embed plugs in

The proposal assumed the seam was the SPA's `createDataSource()` factory. It is
**not**: ~14 stores and components construct `new LiveSource("/")` *directly*
(`App.vue`, `stores/{run,meta,inbox}.ts`, `MetaOverlay.vue`, `InboxPanel.vue`,
`AnnotateButton.vue`, …), so swapping a factory would miss most of them. There is
**no `BridgeSource`**.

Instead the embed bridges one layer lower, at the **transport** — the single
choke point every backend call funnels through. An injected `RpcTransport`
interface (`tools/runstatus/src/transport/transport.ts`) has exactly three
primitives:

| Method | Replaces | Used by |
|---|---|---|
| `call()` | `fetch(${base}rpc)` | every request/response RPC (`JsonRpcClient.post`) |
| `openEventStream()` | `new EventSource(${base}rpc/events│notifications│questions)` | per-session trace SSE, notifications, questions |
| `postEventStream()` | POST-then-SSE `fetch` | `LiveSource.turnStream` / `metaStream` |

Two implementations satisfy it:

- **`HttpTransport`** (same file) — the production browser transport. The exact
  `fetch`/`EventSource` bodies that previously lived inline in `JsonRpcClient` and
  `LiveSource` were lifted here **verbatim**, reconnect/backfill/backoff (the
  `[250, 500, 1000, 2000, 5000]` schedule) preserved. `JsonRpcClient` and
  `LiveSource` now delegate to an injected transport with **identical public
  signatures** — no store or component changed.
- **`BridgeTransport`** (`tools/runstatus/src/transport/bridge-transport.ts`) —
  the webview transport. Each wire op rides a `postMessage` envelope to the
  extension host.

`createTransport(base)` (in `transport.ts`) picks the implementation:
`acquireVsCodeApi` present ⇒ `BridgeTransport`, else `HttpTransport`. Every
existing `new LiveSource("/")` call site transparently bridges in the editor with
**zero call-site edits**.

> **Singleton, by necessity.** The SPA constructs ~15 `LiveSource`/`JsonRpcClient`
> instances, so `createTransport()` runs many times in one webview.
> `acquireVsCodeApi()` may be called **only once** per webview — so
> `createTransport()` returns a process-**singleton** `BridgeTransport`
> (`getSharedBridgeTransport()`). One `acquireVsCodeApi`, one `postMessage`
> listener, one monotonic id space. (Calling it ~15× was the original
> webview-blank bug.) Because all clients share one transport,
> `BridgeTransport.call()` **mints its own wire id** and ignores the
> caller-supplied one — otherwise two clients each starting at `id=1` would
> cross-resolve each other's replies.

### The postMessage envelope protocol

The webview cannot reach the cross-origin `http://127.0.0.1:PORT` backend from a
`vscode-webview://` document, so the **extension host holds the only HTTP/SSE
connection** and relays. The host owns reconnect; the webview side never backs
off (a closed stream is the host's to revive).

```
 webview                          extension host                 kitsoki web
 BridgeTransport                  Relay (relay.ts)               (Go server)
 ───────────────                  ──────────────                 ───────────
 call(method,params) ─{t:call}──▶ POST /rpc ─────────────────────▶ Orchestrator
                     ◀{t:call-ok}─ ◀──── JSON-RPC result ──────────
                     ◀{t:call-err}─ ◀──── JSON-RPC / HTTP error ────

 openEventStream() ─{t:evt-open}─▶ GET /rpc/events (Node fetch SSE,
   onMessage ◀{t:evt-msg}─────────  ◀═ raw `data:` frame string ═══  host-owned
   onError   ◀{t:evt-err}─────────  ◀═ error → host reconnects ═════  backoff)
                  ─{t:evt-close}──▶ abort the host EventSource

 postEventStream() ─{t:post-open}▶ POST /rpc/turn-stream (SSE)
   onFrame ◀{t:post-frame}────────  ◀═ intermediate frame ═════════
   resolve ◀{t:post-done}─────────  ◀═ {type:"done"} sentinel ═════
   reject  ◀{t:post-err}──────────  ◀═ {type:"error"} sentinel ════
```

The discriminant is `t`; `id` is a monotonic int minted in the webview and echoed
on every reply for correlation. The wire contract is defined once on both ends and
guarded by a real `bridge↔relay` integration test (the host emits exactly what the
webview's `BridgeTransport` expects). Key fidelity detail: `evt-msg` carries the
**raw SSE `data:` string** — the data layer (`JsonRpcClient.subscribe` /
`LiveSource.subscribe*`) `JSON.parse`s it, exactly as `HttpTransport` passes
`EventSource` `ev.data` through — so the bridge is byte-transparent to the layer
above it.

- **Webview side:** `BridgeTransport` (`bridge-transport.ts`) — correlates pending
  calls / open streams / pending posts by `id`, throws `JsonRpcError` on
  `call-err`, applies the same `reduce()` terminal-frame logic as `HttpTransport`.
- **Host side:** `Relay` (`tools/vscode-kitsoki/src/relay.ts`) — a Node
  `fetch`/SSE relay, deliberately free of any `vscode` import so it is unit-tested
  against a stub HTTP server. It owns the reconnect backoff for GET-SSE channels
  and parses the `{type:"done"|"error"}` sentinels for POST-SSE channels.

## Backend lifecycle and free-port allocation

The extension owns one `kitsoki web` child per workspace
(`tools/vscode-kitsoki/src/backend.ts`), spawned on the first webview resolve and
shared by both views.

> **Free-port allocation in the extension (no backend change).** `kitsoki web`
> prints the *requested* addr, not the resolved one, so `--addr :0` is
> unparseable. Rather than change the Go server, the **extension** allocates a
> free port in Node (`net.createServer().listen(0)` → read `.address().port` →
> close), then spawns `kitsoki web --addr 127.0.0.1:<port>`. `Backend.start()`
> health-polls `GET /` until the server answers before any webview RPC resolves —
> readiness is asserted, never slept. The port is unique per run, so parallel
> sessions (and parallel e2e runs) never collide. This keeps "Backend: none" true.

Posture flags `--flow`, `--host-cassette`, `--stories-dir`, and the binary path
are read from extension settings (`kitsoki.flow`, `kitsoki.hostCassette`,
`kitsoki.storiesDir`, `kitsoki.binaryPath`) and passed through at spawn — this is
how the deterministic no-LLM demo posture (below) reaches the editor. Child
stdout/stderr stream to the `Kitsoki` `OutputChannel`; the child is killed on
`deactivate()` and via `Kitsoki: Restart Backend`.

## Theming

The webview inherits VS Code's theme automatically through the `--vscode-*` CSS
custom properties and `vscode-light`/`vscode-dark` body classes. A thin,
**webview-only** theme shim (`THEME_SHIM` in `webview.ts`) maps the SPA's tokens to
`var(--vscode-editor-background|foreground|focusBorder)` so a theme switch reflows
instantly with no extension round-trip. It is injected **only** by the webview, so
the browser SPA palette is untouched.

The shim also re-themes the agent room-view "paper" card: the SPA renders it as a
deliberate **light** card (a sheet of paper on a dark chat desk — its intended web
look), but against VS Code's dark editor chrome a white card reads as *unthemed*.
The shim darkens that card (and its kv keys, prose, headings, markdown table,
inline code) to the editor surface for the embed only — fixing the appearance for
the editor without altering the web UI. (Per `tools/runstatus/CLAUDE.md`, this is a
presentation shim, never a trace fix.)

### CSP

The webview loads the singlefile SPA via a strict per-resolve CSP
(`webview.ts:renderHtml`):

```
default-src 'none'; script-src 'nonce-<N>'; style-src 'unsafe-inline';
img-src ${cspSource} data: blob:; font-src ${cspSource}
```

A nonce is stamped onto every inline `<script>` at resolve time. **`style-src` uses
`'unsafe-inline'` alone — no nonce.** Vue injects runtime `<style>` elements with
no nonce, and *a nonce in `style-src` makes the browser ignore `'unsafe-inline'`*,
which would refuse every injected style and strip the UI. Inline styles cannot
execute code, so this is the safe, standard webview posture; the script nonce stays
strict. The SPA is same-document (singlefile, no network), so `connect-src` stays
`'none'`.

## The demo + de-risk pipeline — one spec, two modes

The extension is demoable exactly the way the web UI is
([`kitsoki-ui-demo`](../skills/kitsoki-ui-demo/SKILL.md)), but the frame is the
**whole editor** (activity bar, sidebar, editor pane, trace panel), not just the
embedded SPA. One Playwright spec serves both roles — the worked reference is
**`tools/vscode-kitsoki/tests/vscode-tour.e2e.spec.ts`**, the `_electron` analog of
the web tour's `agent-actions-video.spec.ts`.

`KITSOKI_VSCODE_PACE` gates the two modes (mirroring `WEB_CHAT_PACE`):

| Mode | `PACE` | What runs | Make target |
|---|---|---|---|
| **fast / assert** | `0` (default) | every critical-path beat is a hard assertion; no dwells, no recording. The CI / **de-risk gate**. | `make vscode-e2e-fast` |
| **record** | `≥1` | the *same* asserted beats + per-beat dwells + `recordVideo` + an in-webview narration tour + the editor-pane beats; emits the MP4. | `make vscode-e2e` |

The recorder only **adds** pacing on top of the path the gate proves — it cannot
drift from what the gate asserts. **Validate green at `PACE=0` before recording.**

**Determinism (no LLM, ever).** The spawned backend runs
`kitsoki web --flow stories/weather-report/flows/tour.yaml
--stories-dir stories/weather-report`; the flow's `starlark_http_cassette` replays
*all* HTTP (geocode + forecast). No model, no socket. VS Code is pinned to
**1.96.4** (`@vscode/test-electron`, cached under `.vscode-test/`), with throwaway
user-data + extension dirs, a fixed window, and all `VSCODE_*` env stripped (these
launch facts live in `tools/vscode-kitsoki/tests/_helpers/launch.ts`).

**Two beat kinds, one clock.** A *webview beat* descends the two-iframe webview
guest (`iframe.webview.ready >>> iframe[title]`, the proven 1.96.4 chain in
`launch.ts:webviewFrame`) and drives the SPA by its existing `data-testid`s; it
**reuses the web tour manifest** (`WEATHER_REPORT_TOUR_STEPS`) injected via
`window.__startTourWithSteps`, asserting each popover title against the manifest (a
drift guard — the recording can't diverge from the live overlay). An *editor beat*
(open the story's `app.yaml`, open the Trace panel) is driven on the outer
workbench page from a thin in-spec `EDITOR_BEATS` manifest. Both advance one
`ChapterRecorder` clock, so the chapter sidecar spans the whole editor tour.

**Critical path asserted beat-by-beat** (and the scenarios the QA gate checks the
video against): (a) the Kitsoki activity-bar view opens and shows the themed story
library; (b) the story's `app.yaml` opens in the editor — code + kitsoki in one
window (record only); (c) a session starts (`current-state = lobby`); (d) a turn is
driven and state advances (`forecast` → `current-state = report`, the "Tokyo,
Japan" forecast renders — proving the cassette replay ran end-to-end through
bundle → CSP → BridgeTransport → relay → backend); (e) the trace surfaces render
(`trace-diagram` + `trace-timeline` with a `host.starlark.run` row); (f) the bottom
Trace panel opens (record only).

In record mode `app.close()` flushes the Playwright `.webm`, an in-spec transcode
emits a faststart H.264 MP4 to `.artifacts/vscode-tour/vscode-tour.mp4` with a
`*.chapters.json` sidecar and numbered `NN-<beat>.png` frames. This ships as the
**"full-editor" mode** of [`kitsoki-ui-demo`](../skills/kitsoki-ui-demo/SKILL.md);
the [`kitsoki-ui-qa`](../skills/kitsoki-ui-qa/SKILL.md) vision gate validates the
result (proven `pass`, 6/6 scenarios) using the same frames + chapter sidecar.

## Build and run

The SPA builds to a single inlined `index.html`
(`vite-plugin-singlefile`); the extension build copies that artifact into
`tools/vscode-kitsoki/media/spa/index.html`, the same `make web` staging the Go
embed uses. `make vscode-e2e-fast` does the full chain: build the SPA + stage it,
build the binary, build the extension bundle (esbuild), then run the gate.

> **macOS: `go build` the binary, never `cp` it.** Copying `./kitsoki` to
> `bin/kitsoki` invalidates the ad-hoc Mach-O signature, so Gatekeeper SIGKILLs the
> spawned child (exit 137 → "backend exited before becoming healthy"). Build
> directly to the destination:
> ```
> go build -o bin/kitsoki ./cmd/kitsoki
> ```
> The make targets do exactly this.

Manual run (outside the e2e harness): build `bin/kitsoki`, build the extension
(`cd tools/vscode-kitsoki && pnpm install && pnpm build`), point
`kitsoki.binaryPath` at it, open the Kitsoki activity-bar view. The chat sidebar
spawns the backend and renders the story library.

Tests:

```
cd tools/vscode-kitsoki && pnpm test        # relay unit + bridge↔relay integration
cd tools/runstatus      && pnpm test        # transport/data-layer unit (incl. BridgeTransport)
make vscode-e2e-fast                         # the no-LLM, no-editor-flake de-risk gate
```

## What we lose, honestly

- **Desktop-only.** The web extension host (vscode.dev / github.dev) runs in a
  browser WebWorker with no `child_process`, so it cannot spawn the Go backend. A
  hosted `kitsoki web` could serve vscode.dev later; out of scope.
- **A relay hop per call.** `postMessage` adds a serialise/await per RPC versus the
  browser's direct `fetch` — imperceptible against oracle-bound turn latency.
- **No deep editor wiring.** Selection-aware prompts, open-file actions, and
  diagnostics belong to the inverse [`/ide`](README.md#editor-awareness-ide)
  substrate; this extension only *embeds* the UI. Composing the two is a deliberate
  follow-up.
