---
name: kitsoki-mcp-debug
description: Debug and test the Kitsoki studio MCP server and its tools from the repo checkout. Use when working on `kitsoki mcp`, `cmd/kitsoki/mcp_test_client.go`, `internal/mcp/studio`, MCP tool registration, stdio MCP handshake failures, visual MCP tools (`visual.open`, `visual.observe`, `visual.act`), `mcp-test` output, or when the user wants to verify studio MCP changes without reloading an LLM client.
---

# Kitsoki MCP Debug

Use `kitsoki mcp-test` as the first validation surface for studio MCP work. It
spawns the studio MCP server over stdio with the official Go MCP SDK client, so
it exercises the same transport boundary an attached coding agent uses without
requiring Claude/Codex to reload its MCP tool list.

## Default Smoke

Run from the repo root:

```sh
GOCACHE="${KITSOKI_GOCACHE:-/private/tmp/kitsoki-gocache}" go run ./cmd/kitsoki mcp-test --stories-dir ./stories --timeout 20s
```

Expected behavior:

- the child server prints `kitsoki: studio MCP server on stdio ...` on stderr
- the JSON report has `"ok": true`
- `tools` includes `studio.ping`, `studio.handles`, `story.validate`,
  `session.new`, and render tools
- `tool_runs` contains successful `studio.ping` and `studio.handles` calls

Use a writable temp/shared `GOCACHE` in sandboxed or protected checkouts. Do
not point Go's build cache at `$PWD/.cache/go-build` from the protected primary
checkout; that directory may be read-only and will fail before the MCP server
starts.

When the question is "why is an attached client slow or flaky?", separate Go
build/link latency from MCP server startup before changing server code:

```sh
/usr/bin/time -p kitsoki mcp-test --stories-dir ./stories --timeout 20s
/usr/bin/time -p go run ./cmd/kitsoki mcp-test --stories-dir ./stories --timeout 20s
```

If the binary path is fast but `go run` takes many seconds, the MCP client is
paying compile/link cost on every attach. Fix the client registration to launch
an installed/built `kitsoki` binary rather than `go run ./cmd/kitsoki ...`.
For a precise build-under-test without `go run`, build a temporary binary and
point `mcp-test` at it:

```sh
go build -o /tmp/kitsoki-mcp-test ./cmd/kitsoki
/usr/bin/time -p /tmp/kitsoki-mcp-test mcp-test \
  --server-command /tmp/kitsoki-mcp-test \
  --stories-dir ./stories \
  --timeout 20s
```

Read `studio.ping` before trusting assumptions about the attached server. Its
structured content reports `executable`, `working_dir`, `revision`, and
`modified`; stale PATH installs, wrong worktree cwd, or a temp `go run`
executable are visible there.

## Test One Tool

Use `--tool` and a JSON object in `--tool-args`:

```sh
GOCACHE="${KITSOKI_GOCACHE:-/private/tmp/kitsoki-gocache}" go run ./cmd/kitsoki mcp-test \
  --stories-dir ./stories \
  --tool story.validate \
  --tool-args '{"dir":"stories/bugfix"}'
```

Other useful calls:

```sh
# read-only server shape, useful for meta-mode/Q&A surface checks
GOCACHE="${KITSOKI_GOCACHE:-/private/tmp/kitsoki-gocache}" go run ./cmd/kitsoki mcp-test --read-only

# workspace-bound authoring tools
GOCACHE="${KITSOKI_GOCACHE:-/private/tmp/kitsoki-gocache}" go run ./cmd/kitsoki mcp-test \
  --workspace stories/bugfix \
  --tool story.graph \
  --tool-args '{}'

# point at a built binary instead of go run's current executable
go build -o /tmp/kitsoki-mcp-test ./cmd/kitsoki
/tmp/kitsoki-mcp-test mcp-test --server-command /tmp/kitsoki-mcp-test --stories-dir ./stories
```

`mcp-test` defaults to spawning its current executable with generated `mcp`
args. Use repeated `--server-arg` flags only when the default generated server
args are not appropriate; they replace the generated `mcp` argument list.

## Client Attach Config

The production studio MCP server is the Go binary: `kitsoki mcp`. It is wired in
`cmd/kitsoki/mcp.go` and `internal/mcp/studio/*`. JavaScript entries in
`.mcp.json` such as `slidey`, `stagehand`, or `frontend-mockup` are separate MCP
servers and are not the Kitsoki studio server unless the failing client is
actually attaching to one of those server names.

Check every config surface the current client may read, not only the repo
`.mcp.json`:

```sh
cat .mcp.json
sed -n '1,220p' ~/.codex/config.toml
cat tools/mcp-drive/kitsoki-mcp.json
```

For Codex, a `~/.codex/config.toml` `[mcp_servers.kitsoki]` entry can supersede
repo-local expectations. If it says:

```toml
command = "go"
args = ["run", "./cmd/kitsoki", "mcp", "--stories-dir", "stories"]
```

then Codex is launching `go run` at attach time. Prefer an installed binary:

```toml
[mcp_servers.kitsoki]
command = "kitsoki"
args = ["mcp", "--stories-dir", "stories"]
cwd = "/path/to/Kitsoki"
startup_timeout_sec = 120
tool_timeout_sec = 1800
```

If the attached binary must match the checkout exactly, run the project install
target or build a named binary and set `command` to that absolute path. Verify
the result with `studio.ping` rather than with `kitsoki version` alone.

## No-LLM Boundary

The default smoke is no-LLM:

- `mcp-test` only initializes, lists tools, and calls deterministic tools
- `kitsoki mcp` defaults driving sessions to `harness:replay`
- do not use `session.new` with `harness:"live"` unless the user explicitly asks
  for a live integration test
