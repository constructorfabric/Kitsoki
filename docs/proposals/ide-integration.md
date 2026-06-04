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
  (read verbs) and seeds `world.ide.connected`. The oracle subprocess env is
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

1. **Pin the unverified wire keys (one-time manual capture).** `openDiff`'s
   argument shape and `getDiagnostics`' `path→uri` key are `TODO(schema)` in
   `internal/host/ide_handlers.go` — implemented best-effort against the
   documented tool names but not captured from a live editor. Run a single
   real-socket round-trip (`tools/list` + a real `getDiagnostics` / `openDiff`)
   in the VS Code integrated terminal, then update the handlers and the stub
   server to mirror the captured shapes. Everything else is tested; this is the
   only gap between "passes against the stub" and "matches real VS Code."

2. **`open_diff` verdict capture.** v1 opens the diff tab and returns `{ok}`
   without capturing the operator's accept/reject — that needs a *turn-suspend*
   gate the engine lacks (it is not the clarify turn-boundary, and host handlers
   are synchronous). Follow-up: add a post-effect suspend/resume gate and route
   the verdict through the decider machinery as a recorded decision.

3. **Adopt in the production `bugfix` story.** The `ide_awareness` demo proves
   the diagnostics-behind-an-availability-gate pattern end to end. Migrating
   `bugfix` to pull `host.ide.get_diagnostics` (falling back to its real linter
   when no editor is attached) was intentionally deferred — `bugfix` validates
   via its agent rather than a discrete lint call, so the seam wants design
   work, not a shoehorn (`stories/CLAUDE.md`).

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
