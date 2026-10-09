# TUI: Browser UI for Kitsoki (Feature-Parity with the TUI)

**Status:** Draft v3. **Phases 1–4 shipped + documented** — the interactive web
UI is complete; full reference lives in **[`docs/tui/web-ui.md`](../tui/web-ui.md)**.
This proposal now only tracks the remaining design items (meta-mode, deeper
runtime unification); delete it once those land. `kitsoki web <app.yaml>` serves an interactive chat
surface (room render + live trace + state diagram) backed by a live
orchestrator, driveable by the **same** deterministic machinery as the rest of
kitsoki (flow `host_handlers`, host cassettes, warps, recordings) via a shared
`buildSessionRuntime` used by `run`, `web`, and the flow test rig. A Playwright
test drives the full PRD `happy_path` chat at MacBook resolution (1440×900 @2x),
asserting the state badge per scene and recording a video + per-scene
screenshots in `.artifacts/web-chat/`. Verified scene-by-scene visually.
**Composer free-text routing (WS-D D1, shipped):** the composer's RPC wiring
(`session.turn` → `Orchestrator.Turn`, the same call the TUI makes) was
already correct; the actual bug was a CLI-wide default
(`cmd/kitsoki/semantic_routing.go`) that silently forced the deterministic
semantic-routing stack OFF on every surface unless `--semantic-routing` was
passed, regardless of an app's own `routing.enabled: true` — see
`.context/dwf2-d1-findings.md` for the reproduction and fix. Remaining: full
meta-mode (off-path only today), and the optional deeper `run`/test-rig
runtime unification (the host-stub + cassette + harness + warp mechanisms are
already shared; `runCmd` construction now routes through
`buildSessionRuntime`).

_Historical:_ Phases 1–2 (serve host + live read surface + write RPCs). `kitsoki web <app.yaml>` (`cmd/kitsoki/web.go`) hosts a live
orchestrator and serves the runstatus SPA/RPC/SSE against it via
`server.LiveSession` + `server.NewWithSource` (`internal/runstatus/server/live.go`);
the browser can drive the session through `session.turn/submit/continue/offpath`
RPCs (`server.Driver` + `OrchestratorDriver`, `driver.go`). Phases 3–4 (frontend
room/input pane, e2e) are still open. Reframed around option A (in-process serve
host); free-text routing resolved as orchestrator-owned.
**Kind:**   tui (operator surface) — carries one **runtime** seam (the
`serve` host + write RPCs), called out under Impact. Single focused
proposal for the PoC; if the serve host grows, split that seam into a
`runtime` child.
**Epic:**   — standalone

## Why

The TUI is the only interactive runtime surface today. It works for
terminal-native users but excludes anyone who isn't comfortable in a
shell, makes demos awkward to share or record, and limits the surfaces
where stories can be embedded or observed. The read-only `runstatus`
server (`internal/runstatus/server/server.go`, started via
`kitsoki status serve` — `cmd/kitsoki/status_serve.go:40`) already
serves live run state to a browser, but it only *observes* a trace file;
you still need a terminal to *drive* the story.

## What changes

Add `kitsoki serve <app.yaml>` — a single process that constructs a live
`Orchestrator` (exactly as the TUI startup does) and hosts an HTTP server
**in the same process**. The browser drives the story by calling
orchestrator methods over new write RPCs, and observes it through the
existing `runstatus` snapshot/SSE read pane. One sentence: **the
`runstatus` SPA stays the read pane; the new serve host makes it
writable by giving its RPC layer a live orchestrator to call.**

**Architecture (option A, chosen).** The alternative — adding a write
back-channel to today's standalone `status serve` reader — fails because
that process has no `Orchestrator` in it (it just tails a `.jsonl`). Any
write path forces an orchestrator into the process anyway, at which point
the file-polling read path is redundant. So option A *is* the simple
form: one orchestrator, one HTTP host, reuse the snapshot/SSE plumbing
for reads, call the orchestrator directly for writes. `status serve`
stays unchanged for the "observe an external run" case.

## Impact

- **Code (new):** `cmd/kitsoki/serve.go` — the in-process serve command
  (mirror TUI startup: load app, build orchestrator, `NewSession`,
  `RunInitialOnEnter`, then serve HTTP).
- **Code (runtime seam):** `internal/runstatus/server/server.go` — add
  write RPC methods alongside the read ones (`runstatus.session.*`,
  dispatch at `:233`). These are thin: translate request → orchestrator
  call → serialize `TurnOutcome`. No business logic in the handler (keeps
  the SaaS-evolution door open).
- **Code (read side):** point the snapshot/SSE source at the live
  in-process event store instead of a polled file. The `Snapshot` type
  (`internal/runstatus/snapshot.go:22`) and SSE stream are reused as-is.
- **Rendering:** serialize the **typed** `app.View`
  (`internal/app/view_element.go:50`, element kinds at `:106`) to JSON
  and render it client-side — do **not** ship the ANSI string. The
  frontend grows HTML renderers for each element kind (prose, heading,
  code, list, kv, banner, choice, template).
