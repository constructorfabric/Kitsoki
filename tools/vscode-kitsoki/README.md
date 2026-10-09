# Kitsoki for VS Code

Embeds the kitsoki web UI with **chat in VS Code's bottom panel**, beside
Terminal, Ports, and testing views. Live **trace** and **state graph** views remain
dockable in the Kitsoki Surfaces activity-bar container. Chat never consumes an
editor tab. It is the same Vue SPA the browser web UI serves, relayed into a
webview and driving a local `kitsoki web` backend over the same JSON-RPC/SSE
protocol.

See [`docs/tui/vscode-extension.md`](https://github.com/bsacrobatix/Kitsoki/blob/main/docs/tui/vscode-extension.md)
for the full architecture.

## Requirements

This extension does **not** ship the `kitsoki` binary — it spawns one per
workspace. You need the binary available, then point the extension at it:

- `kitsoki.binaryPath` — absolute path to a `kitsoki` binary. When empty, a
  freshly built `bin/kitsoki` under the workspace root is preferred (a
  kitsoki dev checkout), then `kitsoki` on `PATH`.

If neither resolves, activation fails with an actionable error naming the
missing binary and pointing at the fix: build one with `make build-bin` (the
fast single-artifact build target — not `make build`, which additionally
stages the SPA/story embeds this extension doesn't need at runtime) from a
kitsoki checkout, or set `kitsoki.binaryPath` to an existing binary elsewhere.

Open a workspace that contains a `stories/` directory (or a `.kitsoki.yaml` /
`.kitsoki/stories/` onboarded instance — see **Auto-discovery** below), then run
**Kitsoki: Open Chat** from the Command Palette. Pick a story and the Chat view
opens in the bottom panel. The **Kitsoki Surfaces** activity-bar icon opens Trace
and Graph.

### Auto-discovery

When `kitsoki.storiesDir` is left empty, the extension walks UP from the
workspace root looking for (in order): `.kitsoki/stories/`, `.kitsoki.yaml`,
or a bare `stories/` directory. This means opening a *subdirectory* of a
larger onboarded checkout — a monorepo package, a nested worktree — still
finds the project's stories, without needing `kitsoki.storiesDir` set by
hand. Set `kitsoki.storiesDir` explicitly to override discovery.

## Settings

| Setting | Purpose |
|---|---|
| `kitsoki.binaryPath` | Path to the `kitsoki` binary (empty ⇒ a workspace `bin/kitsoki`, then `kitsoki` on `PATH`). |
| `kitsoki.storiesDir` | `--stories-dir` passed to the spawned backend (empty ⇒ auto-discovered — see above). |
| `kitsoki.flow` | `--flow` fixture (deterministic no-LLM posture; leave empty for live). |
| `kitsoki.hostCassette` | `--host-cassette` (deterministic no-LLM HTTP replay). |
| `kitsoki.mode` | `--mode` (`staged` default, or `one-shot` to auto-advance synthetic gate chains). |
| `kitsoki.ticketRepo` | `--ticket-repo` for the in-product "Report a bug" feature. Empty (default) ⇒ file locally as `issues/bugs/<id>.md` under the workspace, no GitHub auth needed. Set to an `owner/repo` to file real GitHub issues (requires `GH_TOKEN`/`GITHUB_TOKEN`). |

Leave `kitsoki.flow` and `kitsoki.hostCassette` empty for normal (live) use.

## Packaging from source

```
make vscode-package          # builds the SPA + extension, emits the .vsix
```

The `.vsix` lands in `tools/vscode-kitsoki/`. Install it with
**Extensions: Install from VSIX…** in the Command Palette, or
`code --install-extension <file>.vsix`.
