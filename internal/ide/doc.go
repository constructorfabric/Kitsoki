// Package ide is the single persistent MCP-over-WebSocket client that connects
// kitsoki to a running editor (VS Code / Cursor / JetBrains via the Claude Code
// extension). It is what makes a story "editor-aware": the live selection, the
// open editors, and the language-server diagnostics flow in over this one
// socket, and proposed changes flow back out as native diff tabs.
//
// # Where it sits
//
// The editor extension is the MCP *server* (it hosts a loopback WebSocket and
// writes a lock file); kitsoki is the *client* that discovers the lock,
// authenticates, and drives the editor. This package is a leaf: it depends only
// on the standard library and the WebSocket transport. It deliberately does NOT
// import internal/host or internal/orchestrator — instead [Link] satisfies the
// small host.IDELink interface structurally, so the runtime can resolve a link
// from context without an import cycle.
//
// # The three layers
//
//   - [Discoverer] parses ~/.claude/ide/<port>.lock files and ranks them for a
//     working directory (env-port match first, then longest workspace-prefix
//     match). Discovery is read-only and never opens a socket.
//   - [Client] owns one live connection: it dials, runs the MCP handshake
//     (initialize / notifications/initialized / tools/list), and multiplexes
//     concurrent [Client.CallTool] requests over the single socket via a
//     read-pump goroutine and a pending-request map.
//   - [Link] is the process-lifetime handle holding at most one [Client]. It is
//     what the TUI starts and stops and what host.ide.* handlers call. It owns
//     reconnect (single-flight, retry-once on a dropped socket).
//
// # Determinism boundary
//
// This package is the non-deterministic edge: a real editor's selection and
// diagnostics are live state. Reproducibility is preserved one level up — the
// host.ide.* handlers record each pull into the trace and flow fixtures stub
// those handlers by per-invoke id, so replay never opens a socket. Within this
// package, [Client.CallTool] and the per-call wait are ctx-bound so a turn
// cancel or timeout unblocks an in-flight editor call deterministically.
//
// # Non-goals
//
//   - Never a server: kitsoki never hosts the WebSocket or writes a lock file.
//   - No heartbeat in v1: liveness is discovered lazily on the next call; a
//     dropped socket fails in-flight calls with [ErrNotConnected] and the next
//     [Link.CallTool] reconnects once.
//   - Not the host verb mapping: turning host.ide.get_diagnostics into a
//     getDiagnostics tools/call (arg coercion, result unwrapping, the
//     not-connected typed Result, the journal event) lives in internal/host.
//     This package only exposes a generic [Client.CallTool] / [Link.CallTool].
//
// # The wire contract
//
// The lock-file payload, the x-claude-code-ide-authorization header name, the
// transport ("ws", despite the misleadingly named CLAUDE_CODE_SSE_PORT env
// var), the 1008 close on a bad token, and the tool names are all verified
// against the Claude Code VS Code extension. The link's role as a
// connection-oriented, inbound-capable transport — discovery, auth, lifecycle,
// and the agent env-isolation it forces — is documented in docs/architecture/transports.md
// ("The IDE link"); the host.ide.* verbs it backs are in docs/architecture/hosts.md.
package ide