- **Input:** new RPCs map to `Orchestrator.Turn` (free text — routes
  internally), `SubmitDirect` (choice/confirmation — intent + slots) and
  `ContinueTurn` (slot supplements). All already public and
  transport-agnostic (`internal/orchestrator/orchestrator.go:781,1426,1917`).
- **Frontend:** extend the existing Vite/TS SPA under `tools/runstatus/`
  (it already ships Playwright e2e + Vitest) — add the room/input pane;
  the trace/mermaid panes already exist.
- **Docs on ship:** `docs/tui/web-ui.md` (or `docs/web/`); cross-link
  from `docs/tui/README.md`.

## Mental model

The browser is a second head on the same body. The orchestrator is the
body and doesn't know or care which head drives it — the TUI and the web
UI both call `Turn`/`SubmitDirect` and render the `TurnOutcome` they get
back. The `runstatus` SPA already renders the read half (state, trace,
diagram); we're adding the write half to the same page.

## Layout

```
Read-only today (status serve):        Interactive (serve):
┌───────────────┬─────────────┐        ┌───────────────┬─────────────┐
│   mermaid     │   trace     │        │   room view   │   trace     │
│   diagram     │   events    │        │ (typed elems) │   events    │
│               │             │   ─▶   ├───────────────┤  (live SSE) │
│   (no input)  │             │        │ > input / ▸   │             │
└───────────────┴─────────────┘        │   choices     │  + mermaid  │
                                        └───────────────┴─────────────┘
```

## Rendering changes

The room pane renders the **typed `View` model**, not a pre-formatted
string. The server serializes `app.View` elements to JSON (they already
ride along in `Snapshot`); the SPA maps each element kind to an HTML
component:

| Element kind | Browser rendering |
|---|---|
| prose / heading / code / list / kv / banner | direct HTML (markdown for prose/code via a client md renderer) |
| choice | interactive widget → `SubmitDirect(intent, slots)` on pick |
| template | pongo2 is resolved **server-side** (the orchestrator already renders it); the browser receives resolved content |

Rule held from the template: layout is data, not `printf`. We never
hand-build HTML strings in Go — the server emits the typed model, the
client owns presentation. The TUI's `choice_widget.go` is Bubble
Tea-native and is **not** reused; the SPA gets its own choice widget
reading the same element definition.

## Input & commands

Browser interactions, not slash commands. The free-text→intent routing
that the TUI performs is **already inside the orchestrator** — `Turn`
runs the semantic → turn-cache → LLM tiers itself
(`internal/orchestrator/semantic.go`, `cache.go`). The web UI just calls
`Turn` and shows a spinner; it forgoes the TUI's live tier-transition
display (`routing_observer.go`) for v1.

| UI action | RPC | Orchestrator call |
|---|---|---|
| Type free-form text + submit | `runstatus.session.turn` | `Turn(ctx, sid, input)` |
| Click a choice / confirm | `runstatus.session.submit` | `SubmitDirect(ctx, sid, intent, slots)` |
| Supply missing slot(s) | `runstatus.session.continue` | `ContinueTurn(ctx, sid, slots)` |
| Ask off-path | `runstatus.session.offpath` | `AskOffPath(ctx, sid, input)` |
| Live state / trace / diagram | (existing read RPCs + SSE) | snapshot from live event store |

All write RPCs return the serialized `TurnOutcome`
(`internal/orchestrator/outcome.go:59` — Mode, View, NewState,
AllowedIntents). The SSE stream pushes trace/state updates so the read
panes stay live without the client re-polling.

## Testing

The terminal combined-I/O rule (slog+render interleaving) does **not**
apply here — there is no shared terminal. The bar for this surface is:

- **Go handler tests** — each write RPC: construct an orchestrator over a
  flow fixture (no real LLM — see memory: no-llm-tests), call the RPC,
  assert the returned `TurnOutcome` and the resulting event store state.
  Cover the validation-error path (bad intent / missing slot).
- **Playwright e2e** — reuse the existing harness under
  `tools/runstatus/tests/`. Drive a fixture story start-to-finish in a
  headless browser: render room → pick choice → free text → confirmation
  → reach a terminal state, asserting the trace pane updates live.
- **Vitest** — element-kind renderers and the choice widget in isolation.

## Tasks

