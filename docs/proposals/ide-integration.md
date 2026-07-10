# `/ide` — editor awareness: shipped, with follow-ups

**Status:** Both slices shipped. This file now tracks only the deferred
follow-up work; the two slice proposals were deleted and their content
migrated into the narrative docs below.

## What shipped

Terminal kitsoki connects out to a running VS Code (or Cursor/Windsurf) over
the same `~/.claude/ide/<port>.lock` + `ws://127.0.0.1:<port>` MCP mechanism
Claude Code uses, owns that one link, and rents the editor's capabilities to
stories — routed and recorded.

- **Runtime substrate** — `internal/ide/` (lock-file discovery + selection, ws
  dial + `x-claude-code-ide-authorization` auth + MCP `initialize`, the link
  lifecycle with a read-pump/pending-map concurrency model and single-flight
  reconnect). `host.ide.{get_diagnostics,get_selection,get_open_editors,open_file,open_diff}`
  in `internal/host/ide_handlers.go`, resolving the link from ctx
  (`host.WithIDELink`). A connected link is recorded as `ide.context_captured`
  (read verbs) and seeds `world.ide.connected`. The agent subprocess env is
  scrubbed (`CLAUDE_CODE_SSE_PORT` unset, `CLAUDE_CODE_AUTO_CONNECT_IDE=false`)
  at every exec site when a link is held, so the inner `claude` never
  double-connects.
- **TUI** — `/ide [connect|disconnect|status]` with a multi-lock picker
  (`internal/tui/commands_ide.go`), a typed footer chip, and per-turn ambient
  selection (`⧉ Selected N lines from <file>`) gated on a kitsoki-side deny
  list.
- **Tests (no live editor, no real `claude`, no network)** — a faithful stub
  ws MCP server backs unit + e2e coverage of discovery/auth/`initialize`, every
  verb through the real registry→link→client→stub path, reconnect, env hygiene,
  and the multi-lock picker; combined-I/O rendering tests for the footer +
  echo; a flow fixture (stub-by-invoke-id), a legacy-unaffected fixture, and a
  cassette-replay-opens-no-socket fixture via the `testdata/apps/ide_awareness`
  demo app.

**Narrative docs:** `docs/hosts.md` and `docs/architecture/hosts.md`
(`host.ide.*`), `docs/architecture/transports.md` §7 (the IDE link as a connection-oriented,
inbound-capable transport), `docs/tui/README.md` ("Editor awareness: /ide").
Verified wire contract: `.context/claude-code-ide-interface.md`.

## Remaining follow-ups

1. ~~**Pin the unverified wire keys.**~~ Done (WS-D D2): a real-socket round
   trip against the vscode-kitsoki extension's own `IdeServer`/`IdeTools`
   (driven by `tools/vscode-kitsoki/tests/vscode-bugfix-walk.e2e.spec.ts`,
   a real VS Code window) confirmed `getDiagnostics`' arg key — the handler
   was sending `uri`, but `IdeTools.getDiagnostics` reads `args.path`, so the
   narrowing arg was silently dropped on every call; fixed in
   `IDEGetDiagnosticsHandler` (`internal/host/ide_handlers.go`). `openDiff`'s
   `{path, new_text, new_text_path, title}` argument shape and `{ok, verdict}`
   return shape were already exercised by
   `tools/vscode-kitsoki/tests/ide-bridge.e2e.test.ts`; both handlers' doc
   comments now say CONFIRMED instead of carrying a stale `TODO(schema)`.
   ~~One real gap the same capture surfaced: `reviewing_external`'s Mode A
   (`{paths, base}`, reviewing already-applied working-tree edits) is sent by
   `diff_open.go` but the real `DiffController.open()` only implements Mode B
   (`{path, new_text}`).~~ Closed (dwf4-ide-mode-a): `DiffController` now
   implements Mode A too — one diff tab per changed file (left = the file's
   content at `base` via `git show`, right = the on-disk working-tree file),
   sharing one collective accept/reject verdict across the whole set. See
   `tools/vscode-kitsoki/src/diff-mode-a.ts` (the unit-tested git-show +
   arg-normalisation seam), `tools/vscode-kitsoki/src/ide-diff.ts`, and the
   Mode A round-trip case in `ide-bridge.e2e.test.ts`. The extension's
   `IdeTools.openDiff` also stopped fabricating `{verdict:'accepted'}` when no
   `DiffController` is registered — it now throws, which routes
   `reviewing_external` back to its own choice screen via `on_error`
   (`Result.Error`) instead of a silent false accept.

2. **`open_diff` verdict capture.** v1 opens the diff tab and returns `{ok}`
   without capturing the operator's accept/reject — that needs a *turn-suspend*
   gate the engine lacks (it is not the clarify turn-boundary, and host handlers
   are synchronous). Follow-up: add a post-effect suspend/resume gate and route
   the verdict through the decider machinery as a recorded decision.

3. ~~**Adopt in the production `bugfix` story.**~~ Done (WS-C C3): the
   `validating` room pulls `host.ide.get_diagnostics` on entry and threads the
   result into the validator agent's prompt args (`ide_connected`,
   `ide_diagnostics_count`, `ide_diagnostics`) alongside the build log — no
   editor attached degrades honestly (`connected:false`), matching the
   `ide_awareness` demo's pattern. See `stories/bugfix/README.md` ("Editor
   awareness") and `stories/bugfix/flows/validating_surfaces_ide_diagnostics.yaml`.

4. **JetBrains parity.** The lock-file/token/ws contract is shared; the client
   is transport- and tool-agnostic, so JetBrains is a capability-probe away.
   Untested here.

5. **Auto-connect behind a setting.** v1 is explicit-only (`/ide`) so the
   operator opts into ambient injection knowingly. Revisit auto-connect in the
   integrated terminal (where `CLAUDE_CODE_SSE_PORT` is present) once the UX is
   proven, gated by a setting.

<!--
  When these follow-ups land (or are split into their own proposals), delete
  this file — the narrative docs are the durable home.
-->
