# runstatus — kitsoki web UI

Vue 3 SPA served by `kitsoki web`. Displays the live trace of a running kitsoki
session: the state timeline, oracle calls, world diffs, host invocations, and
the rendered room view (typed view elements).

## Architecture

```
kitsoki web (Go server)                    browser
  ├─ GET /              → embedded SPA     Vue 3 + Pinia + Vue Router
  ├─ GET /rpc/events    → SSE event stream ← TraceTimeline, StateView
  ├─ GET /rpc           → JSON-RPC         ← stores
  └─ GET /artifact/{id} → binary stream    ← ViewElement (media kind)
```

The SPA selects its data source at startup via `createDataSource()`
(`src/data/source.ts`):

- **`LiveSource`** — connects to the running server's SSE + RPC endpoints.
- **`SnapshotSource`** — reads from `window.__KITSOKI_SNAPSHOT__` (the static
  `export-status` export). Both sources implement the same `DataSource` interface,
  including `artifactUrl(handle)`, so the rest of the UI is unaware of the mode.

## View elements

Room views are rendered by `ViewElement.vue`. The supported `Kind` values are:

| Kind | Rendered as |
|---|---|
| `prose` | Markdown-rendered paragraph |
| `heading` | `<h3>` block |
| `code` | Fenced code block |
| `list` | Unordered list |
| `kv` | Key/value table |
| `banner` | Highlighted banner |
| `media` | Inline media player / embed (see below) |

### `media` view element

A `media` element references an artifact by its opaque handle (`id` from an
`artifact.emitted` trace event). The renderer dispatches on `Mime`:

| MIME family | Rendered as |
|---|---|
| `video/*` | `<video controls preload="metadata">` with HTTP Range support for seeking |
| `image/*` | `<img loading="lazy">` |
| `application/pdf` | sandboxed `<iframe>` |
| `text/html` | sandboxed `<iframe sandbox>` (e.g. slidey interactive export) |
| other | `<a>` download link |

The `src` / `href` for all cases comes from `DataSource.artifactUrl(handle)`.
In live mode this is `/artifact/{id}`; in snapshot mode it is a relative sidecar
path. An optional `Caption` field renders below the element.

## `/artifact/{id}` route

`GET /artifact/{id}` is the only binary-serving route on the `kitsoki web`
server. It:

1. Looks up the artifact `id` in the session's recorded `artifact.emitted`
   datapoints (`ArtifactResolver` in `internal/runstatus/server/`).
2. Validates the resolved path is under the configured artifacts root
   (path-traversal guard; returns 404 for unknown or escaping ids).
3. Streams the file via `http.ServeContent` — correct `Content-Type`, `ETag`,
   and HTTP `Range` headers (required for video seeking).

Unknown handles, ids that escape the root, and requests with no `ArtifactResolver`
wired (e.g. tests without a session) all return 404.

Implementation: `internal/runstatus/server/server.go` (`handleArtifact`),
`internal/runstatus/server/provider.go` (`ArtifactResolver`).

## Development

```sh
pnpm install
pnpm dev          # vite dev server on :5173
pnpm test         # vitest unit tests
pnpm test:e2e     # Playwright tests (requires a running kitsoki web instance)
pnpm build        # production bundle → dist/
```

The Go server embeds `dist/` at build time via `//go:embed`.
Run `make build` from the repo root to rebuild the Go binary after changing
frontend code.