```
## 1. Serve host (runtime seam) — SHIPPED
- [x] 1.1 `cmd/kitsoki/web.go` (`kitsoki web`, since `serve` is the MCP cmd):
          load app, build live Orchestrator, NewSession + RunInitialOnEnter,
          serve HTTP in-process
- [x] 1.2 Live in-process source: `server.Source` interface +
          `server.LiveSession` (mutex-wrapped JSONLSink, snapshots via
          `runstatus.FromSink`); `status serve` keeps the trace-file source.
          SSE stream reused; file polling kept only for `status serve`.
          Tests: `internal/runstatus/server/live_test.go` (incl. `-race`).

## 2. Write RPCs (thin handlers → orchestrator) — SHIPPED
- [x] 2.1 runstatus.session.turn      → Orchestrator.Turn
- [x] 2.2 runstatus.session.submit    → Orchestrator.SubmitDirect
- [x] 2.3 runstatus.session.continue  → Orchestrator.ContinueTurn
- [x] 2.4 runstatus.session.offpath   → Orchestrator.AskOffPath
- [x] 2.5 Serialize TurnOutcome to JSON (`server.turnResult`: typed_view +
          allowed_intents + rendered view). A guard rejection / missing slot
          rides back as `mode=rejected` / `mode=clarify` (a normal interpreted
          outcome), NOT a transport error; only infra failures are rpcErrors.
          Write RPCs are gated by `WithDriver` — `status serve` (no driver)
          returns codeReadOnly.
- [x] 2.6 Go handler tests over the cloak fixture, no LLM (`write_test.go`):
          submit-transitions (+ live header tracks the new state),
          submit-rejected, missing-intent, read-only-gating.

  **Bug fixed in flight:** the live header reported the pre-transition state
  because `turn.end` is stamped with the turn's STARTING state and the shared
  header derivation took "last state_path wins". Fixed in both
  `FromHistory` and `SessionHeaderFromTrace` to prefer the last `state_entered`
  (regression tests in `fromhistory_test.go` + `server/write_test.go`); this
  also corrects `kitsoki status serve` / `export-status` post-transition.

## 3. Frontend room + input pane (extend tools/runstatus SPA) — SHIPPED
- [x] 3.1 Typed-View element renderers (`ViewElement.vue`: prose/heading/code/
          list/kv/banner/choice) + a `session.view` read RPC for the first
          frame. NB: room views are pongo templates; the SERVER renders them to
          text (the browser never evaluates pongo), and `ChatTranscript.vue`
          formats that markdown — `CurrentView` was fixed to stop leaking raw
          `{{ … }}`.
- [x] 3.2 `InputBar.vue`: action buttons per allowed intent + a text composer
          bound to the intent's single free-text slot → `session.submit`
          (menu picks are unambiguous → SubmitDirect, deterministic, no LLM).
          `ChatTranscript.vue` + `InteractiveView.vue` (chat | trace+diagram).
- [x] 3.3 Off-path RPC wired (`session.offpath`); slot-supplement RPC in place
          (`session.continue`). Full meta-mode still deferred.
- [x] 3.4 Vitest: ViewElement, InputBar, ChatTranscript (markdown + escaping),
          store, live-source — 121 passing.

## 4. Prove + document — SHIPPED (video)
- [x] 4.1 Playwright e2e (`tools/runstatus/tests/playwright/web-chat.spec.ts`):
          spawns `kitsoki web stories/prd/app.yaml --flow …/happy_path.yaml`,
          drives idle→clarifying→brief→references→drafting→@exit:done in-browser,
          asserts the state badge each scene + accumulated trace rows.
- [x] 4.2 Video + per-scene screenshots at 1440×900 @2x in
          `.artifacts/web-chat/` (`prd-chat.webm`, `00..07-*.png`). Verified
          scene-by-scene visually; fixed 3 defects found that way (invisible
          initial card, raw-template leak, trailing empty bubble) + markdown.
- [x] 4.3 Narrative doc written: **`docs/tui/web-ui.md`** (full reference);
          cross-linked from `docs/tui/README.md` and
          `docs/tracing/run-status-ui.md`. Remaining: trim/delete this proposal
          once the last design items below land.
```

## What we lose, honestly

- **No live routing-tier display.** The TUI streams "deterministic →
  semantic → LLM" progress inline; v1 web shows a spinner and the final
  result. Acceptable for a PoC; revisit by streaming routing slog events
  over the existing SSE channel.
- **No full meta mode.** Off-path is clean; full meta mode couples to the
  TUI's `metamode.Controller` + chat store (`internal/tui/metamode.go`,
  `internal/metamode/`). v1 ships off-path only.

## Open questions

1. ~~**Read source for the live snapshot.**~~ **Resolved (Phase 1):**
   neither a temp trace file nor the SQLite store. The orchestrator's
   dual-write attaches an in-memory `store.JSONLSink` (full fidelity —
   SQLite drops `state_path`/`call_id`/`parent_turn`), and the server
   snapshots it via `runstatus.FromSink` (the same path `export-status`
   uses). A mutex in `server.LiveSession` serialises the orchestrator's
   appends against the server's reads, since `JSONLSink` itself isn't
   concurrency-safe.
2. **One session or many in the PoC** — single implicit session, or a
   session list in the URL? *Lean: single session for v1; the
   orchestrator already keys on `SessionID`, so multi-session is additive
   later.*

## Non-goals

- **Not** a redesign or replacement of the TUI — additive only; the TUI
  is unchanged.
- **Not** a hosted/multi-user deployment — localhost dev server, no auth.
  The SaaS form (session pool, auth middleware, possible read/write
  process split) is deliberately deferred; option A's thin-handler seam
  keeps it a hosting project, not a rewrite.
- **Not** a new server process — reuses the `runstatus` HTTP/SSE stack
  in-process.
- **Not** full meta mode in v1 (see "What we lose").
- **Not** a polished product UI — PoC-quality, functional over beautiful.