- tests must use replay/cassettes or in-process SDK transports, not a real LLM

## Local Test Targets

For changes in the CLI wrapper:

```sh
GOCACHE="${KITSOKI_GOCACHE:-/private/tmp/kitsoki-gocache}" go test ./cmd/kitsoki -run 'TestMCP|TestRunStudioMCPTest|TestCLI_TopLevelHelp'
```

For studio server/tool behavior:

```sh
GOCACHE="${KITSOKI_GOCACHE:-/private/tmp/kitsoki-gocache}" go test ./internal/mcp/studio
```

If `go test ./cmd/kitsoki` fails on sandboxed runs with `~/.kitsoki` writes or
Unix socket bind errors, treat that as unrelated unless the touched code is in
those areas. Keep verification focused and report the sandbox blocker.

## Debugging Failures

- `mcp-test: connect`: the child process did not initialize as an MCP server.
  Run with a longer `--timeout`, check the child stderr, and verify the server
  args start with `mcp`.
- `.artifacts/logs/mcp-startup-*.log` containing only `error: context canceled`
  after a client exits usually means the stdio peer disconnected or the parent
  canceled the context. Treat it as shutdown noise unless it is paired with a
  real initialize/list-tools failure or an early nonzero process exit. Do not
  count those logs as proof that startup itself failed.
- slow attach with eventual success: inspect the client registration for
  `go run`, stale `cwd`, or a shared DB path under a slow/locked location; then
  compare `time kitsoki mcp-test ...` with `time go run ./cmd/kitsoki mcp-test
  ...`.
- missing tool in `tools`: inspect registration in `internal/mcp/studio/server.go`
  and the relevant `register*Tools` method.
- tool call returns `"is_error": true`: read the text/structured content in the
  JSON report; studio tools return typed error payloads such as `NO_WORKSPACE`,
  `BAD_REQUEST`, or `UNKNOWN_HANDLE`.
- `story.*` path surprises: pass `--stories-dir ./stories` for `@kitsoki/<name>`
  resolution and pass explicit `dir` or `--workspace` when testing workspace
  tools.
- image/render behavior: start with `render.tui` before `render.tui_png` or
  `render.web`; `render.web` may degrade when no browser-capable host is wired.

## Visual MCP / Web QA

Use visual MCP when the claim is about what a real operator can see or point at
in the web surface. For Slidey deck QA, the high-signal path is:

1. Open a no-LLM/replay session with the story cassette or flow seed.
2. Drive to the rendered deck or review state.
3. Call `visual.open` with `kind:"web"` and a `query` that deep-links the
   single-page app route:

   ```json
   {
     "kind": "web",
     "session_id": "<session>",
     "query": {
       "route": "/s/<session>/chat",
       "visual_annotate": "1"
     }
   }
   ```

4. Call `visual.observe` and verify `regions` include the relevant surface:
   `media`, `deck`, and `annotation` for Slidey deck annotation work.
5. If `visual.act` is involved, verify the action is executable through the
   returned semantic handles. Treat non-actionable semantic text as an MCP/UI
   bug, not as a user workaround.

Use a local DB for repeatable command-line MCP visual smoke when the shared DB
is read-only:

```sh
GOCACHE=/private/tmp/kitsoki-go-cache go run ./cmd/kitsoki mcp-test \
  --stories-dir ./stories \
  --timeout 20s \
  --server-arg mcp \
  --server-arg --stories-dir --server-arg ./stories \
  --server-arg --db --server-arg .artifacts/visual-mcp-smoke/sessions.db
```

Slidey-specific traps:

- `visual.open` must capture the session route, not just the runstatus observer
  landing page.
- The deck annotator should be open when testing anchor/refine flows; use
  `visual_annotate=1` or the specific media handle.
- Baked demo decks rely on ignored generated files under
  `stories/slidey-edit/baked/`. If an isolated worktree shows a missing
  `deck.html`, check whether the main checkout has the ignored artifact before
  changing tracked story logic.
- Prompt args that pass a scene object must serialize it explicitly. If the
  prompt renders `map[...]`, add a string world key such as `scene_json` and
  test the Starlark output.
- Sidecar-promoted videos are not live Slidey embeds. Keep the live picker
  gated to slideshow/deck media so mp4 + semantic sidecars still use the overlay
  path.

## Monitoring / checking a RUNNING session

You **cannot** check on a job another connection/process is driving via a second
`session.status`/`studio.handles` call:
- the MCP CLIENT serialises tool calls per stdio connection (a second call waits
  behind the in-flight `session.drive`), and
- kitsoki sessions are **per-process** — a different `kitsoki mcp` process can't
  see another's handles.

The studio SERVER is NOT the bottleneck — it's provably concurrent (guard test
`internal/mcp/studio/session_concurrent_readonly_test.go`; ticket
`2026-06-25T121622Z-…` is wontfix). So don't go hunting for a server-side lock.

Instead read the trace from disk: **`kitsoki trace status <trace|id> [--json]`** —
one-shot, cross-process, prints `{state, turn, status, last_error, cost, idle}`
and flags ⚠ STALLED. This is the supported way to watch a live MCP-driven run.

**Model selection is a `session.new {profile}` parameter, not a story edit:** a
profile that pins a model (e.g. `codex-native` → gpt-5.5, `synthetic-claude` →
GLM) SUPERSEDES the story agent-def `model:` (`internal/host/agents.go`). The bare
`codex` profile pins nothing and falls back to the agent-def — that's the trap.

Before committing, run `git diff --check` and confirm only intended files are
staged. Leave unrelated untracked files alone.
